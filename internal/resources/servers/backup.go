package servers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/store"
)

// maxBackups is the number of backup slots of a server, shared by manual
// and daily backups.
const maxBackups = 7

// backupWindow is the fixed backup window shown while backups are enabled;
// hlab ignores it.
const backupWindow = "22-02"

// backupEvery is how often the backup job looks for servers due a backup.
const backupEvery = time.Hour

// UseActions implements resource.ActionUser: the backup job starts its
// create_image actions with the router's action manager.
func (m *Module) UseActions(a *actions.Manager) { m.acts = a }

// enableBackup turns on backups of a server as an action.
func (m *Module) enableBackup(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	m.groupAction(w, req, id, "enable_backup", func(tx store.Tx, r Record) error {
		window := backupWindow
		r.Hlab.BackupsEnabled = true
		r.BackupWindow = &window
		return put(tx, r)
	})
}

// disableBackup turns off backups of a server and deletes all its backups
// as an action; the action removes their files.
func (m *Module) disableBackup(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	var dropped []imagesres.Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "disable_backup", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		r.Hlab.BackupsEnabled = false
		r.BackupWindow = nil
		if err := put(tx, r); err != nil {
			return err
		}
		dropped, err = dropBackups(tx, id)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(context.Context, func(int)) error {
		removeImageFiles(dropped)
		return nil
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// dropBackups deletes the records of every backup of server serverID and
// returns them; the caller removes their files after the commit.
func dropBackups(tx store.Tx, serverID int64) ([]imagesres.Record, error) {
	backups, err := imagesres.Backups(tx, serverID)
	if err != nil {
		return nil, err
	}
	for _, b := range backups {
		if err := imagesres.Remove(tx, b); err != nil {
			return nil, err
		}
	}
	return backups, nil
}

// removeImageFiles removes the disk files of deleted images.
func removeImageFiles(imgs []imagesres.Record) {
	for _, img := range imgs {
		if err := os.Remove(img.Hlab.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("remove the disk of a deleted image", "image", img.ID, "path", img.Hlab.Path, "err", err)
		}
	}
}

// backups is the job of the daily backups: each server with backups
// enabled and no daily backup of the current UTC day gets one as action
// create_image. A server with a running action waits for the next run.
func (m *Module) backups(ctx context.Context) {
	if m.acts == nil {
		return
	}
	stor := m.d.Store
	now := m.d.Clock.Now().UTC()
	var due []Record
	if err := stor.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if !r.Hlab.BackupsEnabled {
				continue
			}
			done, err := backedUpOn(tx, r.ID, now)
			if err != nil {
				return err
			}
			if !done {
				due = append(due, r)
			}
		}
		return nil
	}); err != nil {
		slog.Error("server backups", "err", err)
		return
	}
	for _, r := range due {
		_, _, err := m.startImage(ctx, stor, m.acts, imageRequest{
			projectID: r.Hlab.ProjectID, serverID: r.ID, typ: imagesres.TypeBackup,
			labels: map[string]string{}, automatic: true,
		})
		switch {
		case errors.Is(err, actions.ErrLocked):
			slog.Info("server busy, daily backup waits for the next run", "id", r.ID)
		case err != nil:
			slog.Warn("daily backup", "id", r.ID, "err", err)
		}
	}
}

// backedUpOn reports whether server serverID has a daily backup made on the
// UTC day of now.
func backedUpOn(tx store.Tx, serverID int64, now time.Time) (bool, error) {
	backups, err := imagesres.Backups(tx, serverID)
	if err != nil {
		return false, err
	}
	y, mo, d := now.Date()
	for _, b := range backups {
		by, bmo, bd := b.Created.UTC().Date()
		if b.Hlab.Automatic && by == y && bmo == mo && bd == d {
			return true, nil
		}
	}
	return false, nil
}
