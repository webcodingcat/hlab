// Package platformtest provides an in-memory platform.Platform for unit
// tests.
package platformtest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// Call is one recorded platform call.
type Call struct {
	Op  string // method name: "CreateVM", "Power", "SetVM", "ResizeDisk", ...
	VM  string
	Arg any // PowerOp, VMSpec, size in bytes (int64), path, Deny ...
}

// Deny is the Arg of a recorded SetNICDeny call.
type Deny struct {
	MAC  string
	Deny []net.HardwareAddr
}

type vm struct {
	spec         platform.VMSpec
	state        platform.VMState
	holdShutdown bool
	sample       *platform.Sample
	nics         []platform.NICSpec
}

// Fake implements platform.Platform in memory; it is safe for concurrent
// use.
type Fake struct {
	mu       sync.Mutex
	vms      map[string]*vm
	disks    map[string]int64 // path -> nominal size
	res      platform.Resources
	calls    []Call
	failNext map[string][]error
	blocks   map[string][]chan struct{}
}

var _ platform.Platform = (*Fake)(nil)

// NewFake returns an empty fake with 64 GiB RAM free, 16 logical CPUs and
// 1 TiB free disk.
func NewFake() *Fake {
	return &Fake{
		vms:   map[string]*vm{},
		disks: map[string]int64{},
		res: platform.Resources{
			MemoryMB: 64 * 1024, FreeMemoryMB: 64 * 1024,
			LogicalCPUs: 16, FreeDiskBytes: 1 << 40,
		},
		failNext: map[string][]error{},
		blocks:   map[string][]chan struct{}{},
	}
}

// SetResources sets what HostResources returns.
func (f *Fake) SetResources(r platform.Resources) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.res = r
}

// SetState sets the state of vm as if the guest changed it by itself.
func (f *Fake) SetState(name string, s platform.VMState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vm(name).state = s
}

// SetSample sets what MetricsAll returns for vm.
func (f *Fake) SetSample(name string, s platform.Sample) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vm(name).sample = &s
}

// HoldShutdown makes the guest of vm ignore shutdown requests.
func (f *Fake) HoldShutdown(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vm(name).holdShutdown = true
}

// FailNext makes the next call of op return err.
func (f *Fake) FailNext(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext[op] = append(f.failNext[op], err)
}

// Block makes the next call of op wait until release is called or its
// context ends; calling release again does nothing.
func (f *Fake) Block(op string) (release func()) {
	ch := make(chan struct{})
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocks[op] = append(f.blocks[op], ch)
	return sync.OnceFunc(func() { close(ch) })
}

// Calls returns a copy of the recorded calls in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// vm returns the entry of name, creating it; f.mu is held.
func (f *Fake) vm(name string) *vm {
	v, ok := f.vms[name]
	if !ok {
		v = &vm{spec: platform.VMSpec{Name: name}}
		f.vms[name] = v
	}
	return v
}

// begin records the call, waits on a pending block and returns a pending
// injected failure. On nil it returns with f.mu held.
func (f *Fake) begin(ctx context.Context, op, name string, arg any) error {
	f.mu.Lock()
	f.calls = append(f.calls, Call{Op: op, VM: name, Arg: arg})
	var gate chan struct{}
	if b := f.blocks[op]; len(b) > 0 {
		gate, f.blocks[op] = b[0], b[1:]
	}
	if gate != nil {
		f.mu.Unlock()
		select {
		case <-gate:
		case <-ctx.Done():
			return &platform.Error{Kind: platform.Failed, Op: op, Err: ctx.Err()}
		}
		f.mu.Lock()
	}
	if e := f.failNext[op]; len(e) > 0 {
		err := e[0]
		f.failNext[op] = e[1:]
		f.mu.Unlock()
		return err
	}
	return nil
}

func errOf(kind platform.ErrorKind, op, format string, a ...any) error {
	return &platform.Error{Kind: kind, Op: op, Err: fmt.Errorf(format, a...)}
}

