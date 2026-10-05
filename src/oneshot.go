package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The one-shot commands: each does one thing and exits, so scripts and
// agents (see skills/chaaga-app) can drive them. Exit codes: 0 ok, 1 error,
// 3 conflict, 4 linked app no longer exists — see exitCode.

// errConflict marks a refusal because the phone and the folder both
// changed (or the phone changed and push wasn't forced) — exit code 3.
var errConflict = errors.New("conflict")

func exitCode(err error) int {
	switch {
	case errors.Is(err, errConflict):
		return 3
	case isAppGone(err):
		return 4
	}
	return 1
}

// parseCommand splits args into positionals (min..max of them) and the
// boolean flags registered by setup.
func parseCommand(name string, args []string, min, max int, setup func(*flag.FlagSet)) ([]string, error) {
	positional, flagArgs := splitArgs(args, nil)
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if setup != nil {
		setup(fs)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	if len(positional) < min || len(positional) > max {
		return nil, fmt.Errorf("wrong number of arguments — usage: chaaga-cli %s", commandUsage[name])
	}
	return positional, nil
}

var commandUsage = map[string]string{
	"connect": "connect [<phone-ip>]",
	"apps":    "apps [--json]",
	"new":     `new <folder> <name> [<emoji>]   (quote names with spaces: "My Game")`,
	"link":    "link <folder> <appId>",
	"pull":    "pull <folder>",
	"status":  "status <folder>",
	"push":    "push <folder> [--force]",
	"rename":  `rename <folder> <newName> [<emoji>]`,
}

// linkedClient builds a client for a linked folder, with the identity
// guard attached.
func linkedClient(dir string) (*client, *folderLink, error) {
	host, err := loadPhoneHost()
	if err != nil {
		return nil, nil, err
	}
	link, err := loadLink(dir)
	if err != nil {
		return nil, nil, err
	}
	c := newClient(host, link.AppID)
	c.identity = &identityGuard{dir: dir, link: link}
	return c, link, nil
}

func hostClient() (*client, error) {
	host, err := loadPhoneHost()
	if err != nil {
		return nil, err
	}
	return newHostClient(host), nil
}

func absFolder(path string) (string, error) {
	dir, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve folder path: %w", err)
	}
	return dir, nil
}

func runConnect(args []string) error {
	pos, err := parseCommand("connect", args, 0, 1, nil)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		host, err := loadPhoneHost()
		if err != nil {
			return err
		}
		fmt.Println(host)
		return nil
	}
	host := normalizeHost(pos[0])
	path, err := savePhoneHost(host)
	if err != nil {
		return err
	}
	fmt.Printf("saved phone host %s (%s)\n", host, path)
	fmt.Println("checking the connection — if this is a new computer, approve the prompt on your phone…")
	apps, err := newHostClient(host).listApps()
	if err != nil {
		fmt.Printf("warning: could not reach the phone: %v\n", err)
		return nil
	}
	fmt.Printf("connected — %d apps on the phone\n", len(apps))
	return nil
}

func runApps(args []string) error {
	var asJSON bool
	if _, err := parseCommand("apps", args, 0, 0, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	}); err != nil {
		return err
	}
	c, err := hostClient()
	if err != nil {
		return err
	}
	apps, err := c.listApps()
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(apps)
	}
	if len(apps) == 0 {
		fmt.Println("no apps on the phone yet — create one with: chaaga-cli new <folder> <name>")
	}
	for _, app := range apps {
		fmt.Printf("%4d  %s  %s  (updated %s)\n", app.ShortID, app.Emoji, app.Name, app.UpdatedAt)
	}
	return nil
}

func runNew(args []string) error {
	pos, err := parseCommand("new", args, 2, 3, nil)
	if err != nil {
		return err
	}
	dir, err := absFolder(pos[0])
	if err != nil {
		return err
	}
	if link, err := loadLink(dir); err == nil {
		return fmt.Errorf("%s is already linked to app %d %q", dir, link.AppID, link.Name)
	}
	emoji := ""
	if len(pos) == 3 {
		emoji = pos[2]
	}
	c, err := hostClient()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	app, err := c.createApp(pos[1], emoji)
	if err != nil {
		return err
	}
	link := &folderLink{AppID: app.ShortID, UID: app.UID, Name: app.Name}
	if err := saveLink(dir, link); err != nil {
		return err
	}
	fmt.Printf("created app %d %s %q and linked %s\n", app.ShortID, app.Emoji, app.Name, dir)

	c.setAppID(app.ShortID)
	c.identity = &identityGuard{dir: dir, link: link}
	local, err := localHashes(dir)
	if err != nil {
		return err
	}
	if len(local) > 0 {
		if _, err := pushAll(c, dir); err != nil {
			return err
		}
	}
	return writeState(c, dir)
}

