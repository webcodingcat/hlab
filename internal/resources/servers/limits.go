package servers

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/webcodingcat/hlab/internal/actions"
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
	growth, err := m.diskGrowth(stor)
	if err != nil {
		return err
	}
	if avail := res.FreeDiskBytes - m.d.Settings.ReserveDiskGB<<30; growth+addDiskBytes > avail {
		return unavailable("the server needs %d GB disk; %d GB are free beyond the reserve and the growth of existing disks",
			addDiskBytes>>30, max(avail-growth, 0)>>30)
	}
	return nil
}

// diskGrowth is what the disks of all servers can still grow: their
// nominal size minus the current file size (a missing file is size 0).
func (m *Module) diskGrowth(stor *store.Store) (int64, error) {
	var recs []Record
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		recs, err = store.ListAll[Record](tx, Kind)
		return err
	}); err != nil {
		return 0, err
	}
	var growth int64
	for _, r := range recs {
		nominal := int64(r.PrimaryDiskSize) << 30
		var size int64
		fi, err := os.Stat(m.diskPath(r.ID))
		switch {
		case err == nil:
			size = fi.Size()
		case !errors.Is(err, os.ErrNotExist):
			return 0, err
		}
		growth += max(nominal-size, 0)
	}
	return growth, nil
}
