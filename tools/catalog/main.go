// Command catalog generates the shipped catalogs catalog/locations.json,
// catalog/server-types.json, catalog/load-balancer-types.json and
// catalog/pricing.json from the real Hetzner Cloud API and
// catalog/storage-box-types.json from the Hetzner API
// (https://api.hetzner.com/v1) (`task catalog`, run from the repository
// root).
//
// It calls both APIs through the hcloud context named HLab (or
// HLAB_HCLOUD_CONTEXT) when that context is usable, so the token never
// leaves hcloud; otherwise it says why and asks for a token with a hidden
// prompt, which it uses for this run only. Lists are written as the API
// returns them with all pages merged into one list and without meta.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	endpoint       = "https://api.hetzner.cloud/v1"
	hetznerAPI     = "https://api.hetzner.com/v1"
	defaultContext = "HLab"
	outDir         = "catalog"
	perPage        = 50
)

// lists are the paginated endpoints with their list key and output file.
var lists = []struct{ path, key, file string }{
	{"/locations", "locations", "locations.json"},
	{"/server_types", "server_types", "server-types.json"},
	{"/load_balancer_types", "load_balancer_types", "load-balancer-types.json"},
}

// fetcher returns the body of GET path with the query values q.
type fetcher func(ctx context.Context, path string, q url.Values) ([]byte, error)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "catalog:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	name := os.Getenv("HLAB_HCLOUD_CONTEXT")
	if name == "" {
		name = defaultContext
	}
	var get, getHetzner fetcher
	if err := usableContext(ctx, name); err == nil {
		fmt.Printf("using hcloud context %q\n", name)
		get = hcloudFetcher(name)
		getHetzner = hcloudFetcher(name, "--endpoint", hetznerAPI)
	} else {
		fmt.Fprintf(os.Stderr, "hcloud context %q not usable: %v\n", name, err)
		token, err := promptToken()
		if err != nil {
			return err
		}
		get = httpFetcher(endpoint, token)
		getHetzner = httpFetcher(hetznerAPI, token)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, l := range lists {
		items, err := allPages(ctx, get, l.path, l.key)
		if err != nil {
			return fmt.Errorf("%s: %w", l.path, err)
		}
		if err := write(l.file, map[string]any{l.key: items}); err != nil {
			return err
		}
	}
	pricing, err := get(ctx, "/pricing", nil)
	if err != nil {
		return fmt.Errorf("/pricing: %w", err)
	}
	if err := write("pricing.json", json.RawMessage(pricing)); err != nil {
		return err
	}
	boxTypes, err := allPages(ctx, getHetzner, "/storage_box_types", "storage_box_types")
	if err != nil {
		return fmt.Errorf("/storage_box_types: %w", err)
	}
	return write("storage-box-types.json", map[string]any{"storage_box_types": boxTypes})
}

// allPages fetches every page of a list endpoint and returns its items.
func allPages(ctx context.Context, get fetcher, path, key string) ([]json.RawMessage, error) {
	var items []json.RawMessage
	for page := 1; ; {
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
		body, err := get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		var resp map[string]json.RawMessage
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		var part []json.RawMessage
		if err := json.Unmarshal(resp[key], &part); err != nil {
			return nil, fmt.Errorf("page %d: no list %q: %w", page, key, err)
		}
		items = append(items, part...)
		var meta struct {
			Pagination struct {
				NextPage *int `json:"next_page"`
			} `json:"pagination"`
		}
		if m, ok := resp["meta"]; ok {
			if err := json.Unmarshal(m, &meta); err != nil {
				return nil, fmt.Errorf("page %d meta: %w", page, err)
			}
		}
		if meta.Pagination.NextPage == nil || *meta.Pagination.NextPage <= page {
			return items, nil
		}
		page = *meta.Pagination.NextPage
	}
}

func write(file string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(outDir, file)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

// hcloudCmd runs hcloud with HCLOUD_TOKEN, HCLOUD_ENDPOINT and
// HCLOUD_CONTEXT removed from its environment and returns its stdout.
func hcloudCmd(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "hcloud", args...)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HCLOUD_TOKEN", "HCLOUD_ENDPOINT", "HCLOUD_CONTEXT":
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("hcloud %s: %w: %s", args[len(args)-1], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// usableContext reports why the hcloud context name cannot be used, or nil.
func usableContext(ctx context.Context, name string) error {
	if _, err := exec.LookPath("hcloud"); err != nil {
		return errors.New("hcloud is not installed")
	}
	out, err := hcloudCmd(ctx, "context", "list", "-o", "noheader", "-o", "columns=name")
	if err != nil {
		return err
	}
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == name {
			found = true
		}
	}
	if !found {
		return errors.New("hcloud context list does not name it")
	}
	out, err = hcloudCmd(ctx, "--context", name, "config", "get", "endpoint")
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(string(out)); got != endpoint {
		return fmt.Errorf("endpoint is %q, want %s", got, endpoint)
	}
	return nil
}

// hcloudFetcher calls `hcloud --context name <flags> api` for each request.
func hcloudFetcher(name string, flags ...string) fetcher {
	return func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		args := append([]string{"--context", name}, flags...)
		args = append(args, "api")
		for k, vs := range q {
			for _, v := range vs {
				args = append(args, "-v", k+"="+v)
			}
		}
		return hcloudCmd(ctx, append(args, path)...)
	}
}

// httpFetcher calls the API at base with the token itself.
func httpFetcher(base, token string) fetcher {
	return func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		u := base + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: %s: %s", path, resp.Status, bytes.TrimSpace(body))
		}
		return body, nil
	}
}

// promptToken reads a Hetzner API token without echoing it.
func promptToken() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("no terminal to ask for a Hetzner API token")
	}
	fmt.Fprint(os.Stderr, "Hetzner API token (read only is enough, used for this run only): ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("no token given")
	}
	return token, nil
}
