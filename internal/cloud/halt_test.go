//go:build windows

package cloud

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestHalt(t *testing.T) {
	t.Run("INST-04", func(t *testing.T) {
		cases := []struct {
			name       string
			file       *string
			configured string
			behaviour  string
			skip       bool
		}{
			{name: "off skips halting", file: haltFile("off"), configured: "save", skip: true},
			{name: "shutdown overrides save", file: haltFile("shutdown"), configured: "save", behaviour: "shutdown"},
			{name: "no file keeps configured", configured: "save", behaviour: "save"},
			{name: "unknown content keeps configured", file: haltFile("hibernate"), configured: "save", behaviour: "save"},
		}
		for _, c := range cases {
			dir := t.TempDir()
			path := filepath.Join(dir, StopOverrideFile)
			if c.file != nil {
				if err := os.WriteFile(path, []byte(*c.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			behaviour, skip := haltBehaviour(dir, c.configured)
			if skip != c.skip {
				t.Errorf("%s: skip %v, want %v", c.name, skip, c.skip)
			}
			if !c.skip && behaviour != c.behaviour {
				t.Errorf("%s: behaviour %q, want %q", c.name, behaviour, c.behaviour)
			}
			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s: %s still there after reading (stat err %v)", c.name, StopOverrideFile, err)
			}
		}
	})
}

func haltFile(s string) *string { return &s }
