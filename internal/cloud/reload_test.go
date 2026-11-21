//go:build windows

package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/images"
	"github.com/webcodingcat/hlab/internal/mgmt"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/zones"
	"github.com/webcodingcat/hlab/internal/store"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// shippedCatalog is the catalog folder shipped with the program.
const shippedCatalog = "../../catalog"

// testReload runs the tests of POST /settings/reload and the own server
// type file of the cloud start.
func testReload(t *testing.T) {
	t.Run("UI-03", func(t *testing.T) {
		c, config := reloadCloud(t, "info", "[]")
		if got := c.d.Level.Level(); got != slog.LevelInfo {
			t.Fatalf("level before reload %v, want info", got)
		}

		writeFile(t, filepath.Join(config, "settings.toml"), "[logging]\nlevel = \"debug\"\n")
		if err := c.reload(context.Background()); err != nil {
			t.Fatalf("reload with logging.level debug: %v", err)
		}
		if got := c.d.Level.Level(); got != slog.LevelDebug {
			t.Errorf("level after reload %v, want debug", got)
		}

		writeFile(t, filepath.Join(config, "settings.toml"), "[logging]\nlevel = \"loud\"\n")
		writeFile(t, filepath.Join(config, "server-types.custom.json"), ownTypesJSON(t, "own-new"))
		if err := c.reload(context.Background()); err == nil {
			t.Error("reload with an invalid settings file returned nil, want the error")
		}
		if got := c.d.Level.Level(); got != slog.LevelDebug {
			t.Errorf("level after a refused reload %v, want debug unchanged", got)
		}
		if own := ownNames(c.cat); len(own) != 0 {
			t.Errorf("own types after a refused reload %v, want none as before", own)
		}
	})

	t.Run("UI-04", func(t *testing.T) {
		config := t.TempDir()
		custom := filepath.Join(config, "server-types.custom.json")
		if err := ensureCustomServerTypes(config); err != nil {
			t.Fatalf("ensureCustomServerTypes without a file: %v", err)
		}
		var list []json.RawMessage
		if data, err := os.ReadFile(custom); err != nil || json.Unmarshal(data, &list) != nil || list == nil || len(list) != 0 {
			t.Fatalf("created custom file %q (read error %v), want an empty JSON array", data, err)
		}
		existing := ownTypesJSON(t, "own-kept")
		writeFile(t, custom, existing)
		if err := ensureCustomServerTypes(config); err != nil {
			t.Fatalf("ensureCustomServerTypes with a file: %v", err)
		}
		if data, _ := os.ReadFile(custom); string(data) != existing {
			t.Errorf("existing custom file after the start check %q, want it kept", data)
		}

		c, config := reloadCloud(t, "info", "[]")
		custom = filepath.Join(config, "server-types.custom.json")

		writeFile(t, custom, ownTypesJSON(t, "own-a"))
		if err := c.reload(context.Background()); err != nil {
			t.Fatalf("reload with a changed custom file: %v", err)
		}
		if got := ownNames(c.cat); !slices.Equal(got, []string{"own-a"}) {
			t.Errorf("own types after reload %v, want [own-a]", got)
		}
		if hasCustomProblem(c) {
			t.Error("custom_server_types problem with a valid custom file")
		}

		writeFile(t, custom, "[{not json")
		if err := c.reload(context.Background()); err != nil {
			t.Fatalf("reload with an invalid custom file: %v", err)
		}
		if got := ownNames(c.cat); len(got) != 0 {
			t.Errorf("own types after reload of an invalid file %v, want none", got)
		}
		if !hasCustomProblem(c) {
			t.Error("no custom_server_types problem after reload of an invalid custom file")
		}

		writeFile(t, custom, ownTypesJSON(t, "own-b"))
		if err := c.reload(context.Background()); err != nil {
			t.Fatalf("reload with a valid custom file again: %v", err)
		}
		if got := ownNames(c.cat); !slices.Equal(got, []string{"own-b"}) {
			t.Errorf("own types after the repairing reload %v, want [own-b]", got)
		}
		if hasCustomProblem(c) {
			t.Error("custom_server_types problem still reported after a valid custom file")
		}
	})

	t.Run("CAT-10_reload", func(t *testing.T) {
		config := t.TempDir()
		file := filepath.Join(config, catalog.OwnISOsFile)
		if err := ensureCustomISOs(config); err != nil {
			t.Fatalf("ensureCustomISOs without a file: %v", err)
		}
		var list []json.RawMessage
		if data, err := os.ReadFile(file); err != nil || json.Unmarshal(data, &list) != nil || list == nil || len(list) != 0 {
			t.Fatalf("created own ISOs file %q (read error %v), want an empty JSON array", data, err)
		}
		existing := `[{"id":100000,"name":null,"description":"kept","type":"private","deprecation":null,"architecture":"x86",` +
			`"_hlab":{"name":"own-100000-00000000.iso","source":{"url":"https://example.com/kept.iso"}}}]`
		writeFile(t, file, existing)
		if err := ensureCustomISOs(config); err != nil {
			t.Fatalf("ensureCustomISOs with a file: %v", err)
		}
		if data, _ := os.ReadFile(file); string(data) != existing {
			t.Errorf("existing own ISOs file after the start check %q, want it kept", data)
		}

		c, config, data := isoCloud(t)
		file = filepath.Join(config, catalog.OwnISOsFile)
		content := bytes.Repeat([]byte{0x3C}, 8192)
		sum := sha256.Sum256(content)
		good := filepath.Join(t.TempDir(), "good.iso")
		bad := filepath.Join(t.TempDir(), "bad.iso")
		writeFile(t, good, string(content))
		writeFile(t, bad, string(content))
		remote := addOwnISO(t, c, file, "Own remote", catalog.ISOSource{URL: "https://example.com/remote.iso"})
		copied := addOwnISO(t, c, file, "Own copied", catalog.ISOSource{Path: good, SHA256: hex.EncodeToString(sum[:])})
		failed := addOwnISO(t, c, file, "Own failed", catalog.ISOSource{Path: bad, SHA256: strings.Repeat("0", 64)})

		if err := c.reload(context.Background()); err != nil {
			t.Fatalf("reload with own ISOs: %v", err)
		}
		cached := filepath.Join(data, "isos", copied.CacheName)
		waitUntil(t, "the local file copied into the cache and the failed copy dropped", func() bool {
			got, err := os.ReadFile(cached)
			return err == nil && bytes.Equal(got, content) && len(c.cat.OwnISOs()) == 2
		})
		var ids []int64
		for _, iso := range c.cat.OwnISOs() {
			ids = append(ids, iso.ID)
		}
		if !slices.Equal(ids, []int64{remote.ID, copied.ID}) {
			t.Errorf("own ISOs after reload %v, want [%d %d]", ids, remote.ID, copied.ID)
		}
		if _, err := os.Stat(filepath.Join(data, "isos", failed.CacheName)); err == nil {
			t.Errorf("cache holds %s after its failed copy, want nothing", failed.CacheName)
		}
		probs := isoProblems(c)
		if len(probs) != 1 || (!strings.Contains(probs[0], "Own failed") && !strings.Contains(probs[0], strconv.FormatInt(failed.ID, 10))) {
			t.Errorf("custom_isos problems %q, want one naming the dropped ISO %d", probs, failed.ID)
		}
	})

	t.Run("CAT-10_cache", func(t *testing.T) {
		c, config, data := isoCloud(t)
		file := filepath.Join(config, catalog.OwnISOsFile)
		kept := addOwnISO(t, c, file, "Own kept", catalog.ISOSource{URL: "https://example.com/kept.iso"})
		changed := addOwnISO(t, c, file, "Own changed", catalog.ISOSource{URL: "https://example.com/old.iso"})
		oldName := changed.CacheName
		update := catalog.OwnISOSpec{ID: changed.ID, Description: "Own changed", Architecture: "x86",
			Source: catalog.ISOSource{URL: "https://example.com/new.iso"}}
		if _, err := catalog.UpdateOwnISO(c.cat, file, update); err != nil {
			t.Fatalf("UpdateOwnISO: %v", err)
		}

		isoDir := filepath.Join(data, "isos")
		catalogISO := c.cat.ISOs[0].Name
		removed := "own-100500-0badf00d.iso"
		attached := "own-100501-0ddba11a.iso"
		for _, name := range []string{catalogISO, kept.CacheName, oldName, removed, attached} {
			writeFile(t, filepath.Join(isoDir, name), "iso")
		}
		srv := putServerWithISO(t, c.st, attached)

		present := func(name string) bool {
			_, err := os.Stat(filepath.Join(isoDir, name))
			return err == nil
		}
		if err := c.reload(context.Background()); err != nil {
			t.Fatalf("reload: %v", err)
		}
		waitUntil(t, "the cache files of the removed and the changed own ISO deleted", func() bool {
			return !present(removed) && !present(oldName)
		})
		for _, name := range []string{catalogISO, kept.CacheName, attached} {
			if !present(name) {
				t.Errorf("cache file %s deleted by the cleanup, want it kept", name)
			}
		}

		srv.Hlab.ISO = ""
		if err := c.st.Tx(func(tx store.Tx) error { return putServer(tx, srv) }); err != nil {
			t.Fatalf("detach the ISO in the store: %v", err)
		}
		if err := c.reload(context.Background()); err != nil {
			t.Fatalf("reload after the detach: %v", err)
		}
		waitUntil(t, "the cache file of the removed own ISO deleted once detached", func() bool {
			return !present(attached)
		})
		for _, name := range []string{catalogISO, kept.CacheName} {
			if !present(name) {
				t.Errorf("cache file %s deleted by the cleanup after the detach, want it kept", name)
			}
		}
	})
}

