package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/webcodingcat/hlab/internal/labapi"
)

func TestWSLPathTurnsDrivePathsIntoMountPaths(t *testing.T) {
	for windowsPath, want := range map[string]string{
		`C:\HLabDev\data`:           "/mnt/c/HLabDev/data",
		`d:\labs\one`:               "/mnt/d/labs/one",
		`C:\`:                       "/mnt/c/",
		`\\server\share\HLab`:       "",
		`relative\folder`:           "",
		`1:\not-a-drive`:            "",
		`C:/forward/slashes/differ`: "",
	} {
		if got := wslPath(windowsPath); got != want {
			t.Errorf("wslPath(%q) = %q, want %q", windowsPath, got, want)
		}
	}
}

func TestTheInstanceFileNamesTheDataFolder(t *testing.T) {
	defaultDir := t.TempDir()
	if got := instanceDataDir(defaultDir, wslPath); got != defaultDir {
		t.Errorf("without an instance file: %q, want the default %q", got, defaultDir)
	}
	instance := filepath.Join(defaultDir, labapi.InstanceFile)
	// hlabd writes the folder with a Windows line end
	if err := os.WriteFile(instance, []byte("C:\\HLabDev\\data\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := instanceDataDir(defaultDir, wslPath); got != "/mnt/c/HLabDev/data" {
		t.Errorf("with the dev loop's folder: %q, want /mnt/c/HLabDev/data", got)
	}
	if err := os.WriteFile(instance, []byte(`\\server\share`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := instanceDataDir(defaultDir, wslPath); got != defaultDir {
		t.Errorf("with a folder WSL2 cannot reach: %q, want the default %q", got, defaultDir)
	}
}