// existing returns the VM name or a NotFound error; f.mu is held.
func (f *Fake) existing(op, name string) (*vm, error) {
	v, ok := f.vms[name]
	if !ok {
		return nil, errOf(platform.NotFound, op, "vm %s not found", name)
	}
	return v, nil
}

// EnsureSwitch records the call.
func (f *Fake) EnsureSwitch(ctx context.Context, spec platform.SwitchSpec) error {
	if err := f.begin(ctx, "EnsureSwitch", "", spec); err != nil {
		return err
	}
	f.mu.Unlock()
	return nil
}

// DeleteSwitch records the call.
func (f *Fake) DeleteSwitch(ctx context.Context, name string) error {
	if err := f.begin(ctx, "DeleteSwitch", "", name); err != nil {
		return err
	}
	f.mu.Unlock()
	return nil
}

// CreateVM creates a VM that is off.
func (f *Fake) CreateVM(ctx context.Context, spec platform.VMSpec) error {
	if err := f.begin(ctx, "CreateVM", spec.Name, spec); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if _, ok := f.vms[spec.Name]; ok {
		return errOf(platform.InvalidState, "CreateVM", "vm %s exists", spec.Name)
	}
	f.vms[spec.Name] = &vm{spec: spec, state: platform.StateOff}
	return nil
}

// SetVM changes CPUs, memory and stop action of a VM that is off.
func (f *Fake) SetVM(ctx context.Context, name string, spec platform.VMSpec) error {
	if err := f.begin(ctx, "SetVM", name, spec); err != nil {
		return err
	}
	defer f.mu.Unlock()
	v, err := f.existing("SetVM", name)
	if err != nil {
		return err
	}
	if v.state != platform.StateOff {
		return errOf(platform.InvalidState, "SetVM", "vm %s is not off", name)
	}
	if spec.CPUs > 0 {
		v.spec.CPUs = spec.CPUs
	}
	if spec.MemoryMB > 0 {
		v.spec.MemoryMB = spec.MemoryMB
	}
	v.spec.StopAction = spec.StopAction
	return nil
}

// SetStopAction records the stop action of the VM, in any state.
func (f *Fake) SetStopAction(ctx context.Context, name string, a platform.StopAction) error {
	if err := f.begin(ctx, "SetStopAction", name, a); err != nil {
		return err
	}
	defer f.mu.Unlock()
	v, err := f.existing("SetStopAction", name)
	if err != nil {
		return err
	}
	v.spec.StopAction = a
	return nil
}

// StopAction returns the stop action recorded for the VM.
func (f *Fake) StopAction(name string) platform.StopAction {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vm(name).spec.StopAction
}

