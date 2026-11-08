package lab_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// putServer records a server in a new project of its own and gives it a VM
// on the fake machine, running or off.
func putServer(t *testing.T, l *lab.Lab, machine *fake.Fake, name string, running, wantRunning bool) int64 {
	t.Helper()
	var id int64
	err := l.Change(func(tx *state.Tx) error {
		project := &state.Project{ID: state.NewID(tx, state.Projects), Name: name + "-project", Internet: true}
		state.Put(tx, state.Projects, project)
		id = state.NewID(tx, state.Servers)
		status := state.ServerOff
		if running {
			status = state.ServerRunning
		}
		state.Put(tx, state.Servers, &state.Server{
			ID: id, ProjectID: project.ID, Name: name, Type: "cpx22", Location: "fsn1",
			Status: status, WantRunning: wantRunning, MAC: lab.ServerMAC(id),
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	hypervisor := machine.Platform().Hypervisor
	spec := platform.VMSpec{
		Name: lab.ServerVM(id), Tag: system.VMTag, CPUs: 2, MemoryMB: 4096,
		NICs: []platform.NICSpec{{MAC: lab.ServerMAC(id), Switch: system.PublicSwitch, Port: platform.Isolated()}},
	}
	if err := hypervisor.CreateVM(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if running {
		if err := hypervisor.StartVM(t.Context(), spec.Name); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// serverStatus returns a server's status and whether it wants to run.
func serverStatus(t *testing.T, l *lab.Lab, id int64) (state.ServerStatus, bool) {
	t.Helper()
	server, err := lab.Current(l, state.Servers, id)
	if err != nil {
		t.Fatal(err)
	}
	return server.Status, server.WantRunning
}

func TestServerMACIsHetznersPrefixAndTheID(t *testing.T) {
	if mac := lab.ServerMAC(1_000_001); mac != "96:00:00:0f:42:41" {
		t.Errorf("ServerMAC(1000001) = %s", mac)
	}
}

// Addresses go out in order, without .0 and .255; a released one comes back
// only once the pool has gone round.
func TestNewAddressGoesRoundThePool(t *testing.T) {
	l, _ := open(t, lab.Options{})
	var got []string
	err := l.Change(func(tx *state.Tx) error {
		for range 3 {
			address, err := lab.NewAddress(tx, state.IPv4)
			if err != nil {
				return err
			}
			got = append(got, address)
			state.Put(tx, state.PrimaryIPs, &state.PrimaryIP{ID: state.NewID(tx, state.PrimaryIPs), IP: address})
		}
		six, err := lab.NewAddress(tx, state.IPv6)
		got = append(got, six)
		tx.Lab().Pools.NextIPv4 = 254 // near the end of 198.19.0.x
		for range 2 {
			address, err := lab.NewAddress(tx, state.IPv4)
			if err != nil {
				return err
			}
			got = append(got, address)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"198.19.0.1", "198.19.0.2", "198.19.0.3", "2001:db8:1:1::/64", "198.19.0.254", "198.19.1.1"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The servers that want to run may take the host's memory but a reserve
// (25 %, at least 6 GB) and the appliance's 1 GB.
func TestCheckRoomKeepsTheReserve(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	machine.SetHostResources(platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	cpx22, _ := catalog.Builtin().ServerTypeByName("cpx22") // 4 GB
	// 16 GB - 6 GB reserve - 1 GB appliance = 9 GB: two cpx22 fit, not three
	putServer(t, l, machine, "a", true, true)
	check := func() error {
		return l.View(func(tx *state.Tx) error { return l.CheckRoom(tx, 0, cpx22) })
	}
	if err := check(); err != nil {
		t.Fatalf("a second server: %v", err)
	}
	putServer(t, l, machine, "b", true, true)
	if err := check(); !errors.Is(err, lab.ErrNoRoom) {
		t.Errorf("a third server: %v, want ErrNoRoom", err)
	}
}

// A start takes over the servers that run, connects those that are off
// again and starts those that want to run.
func TestStartMachineResumesTheServers(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	running := putServer(t, l, machine, "running", true, true)
	wanted := putServer(t, l, machine, "wanted", false, true)
	resting := putServer(t, l, machine, "resting", false, false)

	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]state.ServerStatus{running: state.ServerRunning, wanted: state.ServerRunning, resting: state.ServerOff} {
		if status, _ := serverStatus(t, l, id); status != want {
			t.Errorf("server %d is %s, want %s", id, status, want)
		}
	}
	calls := strings.Join(machine.Calls(), "\n")
	for _, id := range []int64{wanted, resting} {
		if !strings.Contains(calls, "AttachNIC "+lab.ServerVM(id)) {
			t.Errorf("the NIC of %s was not connected again", lab.ServerVM(id))
		}
	}
	if strings.Contains(calls, "DetachNIC "+lab.ServerVM(running)) {
		t.Error("the NIC of the running server was touched")
	}
}

// A server that cannot be brought back stays off and keeps wanting to run;
// the lab and the other servers start all the same.
func TestStartMachineGoesOnPastABrokenServer(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	broken := putServer(t, l, machine, "broken", false, true)
	sound := putServer(t, l, machine, "sound", false, true)
	machine.FailNext("AttachNIC", errors.New("the switch is gone"))

	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	if status, wants := serverStatus(t, l, broken); status != state.ServerOff || !wants {
		t.Errorf("the broken server is %s, wanting to run %v; want off, wanting to run", status, wants)
	}
	if status, _ := serverStatus(t, l, sound); status != state.ServerRunning {
		t.Errorf("the sound server is %s, want running", status)
	}
}

// localImages returns the built-in catalog with its images on a local HTTP
// server: a small qcow2 file and its SHA256SUMS.
func localImages(t *testing.T) *catalog.Catalog {
	t.Helper()
	disk, err := os.ReadFile(filepath.Join("..", "imagefmt", "testdata", "plain.qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(disk)
	files := http.NewServeMux()
	files.HandleFunc("GET /image.qcow2", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(disk) })
	files.HandleFunc("GET /SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  image.qcow2\n", hex.EncodeToString(sum[:]))
	})
	server := httptest.NewServer(files)
	t.Cleanup(server.Close)
	offer := *catalog.Builtin()
	offer.Images = slices.Clone(offer.Images)
	for i := range offer.Images {
		offer.Images[i].Source = catalog.ImageSource{URL: server.URL + "/image.qcow2", Checksums: server.URL + "/SHA256SUMS"}
	}
	return &offer
}

// A server whose create HLab's end cut short has no VM: the next start
// builds it anew and starts it, as it wants to run.
func TestStartMachineBuildsAServerWithoutAVM(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	offer := localImages(t)
	l := start(t, machine, lab.Options{DataDir: t.TempDir(), Catalog: offer})
	t.Cleanup(func() { _ = l.Close() })
	var id int64
	err := l.Change(func(tx *state.Tx) error {
		project := &state.Project{ID: state.NewID(tx, state.Projects), Name: "default"}
		state.Put(tx, state.Projects, project)
		id = state.NewID(tx, state.Servers)
		state.Put(tx, state.Servers, &state.Server{
			ID: id, ProjectID: project.ID, Name: "web", Type: "cpx22", Location: "fsn1", ImageID: offer.Images[0].ID,
			Status: state.ServerInitializing, WantRunning: true, MAC: lab.ServerMAC(id), DiskGB: 80,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	if status, _ := serverStatus(t, l, id); status != state.ServerRunning {
		t.Errorf("the server is %s, want running", status)
	}
	vm, err := machine.Platform().Hypervisor.VMState(t.Context(), lab.ServerVM(id))
	if err != nil || vm.Power != platform.PowerRunning {
		t.Errorf("the server's VM is %+v, %v; want it running", vm, err)
	}
}

// A server whose VM is gone but whose disk is there (an uninstall that kept
// the data) gets a new VM with that disk at the next start; it is not built
// anew. Its new seed carries the lab's key.
func TestStartMachineGivesAServerWithoutAVMItsDiskAgain(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	dataDir := t.TempDir()
	labKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILabKey hlab"
	l := start(t, machine, lab.Options{DataDir: dataDir, LabKey: labKey})
	t.Cleanup(func() { _ = l.Close() })
	base := filepath.Join(dataDir, lab.ImageCacheDir, "base.vhdx")
	machine.AddDisk(base, 3)
	var id int64
	err := l.Change(func(tx *state.Tx) error {
		project := &state.Project{ID: state.NewID(tx, state.Projects), Name: "default"}
		state.Put(tx, state.Projects, project)
		id = state.NewID(tx, state.Servers)
		state.Put(tx, state.Servers, &state.Server{
			ID: id, ProjectID: project.ID, Name: "web", Type: "cpx22", Location: "fsn1", ImageID: 114690387,
			Status: state.ServerRunning, WantRunning: true, MAC: lab.ServerMAC(id), DiskGB: 80, BaseImage: base,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(dataDir, lab.ServerDiskDir, fmt.Sprint(id)+".vhdx")
	machine.AddDisk(disk, 80)

	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	if status, _ := serverStatus(t, l, id); status != state.ServerRunning {
		t.Errorf("the server is %s, want running", status)
	}
	vm, err := machine.Platform().Hypervisor.VMState(t.Context(), lab.ServerVM(id))
	if err != nil || vm.Power != platform.PowerRunning {
		t.Errorf("the server's VM is %+v, %v; want it running", vm, err)
	}
	for _, call := range machine.Calls() {
		if call == "DeleteDisk "+disk {
			t.Errorf("the start removed the server's disk")
		}
	}
	if !strings.Contains(string(machine.Seed(l.ServerSeed(id))["meta-data"]), "ILabKey") {
		t.Error("the new seed lacks the lab's key")
	}
}

// A start removes from the image cache what no cache entry and no server
// needs, and what a load cut short left.
func TestStartMachineCleansTheImageCache(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	dataDir := t.TempDir()
	cache := filepath.Join(dataDir, lab.ImageCacheDir)
	if err := os.MkdirAll(filepath.Join(cache, "load-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	kept, used, stale := filepath.Join(cache, "kept.vhdx"), filepath.Join(cache, "used.vhdx"), filepath.Join(cache, "stale.vhdx")
	for _, file := range []string{kept, used, stale} {
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l := start(t, machine, lab.Options{DataDir: dataDir})
	t.Cleanup(func() { _ = l.Close() })
	id := putServer(t, l, machine, "web", false, false)
	err := l.Change(func(tx *state.Tx) error {
		tx.Lab().Images = map[string]state.CachedImage{"debian-13": {File: kept}}
		server, err := state.Get(tx, state.Servers, id)
		if err == nil {
			server.BaseImage = used // an image whose source changed since
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cache)
	var left []string
	for _, entry := range entries {
		left = append(left, entry.Name())
	}
	if !slices.Equal(left, []string{"kept.vhdx", "used.vhdx"}) {
		t.Errorf("left in the cache: %v", left)
	}
}

// A start removes the disks and seeds of servers that are gone, as a
// project reset during a create leaves them, and keeps those of the others.
func TestStartMachineRemovesTheFilesOfServersThatAreGone(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	dataDir := t.TempDir()
	l := start(t, machine, lab.Options{DataDir: dataDir})
	t.Cleanup(func() { _ = l.Close() })
	id := putServer(t, l, machine, "web", false, false)
	files := map[string][]string{
		lab.ServerDiskDir: {fmt.Sprint(id) + ".vhdx", "999.vhdx"},
		lab.SeedDir:       {fmt.Sprint(id) + ".iso", "999.iso"},
	}
	for dir, names := range files {
		if err := os.MkdirAll(filepath.Join(dataDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(dataDir, dir, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	for dir, names := range files {
		entries, _ := os.ReadDir(filepath.Join(dataDir, dir))
		var left []string
		for _, entry := range entries {
			left = append(left, entry.Name())
		}
		if !slices.Equal(left, names[:1]) {
			t.Errorf("left in %s: %v, want %v", dir, left, names[:1])
		}
	}
}

// A guest that shuts itself down leaves its server off, and it no longer
// wants to run, as on Hetzner.
func TestWatchdogSeesAServerShutItselfDown(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	id := putServer(t, l, machine, "web", true, true)
	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	if err := machine.Platform().Hypervisor.ShutdownVM(t.Context(), lab.ServerVM(id)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, wants := serverStatus(t, l, id)
		if status == state.ServerOff && !wants {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server is %s (wants to run: %v), want off", status, wants)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Stopping the lab shuts the servers down; they keep wanting to run, so
// the next start starts them.
func TestCloseShutsTheServersDown(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	id := putServer(t, l, machine, "web", true, true)
	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := machine.Platform().Hypervisor.VMState(t.Context(), lab.ServerVM(id))
	if err != nil || state.Power != platform.PowerOff {
		t.Errorf("the server's VM is %+v, %v; want off", state, err)
	}
	if !slices.Contains(machine.Calls(), "ShutdownVM "+lab.ServerVM(id)) {
		t.Error("the guest was not asked to shut down")
	}
}
