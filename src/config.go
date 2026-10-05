package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The three files this CLI keeps:
//
//   - the global config (configPath) holds the phone's address, set once
//     with `chaaga-cli connect` — the only place the host lives;
//   - <folder>/.chaaga.yaml (linkFilename) says which app a folder belongs
//     to — small and human-readable;
//   - <folder>/.chaaga.state (stateFilename) is the machine-only baseline
//     each file had at the last pull/push, for conflict detection.
const (
	linkFilename  = ".chaaga.yaml"
	stateFilename = ".chaaga.state"
)

// isCLIFile reports whether name is one of this CLI's own folder files (or
// a temp file from writing one), which must never be synced to the phone.
func isCLIFile(name string) bool {
	return strings.HasPrefix(name, ".chaaga.")
}

var errNoHost = errors.New("no phone host set — run: chaaga-cli connect <phone-ip>")

type globalConfig struct {
	PhoneHost string `yaml:"phone_host"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config dir: %w", err)
	}
	return filepath.Join(dir, "chaaga-cli", "config.yaml"), nil
}

func loadPhoneHost() (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errNoHost
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var cfg globalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.PhoneHost == "" {
		return "", errNoHost
	}
	return cfg.PhoneHost, nil
}

func savePhoneHost(host string) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := yaml.Marshal(globalConfig{PhoneHost: host})
	if err != nil {
		return "", err
	}
	return path, writeFileAtomic(path, data)
}

// normalizeHost turns "192.168.1.23" into "192.168.1.23:8787", keeping an
// explicit port or scheme as given.
func normalizeHost(host string) string {
	return strings.TrimPrefix(hostBaseURL(host), "http://")
}

// folderLink is .chaaga.yaml. AppID addresses requests but can go stale —
// shortIds are reused after a delete — so UID, the app's permanent id, is
// what identifies it; Name is a label kept up to date automatically.
type folderLink struct {
	AppID int    `yaml:"appId"`
	UID   string `yaml:"uid"`
	Name  string `yaml:"name"`
}

func loadLink(dir string) (*folderLink, error) {
	data, err := os.ReadFile(filepath.Join(dir, linkFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s is not linked to an app — run: chaaga-cli link %s <appId>  (or chaaga-cli new %s <name>)", dir, dir, dir)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", linkFilename, err)
	}
	var link folderLink
	if err := yaml.Unmarshal(data, &link); err != nil {
		return nil, fmt.Errorf("parse %s: %w", linkFilename, err)
	}
	if link.AppID <= 0 || link.UID == "" {
		return nil, fmt.Errorf("%s is incomplete — relink with: chaaga-cli link %s <appId>", linkFilename, dir)
	}
	return &link, nil
}

func saveLink(dir string, link *folderLink) error {
	data, err := yaml.Marshal(link)
	if err != nil {
		return err
	}
	header := "# Links this folder to an app on your phone. Written by chaaga-cli.\n"
	return writeFileAtomic(filepath.Join(dir, linkFilename), append([]byte(header), data...))
}

// fileBaseline is one file as it was at the last pull/push: the phone's
// modifiedAt (compared as a raw string — see manifestEntry) and a hash of
// the content, which unlike mtime doesn't change on a no-op save.
type fileBaseline struct {
	ModifiedAt string `json:"modifiedAt"`
	SHA256     string `json:"sha256"`
}

// syncState is .chaaga.state, keyed by filename (index.html included).
type syncState map[string]fileBaseline

// loadState returns nil (no baseline) when the file is missing or corrupt
// — callers then treat every phone-side file as unknown, so nothing gets
// silently overwritten.
func loadState(dir string) syncState {
	data, err := os.ReadFile(filepath.Join(dir, stateFilename))
	if err != nil {
		return nil
	}
	var state syncState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return state
}

func saveState(dir string, state syncState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, stateFilename), data)
}

func removeState(dir string) error {
	err := os.Remove(filepath.Join(dir, stateFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// writeFileAtomic writes via a temp file in the same directory, so an
// interrupted write never leaves a half-written config behind.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// appGoneError means the app a folder is linked to no longer exists on the
// phone — exit code 4.
type appGoneError struct {
	dir  string
	link folderLink
}

func (e *appGoneError) Error() string {
	return fmt.Sprintf("the app this folder was linked to (%q, id %d) no longer exists on the phone — run: chaaga-cli apps, then chaaga-cli link %s <appId>",
		e.link.Name, e.link.AppID, e.dir)
}

// identityGuard checks every per-app response against the folder's link,
// using the X-Chaaga-App-* headers the server sets on each one:
//
//   - same uid, same name: nothing to do;
//   - same uid, new name: the app was renamed on the phone — update the
//     label in .chaaga.yaml;
//   - a different uid, or a 404 for the shortId itself: the shortId is
//     stale. Look the uid up in GET /apps — if the app moved, update
//     appId and retry; if it's gone, fail with appGoneError.
//
// Commands always start with a GET (the manifest), so a stale id is caught
// before anything is written.
type identityGuard struct {
	dir  string
	link *folderLink
}

func (g *identityGuard) check(c *client, method string, resp *http.Response) (retry bool, err error) {
	uid := resp.Header.Get("X-Chaaga-App-Uid")
	switch {
	case uid == "" && resp.StatusCode != http.StatusNotFound:
		// A Chaaga build without identity headers; nothing to check.
		return false, nil
	case uid == g.link.UID:
		name, err := url.PathUnescape(resp.Header.Get("X-Chaaga-App-Name"))
		if err == nil && name != "" && name != g.link.Name {
			// A PATCH is our own rename; the caller records it.
			if method != http.MethodPatch {
				log.Printf("note: app renamed %q → %q", g.link.Name, name)
			}
			g.link.Name = name
			if err := saveLink(g.dir, g.link); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	apps, err := c.listApps()
	if err != nil {
		return false, fmt.Errorf("app id %d no longer matches this folder; looking it up: %w", g.link.AppID, err)
	}
	for _, app := range apps {
		if app.UID != g.link.UID {
			continue
		}
		if method != http.MethodGet {
			return false, fmt.Errorf("app %q changed id from %d to %d mid-command — run it again", app.Name, g.link.AppID, app.ShortID)
		}
		log.Printf("note: app %q is now id %d (was %d)", app.Name, app.ShortID, g.link.AppID)
		g.link.AppID = app.ShortID
		g.link.Name = app.Name
		if err := saveLink(g.dir, g.link); err != nil {
			return false, err
		}
		c.setAppID(app.ShortID)
		return true, nil
	}
	return false, &appGoneError{dir: g.dir, link: *g.link}
}
