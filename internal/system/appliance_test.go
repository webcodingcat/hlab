package system_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/system"
)

const release = "release.vhdx"

// applianceVM is the appliance's VM with the MAC set A.
var applianceVM = system.ApplianceVM("A")

// fastTimings checks often and gives up quickly, so the tests take
// milliseconds.
var fastTimings = system.ApplianceTimings{
	Interval: 5 * time.Millisecond, Failures: 3, Boot: 5 * time.Second, Shutdown: time.Second,
}

// newAppliance returns a fake machine with the release disk and an agent,
// and an appliance on it that the test stops at its end; its watchdog takes
// the timings, fastTimings without them.
func newAppliance(t *testing.T, timings ...system.ApplianceTimings) (*fake.Fake, *fake.Agent, *system.Appliance) {
	t.Helper()
	machine := fake.New()
	machine.AddDisk(release, 4)
	agent := fake.NewAgent()
	machine.ServeAgent(applianceVM, agent.Serve)
	spec := system.ApplianceSpec{
		VM:         applianceVM,
		Release:    release,
		SystemDisk: "appliance/system-A.vhdx",
		MACs:       [4]string{"02:48:4c:0a:00:01", "02:48:4c:0a:00:02", "02:48:4c:0a:00:03", "02:48:4c:0a:00:04"},
	}
	timings = append(timings, fastTimings)
	appliance := system.New(machine.Platform()).NewAppliance(spec, timings[0], slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = appliance.Stop(context.Background()) })
	return machine, agent, appliance
}

// testPictures returns pictures of one generation that the agent accepts.
func testPictures(generation uint64) system.Pictures {
	return system.Pictures{
		Links:    agentproto.Links{Generation: generation},
		Ruleset:  agentproto.Ruleset{Generation: generation, Text: "flush ruleset\n"},
		DHCP:     agentproto.DHCP{Generation: generation, Instances: []agentproto.DHCPInstance{{Device: "public", Config: "port=0\n"}}},
		Metadata: agentproto.Metadata{Generation: generation},
	}
}

// generationOf returns the generation of the picture the agent applied
// last, or 0.
func generationOf(agent *fake.Agent, name string) uint64 {
	var header struct {
		Generation uint64 `json:"generation"`
	}
	_ = json.Unmarshal(agent.Picture(name), &header)
	return header.Generation
}

// eventually fails the test unless done turns true within a second.
func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// calls returns the fake's calls of a method.
func calls(machine *fake.Fake, method string) []string {
	var matching []string
	for _, call := range machine.Calls() {
		if strings.HasPrefix(call, method+" ") {
			matching = append(matching, call)
		}
	}
	return matching
}

func TestApplianceStartsOnADiskOfItsOwnAndGetsEveryPicture(t *testing.T) {
	machine, agent, appliance := newAppliance(t)
	if _, err := appliance.Start(t.Context(), testPictures(1)); err != nil {
		t.Fatal(err)
	}
	if got := calls(machine, "CreateDisk"); !slices.Equal(got, []string{"CreateDisk appliance/system-A.vhdx 0 " + release}) {
		t.Errorf("CreateDisk calls %q, want a differencing disk on the release", got)
	}
	state, err := machine.Platform().Hypervisor.VMState(t.Context(), applianceVM)
	if err != nil || state.Power != platform.PowerRunning {
		t.Fatalf("the appliance is %v, %v; want running", state, err)
	}
	for _, name := range []string{agentproto.PictureLinks, agentproto.PictureRuleset, agentproto.PictureDHCP, agentproto.PictureMetadata} {
		if got := generationOf(agent, name); got != 1 {
			t.Errorf("the agent has %s of generation %d, want 1", name, got)
		}
	}
}

