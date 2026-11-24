package servers

import (
	"context"
	"log/slog"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// Recover implements resource.Recoverer: at cloud start it ends what a
// fault left on the VMs after an abrupt end. It resumes every Paused server
// VM and removes the IOPS limit of every drive (volumes included). Errors
// are logged per server; the start goes on.
func (m *Module) Recover(ctx context.Context) error {
	var all []Record
	err := m.d.Store.Tx(func(tx store.Tx) error {
		var err error
		all, err = store.ListAll[Record](tx, Kind)
		return err
	})
	if err != nil {
		return err
	}
	for _, r := range all {
		m.recoverVM(ctx, r.ID)
	}
	return nil
}

// recoverVM resumes the VM of server id when it is Paused and removes the
// limits of its drives; a missing VM is skipped.
func (m *Module) recoverVM(ctx context.Context, id int64) {
	name := m.vmName(id)
	st, err := m.d.Platform.State(ctx, name)
	if isNotFound(err) {
		return
	}
	if err != nil {
		slog.Warn("read the state of a server VM at cloud start", "id", id, "vm", name, "err", err)
		return
	}
	if st == platform.StatePaused {
		if err := m.d.Platform.Power(ctx, name, platform.PowerResume); err != nil {
			slog.Warn("resume a paused server VM at cloud start", "id", id, "vm", name, "err", err)
		}
	}
	if err := m.d.Platform.SetDiskIOPS(ctx, name, "", 0); err != nil {
		slog.Warn("remove the disk limits of a server VM at cloud start", "id", id, "vm", name, "err", err)
	}
}
