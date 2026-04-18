package files

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxFileSize = 5 * 1024 * 1024

type FileEntry struct {
	Path        string `json:"path"`
	AbsPath     string `json:"-"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
}

var extToMIME = map[string]string{
	"html":  "text/html; charset=utf-8",
	"htm":   "text/html; charset=utf-8",
	"css":   "text/css; charset=utf-8",
	"js":    "application/javascript; charset=utf-8",
	"mjs":   "application/javascript; charset=utf-8",
	"json":  "application/json; charset=utf-8",
	"map":   "application/json; charset=utf-8",
	"svg":   "image/svg+xml",
	"png":   "image/png",
	"jpg":   "image/jpeg",
	"jpeg":  "image/jpeg",
	"gif":   "image/gif",
	"webp":  "image/webp",
	"ico":   "image/x-icon",
	"txt":   "text/plain; charset=utf-8",
	"md":    "text/markdown; charset=utf-8",
	"xml":   "application/xml; charset=utf-8",
	"woff":  "font/woff",
	"woff2": "font/woff2",
	"ttf":   "font/ttf",
	"otf":   "font/otf",
	"wasm":  "application/wasm",
}

func ContentTypeByExt(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if ct, ok := extToMIME[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}

func Scan(dir string) ([]FileEntry, error) {
	var entries []FileEntry
	var skipped int

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden directories entirely
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// Skip any file whose relative path contains a hidden component
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxFileSize {
			skipped++
			return nil
		}

		sha, err := hashFile(path)
		if err != nil {
			return err
		}

		entries = append(entries, FileEntry{
			Path:        filepath.ToSlash(rel),
			AbsPath:     path,
			Size:        info.Size(),
			SHA256:      sha,
			ContentType: ContentTypeByExt(filepath.Ext(rel)),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return entries, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
