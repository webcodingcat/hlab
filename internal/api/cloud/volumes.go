package cloud

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's texts about volumes (measured 2026-11-09).
const (
	volumeNameEmpty          = "is not allowed to be empty"
	volumeNameInvalid        = "Invalid name."
	volumeNameTaken          = "name is already in use"
	volumeSizeRange          = "Must be between 10 and 10240."
	volumeLocationRequired   = "location is required if no server is given"
	volumeLocationUnknown    = "this location does not exist or is currently not available"
	volumeFormatUnknown      = "unknown value, must be empty or xfs or ext4"
	volumeAutomountNoFormat  = "automount requires format to be provided"
	volumeLabelKey           = "key contains invalid characters or is malformed"
	volumeLabelValue         = "value contains invalid characters or is malformed"
	volumeBadBody            = "can not read request body"
	volumeNotFound           = "volume not found"
	volumeGone               = "volume does not exist"
	volumeProtected          = "volume deletion is protected"
	volumeStillAttached      = "volume with ID '%d' is still attached to a server"
	volumeBusy               = "cannot perform operation because resource is locked"
	volumeNotAttached        = "volume not attached to a server"
	volumeOtherLocation      = "server and volume must be in the same location"
	volumesPerServerReached  = "number of volumes attached to a single server reached"
	volumeAssigneeRequired   = "is required"
	volumeAssigneeType       = "unsupported resource type %q"
	volumeTooSmall           = "volume size is too small"
	volumeTooSmallField      = "size needs to be larger than current volume size"
	volumeAttachFailed       = "volume attach failed"
	volumeMinGB, volumeMaxGB = 10, 10240
	volumesPerServer         = 16
	volumeLinuxDevice        = "/dev/disk/by-id/scsi-0HC_Volume_%d"
	volumeAssigneeTypeServer = "server"
	volumeAlreadyAttached    = "volume already attached"
	// a server's create naming a missing volume: 422, the same text as
	// the volumes' 404
	serverVolumeNotFound = "volume not found"
	serverVolumeLocation = "volume in invalid location"
	volumeNameMax        = 64
	volumeNameTooLong    = "Longer than maximum length 64."
	volumeServerNotFound = "server not found"
)

