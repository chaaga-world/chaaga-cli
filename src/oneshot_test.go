package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePhone stands in for AppServerService with several apps, the /apps
// routes and the X-Chaaga-App-* identity headers — enough to drive the
// one-shot commands end to end.
type fakePhone struct {
	mu    sync.Mutex
	apps  map[int]*phoneApp
	stamp int
	uids  int
}

type phoneApp struct {
	uid, name, emoji string
	index            *string
	indexStamp       string
	files            map[string]string
	stamps           map[string]string
}

func (p *fakePhone) nextStamp() string {
	p.stamp++
	return fmt.Sprintf("stamp-%d", p.stamp)
}

// add creates app id with the given files ("index.html" becomes the index).
func (p *fakePhone) add(id int, name string, files map[string]string) *phoneApp {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.uids++
	app := &phoneApp{uid: fmt.Sprintf("uid-%d", p.uids), name: name, emoji: "📱",
		files: map[string]string{}, stamps: map[string]string{}}
	for n, body := range files {
		p.write(app, n, body)
	}
	p.apps[id] = app
	return app
}

func (p *fakePhone) write(app *phoneApp, name, body string) {
	if name == indexFilename {
		app.index = &body
		app.indexStamp = p.nextStamp()
		return
	}
	app.files[name] = body
	app.stamps[name] = p.nextStamp()
}

func (p *fakePhone) get(id int) *phoneApp {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.apps[id]
}

func (p *fakePhone) edit(id int, name, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.write(p.apps[id], name, body)
}

func (a *phoneApp) info(id int) appInfo {
	return appInfo{ShortID: id, UID: a.uid, Name: a.name, Emoji: a.emoji, UpdatedAt: "now"}
}

func newFakePhone(t *testing.T) (*fakePhone, *httptest.Server) {
	t.Helper()
	p := &fakePhone{apps: map[int]*phoneApp{}}
	srv := httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(srv.Close)
	return p, srv
}

func (p *fakePhone) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	writeJSON := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}

	if len(parts) == 1 && parts[0] == "apps" {
		switch r.Method {
		case http.MethodGet:
			list := []appInfo{}
			for id := 1; id <= 100; id++ {
				if app, ok := p.apps[id]; ok {
					list = append(list, app.info(id))
				}
			}
			writeJSON(http.StatusOK, list)
		case http.MethodPost:
			var body struct{ Name, Emoji string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			id := 1
			for existing := range p.apps {
				if existing >= id {
					id = existing + 1
				}
			}
			p.uids++
			app := &phoneApp{uid: fmt.Sprintf("uid-%d", p.uids), name: body.Name, emoji: body.Emoji,
				files: map[string]string{}, stamps: map[string]string{}}
			p.apps[id] = app
			writeJSON(http.StatusCreated, app.info(id))
		}
		return
	}

	id, _ := strconv.Atoi(parts[1])
	app, ok := p.apps[id]
	if !ok {
		writeJSON(http.StatusNotFound, map[string]string{"error": "unknown app id"})
		return
	}
	w.Header().Set("X-Chaaga-App-Id", strconv.Itoa(id))
	w.Header().Set("X-Chaaga-App-Uid", app.uid)
	w.Header().Set("X-Chaaga-App-Name", url.PathEscape(app.name))
	filename := ""
	if len(parts) == 3 && parts[2] != indexFilename {
		filename = parts[2]
	}
	ok200 := func() { writeJSON(http.StatusOK, app.info(id)) }

	switch r.Method {
	case http.MethodGet:
		switch filename {
		case "manifest":
			m := manifest{ShortID: id, UID: app.uid, Name: app.name, HasIndex: app.index != nil, Files: []manifestEntry{}}
			if app.index != nil {
				m.Index = &manifestIndexEntry{Size: int64(len(*app.index)), ModifiedAt: app.indexStamp}
			}
			for n, body := range app.files {
				m.Files = append(m.Files, manifestEntry{Name: n, Size: int64(len(body)), ModifiedAt: app.stamps[n]})
			}
			writeJSON(http.StatusOK, m)
		case "":
			if app.index == nil {
				writeJSON(http.StatusNotFound, nil)
				return
			}
			_, _ = io.WriteString(w, *app.index)
		default:
			body, ok := app.files[filename]
			if !ok {
				writeJSON(http.StatusNotFound, nil)
				return
			}
			_, _ = io.WriteString(w, body)
		}
	case http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		name := filename
		if name == "" {
			name = indexFilename
		}
		p.write(app, name, string(body))
		ok200()
	case http.MethodDelete:
		delete(app.files, filename)
		delete(app.stamps, filename)
		ok200()
	case http.MethodPatch:
		var body struct{ Name, Emoji string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Name != "" {
			app.name = body.Name
		}
		if body.Emoji != "" {
			app.emoji = body.Emoji
		}
		w.Header().Set("X-Chaaga-App-Name", url.PathEscape(app.name))
		ok200()
	}
}

// setupCLI points the global config at a temp dir and saves srv as the
// phone host; returns a fresh folder to work in.
func setupCLI(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)
	if srv != nil {
		if _, err := savePhoneHost(normalizeHost(srv.URL)); err != nil {
			t.Fatalf("save host: %v", err)
		}
	}
	return filepath.Join(t.TempDir(), "my-app")
}

