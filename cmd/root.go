package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/chaaga-world/chaaga-cli/internal/api"
	"github.com/chaaga-world/chaaga-cli/internal/auth"
	"github.com/chaaga-world/chaaga-cli/internal/config"
	"github.com/spf13/cobra"
)

// resolveToken returns a valid token, re-running device login if the stored
// token is expired (i.e. the first API call returns ErrUnauthorized).
func resolveToken(cfg *config.Config, call func(token string) error) error {
	token, err := auth.EnsureToken(cfg)
	if err != nil {
		return err
	}
	err = call(token)
	if errors.Is(err, api.ErrUnauthorized) {
		token, err = auth.RefreshToken(cfg)
		if err != nil {
			return err
		}
		err = call(token)
	}
	return err
}

var Version = "dev"

var rootCmd = &cobra.Command{
	Use:           "chaaga",
	Short:         "Deploy and manage apps on Chaaga",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return err
	}
	return nil
}

func init() {
	rootCmd.Version = Version
	rootCmd.AddCommand(newDeployCmd())
	rootCmd.AddCommand(newPullCmd())
}
