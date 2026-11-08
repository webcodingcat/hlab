package cloud_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/spectest"
)

// createPrimaryIP creates a Primary IP and fails the test if it cannot.
func createPrimaryIP(t *testing.T, test *testLab, body map[string]any) schema.PrimaryIPCreateResponse {
	t.Helper()
	response := test.client.Do(http.MethodPost, "/v1/primary_ips", body)
	if response.Status != http.StatusCreated {
		t.Fatalf("POST /v1/primary_ips answered %d %s", response.Status, response.Body)
	}
	var created schema.PrimaryIPCreateResponse
	response.Decode(t, &created)
	return created
}

// getPrimaryIP reads a Primary IP.
func getPrimaryIP(t *testing.T, test *testLab, id int64) schema.PrimaryIP {
	t.Helper()
	var answer schema.PrimaryIPGetResponse
	test.client.Get(fmt.Sprintf("/v1/primary_ips/%d", id)).Decode(t, &answer)
	return answer.PrimaryIP
}

// responseOf is an answer that expectError checks.
type responseOf struct{ *spectest.Response }

// primaryIPAction runs a Primary IP action and returns the answer.
func primaryIPAction(test *testLab, id int64, command string, body any) *responseOf {
	return &responseOf{test.client.Do(http.MethodPost, fmt.Sprintf("/v1/primary_ips/%d/actions/%s", id, command), body)}
}

// expectError fails the test unless the answer is the error.
func expectError(t *testing.T, what string, response *responseOf, status int, code, message string) {
	t.Helper()
	got := errorOf(t, response.Response)
	if response.Status != status || got.Code != code || got.Message != message {
		t.Errorf("%s: %d %+v, want %d %s %q", what, response.Status, got, status, code, message)
	}
}

// The life of a Primary IP and a server's own ones, as Hetzner answers
// (measured 2026-11-08).
func TestPrimaryIPLifeCycle(t *testing.T) {
	test := newTestLab(t)
	created := createPrimaryIP(t, test, map[string]any{
		"name": "spare", "type": "ipv4", "location": "fsn1", "assignee_type": "server", "labels": map[string]string{"env": "dev"},
	})
	ip := created.PrimaryIP
	if created.Action != nil || ip.AssigneeID != nil || ip.AssigneeType != "unassigned" || ip.AutoDelete ||
		len(ip.DNSPtr) != 1 || ip.DNSPtr[0].IP != ip.IP || ip.Location.Name != "fsn1" {
		t.Errorf("created %+v, action %+v", ip, created.Action)
	}
	response := test.client.Do(http.MethodPost, "/v1/primary_ips", map[string]any{"name": "spare", "type": "ipv4", "location": "fsn1", "assignee_type": "server"})
	expectError(t, "a taken name", &responseOf{response}, http.StatusConflict, "uniqueness_error", "name is already used")

	server := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "location": "fsn1"})
	waitActions(t, test, server.Action, server.NextActions[0])
	own := server.Server.PublicNet.IPv4.ID
	if got := getPrimaryIP(t, test, own); !got.AutoDelete || got.Name != fmt.Sprintf("primary_ip-%d", own) || *got.AssigneeID != server.Server.ID {
		t.Errorf("the server's own IP is %+v", got)
	}
	expectError(t, "assigning to a running server", primaryIPAction(test, ip.ID, "assign", map[string]any{"assignee_id": server.Server.ID, "assignee_type": "server"}),
		http.StatusUnprocessableEntity, "server_not_stopped", "Server must be offline for this action")
	expectError(t, "deleting an assigned IP", &responseOf{test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/primary_ips/%d", own), nil)},
		http.StatusUnprocessableEntity, "must_be_unassigned", "Primary IP must be unassigned")

	waitActions(t, test, serverAction(t, test, server.Server.ID, "poweroff", nil))
	expectError(t, "a second IPv4", primaryIPAction(test, ip.ID, "assign", map[string]any{"assignee_id": server.Server.ID, "assignee_type": "server"}),
		http.StatusForbidden, "resource_limit_exceeded", "assignment limit reached")

	unassign := primaryIPAction(test, own, "unassign", nil)
	if unassign.Status != http.StatusCreated {
		t.Fatalf("unassign answered %d %s", unassign.Status, unassign.Body)
	}
	var unassigned schema.ActionGetResponse
	unassign.Decode(t, &unassigned)
	if unassigned.Action.Command != "unassign_primary_ip" || unassigned.Action.Resources[1].Type != "server" {
		t.Errorf("the unassign is %+v", unassigned.Action)
	}
	// the IP is locked while its action runs, even for a delete
	waitActions(t, test, unassigned.Action)
	// Hetzner clears auto_delete on unassign
	if got := getPrimaryIP(t, test, own); got.AutoDelete || got.AssigneeID != nil {
		t.Errorf("the unassigned IP is %+v", got)
	}
	if got := getServer(t, test, server.Server.ID); got.PublicNet.IPv4 != nil {
		t.Errorf("the server still has %+v", got.PublicNet.IPv4)
	}
	assign := primaryIPAction(test, ip.ID, "assign", map[string]any{"assignee_id": server.Server.ID, "assignee_type": "server"})
	var assigned schema.ActionGetResponse
	assign.Decode(t, &assigned)
	waitActions(t, test, assigned.Action)
	if got := getServer(t, test, server.Server.ID); got.PublicNet.IPv4 == nil || got.PublicNet.IPv4.ID != ip.ID {
		t.Errorf("the server's IPv4 is %+v", got.PublicNet.IPv4)
	}

	// a protected IP takes auto_delete, and refuses a delete
	primaryIPAction(test, own, "change_protection", map[string]bool{"delete": true})
	var updated schema.PrimaryIPUpdateResponse
	test.client.Do(http.MethodPut, fmt.Sprintf("/v1/primary_ips/%d", own), map[string]any{"auto_delete": true, "name": "kept"}).Decode(t, &updated)
	if !updated.PrimaryIP.AutoDelete || updated.PrimaryIP.Name != "kept" {
		t.Errorf("updated %+v", updated.PrimaryIP)
	}
	expectError(t, "deleting a protected IP", &responseOf{test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/primary_ips/%d", own), nil)},
		http.StatusLocked, "protected", "Primary IP deletion is protected")
	primaryIPAction(test, own, "change_protection", map[string]bool{"delete": false})
	if status := test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/primary_ips/%d", own), nil).Status; status != http.StatusNoContent {
		t.Errorf("deleting the unassigned IP: %d", status)
	}

	// deleting the server keeps the IP without auto_delete, unassigned
	test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/servers/%d", server.Server.ID), nil)
	if got := getPrimaryIP(t, test, ip.ID); got.AssigneeID != nil || got.AssigneeType != "unassigned" {
		t.Errorf("after the server's delete the IP is %+v", got)
	}
	response = test.client.Get(fmt.Sprintf("/v1/primary_ips/%d", own))
	expectError(t, "a deleted IP", &responseOf{response}, http.StatusNotFound, "not_found", fmt.Sprintf("Primary IP with ID %d not found", own))
}

