package volumes_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
)

// endCall is one call of the EndFaults hook with the number of the
// platform calls of each op seen at that moment.
type endCall struct {
	Project    string
	TargetType string
	ID         int64
	Seen       map[string]int
}

// faultEnv is a volEnv whose EndFaults hook records its calls.
type faultEnv struct {
	*volEnv
	mu   sync.Mutex
	ends []endCall
}

// newFaultEnv is newVolEnvWith an EndFaults hook that records each call
// with the platform calls seen at that moment.
func newFaultEnv(t *testing.T) *faultEnv {
	t.Helper()
	fe := &faultEnv{}
	fe.volEnv = newVolEnvWith(t, func(_ context.Context, project, targetType string, id int64) error {
		seen := map[string]int{}
		for _, c := range fe.fake.Calls() {
			seen[c.Op]++
		}
		fe.mu.Lock()
		defer fe.mu.Unlock()
		fe.ends = append(fe.ends, endCall{project, targetType, id, seen})
		return nil
	})
	return fe
}

func (e *faultEnv) endCalls() []endCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.ends)
}

// opCount is the number of platform calls of op so far.
func opCount(e *volEnv, op string) int {
	n := 0
	for _, c := range e.fake.Calls() {
		if c.Op == op {
			n++
		}
	}
	return n
}

// diskCalls returns the calls of op on path after the first skip calls.
func diskCalls(e *volEnv, skip int, op, path string) []platformtest.Call {
	var out []platformtest.Call
	for _, c := range e.fake.Calls()[skip:] {
		if c.Op == op && fmt.Sprint(c.Arg) == path {
			out = append(out, c)
		}
	}
	return out
}

// vmOf is the VM the disk at path was last attached to.
func vmOf(t *testing.T, e *volEnv, path string) string {
	t.Helper()
	calls := diskCalls(e, 0, "AttachDisk", path)
	if len(calls) == 0 {
		t.Fatalf("disk %s never attached", path)
	}
	return calls[len(calls)-1].VM
}

// attachedPair creates a server with two attached volumes and returns the
// server and the second volume (at a LUN other than the first one's).
func attachedPair(t *testing.T, e *faultEnv) (int64, int64) {
	t.Helper()
	s := createServer(t, e.volEnv, "s", "fsn1", nil)
	first := createVolume(t, e.volEnv, "first", 10)
	second := createVolume(t, e.volEnv, "second", 10)
	runVolumeAction(t, e.volEnv, first, "attach", attachTo(s))
	runVolumeAction(t, e.volEnv, second, "attach", attachTo(s))
	return s, second
}

// testFaults runs the fault injection rows of TestVolumes.
func testFaults(t *testing.T) {
	t.Run("FAULT-09/remove_restore", testRemoveRestore)
	t.Run("FAULT-09/ends_first", testEndsFirst)
	t.Run("FAULT-10/set_iops", testSetIOPS)
	t.Run("FAULT-02/start_cleanup", testStartCleanup)
}

func testRemoveRestore(t *testing.T) {
	e := newFaultEnv(t)
	ctx := context.Background()
	s, v := attachedPair(t, e)
	path := e.diskOf(t, v)
	vm := vmOf(t, e.volEnv, path)
	lun := e.lunOf(path)
	project := e.Project.Name

	check := func(state string) {
		t.Helper()
		base := len(e.fake.Calls())
		if err := e.vol.Remove(ctx, project, v); err != nil {
			t.Fatalf("Remove on a %s server: %v", state, err)
		}
		if got := diskCalls(e.volEnv, base, "DetachDisk", path); len(got) != 1 || got[0].VM != vm {
			t.Errorf("Remove on a %s server: DetachDisk calls of %s %+v, want one on VM %s", state, path, got, vm)
		}
		if got := getVolume(t, e.volEnv, v); got.Server == nil || *got.Server != s {
			t.Errorf("after Remove on a %s server: volume server %v, want %d", state, got.Server, s)
		}
		base = len(e.fake.Calls())
		if err := e.vol.Restore(ctx, project, v); err != nil {
			t.Fatalf("Restore on a %s server: %v", state, err)
		}
		if got := diskCalls(e.volEnv, base, "AttachDisk", path); len(got) != 1 || got[0].VM != vm {
			t.Errorf("Restore on a %s server: AttachDisk calls of %s %+v, want one on VM %s", state, path, got, vm)
		}
		if got := e.lunOf(path); got != lun {
			t.Errorf("Restore on a %s server: attached at LUN %d, want the record's LUN %d", state, got, lun)
		}
	}

	check("running")
	resp := e.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/poweroff", s), nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("poweroff server %d: status %d, want 201", s, resp.StatusCode)
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	e.waitSuccess(t, b.Action.ID, "poweroff")
	check("off")
}

