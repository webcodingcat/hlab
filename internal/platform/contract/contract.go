// Package contract holds the contract tests of the platform interfaces: what
// every platform does, the fake as well as Hyper-V (and later Linux). The unit
// tests run them against the fake, the live tests against the real platform; so
// code tested against the fake works on the real one.
//
// The tests create VMs and disks whose names start with "hlab-contract-" and
// remove them at the end.
package contract

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// prefix starts the name of everything the tests create.
const prefix = "hlab-contract-"

// tag marks the tests' VMs, for FindVMs.
const tag = "hlab-contract"

// timeout bounds each test; the real platform takes seconds per call.
const timeout = 5 * time.Minute

// expect fails the test unless err is (or wraps) want; want nil means success.
func expect(t *testing.T, what string, err, want error) {
	t.Helper()
	switch {
	case want == nil && err != nil:
		t.Fatalf("%s: %v", what, err)
	case want != nil && !errors.Is(err, want):
		t.Fatalf("%s: got error %v, want %v", what, err, want)
	}
}

// testContext returns a context that ends with the test.
func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	return ctx
}

// newVM creates a VM with a system disk in dir and deletes it after the test.
func newVM(t *testing.T, hypervisor platform.Hypervisor, dir, name string) {
	t.Helper()
	ctx := testContext(t)
	disk := filepath.Join(dir, name+".vhdx")
	expect(t, "CreateDisk", hypervisor.CreateDisk(ctx, disk, 1, ""), nil)
	spec := platform.VMSpec{
		Name:     name,
		Tag:      tag,
		CPUs:     1,
		MemoryMB: 512,
		Disks:    []platform.DiskSlot{{Slot: 0, Path: disk}},
	}
	expect(t, "CreateVM", hypervisor.CreateVM(ctx, spec), nil)
	t.Cleanup(func() {
		// a fresh context: the test's own one has ended by now
		ctx := context.Background()
		_ = hypervisor.StopVM(ctx, name)
		if err := hypervisor.DeleteVM(ctx, name); err != nil && !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("DeleteVM %s: %v", name, err)
		}
	})
}

