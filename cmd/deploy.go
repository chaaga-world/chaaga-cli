package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/chaaga-world/chaaga-cli/internal/api"
	"github.com/chaaga-world/chaaga-cli/internal/auth"
	"github.com/chaaga-world/chaaga-cli/internal/config"
	"github.com/chaaga-world/chaaga-cli/internal/files"
	"github.com/spf13/cobra"
)

var validSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func newDeployCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deploy [appname]",
		Short: "Deploy the current directory to Chaaga",
		Long: `Upload all files in the current directory to Chaaga.

If appname is omitted, the current directory name is used.
Files larger than 5 MB and dotfiles are skipped.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runDeploy,
	}
}

func runDeploy(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	appname := filepath.Base(cwd)
	if len(args) == 1 {
		appname = args[0]
	}
	if !validSlug.MatchString(appname) {
		return fmt.Errorf("appname %q must be lowercase alphanumeric + hyphens (e.g. my-app)", appname)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	token, err := auth.EnsureToken(cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Scanning %s...\n", cwd)
	entries, err := files.Scan(cwd)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  %d file(s) found\n", len(entries))
	if len(entries) == 0 {
		return fmt.Errorf("no files to deploy")
	}

	if cfg.DryRun {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}

	client := api.New(cfg.API, token)

	me, err := client.GetMe()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Ensuring app '%s' exists...\n", appname)
	if err := client.EnsureApp(appname); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "Requesting presigned URLs...")
	uploads, err := client.Presign(appname, entries)
	if err != nil {
		return err
	}

	// Build path → absPath lookup
	absOf := make(map[string]string, len(entries))
	for _, e := range entries {
		absOf[e.Path] = e.AbsPath
	}

	fmt.Fprintf(os.Stderr, "Uploading %d file(s) (parallelism=%d)...\n", len(uploads), cfg.Parallel)

	sem := make(chan struct{}, cfg.Parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var uploadErrs []error

	for _, up := range uploads {
		wg.Add(1)
		go func(up api.UploadEntry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := client.UploadFile(up, absOf[up.Path]); err != nil {
				mu.Lock()
				uploadErrs = append(uploadErrs, fmt.Errorf("%s: %w", up.Path, err))
				mu.Unlock()
				return
			}
			fmt.Fprintf(os.Stderr, "  ok  %s\n", up.Path)
		}(up)
	}
	wg.Wait()

	if len(uploadErrs) > 0 {
		for _, e := range uploadErrs {
			fmt.Fprintf(os.Stderr, "  FAIL %v\n", e)
		}
		return fmt.Errorf("%d upload(s) failed", len(uploadErrs))
	}

	fmt.Fprintln(os.Stderr, "Finalizing...")
	if _, err := client.Finalize(appname, entries); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  Deployed to https://%s.chaaga.com/%s\n", me.Username, appname)
	return nil
}
