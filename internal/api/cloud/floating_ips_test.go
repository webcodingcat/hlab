package cloud_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/platform"
)

// createFloatingIP creates a Floating IP and fails the test if it cannot.
func createFloatingIP(t *testing.T, test *testLab, body map[string]any) schema.FloatingIPCreateResponse {
	t.Helper()
	response := test.client.Do(http.MethodPost, "/v1/floating_ips", body)
	if response.Status != http.StatusCreated {
		t.Fatalf("POST /v1/floating_ips answered %d %s", response.Status, response.Body)
	}
	var created schema.FloatingIPCreateResponse
	response.Decode(t, &created)
	return created
}

// getFloatingIP reads a Floating IP.
func getFloatingIP(t *testing.T, test *testLab, id int64) schema.FloatingIP {
	t.Helper()
	var answer schema.FloatingIPGetResponse
	test.client.Get(fmt.Sprintf("/v1/floating_ips/%d", id)).Decode(t, &answer)
	return answer.FloatingIP
}

// floatingIPAction runs a Floating IP action and returns the answer.
func floatingIPAction(test *testLab, id int64, command string, body any) *responseOf {
	return &responseOf{test.client.Do(http.MethodPost, fmt.Sprintf("/v1/floating_ips/%d/actions/%s", id, command), body)}
}

// The errors of a create, as Hetzner answers them (measured 2026-11-08):
// the type alone first, then the other fields; field is the field whose
// message the answer names.
func TestFloatingIPCreateErrors(t *testing.T) {
	test := newTestLab(t)
	for name, want := range map[string]struct {
		body          map[string]any
		status        int
		code, message string
		fields        map[string]string
	}{
		"no type":           {map[string]any{"server": 1}, 422, "invalid_input", "invalid input in field 'type'", map[string]string{"type": "'type' is required"}},
		"an unknown type":   {map[string]any{"type": "ipv5", "home_location": "fsn1"}, 422, "invalid_input", "invalid input in field 'type'", map[string]string{"type": "invalid"}},
		"no place":          {map[string]any{"type": "ipv4"}, 422, "invalid_input", "invalid input in fields 'server', 'home_location'", map[string]string{"server": "either server or home_location must be provided", "home_location": "either server or home_location must be provided"}},
		"an unknown home":   {map[string]any{"type": "ipv4", "home_location": "bogus"}, 422, "invalid_input", "invalid input in field 'home_location'", map[string]string{"home_location": "invalid"}},
		"a home by its ID":  {map[string]any{"type": "ipv4", "home_location": "1"}, 422, "invalid_input", "invalid input in field 'home_location'", map[string]string{"home_location": "invalid"}},
		"a long name":       {map[string]any{"type": "ipv4", "home_location": "fsn1", "name": strings.Repeat("x", 300)}, 422, "invalid_input", "invalid input in field 'name'", map[string]string{"name": "value required to be smaller"}},
		"an unknown server": {map[string]any{"type": "ipv4", "server": 999999999}, 404, "not_found", "server not found", nil},
	} {
		response := &responseOf{test.client.Do(http.MethodPost, "/v1/floating_ips", want.body)}
		expectError(t, name, response, want.status, want.code, want.message)
		if want.fields == nil {
			continue
		}
		messages := fieldMessages(t, errorOf(t, response.Response))
		for field, message := range want.fields {
			if got := messages[field]; len(got) != 1 || got[0] != message {
				t.Errorf("%s: the field %s has %q, want %q", name, field, got, message)
			}
		}
	}
}

