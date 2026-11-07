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
		if got := power(); got != platform.PowerPaused {
			t.Fatalf("a paused VM is %s, want paused", got)
		}
		expect(t, "PauseVM to resume", hypervisor.PauseVM(ctx, name, false), nil)
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
		found, err := hypervisor.FindVMs(ctx, tag)
		expect(t, "FindVMs", err, nil)
		if !containsVM(found, name) {
			t.Fatalf("FindVMs(%q) = %v, want %s in it", tag, found, name)
		}
		expect(t, "DeleteVM", hypervisor.DeleteVM(ctx, name), nil)
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

	t.Run("Disks", func(t *testing.T) {
		ctx := testContext(t)
		name := prefix + "disks"
		newVM(t, hypervisor, dir, name)
		base := filepath.Join(dir, prefix+"base.vhdx")
		child := filepath.Join(dir, prefix+"child.vhdx")
		flat := filepath.Join(dir, prefix+"flat.vhdx")
		expect(t, "CreateDisk", hypervisor.CreateDisk(ctx, base, 2, ""), nil)
		expect(t, "CreateDisk twice", hypervisor.CreateDisk(ctx, base, 2, ""), platform.ErrExists)
		expect(t, "CreateDisk on a missing parent", hypervisor.CreateDisk(ctx, child, 2, filepath.Join(dir, "missing.vhdx")), platform.ErrNotFound)
		expect(t, "CreateDisk on a parent", hypervisor.CreateDisk(ctx, child, 2, base), nil)
		expect(t, "ResizeDisk to grow", hypervisor.ResizeDisk(ctx, child, 3), nil)
		if err := hypervisor.ResizeDisk(ctx, child, 1); err == nil {
			t.Fatal("ResizeDisk shrank a disk")
		}
		expect(t, "FlattenDisk", hypervisor.FlattenDisk(ctx, child, flat), nil)
		expect(t, "AttachDisk", hypervisor.AttachDisk(ctx, name, platform.DiskSlot{Slot: 1, Path: flat}), nil)
		expect(t, "AttachDisk to a taken slot", hypervisor.AttachDisk(ctx, name, platform.DiskSlot{Slot: 1, Path: child}), platform.ErrExists)
		expect(t, "DetachDisk", hypervisor.DetachDisk(ctx, name, 1), nil)
		expect(t, "DetachDisk of an empty slot", hypervisor.DetachDisk(ctx, name, 1), platform.ErrNotFound)
		frozen, done, err := hypervisor.CheckpointDisk(ctx, name)
		expect(t, "CheckpointDisk", err, nil)
		if frozen == "" {
			t.Fatal("CheckpointDisk returned no disk")
		}
		expect(t, "the done of CheckpointDisk", done(), nil)
		seed := filepath.Join(dir, prefix+"seed.iso")
		expect(t, "CreateSeed", hypervisor.CreateSeed(ctx, seed, map[string][]byte{"meta-data": []byte("{}")}), nil)
		expect(t, "SetISO", hypervisor.SetISO(ctx, name, seed), nil)
		expect(t, "SetISO to eject", hypervisor.SetISO(ctx, name, ""), nil)
	})

	t.Run("NICs", func(t *testing.T) {
		ctx := testContext(t)
		name := prefix + "nics"
		newVM(t, hypervisor, dir, name)
		nic := platform.NICSpec{MAC: "02:00:00:00:00:01", Port: platform.Isolated()}
		expect(t, "AttachNIC", hypervisor.AttachNIC(ctx, name, nic), nil)
		expect(t, "AttachNIC twice", hypervisor.AttachNIC(ctx, name, nic), platform.ErrExists)
		expect(t, "DetachNIC", hypervisor.DetachNIC(ctx, name, nic.MAC), nil)
		expect(t, "DetachNIC twice", hypervisor.DetachNIC(ctx, name, nic.MAC), platform.ErrNotFound)
	})

	t.Run("Counters", func(t *testing.T) {
		ctx := testContext(t)
		name := prefix + "counters"
		newVM(t, hypervisor, dir, name)
		missing := prefix + "missing"
		counters, err := hypervisor.Counters(ctx, []string{name, missing})
		expect(t, "Counters", err, nil)
		if _, found := counters[name]; !found {
			t.Errorf("Counters has no entry for %s", name)
		}
		if _, found := counters[missing]; found {
			t.Errorf("Counters has an entry for the missing VM %s", missing)
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

// HostNet checks a host network. used is a range the machine uses already;
// hypervisor makes the VM whose port the test sets.
func HostNet(t *testing.T, hostNet platform.HostNet, hypervisor platform.Hypervisor, dir string, used netip.Prefix) {
	ctx := testContext(t)
	// 198.18.0.0/15 is for benchmarks (RFC 2544): no machine uses it
	free := netip.MustParsePrefix("198.18.0.0/24")
	expect(t, "CheckRanges on a free range", hostNet.CheckRanges(ctx, []netip.Prefix{free}), nil)
	expect(t, "CheckRanges on a used range", hostNet.CheckRanges(ctx, []netip.Prefix{free, used}), platform.ErrInUse)

	link := platform.LinkSpec{
		Switch:    prefix + "switch",
		Addresses: []netip.Prefix{netip.MustParsePrefix("198.18.0.1/24")},
		Routes:    []netip.Prefix{netip.MustParsePrefix("198.18.1.0/24")},
		Gateway:   netip.MustParseAddr("198.18.0.2"),
	}
	t.Cleanup(func() { _ = hostNet.TearDown(context.Background()) })
	expect(t, "SetUp", hostNet.SetUp(ctx, link), nil)
	expect(t, "SetUp over a link that is there", hostNet.SetUp(ctx, link), nil)

	name := prefix + "port"
	newVM(t, hypervisor, dir, name)
	mac := "02:00:00:00:00:02"
	expect(t, "AttachNIC", hypervisor.AttachNIC(ctx, name, platform.NICSpec{MAC: mac, Switch: link.Switch, Port: platform.Isolated()}), nil)
	expect(t, "SetPortVLAN", hostNet.SetPortVLAN(ctx, name, mac, platform.Access(10)), nil)
	expect(t, "SetPortVLAN to trunk", hostNet.SetPortVLAN(ctx, name, mac, platform.Trunk()), nil)
	expect(t, "SetPortVLAN of a missing NIC", hostNet.SetPortVLAN(ctx, name, "02:00:00:00:00:99", platform.Trunk()), platform.ErrNotFound)
	expect(t, "SetPortVLAN of a missing VM", hostNet.SetPortVLAN(ctx, prefix+"missing", mac, platform.Trunk()), platform.ErrNotFound)

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

	t.Run("WriteToken", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control.token")
		expect(t, "WriteToken", lifecycle.WriteToken(path, []byte("secret")), nil)
		expect(t, "WriteToken again", lifecycle.WriteToken(path, []byte("newer")), nil)
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
