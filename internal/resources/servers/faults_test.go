package servers_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/servers"
)

// The servers module is the fault manager's server injector.
var _ faults.ServerInjector = (*servers.Module)(nil)

// testFaults holds the rows of faults_test.go; TestServers runs them.
func testFaults(t *testing.T) {
	t.Run("FAULT-05/crash", func(t *testing.T) {
		env, mod, p, h := newFaultEnv(t)
		ctx := lifeCtx(t)
		project := env.Project.Name

		// A running server: hard off, status off at once, no action.
		id := createdServer(t, env, "crash", nil)
		vm := vmName(t, p.Fake)
		actsPath := fmt.Sprintf("/servers/%d/actions", id)
		acts0 := len(listActions(t, env, actsPath))
		n := len(p.Calls())
		if err := mod.Crash(ctx, project, id); err != nil {
			t.Fatalf("Crash on a running server: %v", err)
		}
		if got := powerOps(p.Calls()[n:], vm); !slices.Contains(got, platform.PowerStop) {
			t.Errorf("power calls of Crash: %v, want a %v", got, platform.PowerStop)
		}
		wantVMState(t, p.Fake, vm, platform.StateOff)
		if got := getServer(t, env, id).Status; got != "off" {
			t.Errorf("status right after Crash: %q, want off", got)
		}
		if got := len(listActions(t, env, actsPath)); got != acts0 {
			t.Errorf("GET %s after Crash: %d actions, want %d", actsPath, got, acts0)
		}

		// An off server: accepted, stays off.
		n = len(p.Calls())
		if err := mod.Crash(ctx, project, id); err != nil {
			t.Fatalf("Crash on an off server: %v", err)
		}
		if got := powerOps(p.Calls()[n:], vm); slices.Contains(got, platform.PowerStart) {
			t.Errorf("power calls of Crash on an off server: %v, want no start", got)
		}
		wantVMState(t, p.Fake, vm, platform.StateOff)
		if got := getServer(t, env, id).Status; got != "off" {
			t.Errorf("status after Crash on an off server: %q, want off", got)
		}

		// A frozen server: the freeze ends before the stop.
		frozen := createdServer(t, env, "crash-frozen", nil)
		frozenVM := vmName(t, p.Fake)
		if err := mod.Freeze(ctx, project, frozen); err != nil {
			t.Fatalf("Freeze: %v", err)
		}
		n = len(p.Calls())
		if err := mod.Crash(ctx, project, frozen); err != nil {
			t.Fatalf("Crash on a frozen server: %v", err)
		}
		ends := h.endedOf(faults.KindServerFreeze, frozen)
		if len(ends) == 0 {
			t.Fatalf("Crash on a frozen server called no EndFault(%s)", faults.KindServerFreeze)
		}
		if e := ends[0]; e.project != project || e.targetType != faults.TargetServer {
			t.Errorf("EndFault called with project %q, target type %q, want %q, %q", e.project, e.targetType, project, faults.TargetServer)
		}
		calls := p.Calls()
		seq := ends[0].seq
		if got := powerOps(calls[n:seq], frozenVM); slices.Contains(got, platform.PowerStop) {
			t.Errorf("power calls before EndFault(%s): %v, want no stop", faults.KindServerFreeze, got)
		}
		if got := powerOps(calls[seq:], frozenVM); !slices.Contains(got, platform.PowerStop) {
			t.Errorf("power calls after EndFault(%s): %v, want a %v", faults.KindServerFreeze, got, platform.PowerStop)
		}
		wantVMState(t, p.Fake, frozenVM, platform.StateOff)
		if got := getServer(t, env, frozen).Status; got != "off" {
			t.Errorf("status after Crash on a frozen server: %q, want off", got)
		}
	})

	t.Run("FAULT-05/blocked", func(t *testing.T) {
		env, mod, p, h := newFaultEnv(t)
		ctx := lifeCtx(t)
		id := createdServer(t, env, "blocked", map[string]any{"user_data": blockedUserData})
		vm := vmName(t, p.Fake)
		disk := systemDisk(t, p, vm)
		path := fmt.Sprintf("/servers/%d", id)
		image0 := imageName(rawServer(t, env, path))
		userData0 := seedFile(t, h.dataFolder, "user-data")
		if !fileExists(disk) {
			t.Fatalf("system disk file %s missing after create", disk)
		}
		if err := mod.Crash(ctx, env.Project.Name, id); err != nil {
			t.Fatalf("Crash: %v", err)
		}
		h.crashed.Store(id)

		for _, c := range []struct {
			action string
			body   any
		}{
			{"poweron", nil},
			{"reboot", nil},
			{"reset", nil},
			{"rebuild", map[string]any{"image": "debian-13", "user_data": "#cloud-config\n# hlab-refused-rebuild\n"}},
			{"change_type", map[string]any{"server_type": "cx33", "upgrade_disk": false}},
		} {
			t.Run(c.action, func(t *testing.T) {
				n := len(p.Calls())
				creates := p.createCount()
				act, _ := startAction(t, env, id, c.action, c.body)
				a := env.WaitAction(act)
				if a.Status != "error" || a.Error == nil || a.Error.Code != "server_error" {
					t.Errorf("%s during a crash: action %q %+v, want error server_error", c.action, a.Status, a.Error)
				}
				for _, op := range powerOps(p.Calls()[n:], vm) {
					if op == platform.PowerStart || op == platform.PowerReboot || op == platform.PowerReset {
						t.Errorf("%s during a crash made power call %v, want none that runs the VM", c.action, op)
					}
				}
				wantVMState(t, p.Fake, vm, platform.StateOff)
				if got := getServer(t, env, id).Status; got != "off" {
					t.Errorf("status after %s during a crash: %q, want off", c.action, got)
				}
				if c.action == "rebuild" {
					if got := p.createCount(); got != creates {
						t.Errorf("CreateDisk calls of the refused rebuild: %d, want none", got-creates)
					}
					if detached(p.Calls()[n:], disk) {
						t.Errorf("refused rebuild detached the system disk %s", disk)
					}
					if !fileExists(disk) {
						t.Errorf("system disk file %s gone after the refused rebuild", disk)
					}
					if got := imageName(rawServer(t, env, path)); got != image0 {
						t.Errorf("GET %s image after the refused rebuild: %q, want %q", path, got, image0)
					}
					if got := seedFile(t, h.dataFolder, "user-data"); string(got) != string(userData0) {
						t.Errorf("seed user-data after the refused rebuild:\n%s\nwant unchanged:\n%s", got, userData0)
					}
				}
			})
		}

		runAction(t, env, id, "poweroff", nil)

		h.crashed.Store(0)
		runAction(t, env, id, "poweron", nil)
		wantVMState(t, p.Fake, vm, platform.StateRunning)
		if got := getServer(t, env, id).Status; got != "running" {
			t.Errorf("status after poweron once the crash ended: %q, want running", got)
		}
	})

	t.Run("FAULT-06/freeze", func(t *testing.T) {
		env, mod, p, _ := newFaultEnv(t)
		ctx := lifeCtx(t)
		project := env.Project.Name

		id := createdServer(t, env, "freeze", nil)
		vm := vmName(t, p.Fake)
		if err := mod.Freeze(ctx, project, id); err != nil {
			t.Fatalf("Freeze on a running server: %v", err)
		}
		wantVMState(t, p.Fake, vm, platform.StatePaused)
		tickWatch(t, mod)
		if got := getServer(t, env, id).Status; got != "running" {
			t.Errorf("status of a frozen server after a status watch run: %q, want running", got)
		}

		// Thaw resumes the paused VM.
		if err := mod.Thaw(ctx, project, id); err != nil {
			t.Fatalf("Thaw on a paused VM: %v", err)
		}
		wantVMState(t, p.Fake, vm, platform.StateRunning)

		// Freeze is refused on a starting and on an off server.
		refuse := func(t *testing.T, what string) {
			t.Helper()
			n := len(p.Calls())
			err := mod.Freeze(ctx, project, id)
			var ie *faults.InputError
			if !errors.As(err, &ie) {
				t.Errorf("Freeze on %s server: %v, want *faults.InputError", what, err)
			}
			if got := powerOps(p.Calls()[n:], vm); slices.Contains(got, platform.PowerPause) {
				t.Errorf("Freeze on %s server made power calls %v, want no pause", what, got)
			}
		}
		p.SetState(vm, platform.StateStarting)
		refuse(t, "a starting")
		wantVMState(t, p.Fake, vm, platform.StateStarting)
		p.SetState(vm, platform.StateRunning)
		runAction(t, env, id, "poweroff", nil)
		refuse(t, "an off")
		wantVMState(t, p.Fake, vm, platform.StateOff)

		// Thaw leaves an Off and a Saved VM untouched.
		for _, state := range []platform.VMState{platform.StateOff, platform.StateSaved} {
			p.SetState(vm, state)
			n := len(p.Calls())
			if err := mod.Thaw(ctx, project, id); err != nil {
				t.Errorf("Thaw on a VM in state %v: %v", state, err)
			}
			if got := powerOps(p.Calls()[n:], vm); len(got) != 0 {
				t.Errorf("Thaw on a VM in state %v made power calls %v, want none", state, got)
			}
			wantVMState(t, p.Fake, vm, state)
		}
	})

	t.Run("FAULT-06/power_action_ends", func(t *testing.T) {
		for _, c := range []struct {
			action string
			body   any
			off    bool // the server is off before the action
		}{
			{"poweron", nil, true},
			{"poweroff", nil, false},
			{"shutdown", nil, false},
			{"reboot", nil, false},
			{"reset", nil, false},
			{"rebuild", map[string]any{"image": "ubuntu-24.04"}, false},
		} {
			t.Run(c.action, func(t *testing.T) {
				env, _, p, h := newFaultEnv(t)
				id := createdServer(t, env, "ends-"+c.action, nil)
				vm := vmName(t, p.Fake)
				if c.off {
					runAction(t, env, id, "poweroff", nil)
				}
				h.reset()
				n := len(p.Calls())
				runAction(t, env, id, c.action, c.body)
				ends := h.endedOf(faults.KindServerFreeze, id)
				if len(ends) == 0 {
					t.Fatalf("%s called no EndFault(%s) for server %d", c.action, faults.KindServerFreeze, id)
				}
				e := ends[0]
				if e.project != env.Project.Name || e.targetType != faults.TargetServer {
					t.Errorf("EndFault called with project %q, target type %q, want %q, %q", e.project, e.targetType, env.Project.Name, faults.TargetServer)
				}
				calls := p.Calls()
				if got := powerOps(calls[n:e.seq], vm); len(got) != 0 {
					t.Errorf("%s made power calls %v before EndFault(%s), want none", c.action, got, faults.KindServerFreeze)
				}
				if got := powerOps(calls[e.seq:], vm); len(got) == 0 {
					t.Errorf("%s made no power call after EndFault(%s)", c.action, faults.KindServerFreeze)
				}
			})
		}

		t.Run("attach_iso", func(t *testing.T) {
			env, _, _, h := newFaultEnv(t)
			id := createdServer(t, env, "ends-iso", nil)
			h.reset()
			runAction(t, env, id, "attach_iso", map[string]any{"iso": usableISO(t)})
			if ends := h.endedOf(faults.KindServerFreeze, id); len(ends) != 0 {
				t.Errorf("attach_iso called EndFault(%s) %d times, want none", faults.KindServerFreeze, len(ends))
			}
		})
	})

	t.Run("FAULT-10/system_disk", func(t *testing.T) {
		env, mod, p, _ := newFaultEnv(t)
		ctx := lifeCtx(t)
		project := env.Project.Name

		id := createdServer(t, env, "slow", nil)
		vm := vmName(t, p.Fake)
		disk := systemDisk(t, p, vm)
		createdServer(t, env, "other", nil)
		otherVM := vmName(t, p.Fake)
		otherDisk := systemDisk(t, p, otherVM)
		extra := disk + ".extra.vhdx"
		if err := p.AttachDisk(ctx, vm, extra, 5); err != nil {
			t.Fatalf("attach a second disk to %s: %v", vm, err)
		}

		if err := mod.SetSystemDiskIOPS(ctx, project, id, 200); err != nil {
			t.Fatalf("SetSystemDiskIOPS 200: %v", err)
		}
		if got := p.IOPS(vm, disk); got != 200 {
			t.Errorf("IOPS of the system disk %s: %d, want 200", disk, got)
		}
		if got := p.IOPS(vm, extra); got != 0 {
			t.Errorf("IOPS of the second disk %s: %d, want 0", extra, got)
		}
		if got := p.IOPS(otherVM, otherDisk); got != 0 {
			t.Errorf("IOPS of the other server's system disk %s: %d, want 0", otherDisk, got)
		}

		if err := mod.SetSystemDiskIOPS(ctx, project, id, 0); err != nil {
			t.Fatalf("SetSystemDiskIOPS 0: %v", err)
		}
		if got := p.IOPS(vm, disk); got != 0 {
			t.Errorf("IOPS of the system disk %s after 0: %d, want 0", disk, got)
		}
	})

	t.Run("FAULT-02/start_cleanup", func(t *testing.T) {
		env, mod, p, _ := newFaultEnv(t)
		ctx := lifeCtx(t)
		project := env.Project.Name

		paused := createdServer(t, env, "paused", nil)
		pausedVM := vmName(t, p.Fake)
		off := createdServer(t, env, "off", nil)
		offVM := vmName(t, p.Fake)
		saved := createdServer(t, env, "saved", nil)
		savedVM := vmName(t, p.Fake)

		if err := mod.Freeze(ctx, project, paused); err != nil {
			t.Fatalf("Freeze: %v", err)
		}
		runAction(t, env, off, "poweroff", nil)
		p.SetState(savedVM, platform.StateSaved)

		limited := map[string][]string{} // VM -> disk paths with a limit
		for vm, id := range map[string]int64{pausedVM: paused, offVM: off, savedVM: saved} {
			if err := mod.SetSystemDiskIOPS(ctx, project, id, 200); err != nil {
				t.Fatalf("SetSystemDiskIOPS on server %d: %v", id, err)
			}
			limited[vm] = append(limited[vm], systemDisk(t, p, vm))
		}
		volume := systemDisk(t, p, offVM) + ".volume.vhdx"
		if err := p.AttachDisk(ctx, offVM, volume, 1); err != nil {
			t.Fatalf("attach a volume disk to %s: %v", offVM, err)
		}
		if err := p.SetDiskIOPS(ctx, offVM, volume, 300); err != nil {
			t.Fatalf("limit the volume disk on %s: %v", offVM, err)
		}
		limited[offVM] = append(limited[offVM], volume)

		var m any = mod
		rec, ok := m.(resource.Recoverer)
		if !ok {
			t.Fatal("servers module does not implement resource.Recoverer")
		}
		n := len(p.Calls())
		if err := rec.Recover(ctx); err != nil {
			t.Fatalf("Recover: %v", err)
		}

		wantVMState(t, p.Fake, pausedVM, platform.StateRunning)
		wantVMState(t, p.Fake, offVM, platform.StateOff)
		wantVMState(t, p.Fake, savedVM, platform.StateSaved)
		for _, vm := range []string{offVM, savedVM} {
			if got := powerOps(p.Calls()[n:], vm); len(got) != 0 {
				t.Errorf("Recover made power calls %v on VM %s, want none", got, vm)
			}
		}
		for vm, paths := range limited {
			for _, path := range paths {
				if got := p.IOPS(vm, path); got != 0 {
					t.Errorf("IOPS of %s on %s after Recover: %d, want 0", path, vm, got)
				}
			}
		}
	})
}