// VMs returns the sorted names of the VMs starting with prefix.
func (f *Fake) VMs(ctx context.Context, prefix string) ([]string, error) {
	if err := f.begin(ctx, "VMs", "", prefix); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var out []string
	for name := range f.vms {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// DeleteVM removes the VM; its disks stay.
func (f *Fake) DeleteVM(ctx context.Context, name string) error {
	if err := f.begin(ctx, "DeleteVM", name, nil); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if _, err := f.existing("DeleteVM", name); err != nil {
		return err
	}
	delete(f.vms, name)
	return nil
}

// Power applies op: Start, Reboot, Reset run the VM; Stop turns it off;
// Shutdown turns it off unless HoldShutdown was called; Save saves it.
func (f *Fake) Power(ctx context.Context, name string, op platform.PowerOp) error {
	if err := f.begin(ctx, "Power", name, op); err != nil {
		return err
	}
	defer f.mu.Unlock()
	v, err := f.existing("Power", name)
	if err != nil {
		return err
	}
	switch op {
	case platform.PowerStart, platform.PowerReboot, platform.PowerReset:
		v.state = platform.StateRunning
	case platform.PowerStop:
		v.state = platform.StateOff
	case platform.PowerShutdown, platform.PowerShutdownRetry:
		if !v.holdShutdown {
			v.state = platform.StateOff
		}
	case platform.PowerSave:
		v.state = platform.StateSaved
	default:
		return errOf(platform.Failed, "Power", "unknown power operation %v", op)
	}
	return nil
}

// State returns the VM's state.
func (f *Fake) State(ctx context.Context, name string) (platform.VMState, error) {
	if err := f.begin(ctx, "State", name, nil); err != nil {
		return platform.StateOther, err
	}
	defer f.mu.Unlock()
	v, err := f.existing("State", name)
	if err != nil {
		return platform.StateOther, err
	}
	return v.state, nil
}

// AttachNIC adds the adapter to the VM, running or not.
func (f *Fake) AttachNIC(ctx context.Context, name string, nic platform.NICSpec) error {
	if err := f.begin(ctx, "AttachNIC", name, nic); err != nil {
		return err
	}
	defer f.mu.Unlock()
	v, err := f.existing("AttachNIC", name)
	if err != nil {
		return err
	}
	nic.MAC = append(net.HardwareAddr(nil), nic.MAC...)
	nic.DenyMACs = cloneMACs(nic.DenyMACs)
	v.nics = append(v.nics, nic)
	return nil
}

// DetachNIC removes the VM's adapter with MAC mac; an unknown MAC is
// NotFound.
func (f *Fake) DetachNIC(ctx context.Context, name, mac string) error {
	if err := f.begin(ctx, "DetachNIC", name, mac); err != nil {
		return err
	}
	defer f.mu.Unlock()
	v, i, err := f.nic("DetachNIC", name, mac)
	if err != nil {
		return err
	}
	v.nics = append(v.nics[:i], v.nics[i+1:]...)
	return nil
}

// SetNICDeny records Deny{mac, deny} and stores deny as the deny list of
// the VM's adapter with MAC mac; an unknown MAC is NotFound.
func (f *Fake) SetNICDeny(ctx context.Context, name, mac string, deny []net.HardwareAddr) error {
	if err := f.begin(ctx, "SetNICDeny", name, Deny{MAC: mac, Deny: cloneMACs(deny)}); err != nil {
		return err
	}
	defer f.mu.Unlock()
	v, i, err := f.nic("SetNICDeny", name, mac)
	if err != nil {
		return err
	}
	v.nics[i].DenyMACs = cloneMACs(deny)
	return nil
}

// NICs returns copies of the current adapters of vm with their deny lists.
func (f *Fake) NICs(name string) []platform.NICSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vms[name]
	if !ok {
		return nil
	}
	out := make([]platform.NICSpec, len(v.nics))
	for i, n := range v.nics {
		n.MAC = append(net.HardwareAddr(nil), n.MAC...)
		n.DenyMACs = cloneMACs(n.DenyMACs)
		out[i] = n
	}
	return out
}

// nic returns the VM and the index of its adapter with MAC mac; f.mu is
// held.
func (f *Fake) nic(op, name, mac string) (*vm, int, error) {
	v, err := f.existing(op, name)
	if err != nil {
		return nil, 0, err
	}
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, 0, errOf(platform.Failed, op, "%v", err)
	}
	for i, n := range v.nics {
		if bytes.Equal(n.MAC, hw) {
			return v, i, nil
		}
	}
	return nil, 0, errOf(platform.NotFound, op, "adapter %s of vm %s not found", mac, name)
}

func cloneMACs(macs []net.HardwareAddr) []net.HardwareAddr {
	if macs == nil {
		return nil
	}
	out := make([]net.HardwareAddr, len(macs))
	for i, m := range macs {
		out[i] = append(net.HardwareAddr(nil), m...)
	}
	return out
}

// CreateDisk records a disk of sizeBytes at path.
func (f *Fake) CreateDisk(ctx context.Context, path string, sizeBytes int64, from string, page83 [16]byte) error {
	if err := f.begin(ctx, "CreateDisk", "", sizeBytes); err != nil {
		return err
	}
	defer f.mu.Unlock()
	f.disks[path] = sizeBytes
	return nil
}

