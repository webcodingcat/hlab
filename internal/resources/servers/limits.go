package servers

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// unavailable is the resource_unavailable error naming the resource.
func unavailable(format string, args ...any) error {
	return &actions.Error{Code: "resource_unavailable", Message: fmt.Sprintf(format, args...)}
}

// checkResources refuses with resource_unavailable a server needing cores
// vCPUs, ramMB of RAM and a new disk of addDiskBytes when the host has fewer
// logical CPUs, when ramMB exceeds the free RAM minus reserve_ram_mb, or
// when the remaining growth of the existing server disks (nominal minus
// file size) plus addDiskBytes exceeds the free disk minus reserve_disk_gb.
// RAM is counted live: stopped servers hold none.
func (m *Module) checkResources(ctx context.Context, stor *store.Store, cores int, ramMB, addDiskBytes int64) error {
	res, err := m.d.Platform.HostResources(ctx)
	if err != nil {
		return err
	}
	if cores > res.LogicalCPUs {
		return unavailable("the server needs %d vCPUs; the host has %d logical CPUs", cores, res.LogicalCPUs)
	}
	if avail := res.FreeMemoryMB - m.d.Settings.ReserveRAMMB; ramMB > avail {
		return unavailable("the server needs %d MB RAM; %d MB are free beyond the reserve", ramMB, max(avail, 0))
	}
	return m.checkDisk(stor, res, addDiskBytes)
}

// CheckDisk refuses with resource_unavailable a new disk use of addBytes
// when the remaining growth of the server disks and the extra disks
// (nominal minus file size) plus addBytes exceeds the free disk minus
// reserve_disk_gb.
func (m *Module) CheckDisk(ctx context.Context, stor *store.Store, addBytes int64) error {
	res, err := m.d.Platform.HostResources(ctx)
	if err != nil {
		return err
	}
	return m.checkDisk(stor, res, addBytes)
}

func (m *Module) checkDisk(stor *store.Store, res platform.Resources, addBytes int64) error {
	growth, err := m.diskGrowth(stor)
	if err != nil {
		return err
	}
	if avail := res.FreeDiskBytes - m.d.Settings.ReserveDiskGB<<30; growth+addBytes > avail {
		return unavailable("%d GB more disk are needed; %d GB are free beyond the reserve and the growth of existing disks",
			addBytes>>30, max(avail-growth, 0)>>30)
	}
	return nil
}

// diskGrowth is what the disks of all servers and the extra disks can
// still grow: their nominal size minus the current file size (a missing
// file is size 0).
func (m *Module) diskGrowth(stor *store.Store) (int64, error) {
	var disks []DiskUse
	if err := stor.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			disks = append(disks, DiskUse{Path: m.diskPath(r.ID), NominalBytes: int64(r.PrimaryDiskSize) << 30})
		}
		if m.d.ExtraDisks == nil {
			return nil
		}
		extra, err := m.d.ExtraDisks(tx)
		disks = append(disks, extra...)
		return err
	}); err != nil {
		return 0, err
	}
	var growth int64
	for _, d := range disks {
		size, err := fileSize(d.Path)
		if err != nil {
			return 0, err
		}
		growth += max(d.NominalBytes-size, 0)
	}
	return growth, nil
}

// fileSize is the size of the file at path; a missing file is size 0.
func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	switch {
	case err == nil:
		return fi.Size(), nil
	case errors.Is(err, os.ErrNotExist):
		return 0, nil
	}
	return 0, err
}