// Hypervisor checks a hypervisor; dir is a folder for the disk files.
func Hypervisor(t *testing.T, hypervisor platform.Hypervisor, dir string) {
	t.Run("PowerStates", func(t *testing.T) {
		ctx := testContext(t)
		name := prefix + "power"
		newVM(t, hypervisor, dir, name)
		power := func() platform.PowerState {
			t.Helper()
			state, err := hypervisor.VMState(ctx, name)
			expect(t, "VMState", err, nil)
			return state.Power
		}
		if got := power(); got != platform.PowerOff {
			t.Fatalf("a new VM is %s, want off", got)
		}
		expect(t, "ResetVM on an off VM", hypervisor.ResetVM(ctx, name), platform.ErrNotRunning)
		expect(t, "PauseVM on an off VM", hypervisor.PauseVM(ctx, name, true), platform.ErrNotRunning)
		expect(t, "ShutdownVM on an off VM", hypervisor.ShutdownVM(ctx, name), nil)
		expect(t, "StartVM", hypervisor.StartVM(ctx, name), nil)
		expect(t, "StartVM on a running VM", hypervisor.StartVM(ctx, name), nil)
		if got := power(); got != platform.PowerRunning {
			t.Fatalf("a started VM is %s, want running", got)
		}
		expect(t, "ResetVM", hypervisor.ResetVM(ctx, name), nil)
		expect(t, "PauseVM", hypervisor.PauseVM(ctx, name, true), nil)
		expect(t, "PauseVM on a paused VM", hypervisor.PauseVM(ctx, name, true), nil)
		if got := power(); got != platform.PowerPaused {
			t.Fatalf("a paused VM is %s, want paused", got)
		}
		expect(t, "StartVM on a paused VM", hypervisor.StartVM(ctx, name), platform.ErrRunning)
		expect(t, "PauseVM to resume", hypervisor.PauseVM(ctx, name, false), nil)
		if got := power(); got != platform.PowerRunning {
			t.Fatalf("a resumed VM is %s, want running", got)
		}
		// the guest may ignore the request (the fake's obeys, an empty VM
		// has none); either way the call succeeds
		expect(t, "ShutdownVM on a running VM", hypervisor.ShutdownVM(ctx, name), nil)
		expect(t, "StopVM", hypervisor.StopVM(ctx, name), nil)
		expect(t, "StopVM on an off VM", hypervisor.StopVM(ctx, name), nil)
		if got := power(); got != platform.PowerOff {
			t.Fatalf("a stopped VM is %s, want off", got)
		}
	})

	t.Run("CreateFindDelete", func(t *testing.T) {
		ctx := testContext(t)
		name := prefix + "find"
		newVM(t, hypervisor, dir, name)
		expect(t, "CreateVM twice", hypervisor.CreateVM(ctx, platform.VMSpec{Name: name, CPUs: 1, MemoryMB: 512}), platform.ErrExists)
		missingDisk := platform.VMSpec{
			Name: prefix + "nodisk", CPUs: 1, MemoryMB: 512,
			Disks: []platform.DiskSlot{{Slot: 0, Path: filepath.Join(dir, "missing.vhdx")}},
		}
		expect(t, "CreateVM with a missing disk", hypervisor.CreateVM(ctx, missingDisk), platform.ErrNotFound)
		found, err := hypervisor.FindVMs(ctx, tag)
		expect(t, "FindVMs", err, nil)
		if !containsVM(found, name) || containsVM(found, missingDisk.Name) {
			t.Fatalf("FindVMs(%q) = %v, want %s in it and not %s", tag, found, name, missingDisk.Name)
		}
		expect(t, "StartVM", hypervisor.StartVM(ctx, name), nil)
		expect(t, "DeleteVM of a running VM", hypervisor.DeleteVM(ctx, name), nil)
		expect(t, "DeleteVM twice", hypervisor.DeleteVM(ctx, name), platform.ErrNotFound)
		_, err = hypervisor.VMState(ctx, name)
		expect(t, "VMState of a deleted VM", err, platform.ErrNotFound)
		expect(t, "StartVM of a deleted VM", hypervisor.StartVM(ctx, name), platform.ErrNotFound)
	})

	t.Run("CPUMemory", func(t *testing.T) {
		ctx := testContext(t)
		name := prefix + "cpu"
		newVM(t, hypervisor, dir, name)
		expect(t, "SetCPUMemory on an off VM", hypervisor.SetCPUMemory(ctx, name, 2, 1024), nil)
		expect(t, "StartVM", hypervisor.StartVM(ctx, name), nil)
		expect(t, "SetCPUMemory on a running VM", hypervisor.SetCPUMemory(ctx, name, 1, 512), platform.ErrRunning)
	})

	t.Run("HostResources", func(t *testing.T) {
		resources, err := hypervisor.HostResources(testContext(t))
		expect(t, "HostResources", err, nil)
		if resources.Threads < 1 || resources.MemoryMB < 1024 {
			t.Errorf("HostResources = %+v, want threads and at least 1 GB", resources)
		}
	})

	t.Run("DiskSpace", func(t *testing.T) {
		space, err := hypervisor.DiskSpace(testContext(t), dir)
		expect(t, "DiskSpace", err, nil)
		if space.TotalMB < 1024 || space.FreeMB < 0 || space.FreeMB > space.TotalMB {
			t.Errorf("DiskSpace = %+v, want a drive of at least 1 GB with free space within it", space)
		}
	})

	t.Run("Disks", func(t *testing.T) {
		ctx := testContext(t)
		name := prefix + "disks"
		newVM(t, hypervisor, dir, name)
		base := filepath.Join(dir, prefix+"base.vhdx")
		child := filepath.Join(dir, "servers", prefix+"child.vhdx")
		flat := filepath.Join(dir, prefix+"flat.vhdx")
		expect(t, "CreateDisk", hypervisor.CreateDisk(ctx, base, 2, ""), nil)
		expect(t, "CreateDisk twice", hypervisor.CreateDisk(ctx, base, 2, ""), platform.ErrExists)
		expect(t, "CreateDisk on a missing parent", hypervisor.CreateDisk(ctx, child, 2, filepath.Join(dir, "missing.vhdx")), platform.ErrNotFound)
		expect(t, "CreateDisk on a parent, in a new folder", hypervisor.CreateDisk(ctx, child, 2, base), nil)
		expect(t, "ResizeDisk to grow", hypervisor.ResizeDisk(ctx, child, 3), nil)
		if err := hypervisor.ResizeDisk(ctx, child, 1); err == nil {
			t.Fatal("ResizeDisk shrank a disk")
		}
		expect(t, "ResizeDisk of a missing disk", hypervisor.ResizeDisk(ctx, filepath.Join(dir, "missing.vhdx"), 3), platform.ErrNotFound)
		expect(t, "FlattenDisk", hypervisor.FlattenDisk(ctx, child, flat), nil)
		expect(t, "FlattenDisk onto a disk that is there", hypervisor.FlattenDisk(ctx, child, flat), platform.ErrExists)

		// disks and ISOs change while the VM runs, as volumes do
		expect(t, "StartVM", hypervisor.StartVM(ctx, name), nil)
		expect(t, "AttachDisk", hypervisor.AttachDisk(ctx, name, platform.DiskSlot{Slot: 1, Path: flat}), nil)
		expect(t, "AttachDisk to a taken slot", hypervisor.AttachDisk(ctx, name, platform.DiskSlot{Slot: 1, Path: child}), platform.ErrExists)
		expect(t, "AttachDisk of a missing disk", hypervisor.AttachDisk(ctx, name, platform.DiskSlot{Slot: 2, Path: filepath.Join(dir, "missing.vhdx")}), platform.ErrNotFound)
		volume := platform.DiskSlot{Slot: 2, Path: filepath.Join(dir, prefix+"volume.vhdx"), ID: [16]byte{0x48, 0x4c, 0x41, 0x42, 15: 1}}
		expect(t, "CreateDisk of a volume", hypervisor.CreateDisk(ctx, volume.Path, 1, ""), nil)
		expect(t, "AttachDisk with an ID", hypervisor.AttachDisk(ctx, name, volume), nil)
		expect(t, "DetachDisk of the volume", hypervisor.DetachDisk(ctx, name, 2), nil)
		expect(t, "AttachDisk with an ID again", hypervisor.AttachDisk(ctx, name, volume), nil)
		expect(t, "DetachDisk of the volume again", hypervisor.DetachDisk(ctx, name, 2), nil)
		expect(t, "DeleteDisk of the volume", hypervisor.DeleteDisk(ctx, volume.Path), nil)
		expect(t, "ResizeDisk of an attached disk", hypervisor.ResizeDisk(ctx, flat, 4), nil)
		expect(t, "DeleteDisk of an attached disk", hypervisor.DeleteDisk(ctx, flat), platform.ErrInUse)
		expect(t, "DetachDisk", hypervisor.DetachDisk(ctx, name, 1), nil)
		expect(t, "DetachDisk of an empty slot", hypervisor.DetachDisk(ctx, name, 1), platform.ErrNotFound)
		expect(t, "DeleteDisk", hypervisor.DeleteDisk(ctx, flat), nil)
		expect(t, "DeleteDisk twice", hypervisor.DeleteDisk(ctx, flat), platform.ErrNotFound)
		expect(t, "CreateDisk where a deleted disk was", hypervisor.CreateDisk(ctx, flat, 1, ""), nil)

		seed := filepath.Join(dir, "seeds", prefix+"seed.iso")
		files := map[string][]byte{"meta-data": []byte("instance-id: contract\n"), "network-config": []byte("version: 2\n")}
		expect(t, "CreateSeed", hypervisor.CreateSeed(ctx, seed, files), nil)
		expect(t, "CreateSeed again", hypervisor.CreateSeed(ctx, seed, files), nil)
		expect(t, "SetISO", hypervisor.SetISO(ctx, name, seed), nil)
		expect(t, "SetISO of a missing ISO", hypervisor.SetISO(ctx, name, filepath.Join(dir, "missing.iso")), platform.ErrNotFound)
		expect(t, "SetISO to eject", hypervisor.SetISO(ctx, name, ""), nil)
	})

	t.Run("NICs", func(t *testing.T) {
		ctx := testContext(t)
		name := prefix + "nics"
		newVM(t, hypervisor, dir, name)
		expect(t, "StartVM", hypervisor.StartVM(ctx, name), nil)
		for _, port := range []platform.PortMode{platform.Plain(), platform.Isolated(), platform.Promiscuous(), platform.Access(10), platform.Trunk()} {
			nic := platform.NICSpec{MAC: "02:00:00:00:00:01", Port: port}
			expect(t, "AttachNIC "+string(port.Kind), hypervisor.AttachNIC(ctx, name, nic), nil)
			expect(t, "AttachNIC twice", hypervisor.AttachNIC(ctx, name, nic), platform.ErrExists)
			expect(t, "DetachNIC", hypervisor.DetachNIC(ctx, name, nic.MAC), nil)
			expect(t, "DetachNIC twice", hypervisor.DetachNIC(ctx, name, nic.MAC), platform.ErrNotFound)
		}
	})
}

