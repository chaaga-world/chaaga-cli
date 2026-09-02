package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// initialRequestTimeout is generous enough to cover the on-device approval
// dialog (up to 2 minutes — see app/specs/api.md's own "curl --max-time
// 130" guidance) — used for pushAll/pullAll, the first calls this CLI ever
// makes, before the caller's IP is known to be approved.
const initialRequestTimeout = 150 * time.Second

// watchRequestTimeout is used for every request once the watch loop starts
// — by then approval is already established (pushAll/pullAll already
// succeeded), so there's no reason to wait anywhere near
// initialRequestTimeout for a reply. Kept short instead: the watch loop is
// fully sequential, so one stalled call (e.g. Chaaga briefly losing
// foreground — the API server "stops responding as soon as Chaaga is
// backgrounded," per specs/api.md — mid-request) would otherwise block the
// *entire* tick cycle, silently, for up to initialRequestTimeout before the
// next attempt even starts. 3s keeps that stall well under the push/pull
// poll interval (1s/2s — see pushPollInterval/pullPollInterval) so a
// dropped connection is retried at roughly that same cadence instead of
// going quiet for up to 15s, while still leaving comfortable margin over a
// normal LAN round-trip.
const watchRequestTimeout = 3 * time.Second

// setTimeout adjusts how long future requests wait for a reply — see
// initialRequestTimeout/watchRequestTimeout's doc comments for when each is
// used.
func (c *client) setTimeout(d time.Duration) {
	c.http.Timeout = d
}

// errNotApproved distinguishes "the phone hasn't approved this IP yet (or
// denied it)" from a generic request failure, so callers can print an
// actionable message instead of a raw HTTP error.
var errNotApproved = errors.New(`connection not approved on device — open Chaaga and approve the "Allow API connection?" prompt`)

// isTimeout reports whether err is (or wraps) an HTTP client timeout — e.g.
// watchRequestTimeout being exceeded because the app server accepted the
// connection but never replied (matches specs/api.md: "the API server
// stops responding as soon as Chaaga is backgrounded"). http.Client always
// wraps a failed request in a *url.Error, which implements net.Error, so
// this unwraps to find it regardless of how many fmt.Errorf("...: %w", ...)
// layers sit on top (see client.do). Distinguishing a timeout from other
// failures (connection refused, DNS failure, a bad status code) matters
// because a timeout specifically means the app genuinely isn't answering —
// not a one-off blip — see mirror.go's pushChanges/pullChanges for what
// that triggers.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// client talks to a single sub-app's slice of AppServerService's HTTP API
// (app/lib/services/api/app_server.dart) — GET/POST/DELETE one file at a
// time, plus the manifest route; see that file's doc comment for the full
// route list this mirrors.
type client struct {
	baseURL string // e.g. "http://192.168.1.23:8787/apps/3"
	http    *http.Client
}

func newClient(host string, appID int) *client {
	host = strings.TrimSuffix(host, "/")
	scheme := "http://"
	switch {
	case strings.HasPrefix(host, "http://"):
		host = strings.TrimPrefix(host, "http://")
	case strings.HasPrefix(host, "https://"):
		scheme = "https://"
		host = strings.TrimPrefix(host, "https://")
	}
	if !strings.Contains(host, ":") {
		host += ":8787" // matches AppServerService's own default port
	}
	return &client{
		baseURL: fmt.Sprintf("%s%s/apps/%d", scheme, host, appID),
		http:    &http.Client{Timeout: initialRequestTimeout},
	}
}

// manifestEntry mirrors one entry of GET .../manifest's "files" array.
// ModifiedAt is kept as the server's raw ISO-8601 string, not parsed into a
// time.Time: mirror.go only ever needs to detect "did this change since the
// last manifest poll," which raw-string equality already answers, without
// this CLI having to agree on an exact wire time format with the Dart
// server (whose DateTime.now().toIso8601String() omits a timezone
// designator, unlike Go's default RFC3339 time.Time unmarshaling).
type manifestEntry struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

type manifestIndexEntry struct {
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

type manifest struct {
	HasIndex bool                `json:"hasIndex"`
	Index    *manifestIndexEntry `json:"index"`
	Files    []manifestEntry     `json:"files"`
}

func (c *client) getManifest() (*manifest, error) {
	resp, err := c.do(http.MethodGet, "manifest", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp)
	}
	var m manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &m, nil
}

func (c *client) getIndex() (string, error) {
	resp, err := c.do(http.MethodGet, "", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read index body: %w", err)
	}
	return string(body), nil
}

func (c *client) getFile(name string) ([]byte, error) {
	resp, err := c.do(http.MethodGet, name, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s body: %w", name, err)
	}
	return body, nil
}

func (c *client) postIndex(body string) error {
	resp, err := c.do(http.MethodPost, "", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp)
	}
	return nil
}

func (c *client) postFile(name string, body []byte) error {
	resp, err := c.do(http.MethodPost, name, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp)
	}
	return nil
}

func (c *client) deleteFile(name string) error {
	resp, err := c.do(http.MethodDelete, name, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp)
	}
	return nil
}

func (c *client) do(method, path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL
	if path != "" {
		url += "/" + path
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, errNotApproved
	}
	return resp, nil
}

func statusErr(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
}
