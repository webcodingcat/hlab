package cloud_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
)

// act runs an action on a server of a project; with release, its steps run
// until release is closed.
func act(t *testing.T, l *lab.Lab, project string, server int64, release chan struct{}) state.Action {
	t.Helper()
	found, err := l.ProjectByName(project)
	if err != nil {
		t.Fatal(err)
	}
	table := lab.ActionTable{ResourceType: "server", Busy: "server is locked", Actions: map[string]lab.ActionSpec{
		"poweron": {Name: "start_server", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
			if release == nil {
				return lab.Plan{}, nil
			}
			return lab.Plan{Steps: func(ctx context.Context) error {
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil
			}}, nil
		}},
	}}
	action, err := l.Act(table, "poweron", lab.Request{ProjectID: found.ID, ID: server})
	if err != nil {
		t.Fatal(err)
	}
	return action
}

// Hetzner's API: GET /actions takes IDs and returns only those of the
// token's project; listing all actions is gone.
func TestActions(t *testing.T) {
	test := newTestLab(t)
	release := make(chan struct{})
	defer close(release)
	done := act(t, test.lab, "default", 42, nil)
	running := act(t, test.lab, "default", 43, release)
	newToken(t, test.lab, "other")
	foreign := act(t, test.lab, "other", 44, nil)

	var list schema.ActionListResponse
	response := test.client.Get(fmt.Sprintf("/v1/actions?id=%d&id=%d&id=%d", done.ID, running.ID, foreign.ID))
	response.Decode(t, &list)
	if response.Status != 200 || len(list.Actions) != 2 {
		t.Fatalf("answered %d %s", response.Status, response.Body)
	}
	first, second := list.Actions[0], list.Actions[1]
	if first.ID != done.ID || first.Status != "success" || first.Finished == nil || first.Command != "start_server" {
		t.Errorf("the finished action is %+v", first)
	}
	if second.ID != running.ID || second.Status != "running" || second.Finished != nil {
		t.Errorf("the running action is %+v", second)
	}
	if len(first.Resources) != 1 || first.Resources[0] != (schema.ActionResourceReference{ID: 42, Type: "server"}) {
		t.Errorf("the resources are %+v", first.Resources)
	}

	var one schema.ActionGetResponse
	response = test.client.Get(fmt.Sprintf("/v1/actions/%d", done.ID))
	response.Decode(t, &one)
	if response.Status != 200 || one.Action.ID != done.ID {
		t.Errorf("GET /actions/%d answered %d %s", done.ID, response.Status, response.Body)
	}
}

// Hetzner's API (measured 2026-11-07): an action of no project of the token
// is 404 with the plain text "Not Found", not JSON.
func TestActionsOfOtherProjectsAreNotFound(t *testing.T) {
	test := newTestLab(t)
	newToken(t, test.lab, "other")
	foreign := act(t, test.lab, "other", 44, nil)
	for _, path := range []string{fmt.Sprintf("/v1/actions/%d", foreign.ID), "/v1/actions/999999999"} {
		response := test.client.Get(path)
		if response.Status != 404 || string(response.Body) != "Not Found\n" ||
			response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("GET %s answered %d %q (%s)", path, response.Status, response.Body, response.Header.Get("Content-Type"))
		}
	}
}

// Hetzner's API (measured 2026-11-07): without IDs, listing actions is a
// removed functionality (410); an ID that is no number is invalid input, but
// with the status 406.
func TestActionsNeedIDs(t *testing.T) {
	test := newTestLab(t)
	for query, want := range map[string]struct {
		status        int
		code, message string
	}{
		"":        {410, "deprecated_api_endpoint", "API functionality was removed"},
		"?id=abc": {406, "invalid_input", "invalid integer value"},
	} {
		response := test.client.Get("/v1/actions" + query)
		var body schema.ErrorResponse
		response.Decode(t, &body)
		if response.Status != want.status || body.Error.Code != want.code || body.Error.Message != want.message {
			t.Errorf("GET /v1/actions%s answered %d %s", query, response.Status, response.Body)
		}
	}
}

// Another method than GET on a single action, which is also a command the
// resource does not have, as Hetzner answers it (measured 2026-11-09): the
// servers 404, the networks, firewalls, Floating IPs and Primary IPs 405
// without a body.
func TestUnknownActionsAnswerAsHetznerDoes(t *testing.T) {
	test := newTestLab(t)
	for _, path := range []string{"/v1/networks/1/actions/bogus", "/v1/networks/1/actions/123", "/v1/networks/actions/bogus",
		"/v1/firewalls/1/actions/bogus", "/v1/floating_ips/1/actions/bogus", "/v1/primary_ips/actions/bogus"} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			response := test.client.Do(method, path, map[string]any{})
			if response.Status != http.StatusMethodNotAllowed || len(response.Body) != 0 || response.Header.Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: %d, Allow %q, %q", method, path, response.Status, response.Header.Get("Allow"), response.Body)
			}
		}
	}
	expectError(t, "POST a server's bogus action", &responseOf{test.client.Do(http.MethodPost, "/v1/servers/1/actions/bogus", map[string]any{})},
		http.StatusNotFound, "not_found", "api route not found")
	expectError(t, "POST a server's action by ID", &responseOf{test.client.Do(http.MethodPost, "/v1/servers/1/actions/123", map[string]any{})},
		http.StatusNotFound, "not_found", "404 Not Found")
	// the commands themselves still answer
	if response := test.client.Do(http.MethodPost, "/v1/networks/1/actions/add_subnet", map[string]any{}); response.Status == http.StatusMethodNotAllowed {
		t.Errorf("add_subnet answers %d", response.Status)
	}
}

