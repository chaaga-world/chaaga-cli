package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

const indexFilename = "index.html"

// localFileState is what pushAll/watchPush remembers about each local file
// (including index.html, keyed as indexFilename) between polls, purely to
// detect "did this change since I last looked" — os.FileInfo values
// straight from the filesystem, no cross-process time format to worry
// about (unlike remoteFileState below).
type localFileState struct {
	size    int64
	modTime time.Time
}

// remoteFileState mirrors what the manifest reports for one file (including
// index.html, keyed as indexFilename) — see manifestEntry's doc comment
// for why ModifiedAt stays a raw string rather than a parsed time.Time.
type remoteFileState struct {
	size       int64
	modifiedAt string
}

// pushAll uploads every local file to the sub-app (index.html via the index
// route, everything else as a sibling file), then deletes every remote
// sibling file that isn't present locally — a full, local-is-authoritative
// mirror pass. Returns the local state observed (index.html included,
// keyed as indexFilename), for watchPush to diff future polls against.
func pushAll(c *client, dir string) (map[string]localFileState, error) {
	entries, err := localSiblingFiles(dir)
	if err != nil {
		return nil, err
	}
	state := make(map[string]localFileState, len(entries)+1)

	if indexPath := filepath.Join(dir, indexFilename); fileExists(indexPath) {
		info, err := os.Stat(indexPath)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", indexFilename, err)
		}
		body, err := os.ReadFile(indexPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", indexFilename, err)
		}
		if err := c.postIndex(string(body)); err != nil {
			return nil, fmt.Errorf("push %s: %w", indexFilename, err)
		}
		state[indexFilename] = localFileState{size: info.Size(), modTime: info.ModTime()}
		log.Printf("pushed %s", indexFilename)
	}

	for name, info := range entries {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if err := c.postFile(name, body); err != nil {
			return nil, fmt.Errorf("push %s: %w", name, err)
		}
		state[name] = localFileState{size: info.Size(), modTime: info.ModTime()}
		log.Printf("pushed %s", name)
	}

	m, err := c.getManifest()
	if err != nil {
		return nil, fmt.Errorf("fetch manifest for cleanup: %w", err)
	}
	for _, f := range m.Files {
		if _, ok := entries[f.Name]; ok {
			continue
		}
		if err := c.deleteFile(f.Name); err != nil {
			return nil, fmt.Errorf("delete remote %s: %w", f.Name, err)
		}
		log.Printf("deleted remote %s (not present locally)", f.Name)
	}

	return state, nil
}

// pullAll downloads the sub-app's index (if it's ever been written) and
// every sibling file into dir, then removes any local file absent from the
// remote manifest — a full, remote-is-authoritative mirror pass. Returns
// the remote state observed (index.html included, keyed as indexFilename),
// for watchPull to diff future polls against.
func pullAll(c *client, dir string) (map[string]remoteFileState, error) {
	m, err := c.getManifest()
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	state := make(map[string]remoteFileState, len(m.Files)+1)

	if m.HasIndex {
		body, err := c.getIndex()
		if err != nil {
			return nil, fmt.Errorf("pull %s: %w", indexFilename, err)
		}
		if err := os.WriteFile(filepath.Join(dir, indexFilename), []byte(body), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", indexFilename, err)
		}
		state[indexFilename] = remoteFileState{size: m.Index.Size, modifiedAt: m.Index.ModifiedAt}
		log.Printf("pulled %s", indexFilename)
	}

	keep := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		body, err := c.getFile(f.Name)
		if err != nil {
			return nil, fmt.Errorf("pull %s: %w", f.Name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f.Name), body, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", f.Name, err)
		}
		state[f.Name] = remoteFileState{size: f.Size, modifiedAt: f.ModifiedAt}
		keep[f.Name] = true
		log.Printf("pulled %s", f.Name)
	}

	local, err := localSiblingFiles(dir)
	if err != nil {
		return nil, err
	}
	for name := range local {
		if keep[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return nil, fmt.Errorf("remove local %s: %w", name, err)
		}
		log.Printf("removed local %s (not present remotely)", name)
	}

	return state, nil
}