// CreateChildDisk records the call with the parent as Arg, creates an
// empty file at path and records a disk of the parent's size.
func (f *Fake) CreateChildDisk(ctx context.Context, path, parent string) error {
	if err := f.begin(ctx, "CreateChildDisk", "", parent); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errOf(platform.Failed, "CreateChildDisk", "%v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return errOf(platform.Failed, "CreateChildDisk", "%v", err)
	}
	f.disks[path] = f.disks[parent]
	return nil
}

// ResizeDisk grows the disk at path; shrinking is InvalidState.
func (f *Fake) ResizeDisk(ctx context.Context, path string, sizeBytes int64) error {
	if err := f.begin(ctx, "ResizeDisk", "", sizeBytes); err != nil {
		return err
	}
	defer f.mu.Unlock()
	size, ok := f.disks[path]
	if !ok {
		return errOf(platform.NotFound, "ResizeDisk", "disk %s not found", path)
	}
	if sizeBytes < size {
		return errOf(platform.InvalidState, "ResizeDisk", "disk %s cannot shrink", path)
	}
	f.disks[path] = sizeBytes
	return nil
}

// CompactDisk records the call with the path as Arg.
func (f *Fake) CompactDisk(ctx context.Context, path string) error {
	if err := f.begin(ctx, "CompactDisk", "", path); err != nil {
		return err
	}
	f.mu.Unlock()
	return nil
}

// AttachDisk records the call.
func (f *Fake) AttachDisk(ctx context.Context, name, path string, lun int) error {
	return f.onVM(ctx, "AttachDisk", name, path)
}

// DetachDisk records the call.
func (f *Fake) DetachDisk(ctx context.Context, name, path string) error {
	return f.onVM(ctx, "DetachDisk", name, path)
}

// SetDVD records the call.
func (f *Fake) SetDVD(ctx context.Context, name string, slot int, iso string) error {
	return f.onVM(ctx, "SetDVD", name, iso)
}

// SetBootOrder records the call.
func (f *Fake) SetBootOrder(ctx context.Context, name string, order []platform.BootDevice) error {
	return f.onVM(ctx, "SetBootOrder", name, order)
}

// Snapshot copies the disk at diskPath to target.
func (f *Fake) Snapshot(ctx context.Context, name, diskPath, target string) error {
	if err := f.begin(ctx, "Snapshot", name, target); err != nil {
		return err
	}
	defer f.mu.Unlock()
	size, ok := f.disks[diskPath]
	if !ok {
		return errOf(platform.NotFound, "Snapshot", "disk %s not found", diskPath)
	}
	f.disks[target] = size
	return nil
}

// MetricsAll returns, for every existing VM of vms, the sample set with
// SetSample, else zero counters now; it records the VM names as Arg.
func (f *Fake) MetricsAll(ctx context.Context, vms []string) (map[string]platform.Sample, error) {
	if err := f.begin(ctx, "MetricsAll", "", append([]string(nil), vms...)); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	out := map[string]platform.Sample{}
	for _, name := range vms {
		v, ok := f.vms[name]
		switch {
		case !ok:
		case v.sample != nil:
			out[name] = *v.sample
		default:
			out[name] = platform.Sample{Time: time.Now()}
		}
	}
	return out, nil
}

// HostResources returns the resources set with SetResources.
func (f *Fake) HostResources(ctx context.Context) (platform.Resources, error) {
	if err := f.begin(ctx, "HostResources", "", nil); err != nil {
		return platform.Resources{}, err
	}
	defer f.mu.Unlock()
	return f.res, nil
}

// onVM records a call that only needs the VM to exist.
func (f *Fake) onVM(ctx context.Context, op, name string, arg any) error {
	if err := f.begin(ctx, op, name, arg); err != nil {
		return err
	}
	defer f.mu.Unlock()
	_, err := f.existing(op, name)
	return err
}