// A single action that is not there, or an action ID that is no number,
// gets each resource's own answer from Hetzner (measured 2026-11-09).
func TestMissingActionsAnswerAsHetznerDoes(t *testing.T) {
	test := newTestLab(t)
	server := offServer(t, test, "web").Server.ID
	network := createNetwork(t, test, map[string]any{"name": "net", "ip_range": "10.0.0.0/16"}).ID
	firewall := createFirewall(t, test, map[string]any{"name": "fw"}).Firewall.ID
	floating := createFloatingIP(t, test, map[string]any{"type": "ipv4", "home_location": "fsn1"}).FloatingIP.ID
	primary := createPrimaryIP(t, test, map[string]any{"name": "ip", "type": "ipv4", "location": "fsn1", "assignee_type": "server"}).PrimaryIP.ID
	for path, want := range map[string]struct {
		message, details string
	}{
		fmt.Sprintf("/v1/servers/%d/actions/999", server):        {"action not found", "{}"},
		"/v1/servers/1/actions/999":                              {"action not found", "{}"},
		"/v1/servers/actions/999":                                {"action not found", "{}"},
		fmt.Sprintf("/v1/servers/%d/actions/bogus", server):      {"api route not found", "null"},
		fmt.Sprintf("/v1/networks/%d/actions/999", network):      {"action with ID '999' not found", "null"},
		fmt.Sprintf("/v1/networks/%d/actions/bogus", network):    {fmt.Sprintf("action with ID 'bogus' for network with ID '%d' not found", network), "null"},
		"/v1/networks/1/actions/999":                             {"network with ID 1 not found", "null"},
		"/v1/networks/actions/bogus":                             {"action with ID 'bogus' not found", "null"},
		fmt.Sprintf("/v1/firewalls/%d/actions/bogus", firewall):  {"action with ID 'bogus' not found", "null"},
		"/v1/firewalls/1/actions/999":                            {"firewall with ID 1 not found", "null"},
		fmt.Sprintf("/v1/floating_ips/%d/actions/999", floating): {"action with ID '999' not found", "null"},
		"/v1/floating_ips/1/actions/bogus":                       {"invalid action ID: 'bogus'", "null"},
		"/v1/floating_ips/1/actions/999":                         {"Floating IP with ID 1 not found", "null"},
		fmt.Sprintf("/v1/primary_ips/%d/actions/bogus", primary): {"invalid action ID: 'bogus'", "null"},
		"/v1/primary_ips/1/actions/999":                          {"Primary IP with ID 1 not found", "null"},
		"/v1/primary_ips/actions/999":                            {"action with ID '999' not found", "null"},
	} {
		response := test.client.Get(path)
		got := errorOf(t, response)
		if response.Status != http.StatusNotFound || got.Code != "not_found" || got.Message != want.message || string(got.Details) != want.details {
			t.Errorf("GET %s: %d %s %q %s, want 404 not_found %q %s", path, response.Status, got.Code, got.Message, got.Details, want.message, want.details)
		}
	}
	// an action of another resource is not there either
	created := createServer(t, test, map[string]any{"name": "db", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	path := fmt.Sprintf("/v1/networks/%d/actions/%d", network, created.Action.ID)
	if got := errorOf(t, test.client.Get(path)); got.Message != fmt.Sprintf("action with ID '%d' not found", created.Action.ID) {
		t.Errorf("GET %s: %q", path, got.Message)
	}
}

// A GET of a resource whose ID is no number gets each resource's own
// answer from Hetzner (measured 2026-11-09).
func TestResourceIDsThatAreNoNumberAnswerAsHetznerDoes(t *testing.T) {
	test := newTestLab(t)
	for _, path := range []string{"/v1/networks/bogus", "/v1/floating_ips/bogus"} {
		response := test.client.Get(path)
		if response.Status != http.StatusMethodNotAllowed || len(response.Body) != 0 || response.Header.Get("Allow") != "PUT, DELETE" {
			t.Errorf("GET %s: %d, Allow %q, %q", path, response.Status, response.Header.Get("Allow"), response.Body)
		}
	}
	for path, want := range map[string]string{
		"/v1/firewalls/bogus":   "firewall with ID 'bogus' not found",
		"/v1/primary_ips/bogus": "404 Not Found",
		"/v1/servers/bogus":     "api route not found",
	} {
		response := test.client.Get(path)
		if got := errorOf(t, response); response.Status != http.StatusNotFound || got.Message != want || string(got.Details) != "null" {
			t.Errorf("GET %s: %d %q %s, want 404 %q null", path, response.Status, got.Message, got.Details, want)
		}
	}
	// without a token, a path the lab knows still asks for one
	test.client.Token = ""
	if response := test.client.Get("/v1/firewalls/bogus"); response.Status != http.StatusUnauthorized {
		t.Errorf("GET /v1/firewalls/bogus without a token: %d", response.Status)
	}
}
