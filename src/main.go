package main

import (
	"fmt"
	"os"
)

// Overridden at build time via -ldflags "-X main.version=... " (see publish.sh).
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Printf("chaaga-cli %s (%s, built %s)\n", version, commit, buildDate)
	case "sync":
		if err := runSync(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "chaaga-cli sync:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `chaaga-cli — sync a local folder with a Chaaga sub-app

Usage:
  chaaga-cli sync <folder_path> -a <appId> -h <host>

    -a, -appid   the sub-app's shortId, shown in Chaaga's API tab
    -h, -host    the phone's LAN address, e.g. 192.168.1.23 or 192.168.1.23:8787
                 (port defaults to 8787 if omitted)

While running: press R to force an immediate full push/pull instead of
waiting for the next automatic check; Ctrl+C to stop.

See README.md for details — in particular, the first connection from a new
machine can pause for up to 2 minutes waiting for you to approve it on the
phone.`)
}
