// Package hyperv implements platform.Platform with the Hyper-V PowerShell
// module, run in one long-running Windows PowerShell 5.1 process. The code
// never parses PowerShell text: it uses IDs, enums and JSON.
package hyperv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// Options configures the Hyper-V platform.
type Options struct {
	// DataDir is the folder whose free disk space HostResources reports;
	// empty reports 0.
	DataDir string
	// SwitchName is the virtual switch AttachNIC connects adapters to.
	SwitchName string
}

// Platform is the Hyper-V implementation of platform.Platform.
type Platform struct {
	opts Options
	r    *runner

	mu       sync.Mutex
	cpu      map[string]cpuTicks     // last CPU counters per VM, for MetricsAll
	consoles map[string]net.Listener // served COM1 pipes per VM
}

var _ platform.Platform = (*Platform)(nil)

// New starts the PowerShell process and loads the Hyper-V module.
func New(opts Options) (platform.Platform, error) {
	p := &Platform{opts: opts, r: &runner{}, cpu: map[string]cpuTicks{}, consoles: map[string]net.Listener{}}
	if _, err := p.r.run(context.Background(), "New", nil, "Import-Module Hyper-V"); err != nil {
		_ = p.r.close()
		return nil, err
	}
	return p, nil
}

// Close stops serving the console pipes and ends the PowerShell process.
func (p *Platform) Close() error {
	p.mu.Lock()
	for name, l := range p.consoles {
		_ = l.Close()
		delete(p.consoles, name)
	}
	p.mu.Unlock()
	return p.r.close()
}

// getVM is the shared prelude of scripts working on the VM $name: it sets
// $vm or throws ItemNotFoundException.
const getVM = `$vm = Get-VM -Name $name -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq $name } | Select-Object -First 1
if (-not $vm) { throw [System.Management.Automation.ItemNotFoundException]::new("vm $name not found") }
`

// EnsureSwitch creates the internal switch if it is missing and isolates
// the host adapter in the private VLAN.
func (p *Platform) EnsureSwitch(ctx context.Context, spec platform.SwitchSpec) error {
	_, err := p.r.run(ctx, "EnsureSwitch", map[string]any{
		"name": spec.Name, "primary": spec.PVLANPrimary, "secondary": spec.PVLANSecondary,
	}, `$sw = Get-VMSwitch -Name $name -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq $name } | Select-Object -First 1
if (-not $sw) { $sw = New-VMSwitch -Name $name -SwitchType Internal }
elseif ([int]$sw.SwitchType -ne 1) { throw [System.InvalidOperationException]::new("switch $name exists and is not internal") }
Set-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName $name -Isolated -PrimaryVlanId $primary -SecondaryVlanId $secondary
`)
	return err
}

func stopAction(a platform.StopAction) (string, error) {
	switch a {
	case platform.StopSave:
		return "Save", nil
	case platform.StopShutdown:
		return "ShutDown", nil
	}
	return "", fmt.Errorf("unknown stop action %v", a)
}

// CreateVM creates a stopped Gen2 VM without adapters or disks. COM1 is
// connected to the named pipe <name>-com1: a Gen2 VM shows the guest no
// serial port without one, and Debian 12's first boot, whose kernel
// command line has console=ttyS0, then panics in its initramfs. Power
// serves the pipe before the VM starts (ensureConsole).
func (p *Platform) CreateVM(ctx context.Context, spec platform.VMSpec) error {
	stop, err := stopAction(spec.StopAction)
	if err != nil {
		return &platform.Error{Kind: platform.Failed, Op: "CreateVM", Err: err}
	}
	if spec.Name == "" || spec.CPUs < 1 || spec.MemoryMB < 1 || spec.Folder == "" {
		return &platform.Error{Kind: platform.Failed, Op: "CreateVM", Err: errors.New("name, cpus, memory and folder are required")}
	}
	_, err = p.r.run(ctx, "CreateVM", map[string]any{
		"name": spec.Name, "cpus": spec.CPUs, "memory": spec.MemoryMB * 1024 * 1024,
		"folder": spec.Folder, "stop": stop,
	}, `if (Get-VM -Name $name -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq $name }) {
  throw [System.InvalidOperationException]::new("vm $name exists")
}
$vm = New-VM -Name $name -Generation 2 -MemoryStartupBytes $memory -Path $folder -NoVHD
try {
  Get-VMNetworkAdapter -VM $vm | Remove-VMNetworkAdapter
  Set-VM -VM $vm -ProcessorCount $cpus -StaticMemory -MemoryStartupBytes $memory -AutomaticCheckpointsEnabled $false -CheckpointType Standard -AutomaticStartAction Nothing -AutomaticStopAction $stop
  Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate MicrosoftUEFICertificateAuthority
  Set-VMComPort -VM $vm -Number 1 -Path ('\\.\pipe\' + $name + '-com1')
} catch {
  Remove-VM -VM $vm -Force -ErrorAction SilentlyContinue
  throw
}
`)
	return err
}

