//go:build windows

package cloud

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/mgmt"
	"github.com/webcodingcat/hlab/internal/resources/zones"
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
