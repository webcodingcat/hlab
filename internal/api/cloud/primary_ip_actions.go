package cloud

import (
	"context"
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
			"assign":            {Name: "assign_primary_ip", Plan: planAssign(l)},
			"unassign":          {Name: "unassign_primary_ip", Plan: planUnassign(l)},
			"change_protection": {Name: "change_protection", Plan: planPrimaryIPProtection, Unflagged: true},
		},
	}
}

// primaryIPAssignRequest is the body of the assign action.
type primaryIPAssignRequest struct {
	AssigneeID   *int64  `json:"assignee_id"`
	AssigneeType *string `json:"assignee_type"`
}

// planAssign assigns the IP to a server that is off and has no Primary IP
// of its type; the appliance learns of it in the steps.
func planAssign(l *lab.Lab) func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
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
		field := &server.IPv4
		if ip.Type == state.IPv6 {
			field = &server.IPv6
		}
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
		return primaryIPChangePlan(l, server.ID), nil
	}
}

// planUnassign takes the IP from its server, which must be off. Hetzner
// clears auto_delete then (measured 2026-11-08).
func planUnassign(l *lab.Lab) func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		ip, err := callerPrimaryIP(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		server, err := state.Get(tx, state.Servers, ip.AssigneeID)
		if err != nil {
			// an IP without a server: nothing changes on the machine, so
			// the action only waits Hetzner's time
			return lab.Plan{Steps: func(ctx context.Context) error { return l.Sleep(ctx, lab.PrimaryIPChange) }}, nil
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
		ip.AssigneeID, ip.AutoDelete = 0, false
		return primaryIPChangePlan(l, server.ID), nil
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