// volumeName is a name Hetzner takes for a volume: letters, digits, dots,
// dashes and underscores, starting and ending with a letter or a digit,
// 64 characters at most (measured 2026-11-09).
var volumeName = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9_.-]*[a-zA-Z0-9])?$`)

// volumeAnswer is a volume as Hetzner's API sends it, with the spec's
// order of fields. It is HLab's own type because schema.Volume lacks
// assignee.
type volumeAnswer struct {
	ID          int64                   `json:"id"`
	Created     time.Time               `json:"created"`
	Name        string                  `json:"name"`
	Server      *int64                  `json:"server"`
	Assignee    *volumeAssignee         `json:"assignee"`
	Location    schema.Location         `json:"location"`
	Size        int                     `json:"size"`
	LinuxDevice string                  `json:"linux_device"`
	Protection  schema.VolumeProtection `json:"protection"`
	Labels      map[string]string       `json:"labels"`
	Status      string                  `json:"status"`
	Format      *string                 `json:"format"`
}

// volumeAssignee is what a volume is attached to: Hetzner's newer field
// beside server, and the create's and attach's way to name the server.
type volumeAssignee struct {
	Type *string `json:"type"`
	ID   *int64  `json:"id"`
}

// volumeCreateRequest is the body of POST /volumes; HLab's own type, to
// tell a missing field from an empty one.
type volumeCreateRequest struct {
	Name      *string            `json:"name"`
	Size      *int               `json:"size"`
	Location  *string            `json:"location"`
	Server    *int64             `json:"server"` // deprecated beside assignee
	Assignee  *volumeAssignee    `json:"assignee"`
	Format    *string            `json:"format"`
	Automount *bool              `json:"automount"`
	Labels    *map[string]string `json:"labels"`
}

// volumeUpdateRequest is the body of PUT /volumes/{id}; Hetzner ignores a
// size there (measured 2026-11-09).
type volumeUpdateRequest struct {
	Name   *string            `json:"name"`
	Labels *map[string]string `json:"labels"`
}

// volumeSortKeys are the sort values of GET /volumes.
var volumeSortKeys = web.SortKeys[volumeAnswer]{
	"id":      func(a, b volumeAnswer) int { return cmp.Compare(a.ID, b.ID) },
	"name":    func(a, b volumeAnswer) int { return cmp.Compare(a.Name, b.Name) },
	"created": func(a, b volumeAnswer) int { return a.Created.Compare(b.Created) },
}

// volumeStatuses are the values of the list's status filter; Hetzner
// refuses others (measured 2026-11-09).
var volumeStatuses = []string{string(state.VolumeCreating), string(state.VolumeAvailable)}

// registerVolumes adds the routes of the volumes and their actions.
func registerVolumes(api *web.API, l *lab.Lab) {
	table := volumeActions(l)
	api.Handle("GET /volumes", func(req *web.Request) (int, any, error) {
		selector, err := web.ParseSelector(req.URL.Query().Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		statuses, filtered := req.URL.Query()["status"]
		for _, status := range statuses {
			if !slices.Contains(volumeStatuses, status) {
				return 0, nil, web.InvalidValue("status")
			}
		}
		var volumes []volumeAnswer
		_ = l.View(func(tx *state.Tx) error {
			for _, volume := range state.All(tx, state.Volumes) {
				if volume.ProjectID == req.Caller.ProjectID && selector.Matches(volume.Labels) &&
					(!filtered || slices.Contains(statuses, string(volume.Status))) {
					volumes = append(volumes, volumeJSON(l.Catalog(), volume))
				}
			}
			return nil
		})
		volumes = byName(req, volumes, func(volume volumeAnswer) string { return volume.Name })
		if err := web.Sort(req, volumes, volumeSortKeys); err != nil {
			return 0, nil, err
		}
		return listJSON(req, "volumes", volumes)
	})

	api.Handle("POST /volumes", func(req *web.Request) (int, any, error) {
		return createVolume(req, l, table)
	})

	api.Handle("GET /volumes/{id}", func(req *web.Request) (int, any, error) {
		var out volumeAnswer
		err := l.View(func(tx *state.Tx) error {
			volume, err := callerVolume(tx, req.Caller.ProjectID, web.PathID(req, "id"))
			if err == nil {
				out = volumeJSON(l.Catalog(), volume)
			}
			return err
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]volumeAnswer{"volume": out}, nil
	})

	api.Handle("PUT /volumes/{id}", func(req *web.Request) (int, any, error) {
		return updateVolume(req, l)
	})

	api.Handle("DELETE /volumes/{id}", func(req *web.Request) (int, any, error) {
		return deleteVolume(req, l)
	})

	for _, command := range []string{"attach", "detach", "resize", "change_protection"} {
		api.Handle("POST /volumes/{id}/actions/"+command, func(req *web.Request) (int, any, error) {
			action, err := act(l, table, command, req)
			if err != nil {
				return 0, nil, err
			}
			return http.StatusCreated, map[string]schema.Action{"action": actionJSON(&action)}, nil
		})
	}
	// the servers' answers, and the same 404 for other methods also under
	// /volumes/actions and on a volume (measured 2026-11-09)
	registerResourceActions(api, l, "volumes", lab.VolumeResource, actionMisses{
		unknown: plainActionNotFound, notFoundMethods: []string{"{id}/actions/{action_id}", "actions/{action_id}"},
	})
	api.Handle("PATCH /volumes/{id}", func(*web.Request) (int, any, error) {
		return 0, nil, web.Refusal(http.StatusNotFound, "not_found", "404 Not Found")
	})
}

// decodeVolumeBody decodes a volume's request body; a field of the wrong
// type is Hetzner's json_error "can not read request body" here, not
// "failed to parse json" (measured 2026-11-09 with a size of 10.5 and
// "10").
func decodeVolumeBody(req *web.Request, body any) error {
	err := web.DecodeJSON(req.Request, body)
	if failed, ok := errors.AsType[*web.Error](err); ok && failed.Message == web.ParseFailed().Message {
		return web.JSONError(volumeBadBody)
	}
	return err
}

// createVolume creates a volume in a location, or attached to a server,
// whose location it then takes. Hetzner checks the fields together and
// answers a server that is not there with 500 server_error (measured
// 2026-11-09).
func createVolume(req *web.Request, l *lab.Lab, table lab.ActionTable) (int, any, error) {
	var body volumeCreateRequest
	if err := decodeVolumeBody(req, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	checkVolumeName(&check, body.Name)
	if body.Size == nil || *body.Size < volumeMinGB || *body.Size > volumeMaxGB {
		check.Fail("size", volumeSizeRange)
	}
	serverID := volumeServerOf(body)
	var location catalog.Location
	switch {
	case body.Location != nil:
		var found bool
		if location, found = l.Catalog().LocationByName(*body.Location); !found {
			check.Fail("location", volumeLocationUnknown)
		}
	case serverID == nil:
		check.Fail("location", volumeLocationRequired)
	}
	format := ""
	if body.Format != nil {
		format = *body.Format
		if format != "" && format != "ext4" && format != "xfs" {
			check.Fail("format", volumeFormatUnknown)
		}
	}
	automount := body.Automount != nil && *body.Automount
	if automount && format == "" {
		check.Fail("format", volumeAutomountNoFormat)
	}
	if body.Labels != nil {
		check.LabelsWith(*body.Labels, volumeLabelKey, volumeLabelValue)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	volume := &state.Volume{
		ProjectID: req.Caller.ProjectID,
		Name:      *body.Name,
		Labels:    map[string]string{},
		Created:   l.Now(),
		SizeGB:    *body.Size,
		Location:  location.Name,
		Format:    format,
		Status:    state.VolumeCreating,
	}
	if body.Labels != nil {
		volume.Labels = *body.Labels
	}
	// Hetzner's next_actions: the attach, if a server is given (measured
	// 2026-11-09)
	rows := []lab.Row{{Command: "create"}}
	if serverID != nil {
		raw, _ := json.Marshal(map[string]any{"server": *serverID, "automount": automount})
		rows = append(rows, lab.Row{Command: "attach_after_create", Body: raw})
	}
	var out volumeAnswer
	actions, err := l.Create(table, lab.Request{ProjectID: volume.ProjectID}, func(tx *state.Tx) (int64, error) {
		if serverID != nil {
			server, err := state.Get(tx, state.Servers, *serverID)
			if err != nil || server.ProjectID != volume.ProjectID {
				return 0, web.ServerError("internal server error")
			}
			if volume.Location == "" {
				volume.Location = server.Location
			}
		}
		if _, taken := state.ByName(tx, state.Volumes, volume.ProjectID, volume.Name); taken {
			return 0, web.Uniqueness(volumeNameTaken, "name")
		}
		volume.ID = state.NewID(tx, state.Volumes)
		state.Put(tx, state.Volumes, volume)
		return volume.ID, nil
	}, func(tx *state.Tx, id int64) {
		if current, err := state.Get(tx, state.Volumes, id); err == nil {
			// the volume is attached once the create is done; Hetzner's
			// answer shows it without a server (measured 2026-11-09)
			answer := *current
			answer.ServerID = 0
			out = volumeJSON(l.Catalog(), &answer)
		}
	}, rows...)
	if err != nil {
		return 0, nil, actError(err)
	}
	next := []schema.Action{}
	for i := 1; i < len(actions); i++ {
		next = append(next, actionJSON(&actions[i]))
	}
	return http.StatusCreated, struct {
		Volume      volumeAnswer    `json:"volume"`
		Action      schema.Action   `json:"action"`
		NextActions []schema.Action `json:"next_actions"`
	}{Volume: out, Action: actionJSON(&actions[0]), NextActions: next}, nil
}

// volumeServerOf returns the server a create names, by assignee or by
// the deprecated server; nil for none.
func volumeServerOf(body volumeCreateRequest) *int64 {
	if body.Assignee != nil && body.Assignee.ID != nil {
		return body.Assignee.ID
	}
	return body.Server
}

// checkVolumeName notes a name that is missing, empty or not one Hetzner
// takes.
func checkVolumeName(check *web.Check, name *string) {
	switch {
	case name == nil || *name == "":
		check.Fail("name", volumeNameEmpty)
	case len(*name) > volumeNameMax:
		check.Fail("name", volumeNameTooLong)
	case !volumeName.MatchString(*name):
		check.Fail("name", volumeNameInvalid)
	}
}

// updateVolume changes a volume's name and labels.
func updateVolume(req *web.Request, l *lab.Lab) (int, any, error) {
	var body volumeUpdateRequest
	if err := decodeVolumeBody(req, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Update: true}
	if body.Name != nil {
		checkVolumeName(&check, body.Name)
	}
	if body.Labels != nil {
		check.LabelsWith(*body.Labels, volumeLabelKey, volumeLabelValue)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	var out volumeAnswer
	err := l.Change(func(tx *state.Tx) error {
		volume, err := callerVolume(tx, req.Caller.ProjectID, web.PathID(req, "id"))
		if err != nil {
			return err
		}
		if body.Name != nil && *body.Name != volume.Name {
			if _, taken := state.ByName(tx, state.Volumes, volume.ProjectID, *body.Name); taken {
				return web.Uniqueness(volumeNameTaken, "name")
			}
			volume.Name = *body.Name
		}
		if body.Labels != nil {
			volume.Labels = *body.Labels
		}
		out = volumeJSON(l.Catalog(), volume)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]volumeAnswer{"volume": out}, nil
}

// deleteVolume deletes a volume without an action, and then its disk.
// Hetzner refuses a protected or attached volume; a second delete is 404
// with its own text (measured 2026-11-09). A volume whose actions run is
// 423, as a volume of a server being deleted is.
func deleteVolume(req *web.Request, l *lab.Lab) (int, any, error) {
	id := web.PathID(req, "id")
	err := l.ChangeIdle(lab.VolumeRef(id), volumeBusy, func(tx *state.Tx) error {
		volume, err := state.Get(tx, state.Volumes, id)
		switch {
		case errors.Is(err, state.ErrNotFound) || (err == nil && volume.ProjectID != req.Caller.ProjectID):
			return web.Refusal(http.StatusNotFound, "not_found", volumeGone)
		case err != nil:
			return err
		case volume.Protection.Delete:
			return web.Refusal(http.StatusLocked, "protected", volumeProtected)
		case volume.ServerID != 0:
			if _, err := state.Get(tx, state.Servers, volume.ServerID); err != nil {
				return web.Locked(volumeBusy) // its server's delete runs
			}
			return web.Refusal(http.StatusUnprocessableEntity, "service_error", fmt.Sprintf(volumeStillAttached, volume.ID))
		}
		state.Delete(tx, state.Volumes, id)
		return nil
	})
	if err != nil {
		return 0, nil, actError(err)
	}
	l.Machine().DeleteVolumeFileLater(id)
	return http.StatusNoContent, nil, nil
}

// callerVolume returns the volume of an ID if it belongs to the project,
// with Hetzner's 404 otherwise.
func callerVolume(tx *state.Tx, projectID, id int64) (*state.Volume, error) {
	volume, err := state.Get(tx, state.Volumes, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && volume.ProjectID != projectID) {
		return nil, web.Refusal(http.StatusNotFound, "not_found", volumeNotFound)
	}
	return volume, err
}

// volumeJSON returns a volume as the API shows it. Its linux_device is
// there also while it is detached (measured 2026-11-09).
func volumeJSON(cat *catalog.Catalog, volume *state.Volume) volumeAnswer {
	out := volumeAnswer{
		ID:          volume.ID,
		Name:        volume.Name,
		Status:      string(volume.Status),
		Size:        volume.SizeGB,
		Protection:  schema.VolumeProtection{Delete: volume.Protection.Delete},
		Labels:      volume.Labels,
		LinuxDevice: fmt.Sprintf(volumeLinuxDevice, volume.ID),
		Created:     volume.Created,
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	if volume.Format != "" {
		out.Format = new(volume.Format)
	}
	if volume.ServerID != 0 {
		out.Server = new(volume.ServerID)
		out.Assignee = &volumeAssignee{Type: new(volumeAssigneeTypeServer), ID: new(volume.ServerID)}
	}
	if location, found := cat.LocationByName(volume.Location); found {
		out.Location = locationJSON(location)
	}
	return out
}
