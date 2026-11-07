package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/config"
)

// writeConfig writes hlab.yaml into a new data folder and returns the folder.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.File), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadWithoutAFileGivesTheDefaults(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg != config.Default() {
		t.Errorf("got %+v, want the defaults %+v", cfg, config.Default())
	}
}

func TestLoadKeepsTheDefaultsTheFileLeavesOut(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, "time_scale: 0.5\nports:\n  cloud: 9080\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Default()
	want.TimeScale = 0.5
	want.Ports.Cloud = 9080
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadRefusesWhatTheLabCannotRunWith(t *testing.T) {
	for name, content := range map[string]string{
		"an unknown key":        "time_scal: 1\n",
		"a negative time scale": "time_scale: -1\n",
		"a port out of range":   "ports:\n  control: 70000\n",
		"a port taken twice":    "ports:\n  cloud: 8090\n",
		"no YAML":               "ports: [\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, content))
			if err == nil || !strings.Contains(err.Error(), config.File) {
				t.Errorf("got %v, want an error naming %s", err, config.File)
			}
		})
	}
}

func TestLoadAllowsAnyFreePortMoreThanOnce(t *testing.T) {
	if _, err := config.Load(writeConfig(t, "ports: {cloud: 0, hetzner: 0, control: 0}\n")); err != nil {
		t.Error(err)
	}
}