// faultHooks are the servers module's FaultActive and EndFault hooks in
// place of the fault manager: crashed is the server with an active
// server_crash (0 none); EndFault records each call with its place in the
// fake's call log and, for server_freeze, undoes it with Thaw as the
// manager's Undo does.
type faultHooks struct {
	p       *recPlatform
	crashed atomic.Int64
	inj     faults.ServerInjector
	// dataFolder is the module's data folder (seed files).
	dataFolder string

	mu    sync.Mutex
	ended []endedFault
}

type endedFault struct {
	project, kind, targetType string
	id                        int64
	seq                       int // index in the fake's call log at the call
}

func (h *faultHooks) tune(d *servers.Deps) {
	d.FaultActive = func(project, kind, targetType string, id int64) bool {
		return project == "p" && kind == faults.KindServerCrash && targetType == faults.TargetServer &&
			id != 0 && id == h.crashed.Load()
	}
	d.EndFault = func(ctx context.Context, project, kind, targetType string, id int64) error {
		h.mu.Lock()
		h.ended = append(h.ended, endedFault{project: project, kind: kind, targetType: targetType, id: id, seq: len(h.p.Calls())})
		inj := h.inj
		h.mu.Unlock()
		if kind == faults.KindServerFreeze && inj != nil {
			return inj.Thaw(ctx, project, id)
		}
		return nil
	}
}