func testEndsFirst(t *testing.T) {
	cases := []struct {
		name   string
		action string
		body   any
		op     string // the platform work the action does
	}{
		{"detach", "detach", nil, "DetachDisk"},
		{"resize", "resize", map[string]any{"size": 20}, "ResizeDisk"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newFaultEnv(t)
			_, v := attachedPair(t, e)
			before := opCount(e.volEnv, tc.op)

			runVolumeAction(t, e.volEnv, v, tc.action, tc.body)

			if n := opCount(e.volEnv, tc.op); n <= before {
				t.Fatalf("%s: %d %s calls after the action, want more than %d", tc.action, n, tc.op, before)
			}
			var mine []endCall
			for _, c := range e.endCalls() {
				if c.TargetType == faults.TargetVolume && c.ID == v {
					mine = append(mine, c)
				}
			}
			if len(mine) == 0 {
				t.Fatalf("%s: EndFaults calls %+v, want one for volume %d", tc.action, e.endCalls(), v)
			}
			if mine[0].Project != e.Project.Name {
				t.Errorf("%s: EndFaults project %q, want %q", tc.action, mine[0].Project, e.Project.Name)
			}
			if got := mine[0].Seen[tc.op]; got != before {
				t.Errorf("%s: %d %s calls when EndFaults ran, want %d (the faults end before the platform work)", tc.action, got, tc.op, before)
			}
		})
	}
}

func testSetIOPS(t *testing.T) {
	e := newFaultEnv(t)
	ctx := context.Background()
	_, v := attachedPair(t, e)
	path := e.diskOf(t, v)
	vm := vmOf(t, e.volEnv, path)

	if err := e.vol.SetIOPS(ctx, e.Project.Name, v, 300); err != nil {
		t.Fatalf("SetIOPS 300: %v", err)
	}
	if got := e.fake.IOPS(vm, path); got != 300 {
		t.Errorf("after SetIOPS 300: limit of %s on %s = %d, want 300", path, vm, got)
	}
	if err := e.vol.SetIOPS(ctx, e.Project.Name, v, 0); err != nil {
		t.Fatalf("SetIOPS 0: %v", err)
	}
	if got := e.fake.IOPS(vm, path); got != 0 {
		t.Errorf("after SetIOPS 0: limit of %s on %s = %d, want 0", path, vm, got)
	}
}

func testStartCleanup(t *testing.T) {
	e := newFaultEnv(t)
	s := createServer(t, e.volEnv, "s", "fsn1", nil)
	a := createVolume(t, e.volEnv, "a", 10)
	b := createVolume(t, e.volEnv, "b", 10)
	free := createVolume(t, e.volEnv, "free", 10)
	runVolumeAction(t, e.volEnv, a, "attach", attachTo(s))
	runVolumeAction(t, e.volEnv, b, "attach", attachTo(s))
	attached := map[string]int{} // path -> LUN
	for _, id := range []int64{a, b} {
		p := e.diskOf(t, id)
		attached[p] = e.lunOf(p)
	}
	vm := vmOf(t, e.volEnv, e.diskOf(t, a))
	freePath := e.diskOf(t, free)
	base := len(e.fake.Calls())

	if err := e.vol.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	for path, lun := range attached {
		if got := diskCalls(e.volEnv, base, "AttachDisk", path); len(got) != 1 || got[0].VM != vm {
			t.Errorf("Recover: AttachDisk calls of %s %+v, want one on VM %s", path, got, vm)
		}
		if got := e.lunOf(path); got != lun {
			t.Errorf("Recover: %s attached at LUN %d, want %d", path, got, lun)
		}
	}
	if got := diskCalls(e.volEnv, base, "AttachDisk", freePath); len(got) != 0 {
		t.Errorf("Recover: AttachDisk calls of the unattached volume's disk %s: %+v, want none", freePath, got)
	}
}