// containsVM tells whether FindVMs found a VM.
func containsVM(found []platform.FoundVM, name string) bool {
	for _, vm := range found {
		if vm.Name == name {
			return true
		}
	}
	return false
}

// HostNet checks a host network; used is a range the machine uses already.
// The link's switches start with platform.SwitchPrefix, as the lab's do.
func HostNet(t *testing.T, hostNet platform.HostNet, used netip.Prefix) {
	ctx := testContext(t)
	// 198.18.0.0/15 and 2001:db8::/32 are for benchmarks and documentation
	// (RFC 2544, RFC 3849): no machine uses them
	free := []netip.Prefix{netip.MustParsePrefix("198.18.0.0/16"), netip.MustParsePrefix("2001:db8::/32")}
	expect(t, "CheckRanges on free ranges", hostNet.CheckRanges(ctx, free), nil)
	expect(t, "CheckRanges on a used range", hostNet.CheckRanges(ctx, append(free, used)), platform.ErrInUse)

	link := platform.LinkSpec{
		Switch: platform.SwitchPrefix + "contract-link",
		Addresses: []netip.Prefix{
			netip.MustParsePrefix("198.18.0.1/29"),
			netip.MustParsePrefix("2001:db8:0:ffff::1/64"),
		},
		Routes: []netip.Prefix{
			netip.MustParsePrefix("198.18.0.0/16"),
			netip.MustParsePrefix("2001:db8::/32"),
		},
		Gateways: []netip.Addr{
			netip.MustParseAddr("198.18.0.2"),
			netip.MustParseAddr("2001:db8:0:ffff::2"),
		},
		PrivateSwitches: []string{platform.SwitchPrefix + "contract-public"},
	}
	t.Cleanup(func() { _ = hostNet.TearDown(context.Background()) })
	expect(t, "SetUp", hostNet.SetUp(ctx, link), nil)
	// the link's own addresses and routes are the lab's, not in use
	expect(t, "CheckRanges with the link set up", hostNet.CheckRanges(ctx, free), nil)
	expect(t, "SetUp over a link that is there", hostNet.SetUp(ctx, link), nil)
	expect(t, "TearDown", hostNet.TearDown(ctx), nil)
	expect(t, "TearDown without a link", hostNet.TearDown(ctx), nil)
}

