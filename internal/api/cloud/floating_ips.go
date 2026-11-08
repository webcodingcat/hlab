package cloud

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's texts about Floating IPs (measured 2026-11-08). Their errors
// carry the details null.
const (
	floatingIPResource       = "floating_ip"
	floatingIPNameTaken      = "Floating IP name is already used"
	floatingIPPlaceMissing   = "either server or home_location must be provided"
	floatingIPTypeMissing    = "'type' is required"
	floatingIPInvalid        = "invalid" // an unknown type or home_location
	floatingIPNameTooLong    = "value required to be smaller"
	floatingIPServerMissing  = "value required to be larger" // an assign without a server
	floatingIPNotFound       = "Floating IP with ID %d not found"
	floatingIPProtected      = "Floating IP deletion is protected"
	floatingIPMustUnassign   = "IP must be unassigned"
	floatingIPNotAssigned    = "Floating IP with ID %d is not assigned to any resource"
	floatingIPBusy           = "cannot perform operation because floating_ip is locked"
	floatingIPServerCreating = "resource is locked"
	floatingIPAssigning      = "cannot assign Floating IP because it is already being assigned"
	floatingIPOtherZone      = "assigning Floating IP to a Server in another network_zone is not supported"
	// a PUT's labels; the value's text is not measured, it follows the key's
	floatingIPLabelKey   = "invalid label value: key contains invalid characters or is malformed"
	floatingIPLabelValue = "invalid label value: value contains invalid characters or is malformed"
	floatingIPNameMax    = 255
)

// floatingIPCreateRequest is the body of POST /floating_ips. It is HLab's
// own type, to tell a missing field from an empty one.
type floatingIPCreateRequest struct {
	Type         *string            `json:"type"`
	HomeLocation *string            `json:"home_location"`
	Server       *int64             `json:"server"`
	Name         *string            `json:"name"`
	Description  *string            `json:"description"`
	Labels       *map[string]string `json:"labels"`
}

// floatingIPUpdateRequest is the body of PUT /floating_ips/{id}; a missing
// field stays as it is.
type floatingIPUpdateRequest struct {
	Name        *string            `json:"name"`
	Description *string            `json:"description"`
	Labels      *map[string]string `json:"labels"`
}

// floatingIPSortKeys are the sort values of GET /floating_ips. Hetzner
// ignores an unknown one, and the filter ip too (measured 2026-11-08).
var floatingIPSortKeys = web.SortKeys[schema.FloatingIP]{
	"id":      func(a, b schema.FloatingIP) int { return cmp.Compare(a.ID, b.ID) },
	"created": func(a, b schema.FloatingIP) int { return a.Created.Compare(b.Created) },
}

