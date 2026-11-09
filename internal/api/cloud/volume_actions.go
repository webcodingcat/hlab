package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// volumeActions is the volumes' action table. A volume's actions run one
// after another, as Hetzner runs a detach sent during a resize (201,
// measured 2026-11-09); only a second resize is 423 (planResizeVolume).
// They do not lock the server: two volumes attach to one server at once,
// and a server's reboot runs beside an attach (measured 2026-11-09).
func volumeActions(l *lab.Lab) lab.ActionTable {
	return lab.ActionTable{
		ResourceType: lab.VolumeResource,
		Busy:         volumeBusy,
		Queued:       true,
		Actions: map[string]lab.ActionSpec{
			"create":              {Name: "create_volume", Plan: planCreateVolume(l)},
			"attach":              {Name: "attach_volume", Plan: planAttachVolume(l, false)},
			"attach_after_create": {Name: "attach_volume", Plan: planAttachVolume(l, true)},
			// a server's create attaches the volume: its steps build the
			// VM with the disk, so the action has none and runs beside
			"attach_at_create":  {Name: "attach_volume", Plan: planAttachAtServerCreate, Unflagged: true},
			"detach":            {Name: "detach_volume", Plan: planDetachVolume(l)},
			"resize":            {Name: "resize_volume", Plan: planResizeVolume(l)},
			"change_protection": {Name: "change_protection", Plan: planVolumeProtection, Unflagged: true},
		},
	}
}

// planCreateVolume makes the volume's disk in the steps; if they fail, the
// undo removes the record, by its ID.
func planCreateVolume(l *lab.Lab) planFunc {
	return func(_ *state.Tx, req lab.Request) (lab.Plan, error) {
		return lab.Plan{
			Steps: func(ctx context.Context) error { return l.Machine().CreateVolume(ctx, req.ID) },
			Undo: func(tx *state.Tx) error {
				state.Delete(tx, state.Volumes, req.ID)
				return nil
			},
		}, nil
	}
}

// volumeAttachRequest is the body of the attach action: the server by
// assignee or by the deprecated server.
type volumeAttachRequest struct {
	Server    *int64          `json:"server"`
	Assignee  *volumeAssignee `json:"assignee"`
	Automount *bool           `json:"automount"`
}

// planAttachVolume attaches a volume to a server of its location, also a
// running one; the steps put the disk into the VM. An attach to the
// server it has ends at once (measured 2026-11-09). The attach of a
// volume's create (afterCreate) waits in the volume's queue for the disk;
// a server in another location fails it only in the steps, with
// Hetzner's error (measured 2026-11-09: after 64 s).
func planAttachVolume(l *lab.Lab, afterCreate bool) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body volumeAttachRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.JSONError(volumeBadBody)
		}
		check := web.Check{Singular: true}
		serverID := body.Server
		if body.Assignee != nil {
			serverID = body.Assignee.ID
			if body.Assignee.Type != nil && *body.Assignee.Type != volumeAssigneeTypeServer {
				check.Fail("assignee.type", fmt.Sprintf(volumeAssigneeType, *body.Assignee.Type))
			}
		}
		if serverID == nil {
			check.Fail("assignee", volumeAssigneeRequired)
		}
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		volume, err := callerVolume(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		server, err := state.Get(tx, state.Servers, *serverID)
		if err != nil || server.ProjectID != req.ProjectID {
			return lab.Plan{}, web.Refusal(http.StatusNotFound, "not_found", volumeServerNotFoundError)
		}
		plan := lab.Plan{Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: server.ID}}}
		if volume.ServerID == server.ID {
			return plan, nil
		}
		if afterCreate && server.Location != volume.Location {
			plan.Steps = func(context.Context) error {
				return &lab.Failure{Code: "unknown_error", Message: volumeAttachFailed}
			}
			return plan, nil
		}
		if server.Status == state.ServerInitializing {
			// the server's create runs (assumed as measured for Floating
			// IPs; not measured for volumes)
			return lab.Plan{}, web.Locked(floatingIPServerCreating)
		}
		if err := attachVolume(tx, volume, server, body.Automount != nil && *body.Automount); err != nil {
			return lab.Plan{}, err
		}
		plan.Steps = func(ctx context.Context) error { return l.Machine().AttachVolume(ctx, volume.ID) }
		plan.Undo = undoRecord(state.Volumes, volume.ID, func(_ *state.Tx, volume *state.Volume) {
			if volume.ServerID == server.ID {
				volume.ServerID, volume.Slot = 0, 0
			}
		})
		plan.UndoIfLast = true
		return plan, nil
	}
}

// planAttachAtServerCreate attaches a volume that a server's create names
// (Request.Created); the create's steps make the VM with its disk.
func planAttachAtServerCreate(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	var body volumeAttachRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return lab.Plan{}, web.ParseFailed()
	}
	volume, err := callerVolume(tx, req.ProjectID, req.ID)
	if err != nil {
		return lab.Plan{}, err
	}
	server, err := state.Get(tx, state.Servers, req.Created)
	if err != nil {
		return lab.Plan{}, err
	}
	if err := attachVolume(tx, volume, server, body.Automount != nil && *body.Automount); err != nil {
		return lab.Plan{}, err
	}
	return lab.Plan{Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: server.ID}}}, nil
}

