// Package placementgroups serves Hetzner's placement groups of a project.
// hlab has one host, so a group only records its member servers; it
// places nothing physically.
package placementgroups

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind of placement groups.
const Kind = "placement_groups"

// MaxServers is the number of servers a spread group holds at most.
const MaxServers = 10

// typeSpread is the only placement group type Hetzner offers.
const typeSpread = "spread"

// Group is a placement group in Hetzner's API shape.
type Group struct {
	ID      int64             `json:"id"`
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels"`
	Type    string            `json:"type"`
	Created time.Time         `json:"created"`
	Servers []int64           `json:"servers"`
}

// Module is the placement_groups resource type.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /placement_groups", http.HandlerFunc(m.list))
	r.Handle("POST /placement_groups", http.HandlerFunc(m.create))
	r.Handle("GET /placement_groups/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /placement_groups/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /placement_groups/{id}", http.HandlerFunc(m.delete))
}

// Find returns the group of projectID whose ID or name is ref, or
// store.ErrNotFound.
func Find(tx store.Tx, projectID int64, ref string) (Group, error) {
	groups, err := store.List[Group](tx, Kind, projectID)
	if err != nil {
		return Group{}, err
	}
	for _, g := range groups {
		if g.Name == ref {
			return g, nil
		}
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, g := range groups {
			if g.ID == id {
				return g, nil
			}
		}
	}
	return Group{}, store.ErrNotFound
}

// Members maps every member server of projectID's groups to its group.
// The groups are the only record of membership, so deleting a group
// leaves no server pointing at it.
func Members(tx store.Tx, projectID int64) (map[int64]Group, error) {
	groups, err := store.List[Group](tx, Kind, projectID)
	if err != nil {
		return nil, err
	}
	out := map[int64]Group{}
	for _, g := range groups {
		for _, id := range g.Servers {
			out[id] = g
		}
	}
	return out, nil
}

// Of returns the group of projectID that has serverID as a member; ok is
// false when the server is in none.
func Of(tx store.Tx, projectID, serverID int64) (g Group, ok bool, err error) {
	members, err := Members(tx, projectID)
	if err != nil {
		return Group{}, false, err
	}
	g, ok = members[serverID]
	return g, ok, nil
}

// AddServer makes serverID a member of the group groupID of projectID. A
// full group gives a *api.FieldError placement_error, a server in any
// group already_in_placement_group, an unknown group store.ErrNotFound.
func AddServer(tx store.Tx, projectID, groupID, serverID int64) error {
	g, err := store.Get[Group](tx, Kind, projectID, groupID)
	if err != nil {
		return err
	}
	_, member, err := Of(tx, projectID, serverID)
	if err != nil {
		return err
	}
	if member {
		return &api.FieldError{Code: "already_in_placement_group", Message: "the server is already part of a placement group"}
	}
	if len(g.Servers) >= MaxServers {
		return &api.FieldError{Code: "placement_error",
			Message: fmt.Sprintf("placement group %s is full: a spread group holds at most %d servers", g.Name, MaxServers)}
	}
	g.Servers = append(g.Servers, serverID)
	return put(tx, projectID, g)
}

// RemoveServer removes serverID from the group groupID of projectID; a
// server that is no member changes nothing. An unknown group gives
// store.ErrNotFound.
func RemoveServer(tx store.Tx, projectID, groupID, serverID int64) error {
	g, err := store.Get[Group](tx, Kind, projectID, groupID)
	if err != nil {
		return err
	}
	if !slices.Contains(g.Servers, serverID) {
		return nil
	}
	g.Servers = slices.DeleteFunc(g.Servers, func(id int64) bool { return id == serverID })
	return put(tx, projectID, g)
}

func put(tx store.Tx, projectID int64, g Group) error {
	return store.Put(tx, store.Meta{Kind: Kind, ID: g.ID, ProjectID: projectID, Name: g.Name, Labels: g.Labels, Created: g.Created}, g)
}

// checkName refuses an empty name and one another group of the project has.
func checkName(tx store.Tx, projectID int64, name string, exceptID int64) error {
	if strings.TrimSpace(name) == "" {
		return &api.FieldError{Field: "name", Message: "name is required"}
	}
	taken, err := store.NameTaken(tx, Kind, projectID, name, exceptID)
	if err != nil {
		return err
	}
	if taken {
		return &api.FieldError{Code: "uniqueness_error", Field: "name", Message: "name is already used"}
	}
	return nil
}

var listOptions = api.ListOptions[Group]{
	Name:   func(g Group) string { return g.Name },
	Labels: func(g Group) map[string]string { return g.Labels },
	Sort: map[string]func(a, b Group) int{
		"id":      func(a, b Group) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b Group) int { return strings.Compare(a.Name, b.Name) },
		"created": func(a, b Group) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(Group, []string) bool{
		"type": func(g Group, v []string) bool { return slices.Contains(v, g.Type) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var groups []Group
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		groups, err = store.List[Group](tx, Kind, p.ID)
		return err
	})
	if err != nil {
		api.ServerError(w, err)
		return
	}
	api.ListRequest(w, req, Kind, groups, listOptions)
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var g Group
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		g, err = store.Get[Group](tx, Kind, p.ID, id)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"placement_group": g})
}

func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Type != typeSpread {
		api.InvalidInput(w, "type", "type must be spread")
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	p, _ := api.ProjectFrom(req.Context())
	g := Group{
		Name:    body.Name,
		Labels:  body.Labels,
		Type:    body.Type,
		Created: time.Now().UTC().Truncate(time.Second),
		Servers: []int64{},
	}
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, g.Name, 0); err != nil {
			return err
		}
		if err := api.CheckLabels(g.Labels); err != nil {
			return err
		}
		var err error
		if g.ID, err = store.NextID(tx, Kind); err != nil {
			return err
		}
		return put(tx, p.ID, g)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	// Creating a group needs no work, so there is no action to poll.
	api.JSON(w, http.StatusCreated, map[string]any{"placement_group": g, "action": nil})
}

// update changes only the fields present in the body; labels, when
// present, replace the whole set.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Name   *string            `json:"name"`
		Labels *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var g Group
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if g, err = store.Get[Group](tx, Kind, p.ID, id); err != nil {
			return err
		}
		if body.Name != nil {
			if err := checkName(tx, p.ID, *body.Name, id); err != nil {
				return err
			}
			g.Name = *body.Name
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			g.Labels = *body.Labels
		}
		return put(tx, p.ID, g)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"placement_group": g})
}

func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		return store.Delete(tx, Kind, p.ID, id)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
