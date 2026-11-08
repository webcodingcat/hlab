package system_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/system"
)

func TestStageApplianceReleaseCopiesTheDiskOnceNamedByItsHash(t *testing.T) {
	shipped := filepath.Join(t.TempDir(), "hlab-appliance.vhdx")
	if err := os.WriteFile(shipped, []byte("release 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "appliance")
	sys := system.New(fake.New().Platform())

	release, err := sys.StageApplianceRelease(shipped, dir)
	if err != nil {
		t.Fatal(err)
	}
	const id = "c40ffd4b1da7805cbba7f81a2f97c3ef3aa9d1455588357845a94165683a9c23" // sha256("release 1")
	if want := filepath.Join(dir, id+".vhdx"); release.ID != id || release.Disk != want {
		t.Errorf("StageApplianceRelease() = %+v, want the ID %s and the disk %s", release, id, want)
	}
	if got, err := os.ReadFile(release.Disk); err != nil || string(got) != "release 1" {
		t.Errorf("the copy holds %q, %v; want the shipped disk", got, err)
	}

	// the same disk again: the copy stays; a new one comes beside it
	again, err := sys.StageApplianceRelease(shipped, dir)
	if err != nil || again != release {
		t.Errorf("staging the same disk again = %+v, %v; want %+v", again, err, release)
	}
	if err := os.WriteFile(shipped, []byte("release 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, err := sys.StageApplianceRelease(shipped, dir)
	if err != nil || next.ID == release.ID {
		t.Fatalf("staging a new disk = %+v, %v; want another ID than %s", next, err, release.ID)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("the folder holds %d files, want both releases and nothing else", len(entries))
	}
}
