package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

const (
	pushPollInterval = 1 * time.Second
	pullPollInterval = 2 * time.Second
)

// source is which side of the sync is authoritative for a given run — sync
// never merges/diffs both directions at once (see mirror.go's pushAll/
// pullAll doc comments), so this is chosen once, up front, via
// promptSourceOfTruth, rather than guessed or inferred from timestamps.
type source int

const (
	sourceApp source = iota
	sourceLocal
)

func (s source) String() string {
	if s == sourceApp {
		return "app"
	}
	return "local folder"
}

// flagsWithValue lists every runSync flag that consumes a following value
// token (all of them — there are no boolean flags here), by both its short
// and long name. Used by splitArgs to correctly pull flag/value pairs out
// regardless of where they fall relative to the folder-path positional
// argument.
var flagsWithValue = map[string]bool{"a": true, "appid": true, "h": true, "host": true}

// runSync implements the `sync` subcommand — see main.go's usage() for the
// exact flag/positional shape.
func runSync(args []string) error {
	positional, flagArgs := splitArgs(args, flagsWithValue)

	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	appID := fs.Int("a", 0, "the sub-app's shortId (also -appid)")
	fs.IntVar(appID, "appid", 0, "the sub-app's shortId (also -a)")
	host := fs.String("h", "", "the phone's LAN address, host or host:port (also -host)")
	fs.StringVar(host, "host", "", "the phone's LAN address, host or host:port (also -h)")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("expected exactly one folder path argument, got %d", len(positional))
	}
	dir := positional[0]
	if *appID == 0 {
		return fmt.Errorf("-a/-appid is required")
	}
	if *host == "" {
		return fmt.Errorf("-h/-host is required")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create/open folder %s: %w", dir, err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve folder path: %w", err)
	}

	sourceOfTruth, err := promptSourceOfTruth(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}

	c := newClient(*host, *appID)

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Raw mode delivers each keystroke to Read as soon as it's pressed,
	// instead of the terminal driver buffering a whole line until Enter —
	// what makes "press R" (no Enter) work below. It also strips out the
	// driver's own Ctrl+C -> SIGINT translation (part of what "raw" turns
	// off), so watchStdinForReload watches for that byte itself and this
	// goroutine turns it into the same ctx cancellation os/signal would
	// otherwise have delivered. Skipped entirely when stdin isn't a real
	// terminal (e.g. piped input during scripting/testing), since there's
	// no line-buffering to bypass in that case.
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("enable raw terminal mode: %w", err)
		}
		defer term.Restore(fd, oldState)
	}

	reload, interrupt := watchStdinForReload(os.Stdin)
	go func() {
		select {
		case <-interrupt:
			cancel()
		case <-ctx.Done():
		}
	}()

	log.Printf("syncing %s <-> app %d at %s (source of truth: %s)", absDir, *appID, *host, sourceOfTruth)
	switch sourceOfTruth {
	case sourceApp:
		state, err := pullAll(c, absDir)
		if err != nil {
			return fmt.Errorf("initial pull: %w", err)
		}
		// Approval is already established (pullAll just succeeded) — see
		// watchRequestTimeout's doc comment for why the watch loop uses a
		// much shorter timeout than the initial pass.
		c.setTimeout(watchRequestTimeout)
		log.Printf("initial pull complete, watching the app for changes (Ctrl+C to stop, R to force a full reload)")
		if err := watchPull(ctx, c, absDir, state, pullPollInterval, reload); err != nil {
			return fmt.Errorf("watch pull: %w", err)
		}
	case sourceLocal:
		state, err := pushAll(c, absDir)
		if err != nil {
			return fmt.Errorf("initial push: %w", err)
		}
		c.setTimeout(watchRequestTimeout)
		log.Printf("initial push complete, watching %s for changes (Ctrl+C to stop, R to force a full reload)", absDir)
		if err := watchPush(ctx, c, absDir, state, pushPollInterval, reload); err != nil {
			return fmt.Errorf("watch push: %w", err)
		}
	}
	log.Printf("stopping")
	return nil
}

// watchStdinForReload reads stdin byte-by-byte in the background (called
// after promptSourceOfTruth has already finished its own, line-based read,
// so there's no overlap) and signals on the returned reload channel
// whenever the user presses "r"/"R" — a way to force an immediate full
// push/pull pass instead of waiting for the next poll tick. Byte-at-a-time
// rather than line-based: runSync puts a real terminal into raw mode
// specifically so a keystroke reaches Read immediately rather than sitting
// in the driver's line buffer until Enter, and reading here has to match
// that — buffering a line would just reintroduce the Enter requirement one
// layer up. Each byte is checked independently (not matched against a
// whole line), so "r" always fires the moment it's pressed even mid-word;
// there's no such thing as "typing a word" once each keystroke is already
// live. Both channels are buffered by 1 with non-blocking sends, so a
// request already pending (not yet consumed by the caller) is enough — a
// second R or Ctrl+C before that lands is a no-op, not queued twice.
//
// Ctrl+C (0x03) is watched for here rather than left to the OS: raw mode
// disables the terminal driver's own Ctrl+C -> SIGINT translation (that's
// part of what "raw" means), so without this, Ctrl+C would do nothing once
// the watch loop starts. The caller turns a signal on interrupt into the
// same context cancellation os/signal would otherwise have delivered.
func watchStdinForReload(in io.Reader) (reload <-chan struct{}, interrupt <-chan struct{}) {
	reloadCh := make(chan struct{}, 1)
	interruptCh := make(chan struct{}, 1)
	go func() {
		r := bufio.NewReader(in)
		for {
			b, err := r.ReadByte()
			if err != nil {
				return
			}
			var target chan struct{}
			switch b {
			case 'r', 'R':
				target = reloadCh
			case 0x03:
				target = interruptCh
			default:
				continue
			}
			select {
			case target <- struct{}{}:
			default:
			}
		}
	}()
	return reloadCh, interruptCh
}

// splitArgs separates the folder-path positional argument from flag
// arguments, regardless of where it falls relative to the flags — Go's
// flag package stops parsing at the first non-flag token, which would
// otherwise break the documented `chaaga-cli sync <folder_path> -a <appId> -h
// <host>` order (folder path first, flags after): fs.Parse would see
// <folder_path> as the first token, stop right there, and dump everything
// after it (the real flags) into fs.Args() as if they were extra
// positional arguments too.
func splitArgs(args []string, flagsWithValue map[string]bool) (positional, flagArgs []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		name := strings.TrimLeft(arg, "-")
		// A "-flag=value" token already carries its value; only the
		// space-separated "-flag value" form needs the next token pulled
		// in too, so it isn't mistaken for a positional argument.
		if !strings.Contains(name, "=") && flagsWithValue[name] && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return positional, flagArgs
}

// promptSourceOfTruth asks which side should win this run (see [source]'s
// doc comment) — re-prompts on anything other than a recognized answer.
func promptSourceOfTruth(in io.Reader, out io.Writer) (source, error) {
	reader := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "Source of truth — [a]pp or [l]ocal folder? ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("read answer: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "a", "app":
			return sourceApp, nil
		case "l", "local":
			return sourceLocal, nil
		default:
			fmt.Fprintln(out, `please answer "a"/"app" or "l"/"local"`)
		}
	}
}