// watchPush polls dir every interval, forever (until ctx is cancelled),
// re-pushing any file that changed or is new since state and deleting
// remotely any local file that disappeared — plain polling rather than
// fsnotify, kept simple and dependency-free for what's normally a handful
// of files. A signal on reload (see watchStdinForReload) forces an
// immediate full pushAll pass instead of waiting for the next tick — for
// when you don't want to wait out the poll interval, or want to recover
// from state that's drifted for any reason by just re-pushing everything.
// Returns non-nil only when pushChanges' ping times out — see its doc
// comment — in which case this stops watching entirely rather than
// retrying forever; runSync surfaces that error and exits.
func watchPush(ctx context.Context, c *client, dir string, state map[string]localFileState, interval time.Duration, reload <-chan struct{}) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-reload:
			log.Printf("forcing a full push (R pressed)")
			newState, err := pushAll(c, dir)
			if err != nil {
				log.Printf("full push: %v", err)
				continue
			}
			state = newState
		case <-ticker.C:
			if err := pushChanges(c, dir, state); err != nil {
				return err
			}
		}
	}
}

// pushChanges pings the server (see below) and applies whatever local diffs
// have accumulated since state. Returns non-nil only when the ping times
// out — every other failure (a single file's read/push/delete, or a
// non-timeout ping error) is just logged and swallowed, same as before,
// since those are ordinary transient blips worth retrying next tick; a
// timeout specifically means the app genuinely isn't answering (see
// isTimeout's doc comment), which no amount of retrying fixes, so
// watchPush stops instead of looping forever against a dead server.
func pushChanges(c *client, dir string, state map[string]localFileState) error {
	// Push mode is local-authoritative, so nothing below actually needs the
	// manifest — but unlike pullChanges (which always fetches it as its own
	// poll), a tick with no local changes would otherwise touch the network
	// at all, silently: pushIndexIfChanged no-ops on an unchanged stat, and
	// both loops below no-op when entries already match state. Without this
	// call, the app's web server could go down while you're just watching
	// an idle folder and this loop would never notice — only a real edit
	// (or pressing R) would ever surface the failure. Fetching here instead
	// makes every tick a ping, matching pull mode's cadence.
	if _, err := c.getManifest(); err != nil {
		if isTimeout(err) {
			return fmt.Errorf("ping: %w", err)
		}
		log.Printf("ping: %v", err)
		return nil
	}

	pushIndexIfChanged(c, dir, state)

	entries, err := localSiblingFiles(dir)
	if err != nil {
		log.Printf("list local files: %v", err)
		return nil
	}
	for name, info := range entries {
		prev, seen := state[name]
		if seen && prev.size == info.Size() && prev.modTime.Equal(info.ModTime()) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			log.Printf("read %s: %v", name, err)
			continue
		}
		if err := c.postFile(name, body); err != nil {
			log.Printf("push %s: %v", name, err)
			continue
		}
		state[name] = localFileState{size: info.Size(), modTime: info.ModTime()}
		log.Printf("pushed %s (changed)", name)
	}
	// indexFilename is tracked in state too (see pushIndexIfChanged) but has
	// no delete route and isn't a candidate for "removed locally" cleanup —
	// only real sibling files (present in entries, keyed the same way) are.
	for name := range state {
		if name == indexFilename {
			continue
		}
		if _, ok := entries[name]; ok {
			continue
		}
		if err := c.deleteFile(name); err != nil {
			log.Printf("delete remote %s: %v", name, err)
			continue
		}
		delete(state, name)
		log.Printf("deleted remote %s (removed locally)", name)
	}
	return nil
}

// pushIndexIfChanged re-pushes index.html only if its size/mtime differ
// from what state last recorded — the same change-detection sibling files
// already get, rather than blindly re-posting it every single tick (which
// also meant a successful push never logged anything, silently, since
// there was no "changed" event to report).
func pushIndexIfChanged(c *client, dir string, state map[string]localFileState) {
	indexPath := filepath.Join(dir, indexFilename)
	if !fileExists(indexPath) {
		delete(state, indexFilename)
		return
	}
	info, err := os.Stat(indexPath)
	if err != nil {
		log.Printf("stat %s: %v", indexFilename, err)
		return
	}
	if prev, seen := state[indexFilename]; seen && prev.size == info.Size() && prev.modTime.Equal(info.ModTime()) {
		return
	}
	body, err := os.ReadFile(indexPath)
	if err != nil {
		log.Printf("read %s: %v", indexFilename, err)
		return
	}
	if err := c.postIndex(string(body)); err != nil {
		log.Printf("push %s: %v", indexFilename, err)
		return
	}
	state[indexFilename] = localFileState{size: info.Size(), modTime: info.ModTime()}
	log.Printf("pushed %s (changed)", indexFilename)
}

