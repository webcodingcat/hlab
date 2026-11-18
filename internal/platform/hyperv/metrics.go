package hyperv

import (
	"context"
	"encoding/json"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// cpuTicks are the CPU counters of one Metrics call: the run time of all of
// the VM's virtual processors and the counters' timestamp, both in 100 ns.
type cpuTicks struct{ run, ts uint64 }

// metricsScript reads the raw performance counters of the running VM $name.
// Instance names (not localized): virtual processors "<vm>:Hv VP <n>", disks
// the VHDX path with "\" turned into "-", adapters "<vm>_<adapter name>_...";
// "_Total" never matches. A VM that is not running has no instances and
// answers zeros.
const metricsScript = getVM + `$vp = $name + ':'
$nic = $name + '_'
$r = @{ run = [uint64]0; ts = [uint64]0; dr = [uint64]0; dw = [uint64]0; dro = [uint64]0; dwo = [uint64]0; nrx = [uint64]0; ntx = [uint64]0; nrp = [uint64]0; ntp = [uint64]0 }
foreach ($i in Get-CimInstance -ClassName Win32_PerfRawData_HvStats_HyperVHypervisorVirtualProcessor) {
  if ($i.Name.StartsWith($vp, [System.StringComparison]::Ordinal)) {
    $r.run += [uint64]$i.PercentTotalRunTime
    $r.ts = [uint64]$i.Timestamp_Sys100NS
  }
}
$disks = @{}
foreach ($d in Get-VMHardDiskDrive -VM $vm) { if ($d.Path) { $disks[$d.Path.Replace('\', '-')] = $true } }
foreach ($i in Get-CimInstance -ClassName Win32_PerfRawData_Counters_HyperVVirtualStorageDevice) {
  if ($disks.ContainsKey($i.Name)) {
    $r.dr += [uint64]$i.ReadBytesPersec
    $r.dw += [uint64]$i.WriteBytesPersec
    $r.dro += [uint64]$i.ReadCount
    $r.dwo += [uint64]$i.WriteCount
  }
}
foreach ($i in Get-CimInstance -ClassName Win32_PerfRawData_NvspNicStats_HyperVVirtualNetworkAdapter) {
  if ($i.Name.StartsWith($nic, [System.StringComparison]::Ordinal)) {
    $r.nrx += [uint64]$i.BytesReceivedPersec
    $r.ntx += [uint64]$i.BytesSentPersec
    $r.nrp += [uint64]$i.PacketsReceivedPersec
    $r.ntp += [uint64]$i.PacketsSentPersec
  }
}
$r
`

// Metrics reads the VM's counters: disk counters summed over all of its
// virtual hard disks, network counters over all of its adapters. CPUPercent
// is the busy time of all vCPUs since the previous call for the VM.
func (p *Platform) Metrics(ctx context.Context, vm string) (platform.Sample, error) {
	data, err := p.r.run(ctx, "Metrics", map[string]any{"name": vm}, metricsScript)
	if err != nil {
		return platform.Sample{}, err
	}
	var v struct {
		Run uint64 `json:"run"`
		TS  uint64 `json:"ts"`
		DR  uint64 `json:"dr"`
		DW  uint64 `json:"dw"`
		DRO uint64 `json:"dro"`
		DWO uint64 `json:"dwo"`
		NRx uint64 `json:"nrx"`
		NTx uint64 `json:"ntx"`
		NRP uint64 `json:"nrp"`
		NTP uint64 `json:"ntp"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return platform.Sample{}, &platform.Error{Kind: platform.Failed, Op: "Metrics", Err: err}
	}
	return platform.Sample{
		Time:           time.Now(),
		CPUPercent:     p.cpuPercent(vm, cpuTicks{run: v.Run, ts: v.TS}),
		DiskReadBytes:  v.DR,
		DiskWriteBytes: v.DW,
		NetRxBytes:     v.NRx,
		NetTxBytes:     v.NTx,
		DiskReadOps:    v.DRO,
		DiskWriteOps:   v.DWO,
		NetRxPackets:   v.NRP,
		NetTxPackets:   v.NTP,
	}, nil
}

// cpuPercent keeps cur for vm and returns the busy percentage since the
// previous call; 0 on the first call, after a counter reset (the VM
// restarted) and while the VM has no counters (not running).
func (p *Platform) cpuPercent(vm string, cur cpuTicks) float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	prev, ok := p.cpu[vm]
	if cur.ts == 0 {
		delete(p.cpu, vm)
		return 0
	}
	p.cpu[vm] = cur
	if !ok || cur.ts <= prev.ts || cur.run < prev.run {
		return 0
	}
	return float64(cur.run-prev.run) / float64(cur.ts-prev.ts) * 100
}

// forgetCPU drops the kept CPU counters of a deleted VM.
func (p *Platform) forgetCPU(vm string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cpu, vm)
}
