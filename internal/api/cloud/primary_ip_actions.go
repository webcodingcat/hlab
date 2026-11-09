package cloud

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// primaryIPActions is the Primary IPs' action table. An assign or unassign
// locks the IP while it runs: a delete meanwhile is 423 (measured
// 2026-11-08), and so is a second assign.
func primaryIPActions(l *lab.Lab) lab.ActionTable {
	return lab.ActionTable{
		ResourceType: primaryIPResource,
		Busy:         primaryIPBusy,
		Actions: map[string]lab.ActionSpec{
			"assign":            {Name: "assign_primary_ip", Plan: planPrimaryIPAssign(l)},
			"unassign":          {Name: "unassign_primary_ip", Plan: planPrimaryIPUnassign(l)},
			"change_protection": {Name: "change_protection", Plan: planPrimaryIPProtection, Unflagged: true},
		},
	}
}

// primaryIPAssignRequest is the body of the assign action.
type primaryIPAssignRequest struct {
	AssigneeID   *int64  `json:"assignee_id"`
	AssigneeType *string `json:"assignee_type"`
}

// planPrimaryIPAssign assigns the IP to a server that is off and has no Primary IP
// of its type; the appliance learns of it in the steps.
func planPrimaryIPAssign(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body primaryIPAssignRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		var check web.Check
		check.Required("assignee_id", body.AssigneeID != nil)
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		ip, err := callerPrimaryIP(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		server, err := state.Get(tx, state.Servers, *body.AssigneeID)
		if err != nil || server.ProjectID != req.ProjectID {
			return lab.Plan{}, web.RefusalNull(http.StatusNotFound, "not_found", assignServerMissing)
		}
		if server.Status != state.ServerOff {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "server_not_stopped", primaryIPServerRunning)
		}
		field := primaryIPField(server, ip.Type)
		if *field != 0 {
			return lab.Plan{}, web.RefusalNull(http.StatusForbidden, "resource_limit_exceeded", primaryIPLimitReached)
		}
		switch {
		case ip.AssigneeID != 0:
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "primary_ip_already_assigned", fmt.Sprintf(assignIPAssigned, ip.ID))
		case ip.Location != server.Location:
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", assignIPLocationMismatch)
		}
		ip.AssigneeID, *field = server.ID, ip.ID
		plan := primaryIPChangePlan(l, server.ID)
		plan.Undo = undoPrimaryIPChange(ip.ID, 0, ip.AutoDelete)
		return plan, nil
	}
}

// planPrimaryIPUnassign takes the IP from its server, which must be off. Hetzner
// clears auto_delete then (measured 2026-11-08).
func planPrimaryIPUnassign(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		ip, err := callerPrimaryIP(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		server, err := state.Get(tx, state.Servers, ip.AssigneeID)
		if err != nil {
			// an IP without a server: nothing changes on the machine, so
			// the action has no steps and ends at once
			return lab.Plan{}, nil
		}
		if server.Status != state.ServerOff {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "server_not_stopped", primaryIPServerRunning)
		}
		if server.IPv4 == ip.ID {
			server.IPv4 = 0
		}
		if server.IPv6 == ip.ID {
			server.IPv6 = 0
		}
		autoDelete := ip.AutoDelete
		ip.AssigneeID, ip.AutoDelete = 0, false
		plan := primaryIPChangePlan(l, server.ID)
		plan.Undo = undoPrimaryIPChange(ip.ID, server.ID, autoDelete)
		return plan, nil
	}
}

// primaryIPChangePlan is the slow part of an assign or unassign: the
// appliance gets the pictures with the server's new addresses. The action
// names and locks the server too.
func primaryIPChangePlan(l *lab.Lab, serverID int64) lab.Plan {
	server := state.ResourceRef{Type: lab.ServerResource, ID: serverID}
	return lab.Plan{
		Resources: []state.ResourceRef{server},
		Locks:     []state.ResourceRef{server},
		Steps:     l.Machine().ChangePrimaryIP,
	}
}

// undoPrimaryIPChange returns the undo of an assign or unassign: the IP
// goes back to the server it had before (0: none), with its auto_delete,
// as far as nothing changed meanwhile. The IP's flag and the server's lock
// keep other actions off both while the steps run, but a project's reset
// may have removed either.
func undoPrimaryIPChange(id, serverID int64, autoDelete bool) func(tx *state.Tx) error {
	return undoRecord(state.PrimaryIPs, id, func(tx *state.Tx, ip *state.PrimaryIP) {
		if ip.AssigneeID != 0 {
			if server, err := state.Get(tx, state.Servers, ip.AssigneeID); err == nil {
				*primaryIPField(server, ip.Type) = 0
			}
		}
		ip.AssigneeID, ip.AutoDelete = 0, autoDelete
		if server, err := state.Get(tx, state.Servers, serverID); err == nil {
			*primaryIPField(server, ip.Type) = ip.ID
			ip.AssigneeID = server.ID
		}
	})
}

// primaryIPField returns the server's field that holds its Primary IP of
// a type.
func primaryIPField(server *state.Server, ipType state.IPType) *int64 {
	if ipType == state.IPv6 {
		return &server.IPv6
	}
	return &server.IPv4
}

// primaryIPProtectionRequest is the body of change_protection.
type primaryIPProtectionRequest struct {
	Delete *bool `json:"delete"`
}

// planPrimaryIPProtection sets the delete protection at once.
func planPrimaryIPProtection(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	var body primaryIPProtectionRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return lab.Plan{}, web.ParseFailed()
	}
	ip, err := callerPrimaryIP(tx, req.ProjectID, req.ID)
	if err != nil {
		return lab.Plan{}, err
	}
	if body.Delete != nil {
		ip.Protection.Delete = *body.Delete
	}
	return lab.Plan{}, nil
}
