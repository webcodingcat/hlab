package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/store"
)

// source is the image a server is created or rebuilt from: a catalog image
// or a project image (snapshot or backup) of the server's project.
type source struct {
	raw      json.RawMessage // the image object the server shows
	name     string          // catalog image name; "" for a project image
	imageID  int64           // project image ID; 0 for a catalog image
	diskSize float64         // GB
}

// findImage resolves ref as a catalog image by name or ID (only type system
// when systemOnly), else as an available project image of projectID by ID.
// Anything else is invalid_input on the field image.
func (m *Module) findImage(tx store.Tx, projectID int64, ref string, systemOnly bool) (source, error) {
	if img, ok := m.image(ref); ok && (!systemOnly || img.Type == "system") {
		return source{raw: img.Raw, name: img.Name, diskSize: img.DiskSize}, nil
	}
	notFound := &api.FieldError{Field: "image", Message: "image " + ref + " not found"}
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return source{}, notFound
	}
	r, err := imagesres.Load(tx, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		return source{}, notFound
	}
	if err != nil {
		return source{}, err
	}
	if r.Status != imagesres.StatusAvailable {
		return source{}, &api.FieldError{Field: "image", Message: "image " + ref + " is not available yet"}
	}
	raw, err := json.Marshal(r.Image)
	if err != nil {
		return source{}, err
	}
	return source{raw: raw, imageID: r.ID, diskSize: r.DiskSize}, nil
}

// fits refuses with invalid_input on field a server type whose disk of
// diskGB is smaller than the image's disk_size.
func (s source) fits(diskGB int, field string) error {
	if float64(diskGB) < s.diskSize {
		return &api.FieldError{Field: field, Message: fmt.Sprintf(
			"the image needs a disk of %g GB; the server type has %d GB", s.diskSize, diskGB)}
	}
	return nil
}

// baseDisk is the file the disk of server r is copied from: the project
// image's file, or the catalog image fetched by the image store.
func (m *Module) baseDisk(ctx context.Context, stor *store.Store, r Record) (string, error) {
	if r.Hlab.ImageID == 0 {
		return m.d.Images.Ensure(ctx, r.Hlab.Image)
	}
	var img imagesres.Record
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		img, err = imagesres.Load(tx, r.Hlab.ProjectID, r.Hlab.ImageID)
		return err
	}); err != nil {
		return "", fmt.Errorf("image %d of server %d: %w", r.Hlab.ImageID, r.ID, err)
	}
	return img.Hlab.Path, nil
}

// imagePath is the disk file of project image id.
func (m *Module) imagePath(id int64) string {
	return filepath.Join(m.d.Settings.DataFolder, "images", fmt.Sprintf("image-%d.vhdx", id))
}

// createImage makes a snapshot of the server's disk, or a backup of a
// server with backups enabled, as a project image.
func (m *Module) createImage(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Description *string            `json:"description"`
		Type        *string            `json:"type"`
		Labels      *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	typ := imagesres.TypeSnapshot
	if body.Type != nil {
		typ = *body.Type
	}
	if typ != imagesres.TypeSnapshot && typ != imagesres.TypeBackup {
		api.Fail(w, &api.FieldError{Field: "type", Message: "type must be snapshot or backup"})
		return
	}
	labels := map[string]string{}
	if body.Labels != nil && *body.Labels != nil {
		labels = *body.Labels
	}
	if err := api.CheckLabels(labels); err != nil {
		api.Fail(w, err)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	if err := stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		return checkBackupType(r, typ)
	}); err != nil {
		api.Fail(w, err)
		return
	}
	img, action, err := m.startImage(req.Context(), stor, api.ActionsFrom(req.Context()), imageRequest{
		projectID: p.ID, serverID: id, typ: typ, description: body.Description, labels: labels,
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusCreated, map[string]any{"image": img.Image, "action": action})
}

// checkBackupType refuses a backup of a server without backups enabled.
func checkBackupType(r Record, typ string) error {
	if typ == imagesres.TypeBackup && !r.Hlab.BackupsEnabled {
		return &api.FieldError{Field: "type", Message: "backups are not enabled for the server"}
	}
	return nil
}

// imageRequest is what create_image and the backup job ask for.
type imageRequest struct {
	projectID, serverID int64
	typ                 string
	description         *string // nil = "<server name> <created>"
	labels              map[string]string
	automatic           bool // daily backup
}

