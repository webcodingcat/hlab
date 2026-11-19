package servers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/servers"
)

// haltTimeout is the short shutdown deadline the lifecycle tests use
// instead of the real 60 s.
const haltTimeout = 200 * time.Millisecond

func testLifecycle(t *testing.T) {
	t.Run("LIFE-02", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, mod, _ := newFakeEnv(t, fake, shortHalt)
		lc := lifecycleOf(t, mod)

		ran := createdServer(t, env, "ran", nil)
		ranVM := vmName(t, fake)
		// The guest turned its VM off; the status watch has not seen it yet.
		guestOff := createdServer(t, env, "guest-off", nil)
		guestOffVM := vmName(t, fake)
		fake.SetState(guestOffVM, platform.StateOff)
		stopped := createdServer(t, env, "stopped", nil)
		stoppedVM := vmName(t, fake)
		runAction(t, env, stopped, "poweroff", nil)

		// At a host shutdown Hyper-V has already acted: Halt changes nothing.
		n := len(fake.Calls())
		if err := lc.Halt(lifeCtx(t), "save", true); err != nil {
			t.Fatalf("Halt save at host shutdown: %v", err)
		}
		if p := callsIn(fake.Calls()[n:], "Power"); len(p) != 0 {
			t.Errorf("Halt at host shutdown made power calls %+v, want none", p)
		}
		for id, want := range map[int64]string{ran: "running", guestOff: "running", stopped: "off"} {
			if got := getServer(t, env, id).Status; got != want {
				t.Errorf("server %d status after Halt at host shutdown: %q, want %q", id, got, want)
			}
		}
		wantVMState(t, fake, ranVM, platform.StateRunning)

		n = len(fake.Calls())
		if err := lc.Halt(lifeCtx(t), "save", false); err != nil {
			t.Fatalf("Halt save: %v", err)
		}
		if got := powerOps(fake.Calls()[n:], ranVM); !equalOps(got, platform.PowerSave) {
			t.Errorf("power calls on the running VM during Halt save: %v, want [%v]", got, platform.PowerSave)
		}
		wantVMState(t, fake, ranVM, platform.StateSaved)
		for _, vm := range []string{guestOffVM, stoppedVM} {
			if got := powerOps(fake.Calls()[n:], vm); len(got) != 0 {
				t.Errorf("power calls on off VM %s during Halt save: %v, want none", vm, got)
			}
		}
		for id, want := range map[int64]string{ran: "running", guestOff: "off", stopped: "off"} {
			if got := getServer(t, env, id).Status; got != want {
				t.Errorf("server %d status after Halt save: %q, want %q", id, got, want)
			}
		}

		n = len(fake.Calls())
		if err := lc.Restore(lifeCtx(t), "save"); err != nil {
			t.Fatalf("Restore save: %v", err)
		}
		wantVMState(t, fake, ranVM, platform.StateRunning)
		for _, vm := range []string{guestOffVM, stoppedVM} {
			if got := powerOps(fake.Calls()[n:], vm); len(got) != 0 {
				t.Errorf("power calls on VM %s of an off server during Restore: %v, want none", vm, got)
			}
			wantVMState(t, fake, vm, platform.StateOff)
		}
		tickWatch(t, mod)
		for id, want := range map[int64]string{ran: "running", guestOff: "off", stopped: "off"} {
			if got := getServer(t, env, id).Status; got != want {
				t.Errorf("server %d status after Restore: %q, want %q", id, got, want)
			}
		}

		// A failed save falls back to a guest shutdown, then a hard off.
		fake.HoldShutdown(ranVM)
		fake.FailNext("Power", &platform.Error{Kind: platform.Failed, Op: "Power", Err: errors.New("save refused")})
		n = len(fake.Calls())
		if err := lc.Halt(lifeCtx(t), "save", false); err != nil {
			t.Fatalf("Halt save with a failing save: %v", err)
		}
		if got := powerOps(fake.Calls()[n:], ranVM); !equalOps(got, platform.PowerSave, platform.PowerShutdown, platform.PowerStop) {
			t.Errorf("power calls after a failed save: %v, want [%v %v %v]", got, platform.PowerSave, platform.PowerShutdown, platform.PowerStop)
		}
		wantVMState(t, fake, ranVM, platform.StateOff)
		if got := getServer(t, env, ran).Status; got != "running" {
			t.Errorf("status of the server that ran after a failed save: %q, want running", got)
		}
	})

	t.Run("LIFE-03", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, mod, _ := newFakeEnv(t, fake, shortHalt)
		lc := lifecycleOf(t, mod)

		willing := createdServer(t, env, "willing", nil)
		willingVM := vmName(t, fake)
		holding := createdServer(t, env, "holding", nil)
		holdingVM := vmName(t, fake)
		fake.HoldShutdown(holdingVM)
		stopped := createdServer(t, env, "stopped", nil)
		stoppedVM := vmName(t, fake)
		runAction(t, env, stopped, "poweroff", nil)

		n := len(fake.Calls())
		if err := lc.Halt(lifeCtx(t), "shutdown", false); err != nil {
			t.Fatalf("Halt shutdown: %v", err)
		}
		calls := fake.Calls()[n:]
		if got := powerOps(calls, willingVM); !equalOps(got, platform.PowerShutdown) {
			t.Errorf("power calls on the VM whose guest shuts down: %v, want [%v]", got, platform.PowerShutdown)
		}
		if got := powerOps(calls, holdingVM); !equalOps(got, platform.PowerShutdown, platform.PowerStop) {
			t.Errorf("power calls on the VM whose guest keeps running: %v, want [%v %v]", got, platform.PowerShutdown, platform.PowerStop)
		}
		if got := powerOps(calls, stoppedVM); len(got) != 0 {
			t.Errorf("power calls on the off VM: %v, want none", got)
		}
		// Every guest is asked before anything is turned off hard.
		firstStop, lastShutdown := -1, -1
		for i, c := range callsIn(calls, "Power") {
			switch c.Arg {
			case platform.PowerShutdown:
				lastShutdown = i
			case platform.PowerStop:
				if firstStop < 0 {
					firstStop = i
				}
			}
		}
		if firstStop >= 0 && firstStop < lastShutdown {
			t.Errorf("a VM was turned off hard before every guest was asked to shut down: %+v", callsIn(calls, "Power"))
		}
		for _, vm := range []string{willingVM, holdingVM, stoppedVM} {
			wantVMState(t, fake, vm, platform.StateOff)
		}
		for id, want := range map[int64]string{willing: "running", holding: "running", stopped: "off"} {
			if got := getServer(t, env, id).Status; got != want {
				t.Errorf("server %d status after Halt shutdown: %q, want %q", id, got, want)
			}
		}

		n = len(fake.Calls())
		if err := lc.Restore(lifeCtx(t), "shutdown"); err != nil {
			t.Fatalf("Restore shutdown: %v", err)
		}
		wantVMState(t, fake, willingVM, platform.StateRunning)
		wantVMState(t, fake, holdingVM, platform.StateRunning)
		wantVMState(t, fake, stoppedVM, platform.StateOff)
		if got := powerOps(fake.Calls()[n:], stoppedVM); len(got) != 0 {
			t.Errorf("power calls on the VM of the off server during Restore: %v, want none", got)
		}
		tickWatch(t, mod)
		for id, want := range map[int64]string{willing: "running", holding: "running", stopped: "off"} {
			if got := getServer(t, env, id).Status; got != want {
				t.Errorf("server %d status after Restore: %q, want %q", id, got, want)
			}
		}
	})
}