func TestApplianceStartTakesOverARunningApplianceWhoseAgentAnswers(t *testing.T) {
	machine, _, first := newAppliance(t)
	if _, err := first.Start(t.Context(), testPictures(1)); err != nil {
		t.Fatal(err)
	}
	// hlabd crashed: the appliance runs on, a new hlabd finds it
	spec := system.ApplianceSpec{VM: applianceVM, Release: release, SystemDisk: "appliance/system-A.vhdx"}
	second := system.New(machine.Platform()).NewAppliance(spec, fastTimings, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = second.Stop(context.Background()) })
	takenOver, err := second.Start(t.Context(), testPictures(1))
	if err != nil || !takenOver {
		t.Fatalf("Start() = %v, %v; want the appliance taken over", takenOver, err)
	}
	if got := calls(machine, "CreateVM"); len(got) != 1 {
		t.Errorf("CreateVM calls %q, want only the first start's", got)
	}
}

func TestApplianceStartMakesAStoppedApplianceAnew(t *testing.T) {
	machine, _, appliance := newAppliance(t)
	if _, err := appliance.Start(t.Context(), testPictures(1)); err != nil {
		t.Fatal(err)
	}
	if err := appliance.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := appliance.Start(t.Context(), testPictures(2)); err != nil {
		t.Fatal(err)
	}
	want := []string{"DeleteVM " + applianceVM, "DeleteDisk appliance/system-A.vhdx", "CreateDisk appliance/system-A.vhdx 0 " + release}
	var got []string
	for _, call := range machine.Calls() {
		if strings.HasPrefix(call, "Delete") || strings.HasPrefix(call, "CreateDisk") {
			got = append(got, call)
		}
	}
	if len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
		t.Errorf("the second start's calls %q, want them to end with %q", got, want)
	}
}

func TestWatchdogRestartsADeadApplianceAtOnceAndSendsThePicturesAgain(t *testing.T) {
	// an appliance that is off is dead for sure: no need to wait for three
	// failed checks
	timings := fastTimings
	timings.Failures = 1000
	machine, agent, appliance := newAppliance(t, timings)
	if _, err := appliance.Start(t.Context(), testPictures(3)); err != nil {
		t.Fatal(err)
	}
	// the appliance dies: off, and its agent forgets everything
	if err := machine.Platform().Hypervisor.StopVM(t.Context(), applianceVM); err != nil {
		t.Fatal(err)
	}
	agent.Restart()

	eventually(t, "the restart", func() bool { return generationOf(agent, agentproto.PictureMetadata) == 3 })
	state, err := machine.Platform().Hypervisor.VMState(t.Context(), applianceVM)
	if err != nil || state.Power != platform.PowerRunning {
		t.Errorf("after the restart the appliance is %v, %v; want running", state, err)
	}
	if got := calls(machine, "CreateVM"); len(got) != 1 {
		t.Errorf("CreateVM calls %q: the watchdog should restart the VM, not make it anew", got)
	}
}

func TestWatchdogSendsThePicturesARestartedAgentLacks(t *testing.T) {
	machine, agent, appliance := newAppliance(t)
	if _, err := appliance.Start(t.Context(), testPictures(4)); err != nil {
		t.Fatal(err)
	}
	agent.Restart() // the agent alone restarted; the VM ran on

	eventually(t, "sending the pictures again", func() bool { return generationOf(agent, agentproto.PictureLinks) == 4 })
	if got := calls(machine, "StopVM"); len(got) != 0 {
		t.Errorf("StopVM calls %q: an answering appliance needs no restart", got)
	}
}

// A picture that the watchdog sends never follows a newer one that an
// update sent meanwhile: the update waits until the watchdog is done.
func TestWatchdogNeverSendsAnOlderPictureAfterANewerOne(t *testing.T) {
	slow := fastTimings
	slow.Interval = 50 * time.Millisecond // no check comes between the update and the look
	_, agent, appliance := newAppliance(t, slow)
	if _, err := appliance.Start(t.Context(), testPictures(1)); err != nil {
		t.Fatal(err)
	}
	waiting, release := agent.HoldNext(agentproto.PictureLinks)
	agent.Restart() // the watchdog sends generation 1 again, and its links wait
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog sent no links")
	}
	updated := make(chan error, 1)
	go func() { updated <- appliance.Update(t.Context(), testPictures(2)) }()
	time.Sleep(20 * time.Millisecond) // an update that does not wait has sent its links by now
	release()
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if got := generationOf(agent, agentproto.PictureLinks); got != 2 {
		t.Errorf("the agent has the links of generation %d, want 2", got)
	}
}

