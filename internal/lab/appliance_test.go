package lab_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// runningAppliance makes the fake machine run the appliance's VM of a MAC
// set, as a crashed hlabd leaves it, and the state name a release and a set.
func runningAppliance(t *testing.T, l *lab.Lab, machine *fake.Fake, recorded state.Appliance, sets ...string) {
	t.Helper()
	hypervisor := machine.Platform().Hypervisor
	for _, set := range sets {
		spec := platform.VMSpec{Name: system.ApplianceVM(set), Tag: system.VMTag, CPUs: 2, MemoryMB: 1024}
		if err := hypervisor.CreateVM(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		if err := hypervisor.StartVM(t.Context(), spec.Name); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Change(func(tx *state.Tx) error { tx.Lab().Appliance = recorded; return nil }); err != nil {
		t.Fatal(err)
	}
}

// recordedAppliance returns the appliance the state names.
func recordedAppliance(l *lab.Lab) state.Appliance {
	var recorded state.Appliance
	_ = l.View(func(tx *state.Tx) error {
		recorded = tx.Lab().Appliance
		return nil
	})
	return recorded
}

// power returns the power of a VM, or "gone".
func power(t *testing.T, machine *fake.Fake, vm string) string {
	t.Helper()
	vmState, err := machine.Platform().Hypervisor.VMState(t.Context(), vm)
	if errors.Is(err, platform.ErrNotFound) {
		return "gone"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(vmState.Power)
}

// callIndex returns the position of a call to the fake after the first
// skipped ones, or -1.
func callIndex(machine *fake.Fake, skipped int, call string) int {
	return slices.Index(machine.Calls()[skipped:], call)
}

func TestStartMachineNamesTheReleaseAndTheSetOfANewAppliance(t *testing.T) {
	machine := fake.New()
	withAppliance(machine)
	dataDir := t.TempDir()
	l := start(t, machine, lab.Options{DataDir: dataDir})
	t.Cleanup(func() { _ = l.Close() })
	// what earlier releases left in the appliance's folder
	dir := filepath.Join(dataDir, lab.ApplianceDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old.vhdx", "system-B.vhdx", "new.vhdx.tmp-1"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := l.Machine().Start(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}

	if got, want := recordedAppliance(l), (state.Appliance{Release: releaseID(), MACSet: "A"}); got != want {
		t.Errorf("the state names the appliance %+v, want %+v", got, want)
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, entry := range entries {
		left = append(left, entry.Name())
	}
	if want := []string{releaseID() + ".vhdx"}; !slices.Equal(left, want) {
		t.Errorf("the appliance's folder holds %q, want only %q", left, want)
	}
}

func TestStartMachineUpdatesATakenOverApplianceOfAnOlderRelease(t *testing.T) {
	machine := fake.New()
	_, agentB := withAppliances(machine)
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	runningAppliance(t, l, machine, state.Appliance{Release: "older", MACSet: "A"}, "A")

	skipped := len(machine.Calls())
	if err := l.Machine().Start(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}

	if got := power(t, machine, system.ApplianceVM("A")); got != "gone" {
		t.Errorf("the old appliance is %s, want it gone", got)
	}
	if got := power(t, machine, system.ApplianceVM("B")); got != string(platform.PowerRunning) {
		t.Errorf("the new appliance is %s, want it running", got)
	}
	if got, want := recordedAppliance(l), (state.Appliance{Release: releaseID(), MACSet: "B"}); got != want {
		t.Errorf("the state names the appliance %+v, want %+v", got, want)
	}
	if links := string(agentB.Picture("links")); !strings.Contains(links, "02:48:4c:0b:00:01") {
		t.Errorf("the new appliance's links picture lacks the MAC set B: %s", links)
	}
	// the old one went only after the new one was there
	if created, removed := callIndex(machine, skipped, "StartVM "+system.ApplianceVM("B")), callIndex(machine, skipped, "DeleteVM "+system.ApplianceVM("A")); removed < created {
		t.Errorf("the old appliance went (call %d) before the new one started (call %d)", removed, created)
	}
}

func TestStartMachineKeepsTheOldApplianceWhenTheNewOneDoesNotComeUp(t *testing.T) {
	machine := fake.New()
	machine.ServeAgent(system.ApplianceVM("A"), fake.NewAgent().Serve) // no agent answers in B
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	older := state.Appliance{Release: "older", MACSet: "A"}
	runningAppliance(t, l, machine, older, "A")
	opts := machineOptions
	opts.Timings.Boot = 50 * time.Millisecond

	if err := l.Machine().Start(t.Context(), opts); err != nil {
		t.Fatal(err)
	}

	if got := power(t, machine, system.ApplianceVM("A")); got != string(platform.PowerRunning) {
		t.Errorf("the old appliance is %s, want it running on", got)
	}
	if got := power(t, machine, system.ApplianceVM("B")); got != "gone" {
		t.Errorf("the new appliance that did not come up is %s, want it gone", got)
	}
	if got := recordedAppliance(l); got != older {
		t.Errorf("the state names the appliance %+v, want still %+v", got, older)
	}
}

func TestStartMachineRepeatsAnUpdateThatHLabsEndCutShort(t *testing.T) {
	// the new appliance ran beside the old one, but the state did not name
	// it yet: it goes, the old one is taken over and updated anew
	machine := fake.New()
	withAppliances(machine)
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	runningAppliance(t, l, machine, state.Appliance{Release: "older", MACSet: "A"}, "A", "B")

	skipped := len(machine.Calls())
	if err := l.Machine().Start(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}

	removedStray := callIndex(machine, skipped, "DeleteVM "+system.ApplianceVM("B"))
	if removedStray < 0 || removedStray > callIndex(machine, skipped, "CreateVM "+system.ApplianceVM("B")) {
		t.Errorf("the cut-short appliance B was not removed before the update: calls %q", machine.Calls())
	}
	if got := power(t, machine, system.ApplianceVM("A")); got != "gone" {
		t.Errorf("the old appliance is %s, want it gone", got)
	}
	if got, want := recordedAppliance(l), (state.Appliance{Release: releaseID(), MACSet: "B"}); got != want {
		t.Errorf("the state names the appliance %+v, want %+v", got, want)
	}
}

func TestStartMachineTakesOverTheNewApplianceOfAnUpdateThatHLabsEndCutShort(t *testing.T) {
	// the state named the new appliance, but the old one was still there
	machine := fake.New()
	withAppliances(machine)
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	updated := state.Appliance{Release: releaseID(), MACSet: "B"}
	runningAppliance(t, l, machine, updated, "A", "B")

	skipped := len(machine.Calls())
	if err := l.Machine().Start(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}

	if got := power(t, machine, system.ApplianceVM("A")); got != "gone" {
		t.Errorf("the old appliance is %s, want it gone", got)
	}
	if got := power(t, machine, system.ApplianceVM("B")); got != string(platform.PowerRunning) {
		t.Errorf("the new appliance is %s, want it taken over", got)
	}
	if got := callIndex(machine, skipped, "CreateVM "+system.ApplianceVM("B")); got >= 0 {
		t.Error("the new appliance was made anew; it should have been taken over")
	}
	if got := recordedAppliance(l); got != updated {
		t.Errorf("the state names the appliance %+v, want %+v", got, updated)
	}
}
