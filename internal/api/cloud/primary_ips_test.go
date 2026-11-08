package cloud_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
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
	// Hetzner's default name of an IPv4 (measured 2026-11-08)
	if want := "static." + reversed(ip.IP) + ".clients.your-server.de"; len(ip.DNSPtr) != 1 || ip.DNSPtr[0].DNSPtr != want {
		t.Errorf("dns_ptr %+v, want %s", ip.DNSPtr, want)
	}
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

// A Primary IP a server cannot take: another server holds it, it is in
// another location, it has the other type. Hetzner answers a create and an
// assign with other codes and texts (measured 2026-11-08).
func TestPrimaryIPsAServerCannotTake(t *testing.T) {
	test := newTestLab(t)
	off := map[string]any{"server_type": "cpx22", "image": "debian-13", "location": "fsn1", "start_after_create": false}
	with := func(name string, publicNet map[string]any) map[string]any {
		body := map[string]any{"name": name, "public_net": publicNet}
		for key, value := range off {
			body[key] = value
		}
		return body
	}
	holder := createServer(t, test, with("holder", nil))
	other := createServer(t, test, with("other", map[string]any{"enable_ipv4": false}))
	waitActions(t, test, holder.Action, other.Action)
	held := holder.Server.PublicNet.IPv4.ID
	six := holder.Server.PublicNet.IPv6.ID
	far := createPrimaryIP(t, test, map[string]any{"name": "far", "type": "ipv4", "location": "nbg1"}).PrimaryIP.ID

	for name, test2 := range map[string]struct {
		publicNet     map[string]any
		status        int
		code, message string
	}{
		"held by another":  {map[string]any{"ipv4": held}, 422, "primary_ip_assigned", "primary ip already assigned to another server"},
		"another location": {map[string]any{"ipv4": far}, 422, "primary_ip_location_mismatch", "primary ip and server located in different locations"},
		"the other type":   {map[string]any{"ipv4": six}, 412, "primary_ip_version_mismatch", "primary ip has a wrong ip version"},
	} {
		expectError(t, "a create with an IP "+name, &responseOf{test.client.Do(http.MethodPost, "/v1/servers", with("new", test2.publicNet))},
			test2.status, test2.code, test2.message)
	}
	assign := func(ip, server int64) *responseOf {
		return primaryIPAction(test, ip, "assign", map[string]any{"assignee_id": server, "assignee_type": "server"})
	}
	expectError(t, "assigning an IP held by another", assign(held, other.Server.ID),
		http.StatusUnprocessableEntity, "primary_ip_already_assigned", fmt.Sprintf("Primary IP with ID %d is already assigned to a Resource", held))
	expectError(t, "assigning an IP of another location", assign(far, other.Server.ID),
		http.StatusUnprocessableEntity, "service_error", "Resource and Primary IP are in different locations")
	expectError(t, "assigning to a missing server", assign(far, 999999999),
		http.StatusNotFound, "not_found", "resource not found")
}

