package main

import (
	"os"

	"github.com/chaaga-world/chaaga-cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