// registerFloatingIPs adds the routes of the Floating IPs and their actions.
func registerFloatingIPs(api *web.API, l *lab.Lab) {
	table := floatingIPActions(l)
	api.Handle("GET /floating_ips", func(req *web.Request) (int, any, error) {
		selector, err := web.ParseSelector(req.URL.Query().Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		var ips []schema.FloatingIP
		_ = l.View(func(tx *state.Tx) error {
			for _, ip := range state.All(tx, state.FloatingIPs) {
				if ip.ProjectID == req.Caller.ProjectID && selector.Matches(ip.Labels) {
					ips = append(ips, floatingIPJSON(l.Catalog(), ip))
				}
			}
			return nil
		})
		ips = byName(req, ips, func(ip schema.FloatingIP) string { return ip.Name })
		if err := web.Sort(req, ips, floatingIPSortKeys); err != nil {
			slices.SortFunc(ips, floatingIPSortKeys["id"]) // an unknown sort value
		}
		return listJSON(req, "floating_ips", ips)
	})

	api.Handle("POST /floating_ips", func(req *web.Request) (int, any, error) {
		return createFloatingIP(req, l, table)
	})

	api.Handle("GET /floating_ips/{id}", func(req *web.Request) (int, any, error) {
		var out schema.FloatingIP
		err := l.View(func(tx *state.Tx) error {
			ip, err := callerFloatingIP(tx, req.Caller.ProjectID, web.PathID(req, "id"))
			if err == nil {
				out = floatingIPJSON(l.Catalog(), ip)
			}
			return err
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.FloatingIPGetResponse{FloatingIP: out}, nil
	})

	api.Handle("PUT /floating_ips/{id}", func(req *web.Request) (int, any, error) {
		return updateFloatingIP(req, l)
	})

	api.Handle("DELETE /floating_ips/{id}", func(req *web.Request) (int, any, error) {
		id := web.PathID(req, "id")
		ref := state.ResourceRef{Type: floatingIPResource, ID: id}
		err := l.ChangeIdle(ref, floatingIPBusy, func(tx *state.Tx) error {
			ip, err := callerFloatingIP(tx, req.Caller.ProjectID, id)
			switch {
			case err != nil:
				return err
			case ip.Protection.Delete:
				return web.RefusalNull(http.StatusLocked, "protected", floatingIPProtected)
			case ip.ServerID != 0:
				return web.RefusalNull(http.StatusUnprocessableEntity, "must_be_unassigned", floatingIPMustUnassign)
			}
			state.Delete(tx, state.FloatingIPs, id)
			return nil
		})
		var locked *lab.LockedError
		if errors.As(err, &locked) {
			return 0, nil, web.Locked(locked.Message)
		}
		if err != nil {
			return 0, nil, err
		}
		return http.StatusNoContent, nil, nil
	})

	for _, command := range []string{"assign", "unassign", "change_protection"} {
		api.Handle("POST /floating_ips/{id}/actions/"+command, func(req *web.Request) (int, any, error) {
			action, err := act(l, table, command, req)
			if err != nil {
				return 0, nil, err
			}
			return http.StatusCreated, map[string]schema.Action{"action": actionJSON(&action)}, nil
		})
	}
	registerResourceActions(api, l, "floating_ips", floatingIPResource)
}

// createFloatingIP creates a Floating IP in its home location, or assigned
// to a server, whose location is then its home; Hetzner answers an action
// only for the second. Hetzner checks the type alone first, then the other
// fields; it takes labels with keys its rules refuse elsewhere, and an
// empty name as none (measured 2026-11-08).
func createFloatingIP(req *web.Request, l *lab.Lab, table lab.ActionTable) (int, any, error) {
	var body floatingIPCreateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	if body.Type == nil {
		check.Fail("type", floatingIPTypeMissing)
		return 0, nil, check.Err()
	}
	if *body.Type != string(state.IPv4) && *body.Type != string(state.IPv6) {
		check.Fail("type", floatingIPInvalid)
	}
	if body.HomeLocation == nil && body.Server == nil {
		check.Fail("server", floatingIPPlaceMissing)
		check.Fail("home_location", floatingIPPlaceMissing)
	}
	var location catalog.Location
	if body.HomeLocation != nil {
		// by name only: Hetzner refuses a location's ID here
		var found bool
		if location, found = l.Catalog().LocationByName(*body.HomeLocation); !found {
			check.Fail("home_location", floatingIPInvalid)
		}
	}
	if body.Name != nil && len([]rune(*body.Name)) > floatingIPNameMax {
		check.Fail("name", floatingIPNameTooLong)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	ip := &state.FloatingIP{
		ProjectID: req.Caller.ProjectID,
		Labels:    map[string]string{},
		Created:   l.Now(),
		Type:      state.IPType(*body.Type),
	}
	if body.Name != nil {
		ip.Name = *body.Name
	}
	if body.Description != nil {
		ip.Description = *body.Description
	}
	if body.Labels != nil {
		ip.Labels = *body.Labels
	}
	ip.HomeLocation = location.Name
	request := lab.Request{ProjectID: ip.ProjectID}
	var commands []string
	if body.Server != nil {
		request.Body, _ = json.Marshal(map[string]any{"server": *body.Server})
		commands = append(commands, "assign")
	}
	var out schema.FloatingIP
	actions, err := l.Create(table, request, func(tx *state.Tx) (int64, error) {
		if body.Server != nil {
			server, err := floatingIPServer(tx, ip.ProjectID, *body.Server)
			if err != nil {
				return 0, err
			}
			if ip.HomeLocation == "" {
				ip.HomeLocation = server.Location
			}
		}
		ip.ID = state.NewID(tx, state.FloatingIPs)
		if ip.Name == "" {
			ip.Name = fmt.Sprintf("floating_ip-%d", ip.ID) // Hetzner's name of an IP created without one
		}
		if _, taken := state.ByName(tx, state.FloatingIPs, ip.ProjectID, ip.Name); taken {
			return 0, web.RefusalNull(http.StatusConflict, "uniqueness_error", floatingIPNameTaken)
		}
		address, err := lab.NewAddress(tx, ip.Type)
		if err != nil {
			return 0, web.Refusal(http.StatusPreconditionFailed, "resource_unavailable", err.Error())
		}
		ip.IP = address
		state.Put(tx, state.FloatingIPs, ip)
		out = floatingIPJSON(l.Catalog(), ip)
		return ip.ID, nil
	}, commands...)
	if err != nil {
		return 0, nil, actError(err)
	}
	answer := schema.FloatingIPCreateResponse{FloatingIP: out}
	if len(actions) > 0 {
		answer.Action = new(actionJSON(&actions[0]))
		_ = l.View(func(tx *state.Tx) error {
			if current, err := state.Get(tx, state.FloatingIPs, ip.ID); err == nil {
				answer.FloatingIP = floatingIPJSON(l.Catalog(), current)
			}
			return nil
		})
	}
	return http.StatusCreated, answer, nil
}

// updateFloatingIP changes a Floating IP's name, description and labels.
// Hetzner keeps the name for an empty one and the description for null,
// and checks the labels, unlike a create (measured 2026-11-08).
func updateFloatingIP(req *web.Request, l *lab.Lab) (int, any, error) {
	var body floatingIPUpdateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Update: true}
	if body.Name != nil && len([]rune(*body.Name)) > floatingIPNameMax {
		check.Fail("name", floatingIPNameTooLong)
	}
	if body.Labels != nil {
		check.LabelsWith(*body.Labels, floatingIPLabelKey, floatingIPLabelValue)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	var out schema.FloatingIP
	err := l.Change(func(tx *state.Tx) error {
		ip, err := callerFloatingIP(tx, req.Caller.ProjectID, web.PathID(req, "id"))
		if err != nil {
			return err
		}
		if body.Name != nil && *body.Name != "" && *body.Name != ip.Name {
			if _, taken := state.ByName(tx, state.FloatingIPs, ip.ProjectID, *body.Name); taken {
				return web.RefusalNull(http.StatusConflict, "uniqueness_error", floatingIPNameTaken)
			}
			ip.Name = *body.Name
		}
		if body.Description != nil {
			ip.Description = *body.Description
		}
		if body.Labels != nil {
			ip.Labels = *body.Labels
		}
		out = floatingIPJSON(l.Catalog(), ip)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, schema.FloatingIPUpdateResponse{FloatingIP: out}, nil
}

// callerFloatingIP returns the Floating IP of an ID if it belongs to the
// project, with Hetzner's 404 otherwise.
func callerFloatingIP(tx *state.Tx, projectID, id int64) (*state.FloatingIP, error) {
	ip, err := state.Get(tx, state.FloatingIPs, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && ip.ProjectID != projectID) {
		return nil, web.NotFoundNull(fmt.Sprintf(floatingIPNotFound, id))
	}
	return ip, err
}

// floatingIPServer returns the server a create or an assign names, with
// Hetzner's 404 if the project lacks it: the details null, unlike the
// servers' own 404 (measured 2026-11-08).
func floatingIPServer(tx *state.Tx, projectID, id int64) (*state.Server, error) {
	server, err := state.Get(tx, state.Servers, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && server.ProjectID != projectID) {
		return nil, web.NotFoundNull("server not found")
	}
	return server, err
}

// floatingIPJSON returns a Floating IP as the API shows it. Without a
// change_dns_ptr (phase 2b), an IPv4 has Hetzner's own reverse DNS and an
// IPv6 none.
func floatingIPJSON(cat *catalog.Catalog, ip *state.FloatingIP) schema.FloatingIP {
	out := schema.FloatingIP{
		ID:         ip.ID,
		Name:       ip.Name,
		IP:         ip.IP,
		Type:       string(ip.Type),
		Labels:     ip.Labels,
		Protection: schema.FloatingIPProtection{Delete: ip.Protection.Delete},
		DNSPtr:     []schema.FloatingIPDNSPtr{},
		Created:    ip.Created,
	}
	if ip.Description != "" {
		out.Description = new(ip.Description)
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	if ip.ServerID != 0 {
		out.Server = new(ip.ServerID)
	}
	if ip.Type == state.IPv4 {
		out.DNSPtr = append(out.DNSPtr, schema.FloatingIPDNSPtr{IP: ip.IP, DNSPtr: reverseDNS(ip.IP)})
	}
	if location, found := cat.LocationByName(ip.HomeLocation); found {
		out.HomeLocation = locationJSON(location)
	}
	return out
}