func TestPrimaryIPCreateErrors(t *testing.T) {
	test := newTestLab(t)
	// measured 2026-11-08: neither or both of assignee_id and location
	place := "either assignee_id or location must be provided"
	for name, body := range map[string]map[string]any{
		"no place":    {"name": "x", "type": "ipv4", "assignee_type": "server"},
		"both places": {"name": "x", "type": "ipv4", "assignee_type": "server", "location": "fsn1", "assignee_id": 1},
	} {
		response := &responseOf{test.client.Do(http.MethodPost, "/v1/primary_ips", body)}
		expectError(t, name, response, http.StatusUnprocessableEntity, "invalid_input", "invalid input in fields 'assignee_id', 'location'")
		messages := fieldMessages(t, errorOf(t, response.Response))
		for _, field := range []string{"assignee_id", "location"} {
			if got := messages[field]; len(got) != 1 || got[0] != place {
				t.Errorf("%s: the field %s has %q, want %q", name, field, got, place)
			}
		}
	}
	response := &responseOf{test.client.Do(http.MethodPost, "/v1/primary_ips",
		map[string]any{"name": "x", "type": "bogus", "assignee_type": "server", "location": "fsn1"})}
	expectError(t, "an unknown type", response, http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'type'")
	if got := fieldMessages(t, errorOf(t, response.Response))["type"]; len(got) != 1 || got[0] != "value required to be smaller" {
		t.Errorf("an unknown type: the field type has %q", got)
	}
}

// The lists of Primary IPs and of their actions, as hcloud-go asks for
// them (PrimaryIPListOpts, the action lists).
func TestPrimaryIPLists(t *testing.T) {
	test := newTestLab(t)
	first := createPrimaryIP(t, test, map[string]any{
		"name": "first", "type": "ipv4", "location": "fsn1", "assignee_type": "server", "labels": map[string]string{"env": "dev"},
	}).PrimaryIP
	second := createPrimaryIP(t, test, map[string]any{"name": "second", "type": "ipv6", "location": "fsn1", "assignee_type": "server"}).PrimaryIP

	listed := func(query string) []schema.PrimaryIP {
		var list schema.PrimaryIPListResponse
		test.client.Get("/v1/primary_ips?"+query).Decode(t, &list)
		return list.PrimaryIPs
	}
	if got := listed("label_selector=env%3Ddev"); len(got) != 1 || got[0].ID != first.ID {
		t.Errorf("selected by env=dev %+v", got)
	}
	if got := listed("label_selector=!env"); len(got) != 1 || got[0].ID != second.ID {
		t.Errorf("selected by !env %+v", got)
	}
	for sort, want := range map[string]int64{"id": first.ID, "id:desc": second.ID, "created": first.ID, "created:desc": second.ID} {
		if got := listed("sort=" + sort); len(got) != 2 || got[0].ID != want {
			t.Errorf("sorted by %s: %+v, want %d first", sort, got, want)
		}
	}
	if response := test.client.Get("/v1/primary_ips?sort=name"); response.Status != http.StatusUnprocessableEntity {
		t.Errorf("an unknown sort: %d", response.Status)
	}

	change := primaryIPAction(test, first.ID, "change_protection", map[string]bool{"delete": true})
	var changed schema.ActionGetResponse
	change.Decode(t, &changed)
	var actions schema.ActionListResponse
	test.client.Get("/v1/primary_ips/actions").Decode(t, &actions)
	if len(actions.Actions) != 1 || actions.Actions[0].ID != changed.Action.ID {
		t.Errorf("the Primary IPs' actions are %+v", actions.Actions)
	}
	test.client.Get(fmt.Sprintf("/v1/primary_ips/%d/actions", first.ID)).Decode(t, &actions)
	if len(actions.Actions) != 1 || actions.Actions[0].Command != "change_protection" {
		t.Errorf("the first IP's actions are %+v", actions.Actions)
	}
	test.client.Get(fmt.Sprintf("/v1/primary_ips/%d/actions", second.ID)).Decode(t, &actions)
	if len(actions.Actions) != 0 {
		t.Errorf("the second IP has actions %+v", actions.Actions)
	}
	var one schema.ActionGetResponse
	test.client.Get(fmt.Sprintf("/v1/primary_ips/actions/%d", changed.Action.ID)).Decode(t, &one)
	if one.Action.ID != changed.Action.ID {
		t.Errorf("GET /primary_ips/actions/{id} answered %+v", one.Action)
	}
	test.client.Get(fmt.Sprintf("/v1/primary_ips/%d/actions/%d", first.ID, changed.Action.ID)).Decode(t, &one)
	if one.Action.ID != changed.Action.ID {
		t.Errorf("GET /primary_ips/{id}/actions/{id} answered %+v", one.Action)
	}
}

// reversed returns an IPv4 address with its octets backwards, as in its
// reverse DNS name.
func reversed(ip string) string {
	octets := strings.Split(ip, ".")
	slices.Reverse(octets)
	return strings.Join(octets, ".")
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
