// Command openapi downloads the frozen Hetzner OpenAPI description named in
// api/openapi.lock, checks its SHA-256 against the pin and caches it at
// <user cache dir>/hlab/openapi/cloud.spec.json, where the API tests read
// it. Run it from the repository root (`task openapi`).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const lockPath = "api/openapi.lock"

type lock struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

func main() {
	path, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
	fmt.Println("cached", path)
}

func run() (string, error) {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return "", fmt.Errorf("read %s (run from the repository root): %w", lockPath, err)
	}
	var l lock
	if err := json.Unmarshal(raw, &l); err != nil {
		return "", fmt.Errorf("parse %s: %w", lockPath, err)
	}
	if l.URL == "" || l.SHA256 == "" {
		return "", fmt.Errorf("%s needs url and sha256", lockPath)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(cache, "hlab", "openapi", "cloud.spec.json")
	if data, err := os.ReadFile(dest); err == nil && sum(data) == strings.ToLower(l.SHA256) {
		return dest, nil
	}

	data, err := download(l.URL)
	if err != nil {
		return "", err
	}
	if got := sum(data); got != strings.ToLower(l.SHA256) {
		return "", fmt.Errorf("%s has SHA-256 %s, but %s pins %s: the published description changed; "+
			"review the new version and update the pin in a deliberate commit", l.URL, got, lockPath, l.SHA256)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

func download(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	return data, nil
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}