func shortHalt(d *servers.Deps) { d.HaltTimeout = haltTimeout }

func lifecycleOf(t *testing.T, mod *servers.Module) resource.Lifecycle {
	t.Helper()
	var m any = mod
	lc, ok := m.(resource.Lifecycle)
	if !ok {
		t.Fatal("servers module does not implement resource.Lifecycle")
	}
	return lc
}

func lifeCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), apitest.ActionTimeout+haltTimeout)
	t.Cleanup(cancel)
	return ctx
}

// powerOps lists the power operations of calls on vm in order.
func powerOps(calls []platformtest.Call, vm string) []platform.PowerOp {
	var ops []platform.PowerOp
	for _, c := range callsIn(calls, "Power") {
		if c.VM == vm {
			if op, ok := c.Arg.(platform.PowerOp); ok {
				ops = append(ops, op)
			}
		}
	}
	return ops
}

func equalOps(got []platform.PowerOp, want ...platform.PowerOp) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func wantVMState(t *testing.T, fake *platformtest.Fake, vm string, want platform.VMState) {
	t.Helper()
	got, err := fake.State(context.Background(), vm)
	if err != nil {
		t.Fatalf("state of VM %s: %v", vm, err)
	}
	if got != want {
		t.Errorf("state of VM %s: %v, want %v", vm, got, want)
	}
}
