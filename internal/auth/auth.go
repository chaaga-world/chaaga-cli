package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chaaga-world/chaaga-cli/internal/config"
)

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	VerificationURI         string `json:"verification_uri"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

type deviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
}

func EnsureToken(cfg *config.Config) (string, error) {
	t := ReadToken(cfg.TokenPath)
	if t != "" {
		return t, nil
	}
	if err := deviceLogin(cfg); err != nil {
		return "", err
	}
	t = ReadToken(cfg.TokenPath)
	if t == "" {
		return "", fmt.Errorf("login succeeded but token not found at %s", cfg.TokenPath)
	}
	return t, nil
}

func ReadToken(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func SaveToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token), 0600)
}

func Logout(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func deviceLogin(cfg *config.Config) error {
	fmt.Fprintln(os.Stderr, "Starting device-code login...")

	resp, err := http.Post(cfg.API+"/api/auth/device/code", "application/json", nil) //nolint:noctx
	if err != nil {
		return fmt.Errorf("auth request failed: %w", err)
	}
	defer resp.Body.Close()

	var dc deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return fmt.Errorf("bad /auth/device/code response: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return fmt.Errorf("incomplete device code response")
	}
	if dc.Interval == 0 {
		dc.Interval = 5
	}
	if dc.ExpiresIn == 0 {
		dc.ExpiresIn = 600
	}

	uri := dc.VerificationURIComplete
	if uri == "" {
		uri = cfg.Web + "/device"
	}

	fmt.Fprintf(os.Stderr, "\n  Open this URL in your browser:\n    %s\n\n  Or go to %s/device and enter the code:\n    %s\n\n",
		uri, cfg.Web, dc.UserCode)

	openBrowser(uri)
	fmt.Fprintln(os.Stderr, "Waiting for approval...")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(dc.ExpiresIn)*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 10 * time.Second}
	interval := dc.Interval

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for approval")
		case <-time.After(time.Duration(interval) * time.Second):
		}

		body, _ := json.Marshal(map[string]string{"device_code": dc.DeviceCode})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			cfg.API+"/api/auth/device/token",
			bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		r, err := client.Do(req)
		if err != nil {
			continue
		}

		var tr deviceTokenResponse
		json.NewDecoder(r.Body).Decode(&tr) //nolint:errcheck
		r.Body.Close()

		if r.StatusCode == http.StatusOK && tr.AccessToken != "" {
			if err := SaveToken(cfg.TokenPath, tr.AccessToken); err != nil {
				return fmt.Errorf("save token: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Logged in. Token saved to %s\n", cfg.TokenPath)
			return nil
		}

		switch tr.Error {
		case "authorization_pending":
			// keep polling
		case "slow_down":
			interval += 2
		case "access_denied":
			return fmt.Errorf("access denied")
		case "expired_token":
			return fmt.Errorf("code expired; rerun to try again")
		default:
			if tr.Error != "" {
				return fmt.Errorf("auth error: %s", tr.Error)
			}
		}
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	_ = cmd.Start()
}
