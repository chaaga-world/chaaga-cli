package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeApp is a minimal in-memory stand-in for AppServerService's per-app
// state (app/lib/services/api/app_server.dart) — just enough to exercise
// mirror.go's push/pull logic against a real *http.Client, without a phone.
// Every write gets a fresh, distinct stamp (unlike a naive fake that reuses
// one constant "modifiedAt" for everything) — the real server's modifiedAt
// always changes on a real write, and mirror.go's change-detection relies
// on that; a fake that doesn't honor it would pass tests that a real
// (identical-size-but-different-content) edit could still fail against.
type fakeApp struct {
	mu             sync.Mutex
	index          *string
	indexStamp     string
	files          map[string][]byte
	fileStamps     map[string]string
	nextStamp      int
	postIndexCalls int
	manifestCalls  int
}

func (a *fakeApp) stamp() string {
	a.nextStamp++
	return fmt.Sprintf("stamp-%d", a.nextStamp)
}

func newFakeServer(t *testing.T, app *fakeApp) *httptest.Server {
	t.Helper()
	if app.fileStamps == nil {
		app.fileStamps = make(map[string]string, len(app.files))
		for name := range app.files {
			app.fileStamps[name] = app.stamp()
		}
	}
	if app.index != nil && app.indexStamp == "" {
		app.indexStamp = app.stamp()
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /apps/1/manifest", func(w http.ResponseWriter, r *http.Request) {
		app.mu.Lock()
		defer app.mu.Unlock()
		app.manifestCalls++
		m := manifest{HasIndex: app.index != nil}
		if app.index != nil {
			m.Index = &manifestIndexEntry{Size: int64(len(*app.index)), ModifiedAt: app.indexStamp}
		}
		for name, body := range app.files {
			m.Files = append(m.Files, manifestEntry{Name: name, Size: int64(len(body)), ModifiedAt: app.fileStamps[name]})
		}
		_ = json.NewEncoder(w).Encode(m)
	})

	mux.HandleFunc("GET /apps/1", func(w http.ResponseWriter, r *http.Request) {
		app.mu.Lock()
		defer app.mu.Unlock()
		if app.index == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(*app.index))
	})
	mux.HandleFunc("POST /apps/1", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		app.mu.Lock()
		s := string(body)
		app.index = &s
		app.indexStamp = app.stamp()
		app.postIndexCalls++
		app.mu.Unlock()
		_, _ = w.Write([]byte(`{"shortId":1,"updatedAt":"stamp"}`))
	})

	mux.HandleFunc("GET /apps/1/{filename}", func(w http.ResponseWriter, r *http.Request) {
		app.mu.Lock()
		defer app.mu.Unlock()
		body, ok := app.files[r.PathValue("filename")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("POST /apps/1/{filename}", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		name := r.PathValue("filename")
		app.mu.Lock()
		app.files[name] = body
		app.fileStamps[name] = app.stamp()
		app.mu.Unlock()
		_, _ = w.Write([]byte(`{"shortId":1,"updatedAt":"stamp"}`))
	})
	mux.HandleFunc("DELETE /apps/1/{filename}", func(w http.ResponseWriter, r *http.Request) {
		app.mu.Lock()
		defer app.mu.Unlock()
		name := r.PathValue("filename")
		if _, ok := app.files[name]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(app.files, name)
		delete(app.fileStamps, name)
		_, _ = w.Write([]byte(`{"shortId":1,"updatedAt":"stamp"}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(srv *httptest.Server) *client {
	return &client{baseURL: srv.URL + "/apps/1", http: srv.Client()}
}

func TestPushAllUploadsLocalFilesAndDeletesRemoteOnlyOnes(t *testing.T) {
	app := &fakeApp{files: map[string][]byte{"stale.css": []byte("old")}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.html"), "<html>hi</html>")
	mustWrite(t, filepath.Join(dir, "style.css"), "body {}")

	state, err := pushAll(c, dir)
	if err != nil {
		t.Fatalf("pushAll: %v", err)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.index == nil || *app.index != "<html>hi</html>" {
		t.Errorf("index = %v, want pushed html", app.index)
	}
	if string(app.files["style.css"]) != "body {}" {
		t.Errorf("style.css = %q, want pushed content", app.files["style.css"])
	}
	if _, ok := app.files["stale.css"]; ok {
		t.Errorf("stale.css should have been deleted remotely (not present locally)")
	}
	if _, ok := state["style.css"]; !ok {
		t.Errorf("returned state should track style.css")
	}
}

func TestPullAllDownloadsRemoteFilesAndRemovesLocalOnlyOnes(t *testing.T) {
	index := "<html>from app</html>"
	app := &fakeApp{index: &index, files: map[string][]byte{"app.js": []byte("console.log(1)")}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "local-only.css"), "should be removed")

	state, err := pullAll(c, dir)
	if err != nil {
		t.Fatalf("pullAll: %v", err)
	}

	if got := mustRead(t, filepath.Join(dir, "index.html")); got != index {
		t.Errorf("local index.html = %q, want %q", got, index)
	}
	if got := mustRead(t, filepath.Join(dir, "app.js")); got != "console.log(1)" {
		t.Errorf("local app.js = %q, want pulled content", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "local-only.css")); !os.IsNotExist(err) {
		t.Errorf("local-only.css should have been removed (not present remotely)")
	}
	if _, ok := state["app.js"]; !ok {
		t.Errorf("returned state should track app.js")
	}
}

func TestPushChangesOnlyUploadsWhatChanged(t *testing.T) {
	app := &fakeApp{files: map[string][]byte{}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.css"), "a")
	mustWrite(t, filepath.Join(dir, "b.css"), "b")
	state, err := pushAll(c, dir)
	if err != nil {
		t.Fatalf("pushAll: %v", err)
	}

	// Change only a.css; remove b.css entirely.
	mustWrite(t, filepath.Join(dir, "a.css"), "a-changed")
	if err := os.Remove(filepath.Join(dir, "b.css")); err != nil {
		t.Fatalf("remove b.css: %v", err)
	}

	pushChanges(c, dir, state)

	app.mu.Lock()
	defer app.mu.Unlock()
	if string(app.files["a.css"]) != "a-changed" {
		t.Errorf("a.css = %q, want a-changed", app.files["a.css"])
	}
	if _, ok := app.files["b.css"]; ok {
		t.Errorf("b.css should have been deleted remotely after local removal")
	}
}

// Regression test for the bug reported after the tool shipped: index.html
// edits during watch mode weren't reliably reaching the phone. pushChanges
// used to re-POST index.html unconditionally on every single tick — this
// confirms it's now change-detected like every other file (and, via
// TestPushChangesDoesNotRepostUnchangedIndexHtml below, that an unchanged
// index.html is *not* re-posted every tick).
func TestPushChangesDetectsIndexHtmlChange(t *testing.T) {
	app := &fakeApp{files: map[string][]byte{}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.html"), "<html>v1</html>")
	state, err := pushAll(c, dir)
	if err != nil {
		t.Fatalf("pushAll: %v", err)
	}

	mustWrite(t, filepath.Join(dir, "index.html"), "<html>v2</html>")
	pushChanges(c, dir, state)

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.index == nil || *app.index != "<html>v2</html>" {
		t.Errorf("index = %v, want the changed content pushed", app.index)
	}
}

func TestPushChangesDoesNotRepostUnchangedIndexHtml(t *testing.T) {
	app := &fakeApp{files: map[string][]byte{}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.html"), "<html>same</html>")
	state, err := pushAll(c, dir)
	if err != nil {
		t.Fatalf("pushAll: %v", err)
	}

	app.mu.Lock()
	callsAfterInitialPush := app.postIndexCalls
	app.mu.Unlock()

	pushChanges(c, dir, state)
	pushChanges(c, dir, state)

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.postIndexCalls != callsAfterInitialPush {
		t.Errorf("postIndexCalls = %d, want unchanged from %d (index.html never changed)",
			app.postIndexCalls, callsAfterInitialPush)
	}
}

// Regression test for the gap reported after the reload work: a push-mode
// watch tick with nothing changed locally used to skip the network
// entirely (pushIndexIfChanged no-ops on an unchanged stat, and both loops
// below it no-op when entries already match state) — so a dead app web
// server went undetected indefinitely as long as you weren't actively
// editing. pushChanges now fetches the manifest unconditionally, purely as
// a ping, before doing any of that local-change-detection work.
func TestPushChangesPingsServerEvenWithNoLocalChanges(t *testing.T) {
	app := &fakeApp{files: map[string][]byte{}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.css"), "a")
	state, err := pushAll(c, dir)
	if err != nil {
		t.Fatalf("pushAll: %v", err)
	}

	app.mu.Lock()
	callsAfterInitialPush := app.manifestCalls
	app.mu.Unlock()

	pushChanges(c, dir, state) // nothing local changed

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.manifestCalls != callsAfterInitialPush+1 {
		t.Errorf("manifestCalls = %d, want %d (a ping even though nothing changed)",
			app.manifestCalls, callsAfterInitialPush+1)
	}
}

func TestPushChangesLogsAndReturnsWhenServerUnreachable(t *testing.T) {
	app := &fakeApp{files: map[string][]byte{}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.css"), "a")
	state, err := pushAll(c, dir)
	if err != nil {
		t.Fatalf("pushAll: %v", err)
	}
	srv.Close() // simulate the app web server going down

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	err = pushChanges(c, dir, state)

	if !strings.Contains(logs.String(), "ping:") {
		t.Errorf("expected a logged ping failure, got %q", logs.String())
	}
	// Connection refused isn't a timeout — the server is just gone, not
	// hanging — so this shouldn't be treated as fatal; see
	// TestPushChangesReturnsErrorWhenPingTimesOut for the case that is.
	if err != nil {
		t.Errorf("pushChanges returned %v, want nil (not a timeout)", err)
	}
}

// Regression test for the follow-up request: a ping that outright fails
// (connection refused, above) is routine and gets retried next tick, but a
// ping that times out means the app genuinely isn't answering (see
// isTimeout's doc comment in client.go) — pushChanges should report that as
// fatal so watchPush stops instead of retrying forever.
func TestPushChangesReturnsErrorWhenPingTimesOut(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /apps/1/manifest", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // longer than the client timeout set below
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newTestClient(srv)
	c.setTimeout(10 * time.Millisecond)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.css"), "a")

	err := pushChanges(c, dir, map[string]localFileState{})

	if err == nil {
		t.Fatal("expected a non-nil error when the ping times out")
	}
	if !isTimeout(err) {
		t.Errorf("isTimeout(err) = false, want true (err=%v)", err)
	}
}

func TestPullChangesReturnsErrorWhenPingTimesOut(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /apps/1/manifest", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // longer than the client timeout set below
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newTestClient(srv)
	c.setTimeout(10 * time.Millisecond)

	dir := t.TempDir()

	err := pullChanges(c, dir, map[string]remoteFileState{})

	if err == nil {
		t.Fatal("expected a non-nil error when the ping times out")
	}
	if !isTimeout(err) {
		t.Errorf("isTimeout(err) = false, want true (err=%v)", err)
	}
}

func TestPullChangesDetectsIndexHtmlChange(t *testing.T) {
	index := "<html>v1</html>"
	app := &fakeApp{index: &index, files: map[string][]byte{}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	state, err := pullAll(c, dir)
	if err != nil {
		t.Fatalf("pullAll: %v", err)
	}

	app.mu.Lock()
	updated := "<html>v2</html>"
	app.index = &updated
	app.indexStamp = app.stamp() // a real write always changes modifiedAt too
	app.mu.Unlock()

	pullChanges(c, dir, state)

	if got := mustRead(t, filepath.Join(dir, "index.html")); got != "<html>v2</html>" {
		t.Errorf("local index.html = %q, want the changed content pulled", got)
	}
}

func TestPullChangesOnlyPullsWhatChanged(t *testing.T) {
	app := &fakeApp{files: map[string][]byte{"a.css": []byte("a"), "b.css": []byte("b")}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)

	dir := t.TempDir()
	state, err := pullAll(c, dir)
	if err != nil {
		t.Fatalf("pullAll: %v", err)
	}

	app.mu.Lock()
	app.files["a.css"] = []byte("a-changed")
	app.fileStamps["a.css"] = app.stamp() // a real write always changes modifiedAt too
	delete(app.files, "b.css")
	app.mu.Unlock()

	pullChanges(c, dir, state)

	if got := mustRead(t, filepath.Join(dir, "a.css")); got != "a-changed" {
		t.Errorf("local a.css = %q, want a-changed", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.css")); !os.IsNotExist(err) {
		t.Errorf("b.css should have been removed locally after remote deletion")
	}
}

func TestLocalSiblingFilesSkipsSubdirsAndInvalidNames(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.html"), "<html></html>")
	mustWrite(t, filepath.Join(dir, "good.css"), "ok")
	mustWrite(t, filepath.Join(dir, "bad name.css"), "invalid filename")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	entries, err := localSiblingFiles(dir)
	if err != nil {
		t.Fatalf("localSiblingFiles: %v", err)
	}

	if _, ok := entries["index.html"]; ok {
		t.Errorf("index.html should be excluded (handled separately)")
	}
	if _, ok := entries["good.css"]; !ok {
		t.Errorf("good.css should be included")
	}
	if _, ok := entries["bad name.css"]; ok {
		t.Errorf("invalid filename should be skipped")
	}
	if _, ok := entries["subdir"]; ok {
		t.Errorf("subdirectory should be skipped")
	}
	if _, ok := entries[".git"]; ok {
		t.Errorf(".git directory should be skipped")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