// A Floating IP's record, update, protection and delete, as Hetzner
// answers (measured 2026-11-08).
func TestFloatingIPLifeCycle(t *testing.T) {
	test := newTestLab(t)
	// Hetzner takes label keys its rules refuse elsewhere, and an empty
	// name as none
	created := createFloatingIP(t, test, map[string]any{"type": "ipv4", "home_location": "fsn1", "name": "", "labels": map[string]string{"bad key!": "x"}})
	ip := created.FloatingIP
	if created.Action != nil || ip.Name != fmt.Sprintf("floating_ip-%d", ip.ID) || ip.Description != nil || ip.Server != nil ||
		ip.Blocked || ip.HomeLocation.Name != "fsn1" || ip.Labels["bad key!"] != "x" {
		t.Errorf("created %+v, action %+v", ip, created.Action)
	}
	if want := "static." + reversed(ip.IP) + ".clients.your-server.de"; len(ip.DNSPtr) != 1 || ip.DNSPtr[0].IP != ip.IP || ip.DNSPtr[0].DNSPtr != want {
		t.Errorf("dns_ptr %+v, want %s", ip.DNSPtr, want)
	}
	six := createFloatingIP(t, test, map[string]any{"type": "ipv6", "home_location": "nbg1", "name": "six", "description": "spare"}).FloatingIP
	if !strings.HasSuffix(six.IP, "::/64") || len(six.DNSPtr) != 0 || six.Description == nil || *six.Description != "spare" {
		t.Errorf("the IPv6 is %+v", six)
	}
	expectError(t, "a taken name", &responseOf{test.client.Do(http.MethodPost, "/v1/floating_ips", map[string]any{"type": "ipv4", "home_location": "fsn1", "name": "six"})},
		http.StatusConflict, "uniqueness_error", "Floating IP name is already used")
	expectError(t, "a missing IP", &responseOf{test.client.Get("/v1/floating_ips/999999999")},
		http.StatusNotFound, "not_found", "Floating IP with ID 999999999 not found")

	path := fmt.Sprintf("/v1/floating_ips/%d", ip.ID)
	var updated schema.FloatingIPUpdateResponse
	test.client.Do(http.MethodPut, path, map[string]any{"name": "four", "description": "main", "labels": map[string]string{"env": "dev"}}).Decode(t, &updated)
	if updated.FloatingIP.Name != "four" || *updated.FloatingIP.Description != "main" || updated.FloatingIP.Labels["env"] != "dev" {
		t.Errorf("updated %+v", updated.FloatingIP)
	}
	// an empty name and a null description change nothing
	test.client.Do(http.MethodPut, path, map[string]any{"name": "", "description": nil}).Decode(t, &updated)
	if updated.FloatingIP.Name != "four" || updated.FloatingIP.Description == nil || *updated.FloatingIP.Description != "main" {
		t.Errorf("updated with an empty name and no description %+v", updated.FloatingIP)
	}
	expectError(t, "a taken name", &responseOf{test.client.Do(http.MethodPut, path, map[string]any{"name": "six"})},
		http.StatusConflict, "uniqueness_error", "Floating IP name is already used")

	expectError(t, "an unassign of an unassigned IP", floatingIPAction(test, ip.ID, "unassign", nil),
		http.StatusUnprocessableEntity, "service_error", fmt.Sprintf("Floating IP with ID %d is not assigned to any resource", ip.ID))
	if response := floatingIPAction(test, ip.ID, "change_protection", map[string]bool{"delete": true}); response.Status != http.StatusCreated {
		t.Fatalf("change_protection answered %d %s", response.Status, response.Body)
	}
	expectError(t, "a protected delete", &responseOf{test.client.Do(http.MethodDelete, path, nil)},
		http.StatusLocked, "protected", "Floating IP deletion is protected")
	floatingIPAction(test, ip.ID, "change_protection", map[string]bool{"delete": false})
	if status := test.client.Do(http.MethodDelete, path, nil).Status; status != http.StatusNoContent {
		t.Errorf("the delete answered %d", status)
	}
	expectError(t, "a deleted IP", &responseOf{test.client.Get(path)},
		http.StatusNotFound, "not_found", fmt.Sprintf("Floating IP with ID %d not found", ip.ID))
}

// The lists of Floating IPs and of their actions, as hcloud-go asks for
// them. Hetzner ignores an unknown sort value and the filter ip (measured
// 2026-11-08).
func TestFloatingIPLists(t *testing.T) {
	test := newTestLab(t)
	first := createFloatingIP(t, test, map[string]any{"type": "ipv4", "home_location": "fsn1", "name": "first", "labels": map[string]string{"env": "dev"}}).FloatingIP
	second := createFloatingIP(t, test, map[string]any{"type": "ipv6", "home_location": "fsn1", "name": "second"}).FloatingIP
	listed := func(query string) []schema.FloatingIP {
		var list schema.FloatingIPListResponse
		test.client.Get("/v1/floating_ips?"+query).Decode(t, &list)
		return list.FloatingIPs
	}
	if got := listed("label_selector=env%3Ddev"); len(got) != 1 || got[0].ID != first.ID {
		t.Errorf("selected by env=dev %+v", got)
	}
	if got := listed("name=second"); len(got) != 1 || got[0].ID != second.ID {
		t.Errorf("by name %+v", got)
	}
	for sort, want := range map[string]int64{"id": first.ID, "id:desc": second.ID, "created": first.ID, "created:desc": second.ID, "bogus": first.ID} {
		if got := listed("sort=" + sort); len(got) != 2 || got[0].ID != want {
			t.Errorf("sorted by %s: %+v, want %d first", sort, got, want)
		}
	}
	if got := listed("ip=" + first.IP); len(got) != 2 {
		t.Errorf("by ip, which Hetzner ignores: %+v", got)
	}

	var changed schema.ActionGetResponse
	floatingIPAction(test, first.ID, "change_protection", map[string]bool{"delete": true}).Decode(t, &changed)
	var actions schema.ActionListResponse
	test.client.Get("/v1/floating_ips/actions").Decode(t, &actions)
	if len(actions.Actions) != 1 || actions.Actions[0].ID != changed.Action.ID || actions.Actions[0].Resources[0].Type != "floating_ip" {
		t.Errorf("the Floating IPs' actions are %+v", actions.Actions)
	}
	test.client.Get(fmt.Sprintf("/v1/floating_ips/%d/actions", second.ID)).Decode(t, &actions)
	if len(actions.Actions) != 0 {
		t.Errorf("the second IP has actions %+v", actions.Actions)
	}
	var one schema.ActionGetResponse
	test.client.Get(fmt.Sprintf("/v1/floating_ips/actions/%d", changed.Action.ID)).Decode(t, &one)
	if one.Action.ID != changed.Action.ID {
		t.Errorf("GET /floating_ips/actions/{id} answered %+v", one.Action)
	}
}

