package volumes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

var _ faults.VolumeInjector = (*Module)(nil)

// attached loads volume id of project for a fault: an unknown volume is
// faults.ErrNotFound, an unattached one an *faults.InputError.
func (m *Module) attached(project string, id int64) (Record, error) {
	var r Record
	err := m.d.Store.Tx(func(tx store.Tx) error {
		p, err := store.ProjectByName(tx, project)
		if err != nil {
			return err
		}
		r, err = store.Get[Record](tx, Kind, p.ID, id)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return r, fmt.Errorf("volume %d: %w", id, faults.ErrNotFound)
	}
	if err != nil {
		return r, err
	}
	if r.Server == nil {
		return r, &faults.InputError{Field: "target", Message: fmt.Sprintf("volume %d is not attached", id)}
	}
	return r, nil
}

// Remove implements faults.VolumeInjector: it takes the disk of volume id
// off its server's VM; the record still shows the volume attached.
func (m *Module) Remove(ctx context.Context, project string, id int64) error {
	r, err := m.attached(project, id)
	if err != nil {
		return err
	}
	return m.d.Platform.DetachDisk(ctx, servers.VMName(m.d.VMPrefix, *r.Server), r.Hlab.Path)
}

// Restore implements faults.VolumeInjector: it attaches the disk of volume
// id to its server's VM again at the record's LUN.
func (m *Module) Restore(ctx context.Context, project string, id int64) error {
	r, err := m.attached(project, id)
	if err != nil {
		return err
	}
	return m.d.Platform.AttachDisk(ctx, servers.VMName(m.d.VMPrefix, *r.Server), r.Hlab.Path, r.Hlab.LUN)
}

// SetIOPS implements faults.VolumeInjector: it limits the disk of volume
// id on its server's VM to max I/Os of 8 KiB per second; 0 removes the
// limit.
func (m *Module) SetIOPS(ctx context.Context, project string, id int64, max int) error {
	r, err := m.attached(project, id)
	if err != nil {
		return err
	}
	return m.d.Platform.SetDiskIOPS(ctx, servers.VMName(m.d.VMPrefix, *r.Server), r.Hlab.Path, max)
}

// endFaults ends the faults on volume id of project before an action
// changes its disk; an error is logged and the action goes on.
func (m *Module) endFaults(ctx context.Context, project string, id int64) {
	if m.d.EndFaults == nil {
		return
	}
	if err := m.d.EndFaults(ctx, project, faults.TargetVolume, id); err != nil {
		slog.Warn("end the faults of a volume", "id", id, "err", err)
	}
}