// HostDNS checks the host's DNS rules: applying a set again, emptying it and
// clearing twice all succeed.
func HostDNS(t *testing.T, hostDNS platform.HostDNS) {
	ctx := testContext(t)
	t.Cleanup(func() { _ = hostDNS.Clear(context.Background()) })
	rules := []platform.DNSRule{{
		Domain:  "hlab-contract.internal",
		Servers: []netip.Addr{netip.MustParseAddr("198.18.0.2")},
	}}
	expect(t, "Apply", hostDNS.Apply(ctx, rules), nil)
	expect(t, "Apply the same rules", hostDNS.Apply(ctx, rules), nil)
	expect(t, "Apply no rules", hostDNS.Apply(ctx, nil), nil)
	expect(t, "Clear", hostDNS.Clear(ctx), nil)
	expect(t, "Clear twice", hostDNS.Clear(ctx), nil)
}

// Lifecycle checks the life cycle; requestStop asks the platform to stop, as
// the tray or a SIGTERM would.
func Lifecycle(t *testing.T, lifecycle platform.Lifecycle, requestStop func()) {
	t.Run("Lock", func(t *testing.T) {
		dataDir := t.TempDir()
		unlock, err := lifecycle.Lock(dataDir)
		expect(t, "Lock", err, nil)
		_, err = lifecycle.Lock(dataDir)
		expect(t, "Lock twice", err, platform.ErrLocked)
		unlock()
		unlock, err = lifecycle.Lock(dataDir)
		expect(t, "Lock after unlock", err, nil)
		unlock()
	})

	t.Run("WriteSecret", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control.token")
		expect(t, "WriteSecret", lifecycle.WriteSecret(path, []byte("secret")), nil)
		expect(t, "WriteSecret again", lifecycle.WriteSecret(path, []byte("newer")), nil)
		written, err := os.ReadFile(path)
		expect(t, "reading the token", err, nil)
		if string(written) != "newer" {
			t.Fatalf("the token file holds %q, want %q", written, "newer")
		}
	})

	t.Run("AgentDialer", func(t *testing.T) {
		_, err := lifecycle.AgentDialer().Dial(testContext(t), prefix+"missing", 1)
		expect(t, "Dial a missing VM", err, platform.ErrNotFound)
	})

	t.Run("StopRequests", func(t *testing.T) {
		select {
		case <-lifecycle.StopRequests():
			t.Fatal("a stop was requested before anyone asked")
		default:
		}
		requestStop()
		select {
		case <-lifecycle.StopRequests():
		case <-time.After(timeout):
			t.Fatal("the stop request did not arrive")
		}
	})
}