// assignFloatingIP runs an assign and returns its action.
func assignFloatingIP(t *testing.T, test *testLab, id, serverID int64) schema.Action {
	t.Helper()
	response := floatingIPAction(test, id, "assign", map[string]any{"server": serverID})
	if response.Status != http.StatusCreated {
		t.Fatalf("assign answered %d %s", response.Status, response.Body)
	}
	var answer schema.ActionGetResponse
	response.Decode(t, &answer)
	return answer.Action
}

// Assign, move, unassign and a server's delete, as Hetzner answers
// (measured 2026-11-08): the record names the new server as soon as the
// assign runs, the server is not locked, an unassign may run beside an
// assign, another network zone is refused.
func TestFloatingIPAssignMoveAndUnassign(t *testing.T) {
	test := newTestLab(t)
	off := map[string]any{"server_type": "cpx22", "image": "debian-13", "start_after_create": false}
	with := func(name, location string) map[string]any {
		body := map[string]any{"name": name, "location": location}
		for k, v := range off {
			body[k] = v
		}
		return body
	}
	first := createServer(t, test, with("first", "fsn1"))
	ip := createFloatingIP(t, test, map[string]any{"type": "ipv4", "home_location": "fsn1"}).FloatingIP
	second := createServer(t, test, with("second", "nbg1"))
	far := createServer(t, test, with("far", "sin"))
	waitActions(t, test, first.Action, second.Action, far.Action)

	for name, body := range map[string]any{"no server": map[string]any{}, "server 0": map[string]any{"server": 0}} {
		expectError(t, name, floatingIPAction(test, ip.ID, "assign", body), http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'server'")
	}
	expectError(t, "an unknown server", floatingIPAction(test, ip.ID, "assign", map[string]any{"server": 999999999}),
		http.StatusNotFound, "not_found", "server not found")
	expectError(t, "a server name", floatingIPAction(test, ip.ID, "assign", map[string]any{"server": "first"}),
		http.StatusBadRequest, "json_error", "A valid JSON document is required.")
	expectError(t, "another network zone", floatingIPAction(test, ip.ID, "assign", map[string]any{"server": far.Server.ID}),
		http.StatusUnprocessableEntity, "service_error", "assigning Floating IP to a Server in another network_zone is not supported")

	assigned := assignFloatingIP(t, test, ip.ID, first.Server.ID)
	if assigned.Command != "assign_floating_ip" || len(assigned.Resources) != 2 || assigned.Resources[1].ID != first.Server.ID {
		t.Errorf("the assign is %+v", assigned)
	}
	if got := getFloatingIP(t, test, ip.ID); got.Server == nil || *got.Server != first.Server.ID {
		t.Errorf("while the assign runs the IP is %+v", got)
	}
	waitActions(t, test, assigned)
	if got := getServer(t, test, first.Server.ID); len(got.PublicNet.FloatingIPs) != 1 || got.PublicNet.FloatingIPs[0] != ip.ID {
		t.Errorf("the server's Floating IPs are %v", got.PublicNet.FloatingIPs)
	}
	path := fmt.Sprintf("/v1/floating_ips/%d", ip.ID)
	expectError(t, "deleting an assigned IP", &responseOf{test.client.Do(http.MethodDelete, path, nil)},
		http.StatusUnprocessableEntity, "must_be_unassigned", "IP must be unassigned")
	// an assign to its own server ends at once
	if again := assignFloatingIP(t, test, ip.ID, first.Server.ID); again.Status != "success" {
		t.Errorf("an assign to the same server is %s", again.Status)
	}

	moved := assignFloatingIP(t, test, ip.ID, second.Server.ID)
	waitActions(t, test, moved)
	if got := getFloatingIP(t, test, ip.ID); got.Server == nil || *got.Server != second.Server.ID {
		t.Errorf("after the move the IP is %+v", got)
	}
	if got := getServer(t, test, first.Server.ID); len(got.PublicNet.FloatingIPs) != 0 {
		t.Errorf("the first server still has %v", got.PublicNet.FloatingIPs)
	}

	var unassigned schema.ActionGetResponse
	floatingIPAction(test, ip.ID, "unassign", nil).Decode(t, &unassigned)
	if unassigned.Action.Command != "unassign_floating_ip" || unassigned.Action.Resources[1].ID != second.Server.ID {
		t.Errorf("the unassign is %+v", unassigned.Action)
	}
	waitActions(t, test, unassigned.Action)
	if got := getFloatingIP(t, test, ip.ID); got.Server != nil {
		t.Errorf("after the unassign the IP is on %d", *got.Server)
	}

	// created with a server; the server's delete leaves it unassigned
	onSecond := createFloatingIP(t, test, map[string]any{"type": "ipv6", "server": second.Server.ID})
	if onSecond.Action == nil || onSecond.Action.Command != "assign_floating_ip" || onSecond.FloatingIP.HomeLocation.Name != "nbg1" ||
		onSecond.FloatingIP.Server == nil {
		t.Errorf("created %+v with %+v", onSecond.FloatingIP, onSecond.Action)
	}
	waitActions(t, test, *onSecond.Action)
	test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/servers/%d", second.Server.ID), nil)
	if got := getFloatingIP(t, test, onSecond.FloatingIP.ID); got.Server != nil {
		t.Errorf("after the server's delete the IP is on %d", *got.Server)
	}
}

// An assign flags its IP: a second assign is 423 with Hetzner's own text,
// a delete with the general one, while an unassign runs beside it
// (measured 2026-11-08).
func TestFloatingIPAssignHoldsOffAnotherAssign(t *testing.T) {
	test := newTestLab(t)
	first := createServer(t, test, map[string]any{"name": "first", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	second := createServer(t, test, map[string]any{"name": "second", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	waitActions(t, test, first.Action, second.Action)
	ip := createFloatingIP(t, test, map[string]any{"type": "ipv4", "home_location": "fsn1"}).FloatingIP

	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	waiting, release := agent.HoldNext(agentproto.PictureLinks)
	assigned := assignFloatingIP(t, test, ip.ID, first.Server.ID)
	<-waiting
	expectError(t, "a second assign", floatingIPAction(test, ip.ID, "assign", map[string]any{"server": second.Server.ID}),
		http.StatusLocked, "locked", "cannot assign Floating IP because it is already being assigned")
	expectError(t, "a delete", &responseOf{test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/floating_ips/%d", ip.ID), nil)},
		http.StatusLocked, "locked", "cannot perform operation because floating_ip is locked")
	if response := floatingIPAction(test, ip.ID, "unassign", nil); response.Status != http.StatusCreated {
		t.Errorf("an unassign beside the assign answered %d %s", response.Status, response.Body)
	}
	release()
	waitActions(t, test, assigned)
}

// A PUT checks the labels, with texts of its own, though a create takes
// any (measured 2026-11-08).
func TestFloatingIPUpdateChecksLabels(t *testing.T) {
	test := newTestLab(t)
	ip := createFloatingIP(t, test, map[string]any{"type": "ipv4", "home_location": "fsn1"}).FloatingIP
	response := &responseOf{test.client.Do(http.MethodPut, fmt.Sprintf("/v1/floating_ips/%d", ip.ID), map[string]any{"labels": map[string]string{"bad key!": "x"}})}
	expectError(t, "a bad label key", response, http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'labels'")
	if got := fieldMessages(t, errorOf(t, response.Response))["labels"]; len(got) != 1 || got[0] != "invalid label value: key contains invalid characters or is malformed" {
		t.Errorf("the field labels has %q", got)
	}
}

// A server whose create runs takes no Floating IP yet (measured
// 2026-11-08).
func TestFloatingIPWaitsForTheServersCreate(t *testing.T) {
	test := newTestLab(t)
	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	ip := createFloatingIP(t, test, map[string]any{"type": "ipv4", "home_location": "fsn1"}).FloatingIP
	waiting, release := agent.HoldNext(agentproto.PictureLinks)
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	<-waiting // the create sends the pictures with the server
	expectError(t, "a server whose create runs", floatingIPAction(test, ip.ID, "assign", map[string]any{"server": created.Server.ID}),
		http.StatusLocked, "locked", "resource is locked")
	release()
	waitActions(t, test, created.Action)
	waitActions(t, test, assignFloatingIP(t, test, ip.ID, created.Server.ID))
}
