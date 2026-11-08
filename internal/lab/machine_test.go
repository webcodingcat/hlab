package lab_test

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/system"
)

const release = "release.vhdx"

// machineOptions are the machine's options with a quick watchdog.
var machineOptions = lab.MachineOptions{
	ApplianceRelease: release,
	Timings:          system.ApplianceTimings{Interval: 5 * time.Millisecond, Failures: 3, Boot: 5 * time.Second, Shutdown: time.Second},
}

// withAppliance gives the fake machine the appliance's release disk and an
// agent.
func withAppliance(machine *fake.Fake) *fake.Agent {
	machine.AddDisk(release, 4)
	agent := fake.NewAgent()
	machine.ServeAgent(system.ApplianceVM, agent.Serve)
	return agent
}

func TestStartMachineConnectsTheHostAndStartsTheAppliance(t *testing.T) {
	machine := fake.New()
	agent := withAppliance(machine)
	// a VM of a crashed HLab whose record is gone
	stray := platform.VMSpec{Name: "stray", Tag: system.VMTag, CPUs: 1, MemoryMB: 512}
	if err := machine.Platform().Hypervisor.CreateVM(t.Context(), stray); err != nil {
		t.Fatal(err)
	}
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})

	if err := l.StartMachine(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	link, up := machine.Link()
	if !up || link.Addresses[0].String() != "198.18.0.1/29" || link.Gateways[0].String() != "198.18.0.2" {
		t.Errorf("Link() = %+v, %v; want the host at 198.18.0.1 routing through 198.18.0.2", link, up)
	}
	if _, err := machine.Platform().Hypervisor.VMState(t.Context(), "stray"); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("the stray VM: %v, want it removed", err)
	}
	if agent.Picture("ruleset") == nil {
		t.Error("the appliance got no ruleset")
	}

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, up := machine.Link(); up {
		t.Error("the host link is still there after Close")
	}
	state, err := machine.Platform().Hypervisor.VMState(t.Context(), system.ApplianceVM)
	if err != nil || state.Power != platform.PowerOff {
		t.Errorf("after Close the appliance is %v, %v; want off", state, err)
	}
}

func TestStartMachineRemovesTheHostLinkWhenTheApplianceFails(t *testing.T) {
	machine := fake.New() // without the release disk
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })

	err := l.StartMachine(t.Context(), machineOptions)

	if !errors.Is(err, platform.ErrNotFound) || !strings.Contains(err.Error(), "appliance") {
		t.Fatalf("got %v, want the missing release disk", err)
	}
	if _, up := machine.Link(); up {
		t.Error("the host link stayed after the failed start")
	}
}

func TestStartMachineChecksTheRangesFirst(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	machine.SetUsedRanges(netip.MustParsePrefix("198.19.0.0/16"))
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })

	if err := l.StartMachine(t.Context(), machineOptions); !errors.Is(err, platform.ErrInUse) {
		t.Fatalf("got %v, want ErrInUse", err)
	}
	if created := slices.ContainsFunc(machine.Calls(), func(call string) bool { return strings.HasPrefix(call, "CreateVM") }); created {
		t.Error("the appliance was created although a range is in use")
	}
}
