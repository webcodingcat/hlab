package images

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind of project images (snapshots and backups).
const Kind = "images"

// Image types and statuses of project images.
const (
	TypeSnapshot = "snapshot"
	TypeBackup   = "backup"

	StatusCreating  = "creating"
	StatusAvailable = "available"
)

// Image is an image object in Hetzner's API shape.
type Image struct {
	ID           int64             `json:"id"`
	Type         string            `json:"type"`
	Status       string            `json:"status"`
	Name         *string           `json:"name"`
	Description  string            `json:"description"`
	ImageSize    *float64          `json:"image_size"` // GB
	DiskSize     float64           `json:"disk_size"`  // GB
	Created      time.Time         `json:"created"`
	CreatedFrom  *CreatedFrom      `json:"created_from"`
	BoundTo      *int64            `json:"bound_to"`
	OSFlavor     string            `json:"os_flavor"`
	OSVersion    *string           `json:"os_version"`
	RapidDeploy  bool              `json:"rapid_deploy"`
	Protection   Protection        `json:"protection"`
	Deprecation  json.RawMessage   `json:"deprecation"`
	Deleted      *time.Time        `json:"deleted"`
	Labels       map[string]string `json:"labels"`
	Architecture string            `json:"architecture"`
}

// CreatedFrom names the server an image was created from.
type CreatedFrom struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Protection is the protection of an image.
type Protection struct {
	Delete bool `json:"delete"`
}

// Internal holds hlab's fields of a project image.
type Internal struct {
	ProjectID int64  `json:"project_id"`
	Path      string `json:"path"`                // <data>\images\image-<id>.vhdx
	Automatic bool   `json:"automatic,omitempty"` // daily backup
}

// Record is a stored project image: the image object plus `_hlab`.
type Record struct {
	Image
	Hlab Internal `json:"_hlab"`
}

// Put stores r.
func Put(tx store.Tx, r Record) error {
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	return store.Put(tx, store.Meta{Kind: Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID, Labels: r.Labels, Created: r.Created}, r)
}

// Load returns image id of project projectID, or store.ErrNotFound.
func Load(tx store.Tx, projectID, id int64) (Record, error) {
	return store.Get[Record](tx, Kind, projectID, id)
}

// Backups returns the backups bound to server serverID, oldest first.
func Backups(tx store.Tx, serverID int64) ([]Record, error) {
	all, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return nil, err
	}
	out := []Record{}
	for _, r := range all {
		if r.Type == TypeBackup && r.BoundTo != nil && *r.BoundTo == serverID {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b Record) int {
		return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// Remove deletes the record of r; the caller removes r.Hlab.Path after the
// commit.
func Remove(tx store.Tx, r Record) error {
	return store.Delete(tx, Kind, r.Hlab.ProjectID, r.ID)
}