// watchPull polls the remote manifest every interval, forever (until ctx is
// cancelled), pulling any file that's new/changed since state and removing
// locally any file that dropped out of the manifest. A signal on reload
// (see watchStdinForReload) forces an immediate full pullAll pass instead
// of waiting for the next tick — see watchPush's identical reasoning.
// Returns non-nil only when pullChanges' manifest fetch (its own ping —
// see watchPush's doc comment) times out, in which case this stops
// watching entirely rather than retrying forever; runSync surfaces that
// error and exits.
func watchPull(ctx context.Context, c *client, dir string, state map[string]remoteFileState, interval time.Duration, reload <-chan struct{}) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-reload:
			log.Printf("forcing a full pull (R pressed)")
			newState, err := pullAll(c, dir)
			if err != nil {
				log.Printf("full pull: %v", err)
				continue
			}
			state = newState
		case <-ticker.C:
			if err := pullChanges(c, dir, state); err != nil {
				return err
			}
		}
	}
}

// pullChanges fetches the manifest (this doubles as pull mode's own ping —
// every tick already needs it, unlike push mode, which had to add one
// deliberately, see pushChanges) and applies whatever remote diffs have
// accumulated since state. Returns non-nil only when that fetch times out
// — every other failure is just logged and swallowed, same as before; see
// pushChanges' identical reasoning for why a timeout specifically is
// different.
func pullChanges(c *client, dir string, state map[string]remoteFileState) error {
	m, err := c.getManifest()
	if err != nil {
		if isTimeout(err) {
			return fmt.Errorf("ping: %w", err)
		}
		log.Printf("fetch manifest: %v", err)
		return nil
	}

	if !m.HasIndex {
		delete(state, indexFilename)
	} else {
		prev, seen := state[indexFilename]
		if !seen || prev.size != m.Index.Size || prev.modifiedAt != m.Index.ModifiedAt {
			body, err := c.getIndex()
			if err != nil {
				log.Printf("pull %s: %v", indexFilename, err)
			} else if err := os.WriteFile(filepath.Join(dir, indexFilename), []byte(body), 0o644); err != nil {
				log.Printf("write %s: %v", indexFilename, err)
			} else {
				state[indexFilename] = remoteFileState{size: m.Index.Size, modifiedAt: m.Index.ModifiedAt}
				log.Printf("pulled %s (changed)", indexFilename)
			}
		}
	}

	seen := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		seen[f.Name] = true
		prev, ok := state[f.Name]
		if ok && prev.size == f.Size && prev.modifiedAt == f.ModifiedAt {
			continue
		}
		body, err := c.getFile(f.Name)
		if err != nil {
			log.Printf("pull %s: %v", f.Name, err)
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, f.Name), body, 0o644); err != nil {
			log.Printf("write %s: %v", f.Name, err)
			continue
		}
		state[f.Name] = remoteFileState{size: f.Size, modifiedAt: f.ModifiedAt}
		log.Printf("pulled %s (changed)", f.Name)
	}
	// indexFilename is tracked in state too (handled above) but was never
	// part of m.Files/seen — only real sibling files should be considered
	// for "removed remotely" local cleanup.
	for name := range state {
		if name == indexFilename || seen[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			log.Printf("remove local %s: %v", name, err)
			continue
		}
		delete(state, name)
		log.Printf("removed local %s (removed remotely)", name)
	}
	return nil
}

// warnedSkips tracks which skip warnings localSiblingFiles has already
// logged (keyed by name), across the whole run — since it's called on every
// push/pull poll tick, without this a stray subdirectory or invalid filename
// left sitting in dir would otherwise log the same warning forever, once per
// tick, for as long as it stays there. Read/written from a single goroutine
// only (runSync never runs watchPush/watchPull concurrently with each
// other), so no locking is needed.
var warnedSkips = make(map[string]bool)

// localSiblingFiles lists dir's flat, valid sibling files (excluding
// index.html, which pushAll/pullAll/pushChanges handle separately) —
// subdirectories and invalid filenames are warned about once (not on every
// poll, see warnedSkips) and skipped, since the API has no concept of either
// (see isValidSiblingFilename's doc comment). A .git directory is skipped
// silently, with no warning — it's expected noise when syncing a
// version-controlled folder.
func localSiblingFiles(dir string) (map[string]os.FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	result := make(map[string]os.FileInfo, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			// .git is almost always present when syncing a version-controlled
			// folder and is never something you'd want pushed — skip it
			// silently rather than warning about it on every poll.
			if name == ".git" {
				continue
			}
			if !warnedSkips[name] {
				warnedSkips[name] = true
				log.Printf("skipping %s: subdirectories aren't supported by the API (flat files only)", name)
			}
			continue
		}
		if name == indexFilename {
			continue
		}
		if !isValidSiblingFilename(name) {
			if !warnedSkips[name] {
				warnedSkips[name] = true
				log.Printf("skipping %s: not a valid sibling filename", name)
			}
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", name, err)
		}
		result[name] = info
	}
	return result, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
