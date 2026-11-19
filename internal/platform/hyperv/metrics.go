package hyperv

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// cpuTicks are the CPU counters of one VM in one read: the run time of all
// of the VM's virtual processors and the counters' timestamp, both in
// 100 ns.
type cpuTicks struct{ run, ts uint64 }

// counterRows are the raw rows of one counter read: every instance of the
// three counter classes and the disk paths of every VM on the host (a VM
// without disks has an empty list).
type counterRows struct {
	VPs       []vpRow
	Disks     []diskRow
	NICs      []nicRow
	DiskPaths map[string][]string
}

// vpRow is a virtual processor instance, named "<vm>:Hv VP <n>".
type vpRow struct {
	Name               string
	RunTime, Timestamp uint64
}

// diskRow is a virtual storage device instance, named by the disk path with
// "\" turned into "-".
type diskRow struct {
	Name                                     string
	ReadBytes, WriteBytes, ReadOps, WriteOps uint64
}

// nicRow is a virtual network adapter instance, named
// "<vm>_<adapter name>_<id>".
type nicRow struct {
	Name                                   string
	RxBytes, TxBytes, RxPackets, TxPackets uint64
}

// vmCounters are the summed counters of one VM; sample lacks Time and
// CPUPercent.
type vmCounters struct {
	ticks  cpuTicks
	sample platform.Sample
}

// metricsScript reads every instance of the three raw counter classes once
// and the disk paths of all VMs once. "_Total" and the root partition's
// instances are among the rows; mapCounters ignores them.
const metricsScript = `$r = @{ VPs = @(); Disks = @(); NICs = @(); DiskPaths = @{} }
$r.VPs = @(foreach ($i in Get-CimInstance -ClassName Win32_PerfRawData_HvStats_HyperVHypervisorVirtualProcessor) {
  @{ Name = $i.Name; RunTime = [uint64]$i.PercentTotalRunTime; Timestamp = [uint64]$i.Timestamp_Sys100NS }
})
$r.Disks = @(foreach ($i in Get-CimInstance -ClassName Win32_PerfRawData_Counters_HyperVVirtualStorageDevice) {
  @{ Name = $i.Name; ReadBytes = [uint64]$i.ReadBytesPersec; WriteBytes = [uint64]$i.WriteBytesPersec; ReadOps = [uint64]$i.ReadCount; WriteOps = [uint64]$i.WriteCount }
})
$r.NICs = @(foreach ($i in Get-CimInstance -ClassName Win32_PerfRawData_NvspNicStats_HyperVVirtualNetworkAdapter) {
  @{ Name = $i.Name; RxBytes = [uint64]$i.BytesReceivedPersec; TxBytes = [uint64]$i.BytesSentPersec; RxPackets = [uint64]$i.PacketsReceivedPersec; TxPackets = [uint64]$i.PacketsSentPersec }
})
$vms = @(Get-VM)
foreach ($v in $vms) { $r.DiskPaths[$v.Name] = @() }
foreach ($d in $vms | Get-VMHardDiskDrive) { if ($d.Path) { $r.DiskPaths[$d.VMName] += $d.Path } }
$r
`

// vpInfix separates the VM name from the processor number in a virtual
// processor instance name.
const vpInfix = ":Hv VP "

// mapCounters sums the rows of each VM of vms by exact name: virtual
// processors by the name before vpInfix, disks by the VM's disk paths,
// adapters by the longest VM name on the host that is followed by "_". A VM
// of vms without disk paths entry does not exist and is left out; one
// without rows gets zeros.
func mapCounters(rows counterRows, vms []string) map[string]vmCounters {
	out := map[string]*vmCounters{}
	disks := map[string]string{} // instance name -> vm
	for _, vm := range vms {
		paths, ok := rows.DiskPaths[vm]
		if !ok {
			continue
		}
		out[vm] = &vmCounters{}
		for _, p := range paths {
			disks[strings.ReplaceAll(p, `\`, "-")] = vm
		}
	}
	for _, r := range rows.VPs {
		i := strings.LastIndex(r.Name, vpInfix)
		if i < 0 {
			continue
		}
		if c, ok := out[r.Name[:i]]; ok {
			c.ticks.run += r.RunTime
			c.ticks.ts = max(c.ticks.ts, r.Timestamp)
		}
	}
	for _, r := range rows.Disks {
		if c, ok := out[disks[r.Name]]; ok {
			c.sample.DiskReadBytes += r.ReadBytes
			c.sample.DiskWriteBytes += r.WriteBytes
			c.sample.DiskReadOps += r.ReadOps
			c.sample.DiskWriteOps += r.WriteOps
		}
	}
	// Every VM name on the host, longest first, so that a VM whose name
	// prefixes another's does not take the other's adapters.
	names := slices.Collect(maps.Keys(rows.DiskPaths))
	slices.SortFunc(names, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	for _, r := range rows.NICs {
		for _, n := range names {
			if !strings.HasPrefix(r.Name, n+"_") {
				continue
			}
			if c, ok := out[n]; ok {
				c.sample.NetRxBytes += r.RxBytes
				c.sample.NetTxBytes += r.TxBytes
				c.sample.NetRxPackets += r.RxPackets
				c.sample.NetTxPackets += r.TxPackets
			}
			break
		}
	}
	res := make(map[string]vmCounters, len(out))
	for vm, c := range out {
		res[vm] = *c
	}
	return res
}

// MetricsAll reads the counters of all vms in one run: disk counters summed
// over all virtual hard disks of a VM, network counters over all of its
// adapters. CPUPercent is the busy time of all vCPUs since the previous
// read of the VM.
func (p *Platform) MetricsAll(ctx context.Context, vms []string) (map[string]platform.Sample, error) {
	data, err := p.r.run(ctx, "MetricsAll", nil, metricsScript)
	if err != nil {
		return nil, err
	}
	var rows counterRows
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, &platform.Error{Kind: platform.Failed, Op: "MetricsAll", Err: err}
	}
	now := time.Now()
	out := map[string]platform.Sample{}
	for vm, c := range mapCounters(rows, vms) {
		s := c.sample
		s.Time = now
		s.CPUPercent = p.cpuPercent(vm, c.ticks)
		out[vm] = s
	}
	return out, nil
}

// Metrics reads the counters of one VM through MetricsAll.
func (p *Platform) Metrics(ctx context.Context, vm string) (platform.Sample, error) {
	all, err := p.MetricsAll(ctx, []string{vm})
	if err != nil {
		return platform.Sample{}, err
	}
	s, ok := all[vm]
	if !ok {
		return platform.Sample{}, &platform.Error{Kind: platform.NotFound, Op: "Metrics", Err: fmt.Errorf("vm %s not found", vm)}
	}
	return s, nil
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
