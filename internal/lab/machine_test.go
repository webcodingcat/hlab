package lab_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/system"
)

// machineOptions are the machine's options with a quick watchdog; TestMain
// gives them a release disk.
var machineOptions = lab.MachineOptions{
	Timings: system.ApplianceTimings{Interval: 5 * time.Millisecond, Failures: 3, Boot: 5 * time.Second, Shutdown: time.Second},
}

// releaseContent is what the release disk of machineOptions holds.
const releaseContent = "the appliance's release"

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hlab-lab-test-")
	if err != nil {
		panic(err)
	}
	machineOptions.ApplianceRelease = filepath.Join(dir, "hlab-appliance.vhdx")
	if err := os.WriteFile(machineOptions.ApplianceRelease, []byte(releaseContent), 0o644); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// releaseID is the ID of the release disk of machineOptions.
func releaseID() string {
	sum := sha256.Sum256([]byte(releaseContent))
	return hex.EncodeToString(sum[:])
}

// withAppliance gives the fake machine an agent in the appliance's VM of
// each MAC set and returns the one of set A, which a first start takes.
func withAppliance(machine *fake.Fake) *fake.Agent {
	agent, _ := withAppliances(machine)
	return agent
}

// withAppliances gives the fake machine an agent in the appliance's VM of
// each MAC set and returns both.
func withAppliances(machine *fake.Fake) (setA, setB *fake.Agent) {
	setA, setB = fake.NewAgent(), fake.NewAgent()
	machine.ServeAgent(system.ApplianceVM("A"), setA.Serve)
	machine.ServeAgent(system.ApplianceVM("B"), setB.Serve)
	return setA, setB
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
	state, err := machine.Platform().Hypervisor.VMState(t.Context(), system.ApplianceVM("A"))
	if err != nil || state.Power != platform.PowerOff {
		t.Errorf("after Close the appliance is %v, %v; want off", state, err)
	}
}

func TestStartMachineRemovesTheHostLinkWhenTheApplianceFails(t *testing.T) {
	machine := fake.New()
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	opts := machineOptions
	opts.ApplianceRelease = filepath.Join(t.TempDir(), "missing.vhdx")

	err := l.StartMachine(t.Context(), opts)

	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "appliance") {
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
