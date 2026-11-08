package cloud

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's texts about Primary IPs (measured 2026-11-08). Their errors
// carry the details null.
const (
	primaryIPResource      = "primary_ip"
	primaryIPNameTaken     = "name is already used"
	primaryIPPlaceMissing  = "either assignee_id or location must be provided"
	primaryIPTypeInvalid   = "value required to be smaller" // Hetzner's text for an unknown type
	primaryIPMustUnassign  = "Primary IP must be unassigned"
	primaryIPProtected     = "Primary IP deletion is protected"
	primaryIPBusy          = "cannot perform operation because primary_ip is locked"
	primaryIPServerRunning = "Server must be offline for this action"
	primaryIPLimitReached  = "assignment limit reached"
	primaryIPNameMax       = 255
	// a new server's Primary IP that another server holds, of another
	// location or of the other type; these carry the details {}
	serverIPAssigned         = "primary ip already assigned to another server"
	serverIPLocationMismatch = "primary ip and server located in different locations"
	serverIPTypeMismatch     = "primary ip has a wrong ip version"
	// the same on assign: other codes and texts (the location's is
	// Hetzner's service_error)
	assignIPAssigned         = "Primary IP with ID %d is already assigned to a Resource"
	assignIPLocationMismatch = "Resource and Primary IP are in different locations"
	assignServerMissing      = "resource not found"
)

// primaryIPCreateRequest is the body of POST /primary_ips. It is HLab's own
// type, to tell a missing field from an empty one.
type primaryIPCreateRequest struct {
	Name         *string            `json:"name"`
	Type         *string            `json:"type"`
	AssigneeType *string            `json:"assignee_type"`
	AssigneeID   *int64             `json:"assignee_id"`
	Location     *string            `json:"location"`
	AutoDelete   *bool              `json:"auto_delete"`
	Labels       *map[string]string `json:"labels"`
}

// primaryIPUpdateRequest is the body of PUT /primary_ips/{id}; a missing
// field stays as it is.
type primaryIPUpdateRequest struct {
	Name       *string            `json:"name"`
	Labels     *map[string]string `json:"labels"`
	AutoDelete *bool              `json:"auto_delete"`
}

// primaryIPSortKeys are the sort values of GET /primary_ips.
var primaryIPSortKeys = web.SortKeys[schema.PrimaryIP]{
	"id":      func(a, b schema.PrimaryIP) int { return cmp.Compare(a.ID, b.ID) },
	"created": func(a, b schema.PrimaryIP) int { return a.Created.Compare(b.Created) },
}

