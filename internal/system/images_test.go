package system_test

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/system"
)

// imageServer serves a qcow2 file and a list of checksums with the line
// given; "" lists the file's real SHA-512, as Debian's SHA512SUMS does.
func imageServer(t *testing.T, sums string) (system.ImageSource, []byte) {
	t.Helper()
	disk, err := os.ReadFile(filepath.Join("..", "imagefmt", "testdata", "compressed.qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	if sums == "" {
		sum := sha512.Sum512(disk)
		sums = hex.EncodeToString(sum[:]) + "  disk.qcow2\n"
	}
	files := http.NewServeMux()
	files.HandleFunc("GET /disk.qcow2", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(disk) })
	files.HandleFunc("GET /SUMS", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, sums) })
	server := httptest.NewServer(files)
	t.Cleanup(server.Close)
	return system.ImageSource{URL: server.URL + "/disk.qcow2", Checksums: server.URL + "/SUMS"}, disk
}

func TestLoadImageChecksAndConverts(t *testing.T) {
	machine := fake.New()
	sys := system.New(machine.Platform())
	source, disk := imageServer(t, "")
	cache := t.TempDir()
	loaded, err := sys.LoadImage(t.Context(), source, cache)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(disk)
	if loaded.SHA256 != hex.EncodeToString(sum[:]) || loaded.File != filepath.Join(cache, loaded.SHA256+".vhdx") {
		t.Errorf("loaded %+v", loaded)
	}
	// the VHD went to the hypervisor, and the files in between are gone
	if !strings.Contains(strings.Join(machine.Calls(), "\n"), "FlattenDisk") {
		t.Error("the VHD was not converted")
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Errorf("left in the cache folder: %v", entries)
	}
}

func TestLoadImageRefusesAWrongChecksum(t *testing.T) {
	sys := system.New(fake.New().Platform())
	for name, sums := range map[string]string{
		"a wrong sum":     strings.Repeat("ab", 32) + "  disk.qcow2\n",
		"no sum of it":    strings.Repeat("ab", 32) + "  other.qcow2\n",
		"an unknown hash": "abcd  disk.qcow2\n",
	} {
		source, _ := imageServer(t, sums)
		if _, err := sys.LoadImage(t.Context(), source, t.TempDir()); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}