func mustRun(t *testing.T, run func([]string) error, args ...string) {
	t.Helper()
	if err := run(args); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
}

func mustLink(t *testing.T, dir string) *folderLink {
	t.Helper()
	link, err := loadLink(dir)
	if err != nil {
		t.Fatalf("load link: %v", err)
	}
	return link
}

func TestNewCreatesAppLinksFolderAndPushesFiles(t *testing.T) {
	phone, srv := newFakePhone(t)
	phone.add(1, "Existing", nil)
	dir := setupCLI(t, srv)
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, indexFilename), "<html>game</html>")
	mustWrite(t, filepath.Join(dir, "style.css"), "body {}")

	mustRun(t, runNew, dir, "My Game", "🎮")

	app := phone.get(2)
	if app == nil || app.name != "My Game" || app.emoji != "🎮" {
		t.Fatalf("app 2 = %+v", app)
	}
	if *app.index != "<html>game</html>" || app.files["style.css"] != "body {}" {
		t.Errorf("files not pushed: %+v", app)
	}
	if link := mustLink(t, dir); *link != (folderLink{AppID: 2, UID: app.uid, Name: "My Game"}) {
		t.Errorf("link = %+v", link)
	}
	if state := loadState(dir); len(state) != 2 {
		t.Errorf("state = %+v, want index.html and style.css", state)
	}
	if _, ok := app.files[linkFilename]; ok {
		t.Errorf("%s must not be pushed", linkFilename)
	}
}

func TestNewRefusesAnAlreadyLinkedFolder(t *testing.T) {
	_, srv := newFakePhone(t)
	dir := setupCLI(t, srv)
	mustRun(t, runNew, dir, "A")

	if err := runNew([]string{dir, "B"}); err == nil || !strings.Contains(err.Error(), "already linked") {
		t.Errorf("err = %v, want already linked", err)
	}
}

