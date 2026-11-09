package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// floatingIPActions is the Floating IPs' action table. An assign flags the
// IP while it runs: another assign meanwhile is 423, and so is a delete.
// An unassign takes no flag: Hetzner runs it beside an assign, and it
// neither locks the server (measured 2026-11-08).
func floatingIPActions(l *lab.Lab) lab.ActionTable {
	return lab.ActionTable{
		ResourceType: floatingIPResource,
		Busy:         floatingIPAssigning,
		Actions: map[string]lab.ActionSpec{
			"assign":            {Name: "assign_floating_ip", Plan: planFloatingIPAssign(l)},
			"unassign_for_move": {Name: "unassign_floating_ip", Plan: planFloatingIPUnassignForMove(l), Unflagged: true},
			"unassign":          {Name: "unassign_floating_ip", Plan: planFloatingIPUnassign(l), Unflagged: true},
			"change_protection": {Name: "change_protection", Plan: planFloatingIPProtection, Unflagged: true},
		},
	}
}

// floatingIPAssignRequest is the body of the assign action.
type floatingIPAssignRequest struct {
	Server *int64 `json:"server"`
}

// planFloatingIPAssign assigns the IP to a server of the project, also one
// that runs or lives in another location of the IP's network zone; an
// assigned IP moves. The appliance moves its route in the steps. An assign
// to the server the IP has already ends at once (measured 2026-11-08).
func planFloatingIPAssign(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body floatingIPAssignRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.JSONDocumentRequired()
		}
		// Hetzner reads a missing server as 0 (measured 2026-11-08)
		if body.Server == nil || *body.Server <= 0 {
			check := web.Check{Singular: true}
			check.Fail("server", floatingIPServerMissing)
			return lab.Plan{}, check.Err()
		}
		ip, err := callerFloatingIP(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		server, err := floatingIPServer(tx, req.ProjectID, *body.Server)
		if err != nil {
			return lab.Plan{}, err
		}
		if server.Status == state.ServerInitializing {
			// the server's create runs (measured 2026-11-08)
			return lab.Plan{}, web.Locked(floatingIPServerCreating)
		}
		if zone(l.Catalog(), server.Location) != zone(l.Catalog(), ip.HomeLocation) {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", floatingIPOtherZone)
		}
		plan := lab.Plan{Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: server.ID}}}
		if ip.ServerID == server.ID {
			return plan, nil
		}
		plan.Steps = l.Machine().ChangeFloatingIP
		plan.Undo = undoFloatingIPChange(ip.ID, ip.ServerID)
		ip.ServerID = server.ID
		return plan, nil
	}
}

// assignFloatingIP assigns a Floating IP to a server. A move from another
// server records an unassign_floating_ip of the old server first, which
// runs as long as the assign and is not in the answer (measured
// 2026-11-09). Whether it moves is read before the change: an IP that
// another request unassigns meanwhile still gets the unassign, which does
// no harm.
func assignFloatingIP(req *web.Request, l *lab.Lab, table lab.ActionTable) (int, any, error) {
	body, err := readBody(req)
	if err != nil {
		return 0, nil, err
	}
	var target floatingIPAssignRequest
	_ = json.Unmarshal(body, &target) // the assign's plan refuses a bad body
	id := web.PathID(req, "id")
	rows := []lab.Row{{Command: "assign"}}
	_ = l.View(func(tx *state.Tx) error {
		ip, err := state.Get(tx, state.FloatingIPs, id)
		if err == nil && ip.ProjectID == req.Caller.ProjectID && ip.ServerID != 0 &&
			target.Server != nil && *target.Server != ip.ServerID {
			rows = []lab.Row{{Command: "unassign_for_move"}, {Command: "assign"}}
		}
		return nil
	})
	actions, err := l.ActAll(table, lab.Request{ProjectID: req.Caller.ProjectID, ID: id, Body: body}, rows...)
	if err != nil {
		return 0, nil, actError(err)
	}
	return http.StatusCreated, map[string]schema.Action{"action": actionJSON(&actions[len(actions)-1])}, nil
}

// planFloatingIPUnassignForMove is the unassign_floating_ip of the server
// a moving IP leaves; the assign after it makes the change. Its steps
// change nothing on the machine and only wait as long as the assign's, so
// they call l.Sleep themselves.
func planFloatingIPUnassignForMove(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		ip, err := callerFloatingIP(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: ip.ServerID}},
			Steps:     func(ctx context.Context) error { return l.Sleep(ctx, lab.FloatingIPChange) },
		}, nil
	}
}

// planFloatingIPUnassign takes the IP from its server; the appliance
// removes its route in the steps.
func planFloatingIPUnassign(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		ip, err := callerFloatingIP(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		if ip.ServerID == 0 {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", fmt.Sprintf(floatingIPNotAssigned, ip.ID))
		}
		before := ip.ServerID
		ip.ServerID = 0
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: before}},
			Steps:     l.Machine().ChangeFloatingIP,
			Undo:      undoFloatingIPChange(ip.ID, before),
		}, nil
	}
}

// zone returns the network zone of a location, "" for one the catalog
// lacks.
func zone(cat *catalog.Catalog, name string) string {
	location, _ := cat.LocationByName(name)
	return location.NetworkZone
}

// undoFloatingIPChange returns the undo of an assign or unassign: the IP
// goes back to the server it had before (0: none), if that is still
// there.
func undoFloatingIPChange(id, serverID int64) func(tx *state.Tx) error {
	return func(tx *state.Tx) error {
		ip, err := state.Get(tx, state.FloatingIPs, id)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		ip.ServerID = 0
		if _, err := state.Get(tx, state.Servers, serverID); err == nil {
			ip.ServerID = serverID
		}
		return nil
	}
}

// floatingIPProtectionRequest is the body of change_protection.
type floatingIPProtectionRequest struct {
	Delete *bool `json:"delete"`
}

// planFloatingIPProtection sets the delete protection at once.
func planFloatingIPProtection(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	var body floatingIPProtectionRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return lab.Plan{}, web.ParseFailed()
	}
	ip, err := callerFloatingIP(tx, req.ProjectID, req.ID)
	if err != nil {
		return lab.Plan{}, err
	}
	if body.Delete != nil {
		ip.Protection.Delete = *body.Delete
	}
	return lab.Plan{}, nil
}

// unassignFloatingIPs takes a server's Floating IPs off it, when the
// server goes: they stay, unassigned.
func unassignFloatingIPs(tx *state.Tx, serverID int64) {
	for _, id := range state.IDs(tx, state.FloatingIPs, func(ip *state.FloatingIP) bool { return ip.ServerID == serverID }) {
		if ip, err := state.Get(tx, state.FloatingIPs, id); err == nil {
			ip.ServerID = 0
		}
	}
}
