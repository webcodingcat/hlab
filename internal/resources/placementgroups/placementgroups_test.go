package placementgroups_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/placementgroups"
	"github.com/webcodingcat/hlab/internal/resources/servers"
)

type group struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Servers []int64 `json:"servers"`
}

func TestPlacementGroups(t *testing.T) {
	t.Run("SRV-22", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t,
			servers.New(servers.Deps{
				Catalog:     cat,
				PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
				Machines:    apitest.NoopMachines{},
			}),
			placementgroups.New(),
			resactions.New(),
		)

		// Create, read, list, delete; only type spread.
		resp := env.Do(http.MethodPost, "/placement_groups", map[string]any{"name": "other", "type": "anti"})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("POST /placement_groups type anti: status %d, want 422", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "invalid_input" {
			t.Errorf("POST /placement_groups type anti: error.code %q, want invalid_input", code)
		}
		full := createGroup(t, env, "full")
		spare := createGroup(t, env, "spare")
		gone := createGroup(t, env, "gone")
		if g := getGroup(t, env, full); g.Name != "full" || g.Type != "spread" || len(g.Servers) != 0 {
			t.Errorf("GET placement group %d = %+v, want full, spread, no servers", full, g)
		}
		resp = env.Do(http.MethodGet, "/placement_groups", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /placement_groups: status %d, want 200", resp.StatusCode)
		}
		var list struct {
			PlacementGroups []group `json:"placement_groups"`
		}
		apitest.Decode(t, resp, &list)
		var listed []int64
		for _, g := range list.PlacementGroups {
			listed = append(listed, g.ID)
		}
		if !slices.Equal(listed, []int64{full, spare, gone}) {
			t.Errorf("GET /placement_groups IDs = %v, want %v", listed, []int64{full, spare, gone})
		}
		path := fmt.Sprintf("/placement_groups/%d", gone)
		if resp := env.Do(http.MethodDelete, path, nil); resp.StatusCode != http.StatusNoContent {
			t.Errorf("DELETE %s: status %d, want 204", path, resp.StatusCode)
		}
		if resp := env.Do(http.MethodGet, path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s after delete: status %d, want 404", path, resp.StatusCode)
		}

		// Servers created into a group; the eleventh is refused.
		var members []int64
		for i := 1; i <= placementgroups.MaxServers; i++ {
			id, act := createServer(t, env, fmt.Sprintf("member-%d", i), map[string]any{"placement_group": full})
			env.WaitAction(act)
			members = append(members, id)
		}
		if g := getGroup(t, env, full); !sameIDs(g.Servers, members) {
			t.Errorf("group servers = %v, want %v", g.Servers, members)
		}
		if pg := serverGroup(t, env, members[0]); pg == nil || *pg != full {
			t.Errorf("server %d placement_group = %v, want %d", members[0], pg, full)
		}
		resp = env.Do(http.MethodPost, "/servers", serverRequest("member-11", map[string]any{"placement_group": full}))
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("POST /servers into a full group: status %d, want 422", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "placement_error" {
			t.Errorf("POST /servers into a full group: error.code %q, want placement_error", code)
		}
		if n := countServers(t, env, "member-11"); n != 0 {
			t.Errorf("GET /servers?name=member-11 lists %d servers, want none", n)
		}
		if g := getGroup(t, env, full); len(g.Servers) != placementgroups.MaxServers {
			t.Errorf("full group has %d servers after the refused create, want %d", len(g.Servers), placementgroups.MaxServers)
		}

		// add_to_placement_group.
		running, act := createServer(t, env, "running", map[string]any{"start_after_create": true})
		env.WaitAction(act)
		resp = addToGroup(t, env, running, spare)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("add_to_placement_group on a running server: status %d, want 422", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "server_not_stopped" {
			t.Errorf("add_to_placement_group on a running server: error.code %q, want server_not_stopped", code)
		}

		member, act := createServer(t, env, "off-member", map[string]any{"start_after_create": false, "placement_group": spare})
		env.WaitAction(act)
		resp = addToGroup(t, env, member, spare)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("add_to_placement_group on a member: status %d, want 422", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "already_in_placement_group" {
			t.Errorf("add_to_placement_group on a member: error.code %q, want already_in_placement_group", code)
		}

		off, act := createServer(t, env, "off", map[string]any{"start_after_create": false})
		env.WaitAction(act)
		resp = addToGroup(t, env, off, spare)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("add_to_placement_group on a stopped server: status %d, want 201", resp.StatusCode)
		}
		waitResponseAction(t, env, resp)
		if g := getGroup(t, env, spare); !sameIDs(g.Servers, []int64{member, off}) {
			t.Errorf("spare group servers after add = %v, want %v", g.Servers, []int64{member, off})
		}
		if pg := serverGroup(t, env, off); pg == nil || *pg != spare {
			t.Errorf("server %d placement_group after add = %v, want %d", off, pg, spare)
		}

		// remove_from_placement_group and server deletion leave the group.
		resp = env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/remove_from_placement_group", off), nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("remove_from_placement_group: status %d, want 201", resp.StatusCode)
		}
		waitResponseAction(t, env, resp)
		if g := getGroup(t, env, spare); !sameIDs(g.Servers, []int64{member}) {
			t.Errorf("spare group servers after remove = %v, want %v", g.Servers, []int64{member})
		}
		if pg := serverGroup(t, env, off); pg != nil {
			t.Errorf("server %d placement_group after remove = %d, want null", off, *pg)
		}

		resp = env.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", member), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("DELETE /servers/%d: status %d, want 200", member, resp.StatusCode)
		}
		waitResponseAction(t, env, resp)
		if g := getGroup(t, env, spare); len(g.Servers) != 0 {
			t.Errorf("spare group servers after deleting its member = %v, want none", g.Servers)
		}
	})
}