func runLink(args []string) error {
	pos, err := parseCommand("link", args, 2, 2, nil)
	if err != nil {
		return err
	}
	dir, err := absFolder(pos[0])
	if err != nil {
		return err
	}
	appID, err := strconv.Atoi(pos[1])
	if err != nil || appID <= 0 {
		return fmt.Errorf("app id must be a positive number (see chaaga-cli apps), got %q", pos[1])
	}
	host, err := loadPhoneHost()
	if err != nil {
		return err
	}
	m, err := newClient(host, appID).getManifest()
	if err != nil {
		return err
	}
	if m.UID == "" {
		return errors.New("the Chaaga app on your phone is too old for link — update it and try again")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	// A baseline recorded for another app means nothing for this one.
	if old, err := loadLink(dir); err != nil || old.UID != m.UID {
		if err := removeState(dir); err != nil {
			return err
		}
	}
	if err := saveLink(dir, &folderLink{AppID: appID, UID: m.UID, Name: m.Name}); err != nil {
		return err
	}
	fmt.Printf("linked %s to app %d %q\n", dir, appID, m.Name)
	fmt.Printf("next: chaaga-cli pull %s  (take the phone's files)  or  chaaga-cli push %s --force  (send yours)\n", pos[0], pos[0])
	return nil
}

func runPull(args []string) error {
	pos, err := parseCommand("pull", args, 1, 1, nil)
	if err != nil {
		return err
	}
	dir, err := absFolder(pos[0])
	if err != nil {
		return err
	}
	c, _, err := linkedClient(dir)
	if err != nil {
		return err
	}
	if _, err := pullAll(c, dir); err != nil {
		return err
	}
	return writeState(c, dir)
}

func runStatus(args []string) error {
	pos, err := parseCommand("status", args, 1, 1, nil)
	if err != nil {
		return err
	}
	dir, err := absFolder(pos[0])
	if err != nil {
		return err
	}
	c, link, err := linkedClient(dir)
	if err != nil {
		return err
	}
	cs, err := diffState(c, dir, loadState(dir))
	if err != nil {
		return err
	}
	fmt.Printf("app %d %q\n", link.AppID, link.Name)
	if len(cs.Local) == 0 && len(cs.Remote) == 0 {
		fmt.Println("up to date")
		return nil
	}
	for _, ch := range cs.Local {
		fmt.Printf("local  %-8s %s\n", ch.Kind, ch.Name)
	}
	for _, ch := range cs.Remote {
		fmt.Printf("phone  %-8s %s\n", ch.Kind, ch.Name)
	}
	if len(cs.Conflicts) > 0 {
		fmt.Printf("conflict: changed on both sides: %s\n", strings.Join(cs.Conflicts, ", "))
		return errConflict
	}
	return nil
}

func runPush(args []string) error {
	var force bool
	pos, err := parseCommand("push", args, 1, 1, func(fs *flag.FlagSet) {
		fs.BoolVar(&force, "force", false, "overwrite changes made on the phone")
	})
	if err != nil {
		return err
	}
	dir, err := absFolder(pos[0])
	if err != nil {
		return err
	}
	c, _, err := linkedClient(dir)
	if err != nil {
		return err
	}
	cs, err := diffState(c, dir, loadState(dir))
	if err != nil {
		return err
	}
	if len(cs.Remote) > 0 && !force {
		for _, ch := range cs.Remote {
			fmt.Printf("phone  %-8s %s\n", ch.Kind, ch.Name)
		}
		fmt.Printf("refusing to push: the app changed on the phone since the last sync — run chaaga-cli pull %s to take those changes, or chaaga-cli push %s --force to overwrite them\n", pos[0], pos[0])
		return errConflict
	}
	if _, err := pushAll(c, dir); err != nil {
		return err
	}
	return writeState(c, dir)
}

func runRename(args []string) error {
	pos, err := parseCommand("rename", args, 2, 3, nil)
	if err != nil {
		return err
	}
	dir, err := absFolder(pos[0])
	if err != nil {
		return err
	}
	c, link, err := linkedClient(dir)
	if err != nil {
		return err
	}
	// Checks the link before writing anything.
	if _, err := c.getManifest(); err != nil {
		return err
	}
	emoji := ""
	if len(pos) == 3 {
		emoji = pos[2]
	}
	oldName := link.Name
	app, err := c.renameApp(pos[1], emoji)
	if err != nil {
		return err
	}
	link.Name = app.Name
	if err := saveLink(dir, link); err != nil {
		return err
	}
	fmt.Printf("renamed app %d %q → %s %q\n", app.ShortID, oldName, app.Emoji, app.Name)
	return nil
}

func writeState(c *client, dir string) error {
	state, err := buildState(c, dir)
	if err != nil {
		return err
	}
	return saveState(dir, state)
}
