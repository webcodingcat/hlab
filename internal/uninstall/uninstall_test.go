//go:build windows

package uninstall_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	hostnetwin "github.com/webcodingcat/hlab/internal/hostnet/windows"
	"github.com/webcodingcat/hlab/internal/uninstall"
)

// recorder logs every call of the fakes in order, and whether the data
// folder still existed at that moment.
type recorder struct {
	mu      sync.Mutex
	dataDir string
	calls   []string
	dirGone []string // calls made after the data folder was removed
}

func (r *recorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	if _, err := os.Stat(r.dataDir); err != nil {
		r.dirGone = append(r.dirGone, call)
	}
}

func (r *recorder) has(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (r *recorder) all(prefix string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

type fakeService struct {
	r   *recorder
	err error
}

func (f *fakeService) RemoveService(_ context.Context, mode string) error {
	f.r.add("RemoveService " + mode)
	return f.err
}

type fakeHost struct {
	r       *recorder
	had     bool  // the Root store had the CA
	wslErr  error // returned by RemoveWSLTrust
	rootPEM []byte
	wslPEM  []byte
}

func (f *fakeHost) Restore(context.Context) error {
	f.r.add("Restore")

	return nil
}

func (f *fakeHost) RemoveRootTrust(pemData []byte) (bool, error) {
	f.r.add("RemoveRootTrust")
	f.rootPEM = append([]byte(nil), pemData...)
	return f.had, nil
}

func (f *fakeHost) RemoveWSLTrust(_ context.Context, pemData []byte) error {
	f.r.add("RemoveWSLTrust")
	f.wslPEM = append([]byte(nil), pemData...)
	return f.wslErr
}

func (f *fakeHost) Down(context.Context) error {
	f.r.add("Down")
	return nil
}

// fakeHyperV lists VMs by name prefix as Hyper-V does and deletes them.
type fakeHyperV struct {
	r       *recorder
	vms     []string
	failing map[string]bool // VMs that exist and cannot be deleted
}

func (f *fakeHyperV) VMs(_ context.Context, prefix string) ([]string, error) {
	f.r.add("VMs " + prefix)
	var out []string
	for _, v := range f.vms {
		if strings.HasPrefix(v, prefix) {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *fakeHyperV) DeleteVM(_ context.Context, name string) error {
	f.r.add("DeleteVM " + name)
	if f.failing[name] {
		return fmt.Errorf("delete VM %s: access denied", name)
	}
	return nil
}

func (f *fakeHyperV) DeleteSwitch(_ context.Context, name string) error {
	f.r.add("DeleteSwitch " + name)
	return nil
}

type setup struct {
	rec     *recorder
	svc     *fakeService
	host    *fakeHost
	hv      *fakeHyperV
	dataDir string
	caPEM   []byte
}

// newSetup creates a data folder with settings.toml (data_folder inside it)
// and, when withCA, ca\ca.pem.
func newSetup(t *testing.T, trustCA, withCA bool, vms ...string) *setup {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "hlab")
	dataFolder := filepath.Join(dataDir, "data")
	if err := os.MkdirAll(filepath.Join(dataFolder, "disks"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("[storage]\ndata_folder = '%s'\n[certificates]\ntrust_ca = %t\n", dataFolder, trustCA)
	if err := os.WriteFile(filepath.Join(dataDir, "settings.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataFolder, "disks", "hlab-server-1.vhdx"), []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &setup{rec: &recorder{dataDir: dataDir}, dataDir: dataDir}
	if withCA {
		s.caPEM = []byte("-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n")
		if err := os.MkdirAll(filepath.Join(dataDir, "ca"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "ca", "ca.pem"), s.caPEM, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.svc = &fakeService{r: s.rec}
	s.host = &fakeHost{r: s.rec}
	s.hv = &fakeHyperV{r: s.rec, vms: vms, failing: map[string]bool{}}
	return s
}

func (s *setup) options(keepData bool) uninstall.Options {
	return uninstall.Options{
		Tag:        "hlab",
		DataDir:    s.dataDir,
		KeepData:   keepData,
		Service:    s.svc,
		Host:       s.host,
		Hypervisor: s.hv,
	}
}

// stepOf maps a recorded call to its step in the uninstall order.
func stepOf(call string) string {
	switch {
	case strings.HasPrefix(call, "RemoveService"):
		return "service"
	case call == "Restore":
		return "restore"
	case call == "RemoveRootTrust", call == "RemoveWSLTrust":
		return "trust"
	case call == "Down":
		return "hostnet"
	case strings.HasPrefix(call, "VMs"), strings.HasPrefix(call, "DeleteVM"):
		return "vms"
	case strings.HasPrefix(call, "DeleteSwitch"):
		return "switch"
	}
	return call
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mkFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUninstall(t *testing.T) {
	ctx := context.Background()

	t.Run("INST-04/profile", func(t *testing.T) {
		cases := []struct {
			name string
			want bool
		}{
			{"hlab-appliance", true},
			{"hlab-server-1", true},
			{"hlab-server-42", true},
			{"hlab-dev-server-1", false},
			{"hlab-dev-appliance", false},
			{"hlab-server-x", false},
			{"hlab-server-1-old", false},
			{"hlab-server-", false},
			{"ubuntu-desktop", false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if got := uninstall.ProfileVM("hlab", c.name); got != c.want {
					t.Errorf("ProfileVM(%q, %q) = %v, want %v", "hlab", c.name, got, c.want)
				}
			})
		}
		t.Run("run", func(t *testing.T) {
			var vms []string
			for _, c := range cases {
				vms = append(vms, c.name)
			}
			s := newSetup(t, false, false, vms...)
			if _, err := uninstall.Run(ctx, s.options(false)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			deleted := map[string]bool{}
			for _, c := range s.rec.all("DeleteVM ") {
				deleted[strings.TrimPrefix(c, "DeleteVM ")] = true
			}
			for _, c := range cases {
				if deleted[c.name] != c.want {
					t.Errorf("VM %q deleted = %v, want %v", c.name, deleted[c.name], c.want)
				}
			}
			switches := s.rec.all("DeleteSwitch ")
			if len(switches) != 1 || switches[0] != "DeleteSwitch hlab" {
				t.Errorf("switch deletions = %q, want only the switch %q", switches, "hlab")
			}
		})
	})

	t.Run("INST-04/order", func(t *testing.T) {
		wantOrder := []string{"service", "restore", "trust", "hostnet", "vms", "switch"}
		for _, keep := range []bool{false, true} {
			s := newSetup(t, true, true, "hlab-appliance", "hlab-server-1")
			if _, err := uninstall.Run(ctx, s.options(keep)); err != nil {
				t.Fatalf("Run(keep=%v): %v", keep, err)
			}
			var steps []string
			for _, c := range s.rec.calls {
				st := stepOf(c)
				if len(steps) == 0 || steps[len(steps)-1] != st {
					steps = append(steps, st)
				}
			}
			if strings.Join(steps, ",") != strings.Join(wantOrder, ",") {
				t.Errorf("keep=%v: steps %v (calls %q), want %v", keep, steps, s.rec.calls, wantOrder)
			}
			if len(s.rec.dirGone) > 0 {
				t.Errorf("keep=%v: data folder removed before %q", keep, s.rec.dirGone)
			}
			if !bytes.Equal(s.host.rootPEM, s.caPEM) {
				t.Errorf("keep=%v: RemoveRootTrust got %q, want ca.pem %q", keep, s.host.rootPEM, s.caPEM)
			}
			wantMode := "RemoveService off"
			if keep {
				wantMode = "RemoveService shutdown"
			}
			if got := s.rec.all("RemoveService"); len(got) != 1 || got[0] != wantMode {
				t.Errorf("keep=%v: service calls %q, want %q", keep, got, wantMode)
			}
			if got := exists(s.dataDir); got != keep {
				t.Errorf("keep=%v: data folder exists after Run = %v, want %v", keep, got, keep)
			}
		}

		wsl := []struct {
			trustCA, had, want bool
		}{
			{trustCA: false, had: false, want: false},
			{trustCA: true, had: false, want: true},
			{trustCA: false, had: true, want: true},
		}
		for _, c := range wsl {
			s := newSetup(t, c.trustCA, true)
			s.host.had = c.had
			if _, err := uninstall.Run(ctx, s.options(false)); err != nil {
				t.Fatalf("Run(trust_ca=%v, had=%v): %v", c.trustCA, c.had, err)
			}
			got := s.rec.has("RemoveWSLTrust")
			if got != c.want {
				t.Errorf("trust_ca=%v, root store had=%v: RemoveWSLTrust called = %v, want %v", c.trustCA, c.had, got, c.want)
			}
			if got && !bytes.Equal(s.host.wslPEM, s.caPEM) {
				t.Errorf("RemoveWSLTrust got %q, want ca.pem %q", s.host.wslPEM, s.caPEM)
			}
		}
	})

	t.Run("INST-04/errors", func(t *testing.T) {
		// Nothing there: no service (RemoveService reports a missing one as
		// nil), no VMs, no switch, no ca.pem.
		s := newSetup(t, true, false)
		if _, err := uninstall.Run(ctx, s.options(false)); err != nil {
			t.Errorf("Run with nothing to remove: %v", err)
		}
		if s.rec.has("RemoveRootTrust") || s.rec.has("RemoveWSLTrust") {
			t.Errorf("trust removed without ca.pem: calls %q", s.rec.calls)
		}
		if exists(s.dataDir) {
			t.Error("data folder still there")
		}

		// A VM that exists and cannot be deleted.
		s = newSetup(t, true, true, "hlab-server-1", "hlab-server-2", "hlab-appliance")
		s.hv.failing["hlab-server-1"] = true
		_, err := uninstall.Run(ctx, s.options(false))
		if err == nil || !strings.Contains(err.Error(), "hlab-server-1") {
			t.Errorf("Run with an undeletable VM: error %v, want one naming hlab-server-1", err)
		}
		for _, want := range []string{"DeleteVM hlab-server-2", "DeleteVM hlab-appliance", "DeleteSwitch hlab"} {
			if !s.rec.has(want) {
				t.Errorf("after the failed VM, %q did not run (calls %q)", want, s.rec.calls)
			}
		}
		if exists(s.dataDir) {
			t.Error("after the failed VM, the data folder was not deleted")
		}

		// No console user for WSL: a note, not an error.
		s = newSetup(t, true, true)
		s.host.wslErr = fmt.Errorf("wsl: %w", hostnetwin.ErrNoConsoleUser)
		res, err := uninstall.Run(ctx, s.options(false))
		if err != nil {
			t.Errorf("Run without a console user: %v", err)
		}
		if len(res.Notes) == 0 {
			t.Error("Run without a console user: no note")
		}
		if !s.rec.has("DeleteSwitch hlab") || exists(s.dataDir) {
			t.Errorf("without a console user the later steps did not run (calls %q)", s.rec.calls)
		}
	})

	t.Run("INST-05/data-folder", func(t *testing.T) {
		hlabDirs := []string{"disks", "volumes", "images", "isos", "cache", "appliance", "vms"}

		// data_folder inside the data folder: everything goes.
		dataDir := filepath.Join(t.TempDir(), "hlab")
		mkFile(t, filepath.Join(dataDir, "settings.toml"))
		mkFile(t, filepath.Join(dataDir, "data", "disks", "a.vhdx"))
		if err := uninstall.RemoveFiles(dataDir, filepath.Join(dataDir, "data")); err != nil {
			t.Fatalf("RemoveFiles inside: %v", err)
		}
		if exists(dataDir) {
			t.Error("data folder still there")
		}

		// data_folder outside with files of the user: only hlab's folders go.
		dataDir = filepath.Join(t.TempDir(), "hlab")
		mkFile(t, filepath.Join(dataDir, "state.db"))
		outside := filepath.Join(t.TempDir(), "VMs")
		for _, d := range hlabDirs {
			mkFile(t, filepath.Join(outside, d, "f"))
		}
		mkFile(t, filepath.Join(outside, "notes.txt"))
		mkFile(t, filepath.Join(outside, "photos", "p.jpg"))
		if err := uninstall.RemoveFiles(dataDir, outside); err != nil {
			t.Fatalf("RemoveFiles outside: %v", err)
		}
		if exists(dataDir) {
			t.Error("data folder still there")
		}
		for _, d := range hlabDirs {
			if exists(filepath.Join(outside, d)) {
				t.Errorf("%s still in the outside data_folder", d)
			}
		}
		for _, keep := range []string{"notes.txt", filepath.Join("photos", "p.jpg")} {
			if !exists(filepath.Join(outside, keep)) {
				t.Errorf("user file %s was deleted", keep)
			}
		}

		// data_folder outside holding only hlab's folders: it goes too.
		dataDir = filepath.Join(t.TempDir(), "hlab")
		mkFile(t, filepath.Join(dataDir, "state.db"))
		outside = filepath.Join(t.TempDir(), "VMs")
		for _, d := range hlabDirs {
			mkFile(t, filepath.Join(outside, d, "f"))
		}
		if err := uninstall.RemoveFiles(dataDir, outside); err != nil {
			t.Fatalf("RemoveFiles outside, only hlab: %v", err)
		}
		if exists(outside) {
			t.Error("empty outside data_folder still there")
		}
	})
}