func createGroup(t *testing.T, env *apitest.Env, name string) int64 {
	t.Helper()
	resp := env.Do(http.MethodPost, "/placement_groups", map[string]any{"name": name, "type": "spread"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /placement_groups %s: status %d, want 201", name, resp.StatusCode)
	}
	var body struct {
		PlacementGroup group `json:"placement_group"`
	}
	apitest.Decode(t, resp, &body)
	return body.PlacementGroup.ID
}

func getGroup(t *testing.T, env *apitest.Env, id int64) group {
	t.Helper()
	resp := env.Do(http.MethodGet, fmt.Sprintf("/placement_groups/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /placement_groups/%d: status %d, want 200", id, resp.StatusCode)
	}
	var body struct {
		PlacementGroup group `json:"placement_group"`
	}
	apitest.Decode(t, resp, &body)
	return body.PlacementGroup
}

func serverRequest(name string, extra map[string]any) map[string]any {
	req := map[string]any{
		"name":        name,
		"server_type": "cx23",
		"image":       "ubuntu-24.04",
		"location":    "fsn1",
	}
	for k, v := range extra {
		req[k] = v
	}
	return req
}

// createServer creates a server and returns its ID and its create action.
func createServer(t *testing.T, env *apitest.Env, name string, extra map[string]any) (id, action int64) {
	t.Helper()
	resp := env.Do(http.MethodPost, "/servers", serverRequest(name, extra))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers %s: status %d, want 201", name, resp.StatusCode)
	}
	var body struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	apitest.Decode(t, resp, &body)
	return body.Server.ID, body.Action.ID
}

// serverGroup returns the ID of the server's placement_group, nil for null.
func serverGroup(t *testing.T, env *apitest.Env, id int64) *int64 {
	t.Helper()
	resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var body struct {
		Server struct {
			PlacementGroup *group `json:"placement_group"`
		} `json:"server"`
	}
	apitest.Decode(t, resp, &body)
	if body.Server.PlacementGroup == nil {
		return nil
	}
	return &body.Server.PlacementGroup.ID
}

func countServers(t *testing.T, env *apitest.Env, name string) int {
	t.Helper()
	var list struct {
		Servers []struct {
			ID int64 `json:"id"`
		} `json:"servers"`
	}
	apitest.Decode(t, env.Do(http.MethodGet, "/servers?name="+name, nil), &list)
	return len(list.Servers)
}

func addToGroup(t *testing.T, env *apitest.Env, server, group int64) *http.Response {
	t.Helper()
	return env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/add_to_placement_group", server),
		map[string]any{"placement_group": group})
}

// waitResponseAction waits for the action in resp to succeed.
func waitResponseAction(t *testing.T, env *apitest.Env, resp *http.Response) {
	t.Helper()
	var body struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	apitest.Decode(t, resp, &body)
	if status := env.WaitAction(body.Action.ID).Status; status != "success" {
		t.Fatalf("action %d ended %q, want success", body.Action.ID, status)
	}
}

func sameIDs(got, want []int64) bool {
	a, b := slices.Clone(got), slices.Clone(want)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