// SetVM changes CPUs and memory (when set) and the stop action of a
// stopped VM.
func (p *Platform) SetVM(ctx context.Context, name string, spec platform.VMSpec) error {
	stop, err := stopAction(spec.StopAction)
	if err != nil {
		return &platform.Error{Kind: platform.Failed, Op: "SetVM", Err: err}
	}
	_, err = p.r.run(ctx, "SetVM", map[string]any{
		"name": name, "cpus": spec.CPUs, "memory": spec.MemoryMB * 1024 * 1024, "stop": stop,
	}, getVM+`if ([int]$vm.State -ne 3) { throw [System.InvalidOperationException]::new("vm $name is not off") }
$a = @{ VM = $vm; AutomaticStopAction = $stop }
if ($cpus -gt 0) { $a.ProcessorCount = $cpus }
if ($memory -gt 0) { $a.StaticMemory = $true; $a.MemoryStartupBytes = $memory }
Set-VM @a
`)
	return err
}

// DeleteVM turns the VM off if needed and removes it; its disks stay.
func (p *Platform) DeleteVM(ctx context.Context, name string) error {
	_, err := p.r.run(ctx, "DeleteVM", map[string]any{"name": name}, getVM+`if ([int]$vm.State -ne 3) { Stop-VM -VM $vm -TurnOff -Force }
Remove-VM -VM $vm -Force
`)
	if err == nil {
		p.forgetCPU(name)
		p.closeConsole(name)
	}
	return err
}

// shutdownIC is the ID of the Shutdown integration service.
const shutdownIC = "9F8233AC-BE49-4C79-8EE3-E7E1985B2077"

// guestRequest asks the guest through the Shutdown integration service to
// shut down or reboot ($method) and answers the method's return value
// without waiting.
const guestRequest = `if ([int]$vm.State -ne 2) { throw [System.InvalidOperationException]::new("vm $name is not running") }
$ic = Get-VMIntegrationService -VM $vm | Where-Object { $_.Id -like ('*' + $icid) } | Select-Object -First 1
if (-not $ic -or -not $ic.Enabled) { throw [System.InvalidOperationException]::new("shutdown integration service of vm $name is not enabled") }
$c = Get-CimInstance -Namespace root\virtualization\v2 -ClassName Msvm_ShutdownComponent -Filter ("SystemName='" + $vm.Id + "'")
if (-not $c) { throw [System.InvalidOperationException]::new("shutdown integration service of vm $name is not available") }
$res = Invoke-CimMethod -InputObject $c -MethodName $method -Arguments @{ Force = $true; Reason = 'hlab' }
[int64]$res.ReturnValue
`

// rebootOffTimeout bounds the wait for the guest to turn off when a reboot
// falls back to shutdown and start (systemd's default stop timeout is 90 s).
const rebootOffTimeout = 120 * time.Second

// askGuest runs method (InitiateShutdown or InitiateReboot) of the Shutdown
// integration service and reports whether the guest accepted it (return
// value 0 or 4096) along with the return value.
func (p *Platform) askGuest(ctx context.Context, name, method string) (bool, int64, error) {
	data, err := p.r.run(ctx, "Power", map[string]any{"name": name, "icid": shutdownIC, "method": method}, getVM+guestRequest)
	if err != nil {
		return false, 0, err
	}
	var rv int64
	if err := json.Unmarshal(data, &rv); err != nil {
		return false, 0, &platform.Error{Kind: platform.Failed, Op: "Power", Err: err}
	}
	return rv == 0 || rv == 4096, rv, nil
}

// reboot asks the guest to reboot. A guest refusing InitiateReboot is shut
// down instead, awaited until off and started again.
func (p *Platform) reboot(ctx context.Context, name string) error {
	ok, _, err := p.askGuest(ctx, name, "InitiateReboot")
	if err != nil || ok {
		return err
	}
	ok, rv, err := p.askGuest(ctx, name, "InitiateShutdown")
	if err != nil {
		return err
	}
	if !ok {
		return &platform.Error{Kind: platform.InvalidState, Op: "Power", Err: fmt.Errorf("InitiateShutdown of vm %s returned %d", name, rv)}
	}
	wctx, cancel := context.WithTimeout(ctx, rebootOffTimeout)
	defer cancel()
	for {
		st, err := p.State(wctx, name)
		if err == nil && st == platform.StateOff {
			break
		}
		switch {
		case ctx.Err() != nil:
			return &platform.Error{Kind: platform.Failed, Op: "Power", Err: ctx.Err()}
		case wctx.Err() != nil:
			return &platform.Error{Kind: platform.Failed, Op: "Power", Err: fmt.Errorf("vm %s did not turn off within %s", name, rebootOffTimeout)}
		case err != nil:
			return err
		}
		select {
		case <-wctx.Done():
		case <-time.After(time.Second):
		}
	}
	if err := p.ensureConsole(name); err != nil {
		return err
	}
	_, err = p.r.run(ctx, "Power", map[string]any{"name": name}, getVM+"Start-VM -VM $vm\n")
	return err
}