func TestLinkWritesOnlyTheConfigThenPullMirrorsThePhone(t *testing.T) {
	phone, srv := newFakePhone(t)
	app := phone.add(2, "Zombies", map[string]string{indexFilename: "<html>z</html>", "game.js": "go()"})
	dir := setupCLI(t, srv)
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "local-only.js"), "mine")

	mustRun(t, runLink, dir, "2")

	if mustRead(t, filepath.Join(dir, "local-only.js")) != "mine" {
		t.Errorf("link must not touch files")
	}
	if fileExists(filepath.Join(dir, "game.js")) {
		t.Errorf("link must not pull files")
	}
	if link := mustLink(t, dir); *link != (folderLink{AppID: 2, UID: app.uid, Name: "Zombies"}) {
		t.Errorf("link = %+v", link)
	}

	mustRun(t, runPull, dir)

	if mustRead(t, filepath.Join(dir, "game.js")) != "go()" || mustRead(t, filepath.Join(dir, indexFilename)) != "<html>z</html>" {
		t.Errorf("pull did not mirror the phone")
	}
	if fileExists(filepath.Join(dir, "local-only.js")) {
		t.Errorf("pull should remove files the phone doesn't have")
	}
	if !fileExists(filepath.Join(dir, linkFilename)) || len(loadState(dir)) != 2 {
		t.Errorf("pull must keep %s and record %s", linkFilename, stateFilename)
	}
}

func TestRelinkingToAnotherAppDropsTheBaseline(t *testing.T) {
	phone, srv := newFakePhone(t)
	phone.add(1, "A", map[string]string{"a.js": "a"})
	phone.add(2, "B", map[string]string{"b.js": "b"})
	dir := setupCLI(t, srv)
	mustRun(t, runLink, dir, "1")
	mustRun(t, runPull, dir)

	mustRun(t, runLink, dir, "2")

	if loadState(dir) != nil {
		t.Errorf("baseline from app 1 should be dropped")
	}
	if err := runPush([]string{dir}); !errors.Is(err, errConflict) {
		t.Errorf("push without a baseline = %v, want conflict", err)
	}
	if phone.get(2).files["b.js"] != "b" {
		t.Errorf("phone app 2 must be untouched")
	}
}

func TestLinkRejectsABadID(t *testing.T) {
	_, srv := newFakePhone(t)
	dir := setupCLI(t, srv)
	for _, id := range []string{"zero", "0", "-3"} {
		if err := runLink([]string{dir, id}); err == nil {
			t.Errorf("link %q should fail", id)
		}
	}
}

func TestPullRequiresALinkedFolder(t *testing.T) {
	_, srv := newFakePhone(t)
	dir := setupCLI(t, srv)
	mustMkdir(t, dir)

	err := runPull([]string{dir})
	if err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Errorf("err = %v, want not linked", err)
	}
}

func TestPushRefusesAfterAPhoneEditAndForceOverrides(t *testing.T) {
	phone, srv := newFakePhone(t)
	phone.add(1, "A", map[string]string{indexFilename: "v1", "style.css": "s1"})
	dir := setupCLI(t, srv)
	mustRun(t, runLink, dir, "1")
	mustRun(t, runPull, dir)

	phone.edit(1, "style.css", "phone edit")
	mustWrite(t, filepath.Join(dir, indexFilename), "local edit")

	if err := runPush([]string{dir}); !errors.Is(err, errConflict) || exitCode(err) != 3 {
		t.Fatalf("push = %v, want conflict (exit 3)", err)
	}
	if *phone.get(1).index != "v1" {
		t.Errorf("a refused push must not write")
	}

	mustRun(t, runPush, dir, "--force")

	if *phone.get(1).index != "local edit" || phone.get(1).files["style.css"] != "s1" {
		t.Errorf("forced push should mirror the folder: %+v", phone.get(1))
	}
	if err := runStatus([]string{dir}); err != nil {
		t.Errorf("status after push = %v, want up to date", err)
	}
}