func (h *faultHooks) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ended = nil
}

// endedOf lists the recorded EndFault calls of kind for server id.
func (h *faultHooks) endedOf(kind string, id int64) []endedFault {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []endedFault
	for _, e := range h.ended {
		if e.kind == kind && e.id == id {
			out = append(out, e)
		}
	}
	return out
}

// newFaultEnv mounts the servers module on a recording fake platform that
// writes disk files, with the fault hooks of faultHooks.
func newFaultEnv(t *testing.T) (*apitest.Env, *servers.Module, *recPlatform, *faultHooks) {
	t.Helper()
	p := newRecPlatform()
	p.diskFileBytes = 1
	h := &faultHooks{p: p}
	env, mod, d := newFakeEnv(t, p, h.tune)
	h.dataFolder = d.Settings.DataFolder
	h.mu.Lock()
	h.inj = mod
	h.mu.Unlock()
	return env, mod, p, h
}

// systemDisk is the path of the disk last attached at LUN 0 of vm.
func systemDisk(t *testing.T, p *recPlatform, vm string) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.attaches) - 1; i >= 0; i-- {
		if a := p.attaches[i]; a.vm == vm && a.lun == 0 {
			return a.path
		}
	}
	t.Fatalf("no disk attached at LUN 0 of VM %s", vm)
	return ""
}

func (r *recPlatform) createCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.creates)
}

// blockedUserData is the user_data of the server FAULT-05/blocked crashes.
const blockedUserData = "#cloud-config\n# hlab-before-crash\n"

// imageName is the name of the image of a raw server object, "" when it
// has none.
func imageName(server map[string]any) string {
	img, _ := server["image"].(map[string]any)
	name, _ := img["name"].(string)
	return name
}

// usableISO is the name of a non-deprecated ISO of the shipped catalog.
func usableISO(t *testing.T) string {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	for _, iso := range cat.ISOs {
		if !iso.Deprecated {
			return iso.Name
		}
	}
	t.Fatal("shipped catalog has no usable ISO")
	return ""
}
