package cloud_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"
)

// createPlacementGroup creates a placement group and fails the test if it
// cannot.
func createPlacementGroup(t *testing.T, test *testLab, body map[string]any) schema.PlacementGroup {
	t.Helper()
	response := test.client.Do(http.MethodPost, "/v1/placement_groups", body)
	if response.Status != http.StatusCreated {
		t.Fatalf("POST /v1/placement_groups answered %d %s", response.Status, response.Body)
	}
	var created schema.PlacementGroupCreateResponse
	response.Decode(t, &created)
	return created.PlacementGroup
}

// getPlacementGroup returns a placement group as the API shows it.
func getPlacementGroup(t *testing.T, test *testLab, id int64) schema.PlacementGroup {
	t.Helper()
	var answer schema.PlacementGroupGetResponse
	test.client.Get(fmt.Sprintf("/v1/placement_groups/%d", id)).Decode(t, &answer)
	return answer.PlacementGroup
}

// The life of a placement group, as Hetzner answers (measured 2026-11-09):
// no actions, servers in and out, a delete that takes them out.
func TestPlacementGroupLifeCycle(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	group := createPlacementGroup(t, test, map[string]any{"name": "has space", "type": "spread", "labels": map[string]string{"env": "dev"}})
	if group.Type != "spread" || len(group.Servers) != 0 {
		t.Errorf("the created group: %+v", group)
	}
	server := offServer(t, test, "web")
	add := serverAction(t, test, server.Server.ID, "add_to_placement_group", map[string]any{"placement_group": group.ID})
	if add.Command != "add_to_placement_group" || add.Status != "success" || len(add.Resources) != 1 {
		t.Errorf("the add: %+v", add)
	}
	if got := getPlacementGroup(t, test, group.ID).Servers; len(got) != 1 || got[0] != server.Server.ID {
		t.Errorf("the group's servers: %v", got)
	}
	if got := getServer(t, test, server.Server.ID).PlacementGroup; got == nil || got.ID != group.ID {
		t.Errorf("the server's placement group: %+v", got)
	}
	other := createPlacementGroup(t, test, map[string]any{"name": "other", "type": "spread"})
	path := fmt.Sprintf("/v1/servers/%d/actions/add_to_placement_group", server.Server.ID)
	expectErrors(t, test, path, map[string]struct {
		body any
		want networkError
	}{
		"nothing":       {map[string]any{}, invalidField("placement_group", "Missing data for required field.")},
		"another group": {map[string]any{"placement_group": other.ID}, refused("already_in_placement_group", fmt.Sprintf("server with ID %d is already part of a placement group", server.Server.ID))},
	})
	waitActions(t, test, serverAction(t, test, server.Server.ID, "remove_from_placement_group", nil))
	// also a server in no group (measured 2026-11-09)
	waitActions(t, test, serverAction(t, test, server.Server.ID, "remove_from_placement_group", nil))
	expectErrors(t, test, path, map[string]struct {
		body any
		want networkError
	}{
		"a missing group": {map[string]any{"placement_group": 1}, networkError{http.StatusNotFound, "not_found", "placement group not found", nil}},
	})
	running := createServer(t, test, map[string]any{"name": "running", "server_type": "cpx22", "image": "debian-13"})
	waitActions(t, test, append([]schema.Action{running.Action}, running.NextActions...)...)
	expectError(t, "a running server", serverNetworkAction(test, running.Server.ID, "add_to_placement_group", map[string]any{"placement_group": group.ID}),
		http.StatusUnprocessableEntity, "server_not_stopped", "server must be offline for adding it to a placement group")

	waitActions(t, test, serverAction(t, test, server.Server.ID, "add_to_placement_group", map[string]any{"placement_group": group.ID}))
	if response := test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/placement_groups/%d", group.ID), nil); response.Status != http.StatusNoContent {
		t.Fatalf("the delete answered %d %s", response.Status, response.Body)
	}
	if got := getServer(t, test, server.Server.ID).PlacementGroup; got != nil {
		t.Errorf("after the group's delete the server is in %+v", got)
	}
}

// The errors of placement groups, as Hetzner answers them (measured
// 2026-11-09).
func TestPlacementGroupErrors(t *testing.T) {
	test := newTestLab(t)
	createPlacementGroup(t, test, map[string]any{"name": "taken", "type": "spread"})
	expectErrors(t, test, "/v1/placement_groups", map[string]struct {
		body any
		want networkError
	}{
		"no type":       {map[string]any{"name": "g"}, invalidField("type", "Not a valid choice")},
		"pack":          {map[string]any{"name": "g", "type": "pack"}, invalidField("type", "Not a valid choice")},
		"no name":       {map[string]any{"type": "spread"}, invalidField("name", "Length must be between 1 and 128.")},
		"a name in use": {map[string]any{"name": "taken", "type": "spread"}, networkError{http.StatusConflict, "uniqueness_error", "placement_group not unique", nil}},
	})
	for _, path := range []string{"/v1/placement_groups/999999999", "/v1/placement_groups/bogus"} {
		expectError(t, path, &responseOf{test.client.Get(path)}, http.StatusNotFound, "not_found", "placement group not found")
	}
	expectError(t, "the actions", &responseOf{test.client.Get("/v1/placement_groups/1/actions")}, http.StatusNotFound, "not_found", "404 Not Found")
	for i := range 49 {
		createPlacementGroup(t, test, map[string]any{"name": fmt.Sprintf("g%d", i), "type": "spread"})
	}
	expectError(t, "the 51st group", &responseOf{test.client.Do(http.MethodPost, "/v1/placement_groups", map[string]any{"name": "g51", "type": "spread"})},
		http.StatusForbidden, "resource_limit_exceeded", "placement_group limit exceeded")
}

// A spread group takes 10 servers (Hetzner's documentation; the answer to
// an 11th is assumed, not measured).
func TestPlacementGroupTakesTenServers(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	group := createPlacementGroup(t, test, map[string]any{"name": "spread", "type": "spread"})
	for i := range 10 {
		createServer(t, test, map[string]any{"name": fmt.Sprintf("s%d", i), "server_type": "cpx22", "image": "debian-13",
			"start_after_create": false, "placement_group": group.ID})
	}
	expectError(t, "an 11th server", &responseOf{test.client.Do(http.MethodPost, "/v1/servers", map[string]any{
		"name": "s10", "server_type": "cpx22", "image": "debian-13", "start_after_create": false, "placement_group": group.ID})},
		http.StatusPreconditionFailed, "placement_error", "placement group is full")
}
