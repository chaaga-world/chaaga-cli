package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chaaga-world/chaaga-cli/internal/files"
)

// ErrUnauthorized is returned on any 401 response so callers can trigger re-login.
var ErrUnauthorized = errors.New("session expired")

type Client struct {
	Base        string
	Token       string
	http        *http.Client
	httpUpload  *http.Client
}

func New(base, token string) *Client {
	return &Client{
		Base:       strings.TrimRight(base, "/"),
		Token:      token,
		http:       &http.Client{Timeout: 30 * time.Second},
		httpUpload: &http.Client{Timeout: 5 * time.Minute},
	}
}

// ── App management ────────────────────────────────────────────────────────────

func (c *Client) EnsureApp(slug string) error {
	body, _ := json.Marshal(map[string]string{"title": slug, "slug": slug})
	resp, err := c.do(http.MethodPost, "/api/apps", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		fmt.Fprintf(os.Stderr, "  created app '%s'\n", slug)
	case http.StatusUnprocessableEntity:
		fmt.Fprintf(os.Stderr, "  using existing app '%s'\n", slug)
	default:
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create app failed (%d): %s", resp.StatusCode, b)
	}
	return nil
}

// ── Current user ──────────────────────────────────────────────────────────────

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

func (c *Client) GetMe() (*User, error) {
	resp, err := c.do(http.MethodGet, "/api/me", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get current user failed (%d): %s", resp.StatusCode, b)
	}
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("get current user decode: %w", err)
	}
	return &u, nil
}

// ── Presign ───────────────────────────────────────────────────────────────────

type FileRef struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
}

type UploadEntry struct {
	Path         string `json:"path"`
	PresignedURL string `json:"presigned_url"`
	ContentType  string `json:"content_type"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	Error        string `json:"error,omitempty"`
}

func toRefs(entries []files.FileEntry) []FileRef {
	refs := make([]FileRef, len(entries))
	for i, e := range entries {
		refs[i] = FileRef{Path: e.Path, Size: e.Size, SHA256: e.SHA256, ContentType: e.ContentType}
	}
	return refs
}

func (c *Client) Presign(slug string, entries []files.FileEntry) ([]UploadEntry, error) {
	body, _ := json.Marshal(map[string]any{"files": toRefs(entries)})
	resp, err := c.do(http.MethodPost, "/api/apps/"+slug+"/files/presign", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("presign failed (%d): %s", resp.StatusCode, b)
	}

	var result struct {
		Uploads []UploadEntry `json:"uploads"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("presign decode: %w", err)
	}

	var errs []string
	for _, u := range result.Uploads {
		if u.Error != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", u.Path, u.Error))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("presign errors:\n  %s", strings.Join(errs, "\n  "))
	}
	return result.Uploads, nil
}

// ── Upload ────────────────────────────────────────────────────────────────────

func (c *Client) UploadFile(entry UploadEntry, absPath string) error {
	f, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer f.Close()

	req, err := http.NewRequest(http.MethodPut, entry.PresignedURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = entry.Size
	req.Header.Set("Content-Type", entry.ContentType)

	resp, err := c.httpUpload.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("upload returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// ── Finalize ──────────────────────────────────────────────────────────────────

type FinalizeResponse struct {
	URL           string `json:"url"`
	DeployVersion int    `json:"deploy_version"`
}

func (c *Client) Finalize(slug string, entries []files.FileEntry) (*FinalizeResponse, error) {
	body, _ := json.Marshal(map[string]any{
		"files":          toRefs(entries),
		"delete_missing": true,
	})
	resp, err := c.do(http.MethodPost, "/api/apps/"+slug+"/files/finalize", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("finalize failed (%d): %s", resp.StatusCode, b)
	}

	var result FinalizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("finalize decode: %w", err)
	}
	return &result, nil
}

// ── Pull / list files ─────────────────────────────────────────────────────────

type RemoteFile struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
	UpdatedAt   string `json:"updated_at"`
}

func (c *Client) ListFiles(slug string) ([]RemoteFile, error) {
	resp, err := c.do(http.MethodGet, "/api/apps/"+slug+"/files", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("app '%s' not found", slug)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list files failed (%d): %s", resp.StatusCode, b)
	}

	var result struct {
		Files []RemoteFile `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("list files decode: %w", err)
	}
	return result.Files, nil
}

func (c *Client) DownloadFile(slug, path string) (io.ReadCloser, error) {
	resp, err := c.do(http.MethodGet, "/api/apps/"+slug+"/file/"+path, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s failed (HTTP %d)", path, resp.StatusCode)
	}
	return resp.Body, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (c *Client) do(method, path string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.Base+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrUnauthorized
	}
	return resp, nil
}