// isoCloud is reloadCloud with an own ISOs file, a data folder, a store
// and the image store of a start; it returns the cloud, its config folder
// and its data folder.
func isoCloud(t *testing.T) (*Cloud, string, string) {
	t.Helper()
	c, config := reloadCloud(t, "info", "[]")
	data := t.TempDir()
	writeFile(t, filepath.Join(config, "settings.toml"), "[logging]\nlevel = \"info\"\n[storage]\ndata_folder = '"+data+"'\n")
	if err := ensureCustomISOs(config); err != nil {
		t.Fatalf("ensureCustomISOs: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(data, "isos"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.set.Storage.DataFolder = data
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c.st = st
	c.images = images.New(images.Options{
		Dir:       filepath.Join(data, "images"),
		Catalog:   c.cat.ImageSources(),
		ISOs:      c.cat.ISOSources(),
		ISODir:    filepath.Join(data, "isos"),
		ISOSource: c.cat.ISOSourceByCacheName,
	})
	return c, config, data
}

// addOwnISO adds an x86 own ISO to file as the tray does.
func addOwnISO(t *testing.T, c *Cloud, file, description string, src catalog.ISOSource) catalog.ISO {
	t.Helper()
	iso, err := catalog.AddOwnISO(c.cat, file, catalog.OwnISOSpec{Description: description, Architecture: "x86", Source: src})
	if err != nil {
		t.Fatalf("AddOwnISO %s: %v", description, err)
	}
	return iso
}

// putServerWithISO stores a server of a new project with the ISO cache
// file iso attached.
func putServerWithISO(t *testing.T, st *store.Store, iso string) servers.Record {
	t.Helper()
	var r servers.Record
	err := st.Tx(func(tx store.Tx) error {
		p, err := store.CreateProject(tx, "p", "p")
		if err != nil {
			return err
		}
		id, err := store.NextID(tx, servers.Kind)
		if err != nil {
			return err
		}
		r = servers.Record{
			Server: servers.Server{ID: id, Name: "with-iso", Status: "off", Created: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)},
			Hlab:   servers.Internal{ProjectID: p.ID, ISO: iso},
		}
		return putServer(tx, r)
	})
	if err != nil {
		t.Fatalf("store a server with an attached ISO: %v", err)
	}
	return r
}

func putServer(tx store.Tx, r servers.Record) error {
	return store.Put(tx, store.Meta{Kind: servers.Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID, Name: r.Name, Created: r.Created}, r)
}

// isoProblems are the messages of the custom_isos problems in /status.
func isoProblems(c *Cloud) []string {
	var msgs []string
	for _, p := range c.mgmtStatus().Problems {
		if p.Code == "custom_isos" {
			msgs = append(msgs, p.Message)
		}
	}
	return msgs
}

// waitUntil polls cond until it holds, failing after a deadline of 10 s.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// reloadCloud returns a cloud in the state of a start with logging.level
// level and the own server types custom: settings file, catalog and
// status, without platform or host network.
func reloadCloud(t *testing.T, level, custom string) (*Cloud, string) {
	t.Helper()
	config := t.TempDir()
	writeFile(t, filepath.Join(config, "settings.toml"), "[logging]\nlevel = \""+level+"\"\n")
	customFile := filepath.Join(config, "server-types.custom.json")
	writeFile(t, customFile, custom)
	var lv slog.LevelVar
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		t.Fatal(err)
	}
	lv.Set(l)
	c := New(Deps{Paths: sysint.Paths{Config: config, Catalog: shippedCatalog}, Level: &lv})
	cat, warns, err := catalog.LoadAll(shippedCatalog, config, customFile)
	if err != nil || len(warns) != 0 {
		t.Fatalf("LoadAll: %v, warnings %q", err, warns)
	}
	c.cat = cat
	c.shadow = zones.NewShadow(nil)
	return c, config
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ownTypesJSON is a custom file with one own server type name, a copy of
// the shipped cx23 with ID 100001.
func ownTypesJSON(t *testing.T, name string) string {
	t.Helper()
	cat, err := catalog.Load(shippedCatalog)
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	ref, ok := cat.ServerType("cx23")
	if !ok {
		t.Fatal("shipped catalog lacks cx23")
	}
	var st map[string]any
	if err := json.Unmarshal(ref.Raw, &st); err != nil {
		t.Fatal(err)
	}
	st["id"], st["name"] = catalog.MinOwnID+1, name
	data, err := json.Marshal([]any{st})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func ownNames(cat *catalog.Catalog) []string {
	var names []string
	for _, st := range cat.Own() {
		names = append(names, st.Name)
	}
	slices.Sort(names)
	return names
}

// hasCustomProblem reports whether /status lists the custom_server_types
// problem.
func hasCustomProblem(c *Cloud) bool {
	return slices.ContainsFunc(c.mgmtStatus().Problems, func(p mgmt.Problem) bool {
		return p.Code == "custom_server_types"
	})
}