// registerPrimaryIPs adds the routes of the Primary IPs and their actions.
func registerPrimaryIPs(api *web.API, l *lab.Lab) {
	table := primaryIPActions(l)
	api.Handle("GET /primary_ips", func(req *web.Request) (int, any, error) {
		query := req.URL.Query()
		selector, err := web.ParseSelector(query.Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		var ips []schema.PrimaryIP
		_ = l.View(func(tx *state.Tx) error {
			for _, ip := range state.All(tx, state.PrimaryIPs) {
				if ip.ProjectID == req.Caller.ProjectID && selector.Matches(ip.Labels) &&
					(!query.Has("ip") || ip.IP == query.Get("ip")) {
					ips = append(ips, primaryIPJSON(l.Catalog(), ip))
				}
			}
			return nil
		})
		ips = byName(req, ips, func(ip schema.PrimaryIP) string { return ip.Name })
		if err := web.Sort(req, ips, primaryIPSortKeys); err != nil {
			return 0, nil, err
		}
		return listJSON(req, "primary_ips", ips)
	})

	api.Handle("POST /primary_ips", func(req *web.Request) (int, any, error) {
		return createPrimaryIP(req, l, table)
	})

	api.Handle("GET /primary_ips/{id}", func(req *web.Request) (int, any, error) {
		var out schema.PrimaryIP
		err := l.View(func(tx *state.Tx) error {
			ip, err := callerPrimaryIP(tx, req.Caller.ProjectID, web.PathID(req, "id"))
			if err == nil {
				out = primaryIPJSON(l.Catalog(), ip)
			}
			return err
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.PrimaryIPGetResponse{PrimaryIP: out}, nil
	})

	api.Handle("PUT /primary_ips/{id}", func(req *web.Request) (int, any, error) {
		var body primaryIPUpdateRequest
		if err := web.DecodeJSON(req.Request, &body); err != nil {
			return 0, nil, err
		}
		check := web.Check{Update: true}
		if body.Name != nil {
			check.Length("name", *body.Name, 1, primaryIPNameMax)
		}
		if body.Labels != nil {
			check.Labels(*body.Labels)
		}
		if err := check.Err(); err != nil {
			return 0, nil, err
		}
		var out schema.PrimaryIP
		err := l.Change(func(tx *state.Tx) error {
			ip, err := callerPrimaryIP(tx, req.Caller.ProjectID, web.PathID(req, "id"))
			if err != nil {
				return err
			}
			if body.Name != nil && *body.Name != ip.Name {
				if primaryIPNameUsed(tx, ip.ProjectID, *body.Name) {
					return web.RefusalNull(http.StatusConflict, "uniqueness_error", primaryIPNameTaken)
				}
				ip.Name = *body.Name
			}
			if body.Labels != nil {
				ip.Labels = *body.Labels
			}
			// Hetzner takes auto_delete also on a protected IP (measured
			// 2026-11-08)
			if body.AutoDelete != nil {
				ip.AutoDelete = *body.AutoDelete
			}
			out = primaryIPJSON(l.Catalog(), ip)
			return nil
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.PrimaryIPUpdateResponse{PrimaryIP: out}, nil
	})

	api.Handle("DELETE /primary_ips/{id}", func(req *web.Request) (int, any, error) {
		id := web.PathID(req, "id")
		ref := state.ResourceRef{Type: primaryIPResource, ID: id}
		err := l.ChangeIdle(ref, primaryIPBusy, func(tx *state.Tx) error {
			ip, err := callerPrimaryIP(tx, req.Caller.ProjectID, id)
			switch {
			case err != nil:
				return err
			case ip.Protection.Delete:
				return web.RefusalNull(http.StatusLocked, "protected", primaryIPProtected)
			case ip.AssigneeID != 0:
				return web.RefusalNull(http.StatusUnprocessableEntity, "must_be_unassigned", primaryIPMustUnassign)
			}
			state.Delete(tx, state.PrimaryIPs, id)
			return nil
		})
		var locked *lab.LockedError
		if errors.As(err, &locked) {
			return 0, nil, web.Locked(locked.Message)
		}
		return http.StatusNoContent, nil, err
	})

	for _, command := range []string{"assign", "unassign", "change_protection"} {
		api.Handle("POST /primary_ips/{id}/actions/"+command, func(req *web.Request) (int, any, error) {
			action, err := act(l, table, command, req)
			if err != nil {
				return 0, nil, err
			}
			return http.StatusCreated, map[string]schema.Action{"action": actionJSON(&action)}, nil
		})
	}
	registerResourceActions(api, l, "primary_ips", primaryIPResource)
}

// createPrimaryIP creates a Primary IP in a location, or assigned to a
// server that is off; Hetzner answers an action only for the second.
func createPrimaryIP(req *web.Request, l *lab.Lab, table lab.ActionTable) (int, any, error) {
	var body primaryIPCreateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	check.Required("name", body.Name != nil)
	if body.Name != nil {
		check.Length("name", *body.Name, 1, primaryIPNameMax)
	}
	check.Required("type", body.Type != nil)
	if body.Type != nil && *body.Type != string(state.IPv4) && *body.Type != string(state.IPv6) {
		check.Fail("type", primaryIPTypeInvalid)
	}
	if (body.AssigneeID == nil) == (body.Location == nil) {
		check.Fail("assignee_id", primaryIPPlaceMissing)
		check.Fail("location", primaryIPPlaceMissing)
	}
	if body.Labels != nil {
		check.Labels(*body.Labels)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	ip := &state.PrimaryIP{
		ProjectID: req.Caller.ProjectID,
		Name:      *body.Name,
		Labels:    map[string]string{},
		Created:   l.Now(),
		Type:      state.IPType(*body.Type),
	}
	if body.Labels != nil {
		ip.Labels = *body.Labels
	}
	if body.AutoDelete != nil {
		ip.AutoDelete = *body.AutoDelete
	}
	if body.Location != nil {
		location, found := l.Catalog().LocationByName(*body.Location)
		if !found {
			return 0, nil, web.NotFoundNull("location not found")
		}
		ip.Location = location.Name
	}
	request := lab.Request{ProjectID: ip.ProjectID}
	var commands []string
	if body.AssigneeID != nil {
		request.Body, _ = json.Marshal(map[string]any{"assignee_id": *body.AssigneeID, "assignee_type": "server"})
		commands = append(commands, "assign")
	}
	var out schema.PrimaryIP
	actions, err := l.Create(table, request, func(tx *state.Tx) (int64, error) {
		if primaryIPNameUsed(tx, ip.ProjectID, ip.Name) {
			return 0, web.RefusalNull(http.StatusConflict, "uniqueness_error", primaryIPNameTaken)
		}
		if body.AssigneeID != nil {
			server, err := callerServer(tx, ip.ProjectID, *body.AssigneeID)
			if err != nil {
				return 0, err
			}
			ip.Location = server.Location // the assign below checks the rest
		}
		address, err := lab.NewAddress(tx, ip.Type)
		if err != nil {
			return 0, web.RefusalNull(http.StatusPreconditionFailed, "resource_unavailable", err.Error())
		}
		ip.ID, ip.IP = state.NewID(tx, state.PrimaryIPs), address
		state.Put(tx, state.PrimaryIPs, ip)
		out = primaryIPJSON(l.Catalog(), ip)
		return ip.ID, nil
	}, commands...)
	if err != nil {
		return 0, nil, actError(err)
	}
	answer := schema.PrimaryIPCreateResponse{PrimaryIP: out}
	if len(actions) > 0 {
		answer.Action = new(actionJSON(&actions[0]))
		_ = l.View(func(tx *state.Tx) error {
			if current, err := state.Get(tx, state.PrimaryIPs, ip.ID); err == nil {
				answer.PrimaryIP = primaryIPJSON(l.Catalog(), current)
			}
			return nil
		})
	}
	return http.StatusCreated, answer, nil
}

// assignNewServerIPs gives a new server its Primary IPs: those the request
// names, or new ones with auto_delete unless the request disables them.
func assignNewServerIPs(tx *state.Tx, server *state.Server, publicNet *serverPublicNetRequest) error {
	if publicNet == nil {
		publicNet = &serverPublicNetRequest{}
	}
	for _, family := range []struct {
		ipType  state.IPType
		id      *int64
		enabled *bool
		field   *int64
	}{
		{state.IPv4, publicNet.IPv4, publicNet.EnableIPv4, &server.IPv4},
		{state.IPv6, publicNet.IPv6, publicNet.EnableIPv6, &server.IPv6},
	} {
		switch {
		case family.id != nil:
			ip, err := state.Get(tx, state.PrimaryIPs, *family.id)
			if err != nil || ip.ProjectID != server.ProjectID {
				return web.Refusal(http.StatusNotFound, "primary_ip_not_found", "primary ip not found")
			}
			// the type first: Hetzner refused another server's IPv6 given
			// as ipv4 for its type (measured 2026-11-08)
			switch {
			case ip.Type != family.ipType:
				return web.Refusal(http.StatusPreconditionFailed, "primary_ip_version_mismatch", serverIPTypeMismatch)
			case ip.AssigneeID != 0:
				return web.Refusal(http.StatusUnprocessableEntity, "primary_ip_assigned", serverIPAssigned)
			case ip.Location != server.Location:
				return web.Refusal(http.StatusUnprocessableEntity, "primary_ip_location_mismatch", serverIPLocationMismatch)
			}
			ip.AssigneeID, *family.field = server.ID, ip.ID
		case family.enabled == nil || *family.enabled:
			address, err := lab.NewAddress(tx, family.ipType)
			if err != nil {
				return web.Refusal(http.StatusPreconditionFailed, "resource_unavailable", err.Error())
			}
			ip := &state.PrimaryIP{
				ID:         state.NewID(tx, state.PrimaryIPs),
				ProjectID:  server.ProjectID,
				Labels:     map[string]string{},
				Created:    server.Created,
				Type:       family.ipType,
				IP:         address,
				Location:   server.Location,
				AssigneeID: server.ID,
				AutoDelete: true,
			}
			// Hetzner's name of a server's own Primary IP (measured 2026-11-08)
			ip.Name = fmt.Sprintf("primary_ip-%d", ip.ID)
			state.Put(tx, state.PrimaryIPs, ip)
			*family.field = ip.ID
		}
	}
	return nil
}

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
		Steps: func(ctx context.Context) error {
			if err := l.UpdatePictures(ctx); err != nil {
				return err
			}
			return l.Sleep(ctx, lab.PrimaryIPChange)
		},
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

// primaryIPNameUsed tells whether a Primary IP of the project has the name.
func primaryIPNameUsed(tx *state.Tx, projectID int64, name string) bool {
	for _, ip := range state.All(tx, state.PrimaryIPs) {
		if ip.ProjectID == projectID && ip.Name == name {
			return true
		}
	}
	return false
}

// callerPrimaryIP returns the Primary IP of an ID if it belongs to the
// project, with Hetzner's 404 otherwise.
func callerPrimaryIP(tx *state.Tx, projectID, id int64) (*state.PrimaryIP, error) {
	ip, err := state.Get(tx, state.PrimaryIPs, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && ip.ProjectID != projectID) {
		return nil, web.NotFoundNull(fmt.Sprintf("Primary IP with ID %d not found", id))
	}
	return ip, err
}

// primaryIPJSON returns a Primary IP as the API shows it. An unassigned IP
// has the assignee_type "unassigned" (measured 2026-11-08).
func primaryIPJSON(cat *catalog.Catalog, ip *state.PrimaryIP) schema.PrimaryIP {
	out := schema.PrimaryIP{
		ID:           ip.ID,
		IP:           ip.IP,
		Labels:       ip.Labels,
		Name:         ip.Name,
		Type:         string(ip.Type),
		Protection:   schema.PrimaryIPProtection{Delete: ip.Protection.Delete},
		DNSPtr:       []schema.PrimaryIPDNSPTR{},
		AssigneeType: "unassigned",
		AutoDelete:   ip.AutoDelete,
		Created:      ip.Created,
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	if ip.AssigneeID != 0 {
		out.AssigneeID, out.AssigneeType = new(ip.AssigneeID), "server"
	}
	if ip.Type == state.IPv4 {
		out.DNSPtr = append(out.DNSPtr, schema.PrimaryIPDNSPTR{IP: ip.IP, DNSPtr: dnsPtr(ip)})
	}
	if location, found := cat.LocationByName(ip.Location); found {
		out.Location = locationJSON(location)
	}
	return out
}

// dnsPtr returns the reverse DNS of an IPv4, as Hetzner sets it until it is
// changed: static.<the address backwards>.clients.your-server.de (measured
// 2026-11-08). An IPv6 has none.
func dnsPtr(ip *state.PrimaryIP) string {
	address, err := netip.ParseAddr(ip.IP)
	if err != nil || !address.Is4() {
		return ""
	}
	octets := strings.Split(address.String(), ".")
	slices.Reverse(octets)
	return "static." + strings.Join(octets, ".") + ".clients.your-server.de"
}
