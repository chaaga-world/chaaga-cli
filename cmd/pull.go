package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chaaga-world/chaaga-cli/internal/api"
	"github.com/chaaga-world/chaaga-cli/internal/config"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	var outputDir string
	var force bool

	cmd := &cobra.Command{
		Use:   "pull <appname>",
		Short: "Download deployed files from Chaaga to the current directory",
		Long: `Download all files for an app from Chaaga into the current directory.

Existing files are skipped unless --force is used.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPull(args[0], outputDir, force)
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "Directory to write files into (default: current directory)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing files")
	return cmd
}

func runPull(appname, outputDir string, force bool) error {
	if !validSlug.MatchString(appname) {
		return fmt.Errorf("appname %q must be lowercase alphanumeric + hyphens", appname)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if outputDir == "" {
		outputDir, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	return resolveToken(cfg, func(token string) error {
		client := api.New(cfg.API, token)

		fmt.Fprintf(os.Stderr, "Listing files for '%s'...\n", appname)
		remoteFiles, err := client.ListFiles(appname)
		if err != nil {
			return err
		}
		if len(remoteFiles) == 0 {
			fmt.Fprintln(os.Stderr, "No files found.")
			return nil
		}
		fmt.Fprintf(os.Stderr, "  %d remote file(s)\n", len(remoteFiles))

		skipped := 0
		for _, rf := range remoteFiles {
			if strings.Contains(rf.Path, "..") {
				fmt.Fprintf(os.Stderr, "  skip (suspicious path): %s\n", rf.Path)
				continue
			}

			dest := filepath.Join(outputDir, filepath.FromSlash(rf.Path))

			if !force {
				if _, err := os.Stat(dest); err == nil {
					fmt.Fprintf(os.Stderr, "  skip (exists): %s\n", rf.Path)
					skipped++
					continue
				}
			}

			if err := downloadTo(client, appname, rf.Path, dest); err != nil {
				return fmt.Errorf("download %s: %w", rf.Path, err)
			}
			fmt.Fprintf(os.Stderr, "  ok   %s\n", rf.Path)
		}

		fmt.Println()
		if skipped > 0 {
			fmt.Printf("  Pulled %d file(s) (%d skipped, use --force to overwrite).\n",
				len(remoteFiles)-skipped, skipped)
		} else {
			fmt.Printf("  Pulled %d file(s) to %s\n", len(remoteFiles), outputDir)
		}
		return nil
	})
}

func downloadTo(client *api.Client, slug, remotePath, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	body, err := client.DownloadFile(slug, remotePath)
	if err != nil {
		return err
	}
	defer body.Close()

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, body)
	return err
}