// attachVolume checks an attach to a server and makes it in the records:
// the server's location, at most 16 volumes per server (measured
// 2026-11-09), a volume attached elsewhere (assumed, not measured); the
// volume takes the server's lowest free slot.
func attachVolume(tx *state.Tx, volume *state.Volume, server *state.Server, automount bool) error {
	if volume.ServerID != 0 {
		if _, err := state.Get(tx, state.Servers, volume.ServerID); err != nil {
			return web.Locked(volumeBusy) // its server's delete runs
		}
		return web.Refusal(http.StatusUnprocessableEntity, "volume_already_attached", volumeAttachedElsewhere)
	}
	if server.Location != volume.Location {
		return web.Refusal(http.StatusUnprocessableEntity, "service_error", volumeOtherLocation)
	}
	var slots []int
	for _, attached := range state.All(tx, state.Volumes) {
		if attached.ServerID == server.ID {
			slots = append(slots, attached.Slot)
		}
	}
	if len(slots) >= volumesPerServer {
		return web.Refusal(http.StatusForbidden, "resource_limit_exceeded", volumesPerServerReached)
	}
	slot := 1
	for slices.Contains(slots, slot) {
		slot++
	}
	volume.ServerID, volume.Slot, volume.Automount = server.ID, slot, automount
	return nil
}

// planDetachVolume takes a volume from its server; the steps take the
// disk out of the VM. A volume whose server is being deleted is 423, as
// on Hetzner (measured 2026-11-09). If the steps fail, the undo gives the
// volume back to the server, if it and the slot are still there.
func planDetachVolume(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		volume, err := callerVolume(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		if volume.ServerID == 0 {
			return lab.Plan{}, web.Refusal(http.StatusUnprocessableEntity, "service_error", volumeNotAttached)
		}
		if _, err := state.Get(tx, state.Servers, volume.ServerID); err != nil {
			return lab.Plan{}, web.Locked(volumeBusy)
		}
		serverID, slot := volume.ServerID, volume.Slot
		volume.ServerID, volume.Slot = 0, 0
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: serverID}},
			Steps: func(ctx context.Context) error {
				return l.Machine().DetachVolume(ctx, serverID, slot)
			},
			Undo: undoRecord(state.Volumes, volume.ID, func(tx *state.Tx, volume *state.Volume) {
				_, err := state.Get(tx, state.Servers, serverID)
				taken := slices.ContainsFunc(state.All(tx, state.Volumes), func(other *state.Volume) bool {
					return other.ServerID == serverID && other.Slot == slot
				})
				if volume.ServerID == 0 && err == nil && !taken {
					volume.ServerID, volume.Slot = serverID, slot
				}
			}),
			UndoIfLast: true,
		}, nil
	}
}

// volumeResizeRequest is the body of the resize action.
type volumeResizeRequest struct {
	Size *int `json:"size"`
}

// planResizeVolume grows a volume, also an attached one, whose action
// then names the server too (measured 2026-11-09).
func planResizeVolume(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body volumeResizeRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.JSONError(volumeBadBody)
		}
		if body.Size == nil || *body.Size < volumeMinGB || *body.Size > volumeMaxGB {
			check := web.Check{Singular: true}
			check.Fail("size", volumeSizeRange)
			return lab.Plan{}, check.Err()
		}
		volume, err := callerVolume(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		if *body.Size <= volume.SizeGB {
			return lab.Plan{}, web.InvalidInput(volumeTooSmall, web.Field{Name: "size", Messages: []string{volumeTooSmallField}})
		}
		// a second resize while one runs is 423; the volume's other
		// actions wait in its queue (measured 2026-11-09)
		if resizing(tx, volume.ID) {
			return lab.Plan{}, web.Locked(volumeBusy)
		}
		plan := lab.Plan{
			Steps:      func(ctx context.Context) error { return l.Machine().ResizeVolume(ctx, volume.ID) },
			Undo:       undoVolumeSize(volume.ID, volume.SizeGB),
			UndoIfLast: true,
		}
		if volume.ServerID != 0 {
			plan.Resources = []state.ResourceRef{{Type: lab.ServerResource, ID: volume.ServerID}}
		}
		volume.SizeGB = *body.Size
		return plan, nil
	}
}

// resizing tells whether a resize of the volume runs or waits.
func resizing(tx *state.Tx, volumeID int64) bool {
	running := state.IDs(tx, state.Actions, func(action *state.Action) bool {
		return action.Status == state.ActionRunning && action.Command == "resize_volume" &&
			namesResource(action, lab.VolumeResource, volumeID)
	})
	return len(running) > 0
}

// undoVolumeSize returns the undo of a resize: the size it had.
func undoVolumeSize(id int64, sizeGB int) func(tx *state.Tx) error {
	return undoRecord(state.Volumes, id, func(_ *state.Tx, volume *state.Volume) { volume.SizeGB = sizeGB })
}

// volumeProtectionRequest is the body of change_protection.
type volumeProtectionRequest struct {
	Delete *bool `json:"delete"`
}

// planVolumeProtection sets the delete protection at once; a body without
// delete turns it off (measured 2026-11-09).
func planVolumeProtection(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	var body volumeProtectionRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return lab.Plan{}, web.JSONError(volumeBadBody)
	}
	volume, err := callerVolume(tx, req.ProjectID, req.ID)
	if err != nil {
		return lab.Plan{}, err
	}
	volume.Protection.Delete = body.Delete != nil && *body.Delete
	return lab.Plan{}, nil
}

// serverVolumeIDs returns the IDs of the volumes attached to a server.
func serverVolumeIDs(tx *state.Tx, serverID int64) []int64 {
	ids := state.IDs(tx, state.Volumes, func(volume *state.Volume) bool { return volume.ServerID == serverID })
	slices.Sort(ids)
	return ids
}