func TestRepeatedPushesDontReportTheirOwnChangesAsConflicts(t *testing.T) {
	phone, srv := newFakePhone(t)
	phone.add(1, "A", map[string]string{indexFilename: "v1"})
	dir := setupCLI(t, srv)
	mustRun(t, runLink, dir, "1")
	mustRun(t, runPull, dir)

	for i, body := range []string{"v2", "v3"} {
		mustWrite(t, filepath.Join(dir, indexFilename), body)
		if err := runPush([]string{dir}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	if *phone.get(1).index != "v3" {
		t.Errorf("index = %q", *phone.get(1).index)
	}
}

func TestStatusReportsEachSideAndConflicts(t *testing.T) {
	phone, srv := newFakePhone(t)
	phone.add(1, "A", map[string]string{indexFilename: "v1", "a.js": "a", "b.js": "b"})
	dir := setupCLI(t, srv)
	mustRun(t, runLink, dir, "1")
	mustRun(t, runPull, dir)

	mustWrite(t, filepath.Join(dir, "a.js"), "local a")
	phone.edit(1, "b.js", "phone b")
	if err := runStatus([]string{dir}); err != nil {
		t.Fatalf("separate edits are not a conflict: %v", err)
	}
	cs, err := diffState(mustLinkedClient(t, dir), dir, loadState(dir))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cs.Local) != "[{a.js modified}]" || fmt.Sprint(cs.Remote) != "[{b.js modified}]" {
		t.Errorf("changes = %+v", cs)
	}

	mustWrite(t, filepath.Join(dir, "b.js"), "local b")
	if err := runStatus([]string{dir}); !errors.Is(err, errConflict) {
		t.Errorf("status = %v, want conflict on b.js", err)
	}
}

func TestStaleAppIDFollowsTheAppToItsNewID(t *testing.T) {
	phone, srv := newFakePhone(t)
	original := phone.add(2, "Zombies", map[string]string{indexFilename: "z"})
	dir := setupCLI(t, srv)
	mustRun(t, runLink, dir, "2")

	// The app moves to id 5 and an unrelated app takes id 2.
	phone.mu.Lock()
	delete(phone.apps, 2)
	phone.apps[5] = original
	phone.mu.Unlock()
	phone.add(2, "Recipes", map[string]string{indexFilename: "recipes"})

	mustRun(t, runPull, dir)

	if link := mustLink(t, dir); link.AppID != 5 || link.UID != original.uid {
		t.Errorf("link = %+v, want appId 5", link)
	}
	if mustRead(t, filepath.Join(dir, indexFilename)) != "z" {
		t.Errorf("pulled the wrong app")
	}
}

func TestDeletedAppStopsWithExit4AndWritesNothing(t *testing.T) {
	phone, srv := newFakePhone(t)
	phone.add(2, "Zombies", map[string]string{indexFilename: "z"})
	dir := setupCLI(t, srv)
	mustRun(t, runLink, dir, "2")
	mustRun(t, runPull, dir)
	mustWrite(t, filepath.Join(dir, indexFilename), "local z")

	// Deleted, and a new app with the very same name reuses id 2.
	phone.mu.Lock()
	delete(phone.apps, 2)
	phone.mu.Unlock()
	phone.add(2, "Zombies", map[string]string{indexFilename: "imposter"})

	for _, run := range []func([]string) error{runPush, runPull, runStatus} {
		err := run([]string{dir})
		if !isAppGone(err) || exitCode(err) != 4 {
			t.Errorf("err = %v, want app gone (exit 4)", err)
		}
	}
	if err := runRename([]string{dir, "New"}); !isAppGone(err) {
		t.Errorf("rename err = %v, want app gone", err)
	}
	if *phone.get(2).index != "imposter" || phone.get(2).name != "Zombies" {
		t.Errorf("the other app must be untouched")
	}
	if mustRead(t, filepath.Join(dir, indexFilename)) != "local z" {
		t.Errorf("local files must be untouched")
	}
}

func TestAPhoneSideRenameJustUpdatesTheLabel(t *testing.T) {
	phone, srv := newFakePhone(t)
	phone.add(1, "Old", map[string]string{indexFilename: "x"})
	dir := setupCLI(t, srv)
	mustRun(t, runLink, dir, "1")

	phone.mu.Lock()
	phone.apps[1].name = "Café 🎉"
	phone.mu.Unlock()
	mustRun(t, runPull, dir)

	if link := mustLink(t, dir); link.Name != "Café 🎉" {
		t.Errorf("name = %q", link.Name)
	}
}

func TestRenameUpdatesThePhoneAndTheLink(t *testing.T) {
	phone, srv := newFakePhone(t)
	phone.add(1, "Old", nil)
	dir := setupCLI(t, srv)
	mustRun(t, runLink, dir, "1")

	mustRun(t, runRename, dir, "My Game", "🎮")

	if app := phone.get(1); app.name != "My Game" || app.emoji != "🎮" {
		t.Errorf("phone app = %+v", app)
	}
	if link := mustLink(t, dir); link.Name != "My Game" {
		t.Errorf("link name = %q", link.Name)
	}
	mustRun(t, runPull, dir)
}

func TestPositionalArgumentCounts(t *testing.T) {
	_, srv := newFakePhone(t)
	dir := setupCLI(t, srv)
	cases := []struct {
		run  func([]string) error
		args []string
	}{
		{runNew, []string{dir}},
		{runNew, []string{dir, "a", "b", "c"}},
		{runRename, []string{dir}},
		{runRename, []string{dir, "a", "b", "c"}},
		{runLink, []string{dir}},
		{runPull, []string{dir, "3"}},
		{runPush, []string{}},
		{runApps, []string{"extra"}},
	}
	for _, c := range cases {
		if err := c.run(c.args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: err = %v, want a usage error", c.args, err)
		}
	}
}

func TestConnectNormalizesAndSavesTheHost(t *testing.T) {
	_, srv := newFakePhone(t)
	setupCLI(t, nil)

	mustRun(t, runConnect, srv.URL+"/")

	host, err := loadPhoneHost()
	if err != nil || host != strings.TrimPrefix(srv.URL, "http://") {
		t.Errorf("host = %q, %v", host, err)
	}
	if got := normalizeHost("192.168.1.23"); got != "192.168.1.23:8787" {
		t.Errorf("normalizeHost = %q", got)
	}
	if got := normalizeHost(" 192.168.1.23:9000 "); got != "192.168.1.23:9000" {
		t.Errorf("normalizeHost = %q", got)
	}
}

func TestCommandsNeedAHost(t *testing.T) {
	dir := setupCLI(t, nil)
	if err := runPull([]string{dir}); !errors.Is(err, errNoHost) {
		t.Errorf("pull err = %v, want errNoHost", err)
	}
	if err := runApps(nil); !errors.Is(err, errNoHost) {
		t.Errorf("apps err = %v, want errNoHost", err)
	}
}

func TestSyncRejectsLegacyFlagsAndUnlinkedFolders(t *testing.T) {
	_, srv := newFakePhone(t)
	dir := setupCLI(t, srv)

	for _, args := range [][]string{{dir, "-h", "1.2.3.4"}, {dir, "-a", "3"}, {dir, "--host=1.2.3.4"}} {
		if err := runSync(args); err == nil || !strings.Contains(err.Error(), "no longer supported") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	if err := runSync([]string{dir}); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Errorf("err = %v, want not linked", err)
	}
}

func TestWatchPushRunsOnChangeOnlyAfterAChange(t *testing.T) {
	app := &fakeApp{files: map[string][]byte{}}
	srv := newFakeServer(t, app)
	c := newTestClient(srv)
	dir := t.TempDir()
	state, err := pushAll(c, dir)
	if err != nil {
		t.Fatal(err)
	}

	changes := make(chan struct{}, 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- watchPush(ctx, c, dir, state, 10*time.Millisecond, nil, func() { changes <- struct{}{} })
	}()

	time.Sleep(50 * time.Millisecond)
	if len(changes) != 0 {
		t.Errorf("onChange ran without a change")
	}
	mustWrite(t, filepath.Join(dir, "new.js"), "x")
	select {
	case <-changes:
	case <-time.After(2 * time.Second):
		t.Errorf("onChange never ran after a local change")
	}
	cancel()
	<-done
}

func mustLinkedClient(t *testing.T, dir string) *client {
	t.Helper()
	c, _, err := linkedClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
