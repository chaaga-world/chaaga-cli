package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

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
