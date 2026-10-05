package main

import (
	"errors"
	"fmt"
	"log"
	"os"
)

// Overridden at build time via -ldflags "-X main.version=... " (see publish.sh).
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

var commands = map[string]func([]string) error{
	"sync":    runSync,
	"connect": runConnect,
	"apps":    runApps,
	"agents":  runAgents,
	"new":     runNew,
	"link":    runLink,
	"pull":    runPull,
	"status":  runStatus,
	"push":    runPush,
	"rename":  runRename,
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	name := os.Args[1]
	switch name {
	case "version", "-v", "--version":
		fmt.Printf("chaaga-cli %s (%s, built %s)\n", version, commit, buildDate)
		return
	case "help", "-h", "--help":
		usage()
		return
	}
	run, ok := commands[name]
	if !ok {
		usage()
		os.Exit(1)
	}
	if name != "sync" {
		// One-shot commands print plain lines to stdout (no timestamps),
		// so scripts and agents can read them.
		log.SetFlags(0)
		log.SetOutput(os.Stdout)
	}
	if err := run(os.Args[2:]); err != nil {
		if !errors.Is(err, errConflict) {
			fmt.Fprintf(os.Stderr, "chaaga-cli %s: %v\n", name, err)
		}
		os.Exit(exitCode(err))
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `chaaga-cli — work on your Chaaga apps from your computer

Setup (once per phone):
  chaaga-cli connect <phone-ip>        save the phone's address (shown in the app's
                                       API tab) and approve the prompt on the phone
  chaaga-cli connect                   show the saved address

Apps:
  chaaga-cli apps [--json]             list the apps on the phone
  chaaga-cli new <folder> <name> [<emoji>]
                                       create an app and link the folder to it
  chaaga-cli link <folder> <appId>     link a folder to an existing app (no files copied)
  chaaga-cli rename <folder> <newName> [<emoji>]
  chaaga-cli agents                    print how to build Chaaga apps: the rules and
                                       chaaga.* APIs your phone's Chaaga supports

Files (folder must be linked):
  chaaga-cli pull <folder>             copy the phone's files into the folder
  chaaga-cli status <folder>           show what changed on each side since the last sync
  chaaga-cli push <folder> [--force]   copy the folder to the phone; refuses if the
                                       phone changed too, unless --force
  chaaga-cli sync <folder>             keep syncing live in one direction until Ctrl+C
                                       (press R to force a full pass)

Quote names with spaces: chaaga-cli new ./game "My Game" 🎮

Exit codes: 0 ok, 1 error, 3 conflict, 4 the linked app no longer exists.
The first request from a new computer waits (up to 2 minutes) for you to
approve it on the phone.`)
}
