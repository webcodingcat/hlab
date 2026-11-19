package hyperv

import (
	"testing"

	"github.com/webcodingcat/hlab/internal/platform"
)

func TestHyperV(t *testing.T) {
	t.Run("SRV-21", func(t *testing.T) {
		// "vm-1_a" starts with "vm-1" and continues with the "_" that
		// separates a VM name from the adapter in a NIC instance name.
		vms := []string{"vm-1", "vm-1_a", "vm-3"}
		rows := counterRows{
			VPs: []vpRow{
				{Name: "vm-1:Hv VP 0", RunTime: 100, Timestamp: 5000},
				{Name: "vm-1:Hv VP 1", RunTime: 200, Timestamp: 5000},
				{Name: "vm-1_a:Hv VP 0", RunTime: 4000, Timestamp: 5000},
				{Name: "Root VP 0", RunTime: 70000, Timestamp: 5000},
				{Name: "_Total", RunTime: 90000, Timestamp: 5000},
			},
			Disks: []diskRow{
				{Name: `C:-hlab-servers-1-root.vhdx`, ReadBytes: 1, WriteBytes: 2, ReadOps: 3, WriteOps: 4},
				{Name: `C:-hlab-volumes-5.vhdx`, ReadBytes: 10, WriteBytes: 20, ReadOps: 30, WriteOps: 40},
				{Name: `C:-hlab-servers-2-root.vhdx`, ReadBytes: 100, WriteBytes: 200, ReadOps: 300, WriteOps: 400},
				{Name: `C:-other-unrelated.vhdx`, ReadBytes: 7000, WriteBytes: 7000, ReadOps: 7000, WriteOps: 7000},
				{Name: "_Total", ReadBytes: 9000, WriteBytes: 9000, ReadOps: 9000, WriteOps: 9000},
			},
			NICs: []nicRow{
				{Name: "vm-1_Network Adapter_{11111111-1111-1111-1111-111111111111}", RxBytes: 1, TxBytes: 2, RxPackets: 3, TxPackets: 4},
				{Name: "vm-1_Network Adapter_{22222222-2222-2222-2222-222222222222}", RxBytes: 10, TxBytes: 20, RxPackets: 30, TxPackets: 40},
				{Name: "vm-1_a_Network Adapter_{33333333-3333-3333-3333-333333333333}", RxBytes: 100, TxBytes: 200, RxPackets: 300, TxPackets: 400},
				{Name: "_Total", RxBytes: 9000, TxBytes: 9000, RxPackets: 9000, TxPackets: 9000},
			},
			DiskPaths: map[string][]string{
				"vm-1":   {`C:\hlab\servers\1\root.vhdx`, `C:\hlab\volumes\5.vhdx`},
				"vm-1_a": {`C:\hlab\servers\2\root.vhdx`},
				"vm-3":   {`C:\hlab\servers\3\root.vhdx`},
			},
		}
		want := map[string]struct {
			run    uint64
			sample platform.Sample
		}{
			"vm-1": {300, platform.Sample{
				DiskReadBytes: 11, DiskWriteBytes: 22, DiskReadOps: 33, DiskWriteOps: 44,
				NetRxBytes: 11, NetTxBytes: 22, NetRxPackets: 33, NetTxPackets: 44,
			}},
			"vm-1_a": {4000, platform.Sample{
				DiskReadBytes: 100, DiskWriteBytes: 200, DiskReadOps: 300, DiskWriteOps: 400,
				NetRxBytes: 100, NetTxBytes: 200, NetRxPackets: 300, NetTxPackets: 400,
			}},
			"vm-3": {0, platform.Sample{}},
		}

		got := mapCounters(rows, vms)
		for vm, w := range want {
			c, ok := got[vm]
			if !ok {
				t.Errorf("%s: no counters, want them", vm)
				continue
			}
			if c.ticks.run != w.run {
				t.Errorf("%s: CPU run time %d, want %d", vm, c.ticks.run, w.run)
			}
			c.sample.Time, c.sample.CPUPercent = w.sample.Time, w.sample.CPUPercent
			if c.sample != w.sample {
				t.Errorf("%s: counters %+v, want %+v", vm, c.sample, w.sample)
			}
		}
		if len(got) != len(want) {
			t.Errorf("counters for %d VMs, want %d: %+v", len(got), len(want), got)
		}
	})
}