func TestPrimaryIPCreateErrors(t *testing.T) {
	test := newTestLab(t)
	// measured 2026-11-08: neither or both of assignee_id and location
	for name, body := range map[string]map[string]any{
		"no place":    {"name": "x", "type": "ipv4", "assignee_type": "server"},
		"both places": {"name": "x", "type": "ipv4", "assignee_type": "server", "location": "fsn1", "assignee_id": 1},
	} {
		expectError(t, name, &responseOf{test.client.Do(http.MethodPost, "/v1/primary_ips", body)},
			http.StatusUnprocessableEntity, "invalid_input", "invalid input in fields 'assignee_id', 'location'")
	}
	expectError(t, "an unknown type", &responseOf{test.client.Do(http.MethodPost, "/v1/primary_ips",
		map[string]any{"name": "x", "type": "bogus", "assignee_type": "server", "location": "fsn1"})},
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'type'")
}

// A Primary IP created for a server that is off is assigned at once, with
// an action; a server created with it takes it.
func TestPrimaryIPAssignedOnCreate(t *testing.T) {
	test := newTestLab(t)
	server := createServer(t, test, map[string]any{
		"name": "web", "server_type": "cpx22", "image": "debian-13", "start_after_create": false,
		"public_net": map[string]any{"enable_ipv4": false, "enable_ipv6": false},
	})
	waitActions(t, test, server.Action)
	if server.Server.PublicNet.IPv4 != nil || server.Server.PublicNet.IPv6 != nil {
		t.Errorf("a server without public net has %+v", server.Server.PublicNet)
	}
	created := createPrimaryIP(t, test, map[string]any{"name": "six", "type": "ipv6", "assignee_type": "server", "assignee_id": server.Server.ID})
	if created.Action == nil || created.Action.Command != "assign_primary_ip" || created.PrimaryIP.AssigneeID == nil {
		t.Errorf("created %+v with %+v", created.PrimaryIP, created.Action)
	}
	waitActions(t, test, *created.Action)
	if got := getServer(t, test, server.Server.ID); got.PublicNet.IPv6 == nil || got.PublicNet.IPv6.IP != created.PrimaryIP.IP {
		t.Errorf("the server's IPv6 is %+v", got.PublicNet.IPv6)
	}

	spare := createPrimaryIP(t, test, map[string]any{"name": "four", "type": "ipv4", "location": "fsn1", "assignee_type": "server"})
	other := createServer(t, test, map[string]any{
		"name": "db", "server_type": "cpx22", "image": "debian-13", "location": "fsn1",
		"public_net": map[string]any{"ipv4": spare.PrimaryIP.ID, "enable_ipv6": false},
	})
	if other.Server.PublicNet.IPv4 == nil || other.Server.PublicNet.IPv4.ID != spare.PrimaryIP.ID || other.Server.PublicNet.IPv6 != nil {
		t.Errorf("the server's public net is %+v", other.Server.PublicNet)
	}
	var list schema.PrimaryIPListResponse
	test.client.Get("/v1/primary_ips?name=four").Decode(t, &list)
	if len(list.PrimaryIPs) != 1 || *list.PrimaryIPs[0].AssigneeID != other.Server.ID {
		t.Errorf("filtered %+v", list.PrimaryIPs)
	}
	test.client.Get("/v1/primary_ips?ip="+spare.PrimaryIP.IP).Decode(t, &list)
	if len(list.PrimaryIPs) != 1 {
		t.Errorf("by address %+v", list.PrimaryIPs)
	}
}