// startImage stores the image of q as creating with a create_image action
// on the server and runs the action: it copies the disk and makes the
// image available, or removes record and file on failure. A server with a
// running action gives actions.ErrLocked.
func (m *Module) startImage(ctx context.Context, stor *store.Store, acts *actions.Manager, q imageRequest) (imagesres.Record, *actions.Action, error) {
	id := q.serverID
	size, err := fileSize(m.diskPath(id))
	if err != nil {
		return imagesres.Record{}, nil, err
	}
	if err := m.CheckDisk(ctx, stor, size); err != nil {
		return imagesres.Record{}, nil, err
	}

	var img imagesres.Record
	var action *actions.Action
	err = stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, q.projectID, id)
		if err != nil {
			return err
		}
		if err := checkBackupType(r, q.typ); err != nil {
			return err
		}
		imgID, err := store.NextID(tx, imagesres.Kind)
		if err != nil {
			return err
		}
		var meta struct {
			OSFlavor     string  `json:"os_flavor"`
			OSVersion    *string `json:"os_version"`
			Architecture string  `json:"architecture"`
		}
		_ = json.Unmarshal(r.Image, &meta) // a field that does not decode stays empty
		created := m.d.Clock.Now().UTC().Truncate(time.Second)
		description := r.Name + " " + created.Format(time.RFC3339)
		if q.description != nil {
			description = *q.description
		}
		var boundTo *int64
		if q.typ == imagesres.TypeBackup {
			boundTo = &r.ID
		}
		img = imagesres.Record{
			Image: imagesres.Image{
				ID:           imgID,
				Type:         q.typ,
				Status:       imagesres.StatusCreating,
				Description:  description,
				DiskSize:     float64(r.PrimaryDiskSize),
				Created:      created,
				CreatedFrom:  &imagesres.CreatedFrom{ID: r.ID, Name: r.Name},
				BoundTo:      boundTo,
				OSFlavor:     meta.OSFlavor,
				OSVersion:    meta.OSVersion,
				Labels:       q.labels,
				Architecture: meta.Architecture,
			},
			Hlab: imagesres.Internal{ProjectID: q.projectID, Path: m.imagePath(imgID), Automatic: q.automatic},
		}
		action, err = acts.Start(tx, q.projectID, "create_image", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		return imagesres.Put(tx, img)
	})
	if err != nil {
		return imagesres.Record{}, nil, err
	}

	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		err := m.snapshot(ctx, stor, id, img)
		if err != nil {
			m.dropImage(stor, img)
		}
		return err
	})
	return img, action, nil
}

// snapshot copies the disk of server id into the file of img and makes img
// available with its image_size. For a backup, the server's oldest backups
// beyond maxBackups are deleted with their files.
func (m *Module) snapshot(ctx context.Context, stor *store.Store, id int64, img imagesres.Record) error {
	if err := os.MkdirAll(filepath.Dir(img.Hlab.Path), 0o750); err != nil {
		return err
	}
	if err := m.d.Platform.Snapshot(ctx, m.vmName(id), m.diskPath(id), img.Hlab.Path); err != nil {
		return err
	}
	size, err := fileSize(img.Hlab.Path)
	if err != nil {
		return err
	}
	gb := float64(size) / (1 << 30)
	var pruned []imagesres.Record
	if err := stor.Tx(func(tx store.Tx) error {
		r, err := imagesres.Load(tx, img.Hlab.ProjectID, img.ID)
		if err != nil {
			return err
		}
		r.Status = imagesres.StatusAvailable
		r.ImageSize = &gb
		if err := imagesres.Put(tx, r); err != nil {
			return err
		}
		if r.Type != imagesres.TypeBackup {
			return nil
		}
		backups, err := imagesres.Backups(tx, *r.BoundTo)
		if err != nil {
			return err
		}
		for len(backups) > maxBackups {
			if err := imagesres.Remove(tx, backups[0]); err != nil {
				return err
			}
			pruned = append(pruned, backups[0])
			backups = backups[1:]
		}
		return nil
	}); err != nil {
		return err
	}
	removeImageFiles(pruned)
	return nil
}

// dropImage removes the record of a failed image, if it is still there,
// and its file.
func (m *Module) dropImage(stor *store.Store, img imagesres.Record) {
	err := stor.Tx(func(tx store.Tx) error {
		err := imagesres.Remove(tx, img)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		slog.Warn("remove the record of a failed image", "image", img.ID, "err", err)
	}
	if err := os.Remove(img.Hlab.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("remove the disk of a failed image", "image", img.ID, "path", img.Hlab.Path, "err", err)
	}
}
