package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the real Hetzner Cloud API.
const DefaultBaseURL = "https://api.hetzner.cloud/v1"

// updateTimeout bounds one request to the Hetzner API when no client is
// given.
const updateTimeout = time.Minute

// UpdateOptions say where Update fetches from and writes to.
type UpdateOptions struct {
	// BaseURL is the Hetzner API; "" is DefaultBaseURL.
	BaseURL string
	// DataDir is the folder whose catalog\server-types.json Update writes
	// (the config root in hlabd).
	DataDir string
	// Client sends the requests; nil uses a client with a timeout.
	Client *http.Client
}

// Update fetches every page of /server_types from the Hetzner API with
// token, writes them to <DataDir>\catalog\server-types.json atomically and
// replaces the served server types (own types kept). On any error the file
// and the served types stay as they were. The token is only sent in the
// Authorization header; it is never logged or written.
func Update(ctx context.Context, c *Catalog, opts UpdateOptions, token string) error {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: updateTimeout}
	}
	var raw []json.RawMessage
	for page := 1; page > 0; {
		items, next, err := fetchPage(ctx, client, base, token, page)
		if err != nil {
			return err
		}
		raw = append(raw, items...)
		page = next
	}
	if len(raw) == 0 {
		return errors.New("the Hetzner API listed no server types")
	}
	if _, err := serverTypes(raw); err != nil {
		return fmt.Errorf("server types of the Hetzner API: %w", err)
	}
	data, err := json.MarshalIndent(map[string]any{"server_types": raw}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(opts.DataDir, "catalog"), "server-types.json", data); err != nil {
		return err
	}
	if err := c.ReplaceServerTypes(raw); err != nil {
		return err
	}
	slog.Info("server type catalog updated", "server_types", len(raw))
	return nil
}

// fetchPage gets one page of /server_types and returns its items and the
// next page (0: none).
func fetchPage(ctx context.Context, client *http.Client, base, token string, page int) ([]json.RawMessage, int, error) {
	u := base + "/server_types?per_page=50&page=" + strconv.Itoa(page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("reach the Hetzner API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("read the Hetzner API answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			return nil, 0, fmt.Errorf("the Hetzner API answered %s: %s (%s)", resp.Status, e.Error.Message, e.Error.Code)
		}
		return nil, 0, fmt.Errorf("the Hetzner API answered %s", resp.Status)
	}
	var body struct {
		ServerTypes []json.RawMessage `json:"server_types"`
		Meta        struct {
			Pagination *struct {
				NextPage *int `json:"next_page"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, 0, fmt.Errorf("the Hetzner API answer: %w", err)
	}
	next := 0
	if p := body.Meta.Pagination; p != nil && p.NextPage != nil && *p.NextPage > page {
		next = *p.NextPage
	}
	return body.ServerTypes, next, nil
}

// writeAtomic writes data to dir\name through a temporary file in dir and
// a rename, so readers see the old or the new file only.
func writeAtomic(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Join(dir, name), err)
	}
	return nil
}
