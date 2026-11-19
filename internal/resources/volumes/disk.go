package volumes

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/store"
)

// diskPath is the disk file of volume id: <data>\volumes\volume-<id>.vhdx.
func (m *Module) diskPath(id int64) string {
	return filepath.Join(m.d.DataFolder, "volumes", fmt.Sprintf("volume-%d.vhdx", id))
}

// Reconcile implements resource.Reconciler: a volume still being created
// gets its disk (when missing) and its filesystem, then becomes available.
func (m *Module) Reconcile(ctx context.Context, id int64) error {
	return m.reconcile(ctx, m.d.Store, id)
}

func (m *Module) reconcile(ctx context.Context, stor *store.Store, id int64) error {
	var r Record
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		r, err = load(tx, id)
		return err
	}); err != nil {
		return err
	}
	if r.Status != StatusCreating {
		return nil
	}
	if _, err := os.Stat(r.Hlab.Path); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(r.Hlab.Path), 0o750); err != nil {
			return err
		}
		if err := m.d.Platform.CreateDisk(ctx, r.Hlab.Path, int64(r.Size)<<30, "", Page83(id)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if r.Format != nil {
		if err := m.d.ApplianceJob(ctx, r.Hlab.Path, agentapi.Job{Kind: agentapi.JobMkfs, FS: *r.Format}); err != nil {
			return fmt.Errorf("format volume %d as %s: %w", id, *r.Format, err)
		}
	}
	return stor.Tx(func(tx store.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Status = StatusAvailable
		return put(tx, r)
	})
}

// Delete implements resource.Deleter for project deletion: it removes the
// disk file and the row of volume id. A volume already gone is no error.
func (m *Module) Delete(_ context.Context, id int64) error {
	var r Record
	err := m.d.Store.Tx(func(tx store.Tx) error {
		var err error
		if r, err = load(tx, id); err != nil {
			return err
		}
		return store.Delete(tx, Kind, r.Hlab.ProjectID, id)
	})
	if errors.Is(err, store.ErrNotFound) {
		return removeFile(m.diskPath(id))
	}
	if err != nil {
		return err
	}
	return removeFile(r.Hlab.Path)
}

// Recover implements resource.Recoverer: it removes every volume record
// with status creating and its disk file; a missing file is no error.
func (m *Module) Recover(_ context.Context) error {
	var cut []Record
	err := m.d.Store.Tx(func(tx store.Tx) error {
		all, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range all {
			if r.Status != StatusCreating {
				continue
			}
			if err := store.Delete(tx, Kind, r.Hlab.ProjectID, r.ID); err != nil {
				return err
			}
			cut = append(cut, r)
		}
		return nil
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range cut {
		slog.Info("removed volume left creating", "volume", r.ID)
		if err := removeFile(r.Hlab.Path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// drop removes the record of a volume whose creation failed, if it is
// still there, and its file.
func (m *Module) drop(stor *store.Store, r Record) {
	err := stor.Tx(func(tx store.Tx) error {
		err := store.Delete(tx, Kind, r.Hlab.ProjectID, r.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		slog.Warn("remove the record of a failed volume", "volume", r.ID, "err", err)
	}
	if err := removeFile(r.Hlab.Path); err != nil {
		slog.Warn("remove the disk of a failed volume", "volume", r.ID, "path", r.Hlab.Path, "err", err)
	}
}

// removeFile removes path; a missing file is no error.
func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
