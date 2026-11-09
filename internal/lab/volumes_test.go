package lab_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
)

// A start makes the disk anew of a volume whose create HLab's end cut
// short, detaches the volumes of a server whose delete it cut short, and
// removes the disks of volumes that are gone.
func TestStartMachineResumesTheVolumes(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	dataDir := t.TempDir()
	l := start(t, machine, lab.Options{DataDir: dataDir})
	t.Cleanup(func() { _ = l.Close() })
	var creating, orphaned int64
	err := l.Change(func(tx *state.Tx) error {
		project := &state.Project{ID: state.NewID(tx, state.Projects), Name: "default"}
		state.Put(tx, state.Projects, project)
		creating, orphaned = state.NewID(tx, state.Volumes), state.NewID(tx, state.Volumes)
		state.Put(tx, state.Volumes, &state.Volume{ID: creating, ProjectID: project.ID, Name: "new", SizeGB: 10,
			Location: "fsn1", Format: "ext4", Status: state.VolumeCreating})
		state.Put(tx, state.Volumes, &state.Volume{ID: orphaned, ProjectID: project.ID, Name: "orphaned", SizeGB: 10,
			Location: "fsn1", Status: state.VolumeAvailable, ServerID: 999, Slot: 1})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, lab.VolumeDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{fmt.Sprint(orphaned) + ".vhdx", "999.vhdx"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := l.Machine().Start(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	if volume, err := lab.Current(l, state.Volumes, creating); err != nil || volume.Status != state.VolumeAvailable {
		t.Errorf("the volume whose create was cut short: %+v, %v", volume, err)
	}
	if !slices.Contains(machine.Calls(), "CreateDisk "+filepath.Join(dir, fmt.Sprint(creating)+".vhdx")+" 10 ") {
		t.Errorf("its disk was not made anew: %v", machine.Calls())
	}
	if volume, err := lab.Current(l, state.Volumes, orphaned); err != nil || volume.ServerID != 0 {
		t.Errorf("the volume of a server that is gone: %+v, %v", volume, err)
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, entry := range entries {
		left = append(left, entry.Name())
	}
	if !slices.Equal(left, []string{fmt.Sprint(orphaned) + ".vhdx"}) {
		t.Errorf("left in %s: %v", lab.VolumeDir, left)
	}
}
