package cloud

import (
	"cmp"
	"errors"
	"net/http"
	"slices"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's texts about placement groups (measured 2026-11-09).
const (
	placementGroupNotFound    = "placement group not found"
	placementGroupTaken       = "placement_group not unique"
	placementGroupTypeInvalid = "Not a valid choice"
	placementGroupLimit       = "placement_group limit exceeded"
	placementGroupLimitName   = "placement_group_limit"
	placementGroupNoActions   = "404 Not Found"
	placementGroupSpread      = "spread"
	placementGroupsPerProject = 50
	placementGroupNameMax     = 128
	// Hetzner's documentation allows 10 servers in a spread group; its
	// answer to an 11th is assumed, not measured
	placementGroupServersMax  = 10
	placementGroupFull        = "placement group is full"
	placementGroupMissingBody = "Missing data for required field."
	serverInPlacementGroup    = "server with ID %d is already part of a placement group"
	serverNotStopped          = "server must be offline for adding it to a placement group"
)

// placementGroupRequest is the body of POST and PUT /placement_groups;
// HLab's own type, to tell a missing field from an empty one. A PUT
// ignores type (measured 2026-11-09).
type placementGroupRequest struct {
	Name   *string            `json:"name"`
	Type   *string            `json:"type"`
	Labels *map[string]string `json:"labels"`
}

// placementGroupSortKeys are the sort values of GET /placement_groups.
var placementGroupSortKeys = web.SortKeys[schema.PlacementGroup]{
	"id":      func(a, b schema.PlacementGroup) int { return cmp.Compare(a.ID, b.ID) },
	"name":    func(a, b schema.PlacementGroup) int { return cmp.Compare(a.Name, b.Name) },
	"created": func(a, b schema.PlacementGroup) int { return a.Created.Compare(b.Created) },
}

// registerPlacementGroups adds the routes of the placement groups. They
// only hold servers: the lab runs on one host, where spread always holds.
// They have no actions; Hetzner answers their actions' paths with "404
// Not Found" (measured 2026-11-09).
func registerPlacementGroups(api *web.API, l *lab.Lab) {
	api.Handle("GET /placement_groups", func(req *web.Request) (int, any, error) {
		selector, err := web.ParseSelector(req.URL.Query().Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		types, filtered := req.URL.Query()["type"]
		var groups []schema.PlacementGroup
		_ = l.View(func(tx *state.Tx) error {
			for _, group := range state.All(tx, state.PlacementGroups) {
				if group.ProjectID == req.Caller.ProjectID && selector.Matches(group.Labels) &&
					(!filtered || slices.Contains(types, group.Type)) {
					groups = append(groups, placementGroupJSON(tx, group))
				}
			}
			return nil
		})
		groups = byName(req, groups, func(group schema.PlacementGroup) string { return group.Name })
		if err := web.Sort(req, groups, placementGroupSortKeys); err != nil {
			return 0, nil, err
		}
		return listJSON(req, "placement_groups", groups)
	})

	api.Handle("POST /placement_groups", func(req *web.Request) (int, any, error) {
		return createPlacementGroup(req, l)
	})

	notFound := func(*web.Request) (int, any, error) {
		return 0, nil, web.NotFound("placement group")
	}
	api.AnswerInvalidID("GET /placement_groups/{id}", notFound)
	api.Handle("GET /placement_groups/{id}", func(req *web.Request) (int, any, error) {
		var out schema.PlacementGroup
		err := l.View(func(tx *state.Tx) error {
			group, err := callerPlacementGroup(tx, req.Caller.ProjectID, web.PathID(req, "id"))
			if err == nil {
				out = placementGroupJSON(tx, group)
			}
			return err
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.PlacementGroupGetResponse{PlacementGroup: out}, nil
	})

	api.Handle("PUT /placement_groups/{id}", func(req *web.Request) (int, any, error) {
		return updatePlacementGroup(req, l)
	})

	// a group's delete takes its servers out (measured 2026-11-09)
	api.Handle("DELETE /placement_groups/{id}", func(req *web.Request) (int, any, error) {
		id := web.PathID(req, "id")
		err := l.Change(func(tx *state.Tx) error {
			if _, err := callerPlacementGroup(tx, req.Caller.ProjectID, id); err != nil {
				return err
			}
			for _, serverID := range placementGroupServers(tx, id) {
				if server, err := state.Get(tx, state.Servers, serverID); err == nil {
					server.PlacementGroupID = 0
				}
			}
			state.Delete(tx, state.PlacementGroups, id)
			return nil
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusNoContent, nil, nil
	})

	noActions := func(*web.Request) (int, any, error) {
		return 0, nil, web.Refusal(http.StatusNotFound, "not_found", placementGroupNoActions)
	}
	api.Handle("GET /placement_groups/{id}/actions", noActions)
	api.Handle("POST /placement_groups/{id}/actions/{action}", noActions)
}

// createPlacementGroup creates a placement group without an action, 50 at
// most per project (measured 2026-11-09).
func createPlacementGroup(req *web.Request, l *lab.Lab) (int, any, error) {
	var body placementGroupRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	name := ""
	if body.Name != nil {
		name = *body.Name
	}
	check.Length("name", name, 1, placementGroupNameMax)
	if body.Type == nil || *body.Type != placementGroupSpread {
		check.Fail("type", placementGroupTypeInvalid)
	}
	if body.Labels != nil {
		check.Labels(*body.Labels)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	group := &state.PlacementGroup{
		ProjectID: req.Caller.ProjectID,
		Name:      name,
		Labels:    map[string]string{},
		Created:   l.Now(),
		Type:      placementGroupSpread,
	}
	if body.Labels != nil {
		group.Labels = *body.Labels
	}
	var out schema.PlacementGroup
	err := l.Change(func(tx *state.Tx) error {
		if err := lab.CheckProject(tx, group.ProjectID); err != nil {
			return web.TokenInvalid()
		}
		if _, taken := state.ByName(tx, state.PlacementGroups, group.ProjectID, group.Name); taken {
			return web.Uniqueness(placementGroupTaken, "name")
		}
		owned := state.IDs(tx, state.PlacementGroups, func(other *state.PlacementGroup) bool { return other.ProjectID == group.ProjectID })
		if len(owned) >= placementGroupsPerProject {
			return web.LimitExceeded(placementGroupLimit, placementGroupLimitName)
		}
		group.ID = state.NewID(tx, state.PlacementGroups)
		state.Put(tx, state.PlacementGroups, group)
		out = placementGroupJSON(tx, group)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, schema.PlacementGroupCreateResponse{PlacementGroup: out}, nil
}

// updatePlacementGroup changes a placement group's name and labels.
func updatePlacementGroup(req *web.Request, l *lab.Lab) (int, any, error) {
	var body placementGroupRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Update: true}
	if body.Name != nil {
		check.Length("name", *body.Name, 1, placementGroupNameMax)
	}
	if body.Labels != nil {
		check.Labels(*body.Labels)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	var out schema.PlacementGroup
	err := l.Change(func(tx *state.Tx) error {
		group, err := callerPlacementGroup(tx, req.Caller.ProjectID, web.PathID(req, "id"))
		if err != nil {
			return err
		}
		if body.Name != nil && *body.Name != group.Name {
			if _, taken := state.ByName(tx, state.PlacementGroups, group.ProjectID, *body.Name); taken {
				return web.Uniqueness(placementGroupTaken, "name")
			}
			group.Name = *body.Name
		}
		if body.Labels != nil {
			group.Labels = *body.Labels
		}
		out = placementGroupJSON(tx, group)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, schema.PlacementGroupUpdateResponse{PlacementGroup: out}, nil
}

// callerPlacementGroup returns the placement group of an ID if it belongs
// to the project, with Hetzner's 404 otherwise.
func callerPlacementGroup(tx *state.Tx, projectID, id int64) (*state.PlacementGroup, error) {
	group, err := state.Get(tx, state.PlacementGroups, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && group.ProjectID != projectID) {
		return nil, web.NotFound("placement group")
	}
	return group, err
}

// joinPlacementGroup puts a server into a group of its project, which
// holds 10 servers at most (Hetzner's documentation; the answer to an
// 11th is assumed, not measured).
func joinPlacementGroup(tx *state.Tx, server *state.Server, groupID int64) error {
	if _, err := callerPlacementGroup(tx, server.ProjectID, groupID); err != nil {
		return err
	}
	if len(placementGroupServers(tx, groupID)) >= placementGroupServersMax {
		return web.Refusal(http.StatusPreconditionFailed, "placement_error", placementGroupFull)
	}
	server.PlacementGroupID = groupID
	return nil
}

// placementGroupServers returns the IDs of a group's servers, in order.
func placementGroupServers(tx *state.Tx, groupID int64) []int64 {
	ids := state.IDs(tx, state.Servers, func(server *state.Server) bool { return server.PlacementGroupID == groupID })
	slices.Sort(ids)
	return ids
}

// placementGroupJSON returns a placement group as the API shows it.
func placementGroupJSON(tx *state.Tx, group *state.PlacementGroup) schema.PlacementGroup {
	out := schema.PlacementGroup{
		ID:      group.ID,
		Name:    group.Name,
		Labels:  group.Labels,
		Created: group.Created,
		Servers: append([]int64{}, placementGroupServers(tx, group.ID)...),
		Type:    group.Type,
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	return out
}