func TestWatchdogDoesNotSendARefusedPictureAgain(t *testing.T) {
	_, agent, appliance := newAppliance(t)
	pictures := testPictures(5)
	pictures.DHCP.Instances[0].Config = "" // the check refuses it
	if _, err := appliance.Start(t.Context(), pictures); err != nil {
		t.Fatalf("a refused picture failed the start: %v", err)
	}
	if got := generationOf(agent, agentproto.PictureDHCP); got != 0 {
		t.Fatalf("the agent took the broken picture (generation %d)", got)
	}
	if got := generationOf(agent, agentproto.PictureMetadata); got != 5 {
		t.Errorf("the other pictures did not arrive: metadata generation %d", got)
	}
	time.Sleep(10 * fastTimings.Interval) // ten rounds of the watchdog
	if puts := agent.Puts(agentproto.PictureDHCP); puts != 1 {
		t.Errorf("the refused DHCP picture was sent %d times, want once", puts)
	}
}

// An appliance that Start made anew and that then fails is shut down
// again: nothing else would stop it.
func TestFailedApplianceStartShutsItDown(t *testing.T) {
	machine, agent, appliance := newAppliance(t)
	agent.FailNext(agentproto.PictureLinks)
	if _, err := appliance.Start(t.Context(), testPictures(1)); err == nil {
		t.Fatal("the start succeeded although the agent refused the links")
	}
	state, err := machine.Platform().Hypervisor.VMState(t.Context(), applianceVM)
	if err != nil || state.Power != platform.PowerOff {
		t.Errorf("after the failed start the appliance is %v, %v; want off", state, err)
	}
}

func TestApplianceStopShutsItDownAndKeepsTheVM(t *testing.T) {
	machine, _, appliance := newAppliance(t)
	if _, err := appliance.Start(t.Context(), testPictures(1)); err != nil {
		t.Fatal(err)
	}
	if err := appliance.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := machine.Platform().Hypervisor.VMState(t.Context(), applianceVM)
	if err != nil || state.Power != platform.PowerOff {
		t.Errorf("after Stop the appliance is %v, %v; want off and still there", state, err)
	}
	if got := calls(machine, "ShutdownVM"); len(got) != 1 {
		t.Errorf("ShutdownVM calls %q, want one", got)
	}
}

func TestApplianceRemoveDeletesItsVMAndItsDisk(t *testing.T) {
	machine, _, appliance := newAppliance(t)
	if _, err := appliance.Start(t.Context(), testPictures(1)); err != nil {
		t.Fatal(err)
	}
	if err := appliance.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Platform().Hypervisor.VMState(t.Context(), applianceVM); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("after Remove the appliance's VM: %v, want it gone", err)
	}
	if got := calls(machine, "DeleteDisk"); !slices.Contains(got, "DeleteDisk appliance/system-A.vhdx") {
		t.Errorf("DeleteDisk calls %q, want the system disk deleted", got)
	}
	// the watchdog ended with it: nothing makes the appliance anew
	time.Sleep(10 * fastTimings.Interval)
	if got := calls(machine, "CreateVM"); len(got) != 1 {
		t.Errorf("CreateVM calls %q after Remove, want only the start's", got)
	}
}

func TestApplianceAnnounceSendsTheLinksAgain(t *testing.T) {
	_, agent, appliance := newAppliance(t)
	if _, err := appliance.Start(t.Context(), testPictures(2)); err != nil {
		t.Fatal(err)
	}
	agent.Restart() // forgets the pictures; the watchdog is slower than the check below
	if err := appliance.Announce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := generationOf(agent, agentproto.PictureLinks); got != 2 {
		t.Errorf("the agent has links of generation %d after Announce, want 2", got)
	}
}
