package servers_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
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

	t.Run("INST-05/restore-recreates-vm", func(t *testing.T) {
		p := newRecPlatform()
		p.diskFileBytes = 1 // the kept disks exist as files
		env, mod, _ := newFakeEnv(t, p, shortHalt)
		lc := lifecycleOf(t, mod)

		type kept struct {
			id     int64
			vm     string
			disk   string
			status string
		}
		var all []kept
		for _, c := range []struct{ name, status string }{
			{"run-a", "running"}, {"off-a", "off"}, {"run-b", "running"}, {"off-b", "off"},
		} {
			id := createdServer(t, env, c.name, nil)
			vm := vmName(t, p.Fake)
			if c.status == "off" {
				runAction(t, env, id, "poweroff", nil)
			}
			all = append(all, kept{id: id, vm: vm, disk: systemDisk(t, p, vm), status: c.status})
		}

		// A KeepData uninstall removed the VMs; state and disks stay.
		for _, s := range all {
			if err := p.DeleteVM(context.Background(), s.vm); err != nil {
				t.Fatalf("delete VM %s as an uninstall does: %v", s.vm, err)
			}
		}

		logs := captureDefaultLog(t)
		p.FailNext("CreateVM", &platform.Error{Kind: platform.Failed, Op: "CreateVM", Err: errors.New("create refused")})
		n := len(p.Calls())
		if err := lc.Restore(lifeCtx(t), "shutdown"); err != nil {
			t.Fatalf("Restore with one failing reconcile: %v", err)
		}
		calls := p.Calls()[n:]

		creates := callsIn(calls, "CreateVM")
		if len(creates) == 0 {
			t.Fatal("Restore made no CreateVM call for the servers without a VM")
		}
		failed := creates[0].VM
		if failed == "" {
			failed = vmSpec(t, creates[0]).Name
		}
		if d := callsIn(calls, "CreateDisk"); len(d) != 0 {
			t.Errorf("Restore created %d new disks, want none (the disks are kept)", len(d))
		}
		if d := callsIn(calls, "CreateChildDisk"); len(d) != 0 {
			t.Errorf("Restore created %d new child disks, want none (the disks are kept)", len(d))
		}
		if !strings.Contains(logs.String(), "create refused") {
			t.Errorf("the failed reconcile of VM %s is not in the log:\n%s", failed, logs.String())
		}

		restored := 0
		for _, s := range all {
			if s.vm == failed {
				continue
			}
			restored++
			want := platform.StateOff
			if s.status == "running" {
				want = platform.StateRunning
			}
			wantVMState(t, p.Fake, s.vm, want)
			if got := systemDisk(t, p, s.vm); got != s.disk {
				t.Errorf("system disk of the recreated VM %s: %s, want the kept %s", s.vm, got, s.disk)
			}
			if s.status == "off" {
				if got := powerOps(calls, s.vm); len(got) != 0 {
					t.Errorf("power calls on the recreated VM %s of an off server: %v, want none", s.vm, got)
				}
			}
		}
		if restored != len(all)-1 {
			t.Errorf("servers restored after the failed one: %d, want %d", restored, len(all)-1)
		}
		tickWatch(t, mod)
		for _, s := range all {
			if s.vm == failed {
				continue
			}
			if got := getServer(t, env, s.id).Status; got != s.status {
				t.Errorf("server %d status after Restore: %q, want %q", s.id, got, s.status)
			}
		}
	})
}

// logBuffer is a log destination safe for concurrent writes.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureDefaultLog sends the default logger, at debug level, to a buffer
// until t ends. A test using it must not run in parallel.
func captureDefaultLog(t *testing.T) *logBuffer {
	t.Helper()
	buf := &logBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
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