// Power runs a power operation; Shutdown returns once the guest was asked,
// Reboot once the guest was asked or, in its fallback, started again.
func (p *Platform) Power(ctx context.Context, name string, op platform.PowerOp) error {
	var body string
	vars := map[string]any{"name": name}
	switch op {
	case platform.PowerStart:
		body = `Start-VM -VM $vm`
	case platform.PowerStop:
		body = `Stop-VM -VM $vm -TurnOff -Force`
	case platform.PowerShutdown:
		ok, rv, err := p.askGuest(ctx, name, "InitiateShutdown")
		if err == nil && !ok {
			err = &platform.Error{Kind: platform.InvalidState, Op: "Power", Err: fmt.Errorf("InitiateShutdown of vm %s returned %d", name, rv)}
		}
		return err
	case platform.PowerReboot:
		return p.reboot(ctx, name)
	case platform.PowerReset:
		body = `Restart-VM -VM $vm -Force`
	case platform.PowerSave:
		body = `Save-VM -VM $vm`
	default:
		return &platform.Error{Kind: platform.Failed, Op: "Power", Err: fmt.Errorf("unknown power operation %v", op)}
	}
	if op == platform.PowerStart || op == platform.PowerReset {
		if err := p.ensureConsole(name); err != nil {
			return err
		}
	}
	_, err := p.r.run(ctx, "Power", vars, getVM+body+"\n")
	return err
}

// State returns the VM's state, mapped from the numeric Hyper-V state.
func (p *Platform) State(ctx context.Context, name string) (platform.VMState, error) {
	data, err := p.r.run(ctx, "State", map[string]any{"name": name}, getVM+`[int]$vm.State`)
	if err != nil {
		return platform.StateOther, err
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return platform.StateOther, &platform.Error{Kind: platform.Failed, Op: "State", Err: err}
	}
	return mapState(n), nil
}

// mapState maps Microsoft.HyperV.PowerShell.VMState values.
func mapState(n int) platform.VMState {
	switch n {
	case 3, 32785: // Off, OffCritical
		return platform.StateOff
	case 10, 32777, 32789, 32793: // Starting, Resuming, StartingCritical, ResumingCritical
		return platform.StateStarting
	case 2, 32784: // Running, RunningCritical
		return platform.StateRunning
	case 4, 32773, 32780, 32781, 32786, 32791, 32795: // Stopping, Saving, FastSaving, ForceShutdown, StoppingCritical, SavingCritical, FastSavingCritical
		return platform.StateStopping
	case 6, 32779, 32787, 32794: // Saved, FastSaved, SavedCritical, FastSavedCritical
		return platform.StateSaved
	case 9, 32788: // Paused, PausedCritical
		return platform.StatePaused
	}
	return platform.StateOther
}

// HostResources returns the host's memory, logical CPUs and the free disk
// space of Options.DataDir.
func (p *Platform) HostResources(ctx context.Context) (platform.Resources, error) {
	data, err := p.r.run(ctx, "HostResources", map[string]any{"dir": p.opts.DataDir}, `$os = Get-CimInstance -ClassName Win32_OperatingSystem
$cs = Get-CimInstance -ClassName Win32_ComputerSystem
$free = 0
if ($dir) { $free = [System.IO.DriveInfo]::new([System.IO.Path]::GetPathRoot([System.IO.Path]::GetFullPath($dir))).AvailableFreeSpace }
@{ memoryKB = [int64]$os.TotalVisibleMemorySize; freeMemoryKB = [int64]$os.FreePhysicalMemory; cpus = [int]$cs.NumberOfLogicalProcessors; freeDisk = [int64]$free }
`)
	if err != nil {
		return platform.Resources{}, err
	}
	var v struct {
		MemoryKB     int64 `json:"memoryKB"`
		FreeMemoryKB int64 `json:"freeMemoryKB"`
		CPUs         int   `json:"cpus"`
		FreeDisk     int64 `json:"freeDisk"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return platform.Resources{}, &platform.Error{Kind: platform.Failed, Op: "HostResources", Err: err}
	}
	return platform.Resources{
		MemoryMB:      v.MemoryKB / 1024,
		FreeMemoryMB:  v.FreeMemoryKB / 1024,
		LogicalCPUs:   v.CPUs,
		FreeDiskBytes: v.FreeDisk,
	}, nil
}
