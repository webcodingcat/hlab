package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/webcodingcat/hlab/internal/catalog"
)

const hetznerToken = "hz-secret-Upd4teT0kenValue-0123456789abcdefghijklmnopqrstuv"

// recordedHetzner serves the shipped server types as Hetzner's
// /server_types does, split into pages of pageSize, and records every
// request.
type recordedHetzner struct {
	types    []map[string]any
	pageSize int
	status   int // != 0: answer every request with this status

	mu       sync.Mutex
	pages    []int
	badAuths []string
}

func (h *recordedHetzner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if h.status != 0 {
		w.WriteHeader(h.status)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"unable to authenticate","details":{}}}`))
		return
	}
	if r.URL.Path != "/server_types" {
		http.NotFound(w, r)
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+hetznerToken {
		h.badAuths = append(h.badAuths, got)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"unable to authenticate","details":{}}}`))
		return
	}
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	h.pages = append(h.pages, page)
	last := (len(h.types) + h.pageSize - 1) / h.pageSize
	lo := min((page-1)*h.pageSize, len(h.types))
	hi := min(lo+h.pageSize, len(h.types))
	var next, prev any
	if page < last {
		next = page + 1
	}
	if page > 1 {
		prev = page - 1
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"server_types": h.types[lo:hi],
		"meta": map[string]any{"pagination": map[string]any{
			"page": page, "per_page": h.pageSize, "previous_page": prev, "next_page": next,
			"last_page": last, "total_entries": len(h.types),
		}},
	})
}

// captureLogs sends the default slog logger and the standard logger into
// a buffer until the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	w := writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	})
	oldSlog, oldOut, oldFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(w)
	t.Cleanup(func() {
		slog.SetDefault(oldSlog)
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	})
	return &buf
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// updateSetup loads a catalog whose data folder holds two types and whose
// custom file holds one own type.
func updateSetup(t *testing.T) (cat *catalog.Catalog, dataDir string, before []byte) {
	t.Helper()
	dataDir = t.TempDir()
	customFile := filepath.Join(t.TempDir(), "server-types.custom.json")
	writeDataCatalog(t, dataDir, pick(t, "cpx11", "cx23"))
	writeJSON(t, customFile, []any{ownType(100001, "own-a")})
	cat, warns, err := catalog.LoadAll(shippedDir, dataDir, customFile)
	if err != nil || len(warns) != 0 {
		t.Fatalf("LoadAll: %v, warnings %q", err, warns)
	}
	before, err = os.ReadFile(filepath.Join(dataDir, "catalog", "server-types.json"))
	if err != nil {
		t.Fatal(err)
	}
	return cat, dataDir, before
}

// noTokenIn fails when the token appears in a file below dir.
func noTokenIn(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte(hetznerToken)) {
			t.Errorf("file %s contains the Hetzner token", path)
		}
		return nil
	})
}

func testUpdate(t *testing.T) {
	logs := captureLogs(t)
	recorded := shippedTypes(t)
	want := append(mapNames(recorded), "own-a")
	slices.Sort(want)

	// Success: every page with the token, file written, types replaced.
	cat, dataDir, _ := updateSetup(t)
	h := &recordedHetzner{types: recorded, pageSize: 10}
	srv := httptest.NewServer(h)
	defer srv.Close()
	err := catalog.Update(context.Background(), cat, catalog.UpdateOptions{
		BaseURL: srv.URL, DataDir: dataDir, Client: srv.Client(),
	}, hetznerToken)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	lastPage := (len(recorded) + 9) / 10
	h.mu.Lock()
	pages, badAuths := slices.Clone(h.pages), slices.Clone(h.badAuths)
	h.mu.Unlock()
	slices.Sort(pages)
	pages = slices.Compact(pages)
	if len(pages) != lastPage {
		t.Errorf("Update fetched pages %v, want all %d pages", pages, lastPage)
	}
	if len(badAuths) != 0 {
		t.Errorf("Update sent Authorization %q, want Bearer with the token", badAuths)
	}
	catDir := filepath.Join(dataDir, "catalog")
	data, err := os.ReadFile(filepath.Join(catDir, "server-types.json"))
	if err != nil {
		t.Fatalf("read written catalog: %v", err)
	}
	var file struct {
		ServerTypes []map[string]any `json:"server_types"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("written catalog is not {\"server_types\": [...]}: %v", err)
	}
	if got, w := mapNames(file.ServerTypes), mapNames(recorded); !slices.Equal(got, w) {
		t.Errorf("written catalog has types %v, want %v", got, w)
	}
	if entries, _ := os.ReadDir(catDir); len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("catalog folder holds %v after the update, want only server-types.json", names)
	}
	if got := typeNames(cat.Types()); !slices.Equal(got, want) {
		t.Errorf("Types() after update = %v, want %v", got, want)
	}

	// Failures leave file and served types unchanged.
	refused := httptest.NewServer(&recordedHetzner{types: recorded, pageSize: 10, status: http.StatusUnauthorized})
	defer refused.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	for _, c := range []struct {
		name, baseURL string
	}{
		{"401", refused.URL},
		{"unreachable", goneURL},
	} {
		cat, dataDir, before := updateSetup(t)
		beforeTypes := typeNames(cat.Types())
		err := catalog.Update(context.Background(), cat, catalog.UpdateOptions{
			BaseURL: c.baseURL, DataDir: dataDir, Client: &http.Client{},
		}, hetznerToken)
		if err == nil {
			t.Errorf("%s: Update succeeded, want an error", c.name)
		} else if strings.Contains(err.Error(), hetznerToken) {
			t.Errorf("%s: Update error contains the token", c.name)
		}
		after, _ := os.ReadFile(filepath.Join(dataDir, "catalog", "server-types.json"))
		if !bytes.Equal(after, before) {
			t.Errorf("%s: data-folder catalog changed", c.name)
		}
		if got := typeNames(cat.Types()); !slices.Equal(got, beforeTypes) {
			t.Errorf("%s: Types() = %v, want unchanged %v", c.name, got, beforeTypes)
		}
		noTokenIn(t, dataDir)
	}

	noTokenIn(t, dataDir)
	if strings.Contains(logs.String(), hetznerToken) {
		t.Errorf("a log line contains the Hetzner token")
	}
}
