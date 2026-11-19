package discard_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/discard"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	"github.com/webcodingcat/hlab/internal/store"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscard(t *testing.T) {
	t.Run("LIFE-08", func(t *testing.T) {
		ctx := context.Background()
		root := t.TempDir()
		data := filepath.Join(root, "data")
		statePath := filepath.Join(root, "state.db")
		writeFile(t, statePath)

		fake := platformtest.NewFake()
		servers := []string{"hlab-dev-server-1", "hlab-dev-server-2"}
		kept := []string{"hlab-dev-appliance", "hlab-server-1"} // appliance, other profile
		for _, name := range append(append([]string{}, servers...), kept...) {
			if err := fake.CreateVM(ctx, platform.VMSpec{Name: name, CPUs: 1, MemoryMB: 1024}); err != nil {
				t.Fatal(err)
			}
		}
		fake.SetState("hlab-dev-server-1", platform.StateRunning)

		deleted := []string{
			filepath.Join(data, "disks", "server-1.vhdx"),
			filepath.Join(data, "disks", "server-1-cidata.iso"),
			filepath.Join(data, "volumes", "volume-3.vhdx"),
			filepath.Join(data, "vms", "hlab-dev-server-1", "config.vmcx"),
			filepath.Join(data, "images", "image-7.vhdx"),
		}
		stays := []string{
			filepath.Join(data, "images", "ubuntu-24.04.vhdx"),
			filepath.Join(data, "isos", "virtio-win.iso"),
			filepath.Join(data, "cache", "debs", "haproxy.deb"),
			filepath.Join(data, "appliance", "appliance.vhdx"),
		}
		for _, p := range append(append([]string{}, deleted...), stays...) {
			writeFile(t, p)
		}

		now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
		renamed, err := discard.Run(ctx, discard.Options{
			StatePath: statePath, DataFolder: data, VMPrefix: "hlab-dev-server-",
			Platform: fake, Now: now,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if want := statePath + ".discarded-20261119T120000Z"; renamed != want {
			t.Errorf("Run renamed the state to %s, want %s", renamed, want)
		}
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Errorf("state file still exists after Run (%v)", err)
		}

		for _, name := range servers {
			_, err := fake.State(ctx, name)
			var pe *platform.Error
			if !errors.As(err, &pe) || pe.Kind != platform.NotFound {
				t.Errorf("server VM %s after Run: %v, want deleted", name, err)
			}
		}
		for _, name := range kept {
			if _, err := fake.State(ctx, name); err != nil {
				t.Errorf("VM %s after Run: %v, want kept", name, err)
			}
		}
		for _, p := range deleted {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("%s still exists after Run (%v)", p, err)
			}
		}
		for _, p := range stays {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s gone after Run: %v", p, err)
			}
		}

		incompatible := &store.IncompatibleError{Found: store.SchemaVersion + 1, Want: store.SchemaVersion}
		for _, c := range []struct {
			params []string
			err    error
			want   bool
		}{
			{params: []string{"discard-state"}, err: incompatible, want: true},
			{params: []string{"discard-state"}, err: fmt.Errorf("open state: %w", incompatible), want: true},
			{params: []string{"discard-state"}, err: nil, want: false},
			{params: []string{"discard-state"}, err: errors.New("disk full"), want: false},
			{params: nil, err: incompatible, want: false},
			{params: []string{"other"}, err: incompatible, want: false},
		} {
			if got := discard.Requested(c.params, c.err); got != c.want {
				t.Errorf("Requested(%q, %v) = %v, want %v", c.params, c.err, got, c.want)
			}
		}
	})
}
