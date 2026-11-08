package cloud_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
)

// serverAnswer is what the tests read of a server.
type serverAnswer struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Locked     bool   `json:"locked"`
	ServerType struct {
		Name string `json:"name"`
	} `json:"server_type"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	PrimaryDiskSize int `json:"primary_disk_size"`
	PublicNet       struct {
		IPv4 *schema.ServerPublicNetIPv4 `json:"ipv4"`
		IPv6 *schema.ServerPublicNetIPv6 `json:"ipv6"`
	} `json:"public_net"`
	IncludedTraffic uint64 `json:"included_traffic"`
}

// createAnswer is the answer of POST /servers.
type createAnswer struct {
	Server       serverAnswer    `json:"server"`
	Action       schema.Action   `json:"action"`
	NextActions  []schema.Action `json:"next_actions"`
	RootPassword *string         `json:"root_password"`
}

// createServer creates a server and fails the test if it cannot.
func createServer(t *testing.T, test *testLab, body map[string]any) createAnswer {
	t.Helper()
	response := test.client.Do(http.MethodPost, "/v1/servers", body)
	if response.Status != http.StatusCreated {
		t.Fatalf("POST /v1/servers answered %d %s", response.Status, response.Body)
	}
	var created createAnswer
	response.Decode(t, &created)
	return created
}

// serverAction runs a server action and returns it.
func serverAction(t *testing.T, test *testLab, id int64, command string, body any) schema.Action {
	t.Helper()
	response := test.client.Do(http.MethodPost, fmt.Sprintf("/v1/servers/%d/actions/%s", id, command), body)
	if response.Status != http.StatusCreated {
		t.Fatalf("%s answered %d %s", command, response.Status, response.Body)
	}
	var answer schema.ActionGetResponse
	response.Decode(t, &answer)
	return answer.Action
}

// waitActions waits until the actions have ended and returns them; the
// lab's time scale is 0, so they end at once but in goroutines.
func waitActions(t *testing.T, test *testLab, actions ...schema.Action) []schema.Action {
	t.Helper()
	query := []string{}
	for _, action := range actions {
		query = append(query, fmt.Sprintf("id=%d", action.ID))
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var answer schema.ActionListResponse
		test.client.Get("/v1/actions?"+strings.Join(query, "&")).Decode(t, &answer)
		if !slices.ContainsFunc(answer.Actions, func(a schema.Action) bool { return a.Status == "running" }) {
			return answer.Actions
		}
		if time.Now().After(deadline) {
			t.Fatalf("the actions still run: %+v", answer.Actions)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// getServer reads a server.
func getServer(t *testing.T, test *testLab, id int64) serverAnswer {
	t.Helper()
	var answer struct {
		Server serverAnswer `json:"server"`
	}
	test.client.Get(fmt.Sprintf("/v1/servers/%d", id)).Decode(t, &answer)
	return answer.Server
}

// vmPower returns the power state of a server's VM on the fake machine.
func vmPower(t *testing.T, test *testLab, id int64) platform.PowerState {
	t.Helper()
	state, err := test.machine.Platform().Hypervisor.VMState(t.Context(), lab.ServerVM(id))
	if err != nil {
		t.Fatal(err)
	}
	return state.Power
}

// Hetzner's create answer and the server's life (measured 2026-11-07 and
// 2026-11-08).
func TestServerLifeCycle(t *testing.T) {
	test := newTestLab(t)
	key := createSSHKey(t, test, map[string]any{"name": "admin", "public_key": adminKey})
	created := createServer(t, test, map[string]any{
		"name": "web", "server_type": "cpx22", "image": "debian-13", "location": "hel1",
		"ssh_keys": []any{key.ID}, "labels": map[string]string{"env": "dev"},
	})
	server := created.Server
	if server.Status != "initializing" || server.Locked || server.IncludedTraffic != 0 ||
		server.ServerType.Name != "cpx22" || server.Location.Name != "hel1" || server.PrimaryDiskSize != 80 {
		t.Errorf("created %+v", server)
	}
	if created.RootPassword != nil {
		t.Error("a server with SSH keys got a root password")
	}
	if created.Action.Command != "create_server" || len(created.Action.Resources) != 2 || created.Action.Resources[1].Type != "image" {
		t.Errorf("the action is %+v", created.Action)
	}
	if len(created.NextActions) != 1 || created.NextActions[0].Command != "start_server" {
		t.Errorf("the next actions are %+v", created.NextActions)
	}
	if server.PublicNet.IPv4 == nil || !strings.HasPrefix(server.PublicNet.IPv4.IP, "198.19.") ||
		server.PublicNet.IPv4.DNSPtr == "" || server.PublicNet.IPv6 == nil || !strings.HasSuffix(server.PublicNet.IPv6.IP, "::/64") {
		t.Errorf("the public net is %+v", server.PublicNet)
	}
	waitActions(t, test, append(created.NextActions, created.Action)...)
	if got := getServer(t, test, server.ID); got.Status != "running" || got.IncludedTraffic == 0 {
		t.Errorf("after the start: %+v", got)
	}
	if power := vmPower(t, test, server.ID); power != platform.PowerRunning {
		t.Errorf("the VM is %s", power)
	}
	seed := test.machine.Seed(test.lab.ServerSeed(server.ID))
	if !strings.Contains(string(seed["network-config"]), lab.ServerMAC(server.ID)) ||
		!strings.Contains(string(seed["meta-data"]), adminKey) {
		t.Errorf("the seed is %s / %s", seed["network-config"], seed["meta-data"])
	}

	for _, step := range []struct{ command, status string }{
		{"poweroff", "off"}, {"poweron", "running"}, {"reset", "running"}, {"reboot", "running"},
	} {
		waitActions(t, test, serverAction(t, test, server.ID, step.command, nil))
		if got := getServer(t, test, server.ID); got.Status != step.status {
			t.Errorf("after %s the server is %s, want %s", step.command, got.Status, step.status)
		}
	}
	// a shutdown ends once the guest got the request; the fake's guest
	// obeys at once
	waitActions(t, test, serverAction(t, test, server.ID, "shutdown", nil))
	if power := vmPower(t, test, server.ID); power != platform.PowerOff {
		t.Errorf("after the shutdown the VM is %s", power)
	}

	response := test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/servers/%d", server.ID), nil)
	if response.Status != http.StatusOK {
		t.Fatalf("DELETE answered %d %s", response.Status, response.Body)
	}
	var deleted schema.ServerDeleteResponse
	response.Decode(t, &deleted)
	if deleted.Action.Command != "delete_server" {
		t.Errorf("the delete's action is %+v", deleted.Action)
	}
	// the server is gone at once, its own Primary IPs with it
	if status := test.client.Get(fmt.Sprintf("/v1/servers/%d", server.ID)).Status; status != http.StatusNotFound {
		t.Errorf("GET after the delete answered %d", status)
	}
	var ips schema.PrimaryIPListResponse
	test.client.Get("/v1/primary_ips").Decode(t, &ips)
	if len(ips.PrimaryIPs) != 0 {
		t.Errorf("Primary IPs left: %+v", ips.PrimaryIPs)
	}
	waitActions(t, test, deleted.Action)
	if _, err := test.machine.Platform().Hypervisor.VMState(t.Context(), lab.ServerVM(server.ID)); err == nil {
		t.Error("the VM is still there")
	}
}

// Hetzner runs a server's actions one after another instead of refusing
// them with 423 (measured 2026-11-08: poweron, reset, shutdown and reboot
// sent during a poweroff all ran, in order, and the server ended running).
func TestServerActionsWaitForEachOther(t *testing.T) {
	test := newTestLab(t)
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13"})
	// sent during the create, as the probe did
	actions := []schema.Action{created.Action, created.NextActions[0]}
	for _, command := range []string{"poweroff", "poweron", "reset", "shutdown", "reboot"} {
		actions = append(actions, serverAction(t, test, created.Server.ID, command, nil))
	}
	ended := waitActions(t, test, actions...)
	slices.SortFunc(ended, func(a, b schema.Action) int { return a.Finished.Compare(*b.Finished) })
	var order []string
	for _, action := range ended {
		if action.Status != "success" {
			t.Errorf("%s ended with %+v", action.Command, action.Error)
		}
		order = append(order, action.Command)
	}
	want := []string{"create_server", "start_server", "stop_server", "start_server", "reset_server", "shutdown_server", "reboot_server"}
	if !slices.Equal(order, want) {
		t.Errorf("the actions ended in the order %v, want %v", order, want)
	}
	if got := getServer(t, test, created.Server.ID); got.Status != "running" {
		t.Errorf("the server is %s, want running", got.Status)
	}
}

// A start reserves its room when it is asked for, not when it runs:
// starts of off servers that are sent at once cannot take more memory
// than the host has; Hetzner answers 412 resource_unavailable.
func TestStartsSentAtOnceKeepToTheRoom(t *testing.T) {
	test := newTestLab(t)
	// 16 GB - 6 GB reserve - 1 GB appliance = 9 GB: two cpx22 (4 GB) fit
	startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	var ids []int64
	for _, name := range []string{"a", "b", "c"} {
		created := createServer(t, test, map[string]any{
			"name": name, "server_type": "cpx22", "image": "debian-13", "start_after_create": false,
		})
		ids = append(ids, created.Server.ID)
	}
	// the starts' steps wait, so that none of them has run yet
	test.machine.Delay("VMState", 200*time.Millisecond)
	first := serverAction(t, test, ids[0], "poweron", nil)
	second := serverAction(t, test, ids[1], "poweron", nil)
	third := &responseOf{test.client.Do(http.MethodPost, fmt.Sprintf("/v1/servers/%d/actions/poweron", ids[2]), nil)}
	expectError(t, "a third start", third, http.StatusPreconditionFailed, "resource_unavailable",
		"error during placement")
	waitActions(t, test, first, second)
}

func TestServerWithoutKeysGetsARootPassword(t *testing.T) {
	test := newTestLab(t)
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	if created.RootPassword == nil || len(*created.RootPassword) < 16 {
		t.Errorf("the root password is %v", created.RootPassword)
	}
	if len(created.NextActions) != 0 {
		t.Errorf("start_after_create false has next actions %+v", created.NextActions)
	}
	waitActions(t, test, created.Action)
	if got := getServer(t, test, created.Server.ID); got.Status != "off" {
		t.Errorf("the server is %s, want off", got.Status)
	}
	vendorData := string(test.machine.Seed(test.lab.ServerSeed(created.Server.ID))["vendor-data"])
	if !strings.Contains(vendorData, *created.RootPassword) {
		t.Errorf("the vendor data lacks the password: %s", vendorData)
	}
}

// The errors of a create, as Hetzner answers them (measured 2026-11-08).
func TestServerCreateErrors(t *testing.T) {
	test := newTestLab(t)
	base := map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "location": "fsn1"}
	with := func(key string, value any) map[string]any {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		if value == nil {
			delete(body, key)
		} else {
			body[key] = value
		}
		return body
	}
	for name, test2 := range map[string]struct {
		body           map[string]any
		status         int
		code, message  string
		detailsAreNull bool
	}{
		"an unknown image":    {with("image", "bogus-99"), 422, "invalid_input", "image not found", false},
		"an unknown type":     {with("server_type", "bogus"), 404, "not_found", "server type not found", false},
		"an unknown location": {with("location", "bogus"), 404, "not_found", "datacenter not found", false},
		"a bad name":          {with("name", "bad_name"), 422, "invalid_input", "invalid input in field 'name'", false},
		"no image":            {with("image", nil), 422, "invalid_input", "invalid input in field 'image'", false},
		"an unknown SSH key":  {with("ssh_keys", []any{999999999}), 404, "not_found", "SSH key not found", false},
		"too much user data":  {with("user_data", strings.Repeat("x", 32769)), 422, "invalid_input", "invalid input in field 'user_data'", false},
		"an unknown IPv4":     {with("public_net", map[string]any{"ipv4": 999999999}), 404, "primary_ip_not_found", "primary ip not found", false},
	} {
		response := test.client.Do(http.MethodPost, "/v1/servers", test2.body)
		got := errorOf(t, response)
		if response.Status != test2.status || got.Code != test2.code || got.Message != test2.message {
			t.Errorf("%s: %d %+v, want %d %s %q", name, response.Status, got, test2.status, test2.code, test2.message)
		}
	}
	createServer(t, test, base)
	response := test.client.Do(http.MethodPost, "/v1/servers", base)
	if got := errorOf(t, response); response.Status != http.StatusConflict || got.Message != "server name is already used" {
		t.Errorf("a taken name: %d %+v", response.Status, got)
	}
}

func TestServerDeleteProtection(t *testing.T) {
	test := newTestLab(t)
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13"})
	serverAction(t, test, created.Server.ID, "change_protection", map[string]bool{"delete": true, "rebuild": true})
	path := fmt.Sprintf("/v1/servers/%d", created.Server.ID)
	response := test.client.Do(http.MethodDelete, path, nil)
	// measured 2026-11-08
	if got := errorOf(t, response); response.Status != http.StatusLocked || got.Code != "protected" || got.Message != "server deletion is protected" {
		t.Errorf("deleting a protected server: %d %+v", response.Status, got)
	}
	serverAction(t, test, created.Server.ID, "change_protection", map[string]bool{"delete": false})
	if status := test.client.Do(http.MethodDelete, path, nil).Status; status != http.StatusOK {
		t.Errorf("deleting the unprotected server: %d", status)
	}
}

func TestServerUpdateAndList(t *testing.T) {
	test := newTestLab(t)
	web := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "labels": map[string]string{"role": "web"}})
	createServer(t, test, map[string]any{"name": "db", "server_type": "cpx22", "image": "debian-13"})
	var updated struct {
		Server serverAnswer `json:"server"`
	}
	test.client.Do(http.MethodPut, fmt.Sprintf("/v1/servers/%d", web.Server.ID), map[string]any{"name": "www"}).Decode(t, &updated)
	if updated.Server.Name != "www" {
		t.Errorf("renamed to %+v", updated.Server)
	}
	if response := test.client.Do(http.MethodPut, fmt.Sprintf("/v1/servers/%d", web.Server.ID), map[string]any{"name": "db"}); response.Status != http.StatusConflict {
		t.Errorf("renaming to a taken name: %d", response.Status)
	}
	var list struct {
		Servers []serverAnswer `json:"servers"`
	}
	test.client.Get("/v1/servers?label_selector=role%3Dweb").Decode(t, &list)
	if len(list.Servers) != 1 || list.Servers[0].Name != "www" {
		t.Errorf("selected %+v", list.Servers)
	}
	test.client.Get("/v1/servers?sort=name").Decode(t, &list)
	if len(list.Servers) != 2 || list.Servers[0].Name != "db" {
		t.Errorf("sorted %+v", list.Servers)
	}
	if response := test.client.Get("/v1/servers?status=bogus"); response.Status != http.StatusUnprocessableEntity {
		t.Errorf("an unknown status: %d", response.Status)
	}
}

// The servers' action lists need no IDs; a missing server has none
// (measured 2026-11-07).
func TestServerActionLists(t *testing.T) {
	test := newTestLab(t)
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13"})
	waitActions(t, test, created.Action, created.NextActions[0])
	var list schema.ActionListResponse
	test.client.Get(fmt.Sprintf("/v1/servers/%d/actions?sort=id", created.Server.ID)).Decode(t, &list)
	if len(list.Actions) != 2 || list.Actions[0].Command != "create_server" {
		t.Errorf("the server's actions are %+v", list.Actions)
	}
	test.client.Get("/v1/servers/actions?status=success").Decode(t, &list)
	if len(list.Actions) != 2 {
		t.Errorf("the servers' actions are %+v", list.Actions)
	}
	test.client.Get("/v1/servers/999999999/actions").Decode(t, &list)
	if len(list.Actions) != 0 {
		t.Errorf("a missing server has actions %+v", list.Actions)
	}
	var one schema.ActionGetResponse
	test.client.Get(fmt.Sprintf("/v1/servers/actions/%d", created.Action.ID)).Decode(t, &one)
	if one.Action.ID != created.Action.ID {
		t.Errorf("GET /servers/actions/{id} answered %+v", one.Action)
	}
	test.client.Get(fmt.Sprintf("/v1/servers/%d/actions/%d", created.Server.ID, created.Action.ID)).Decode(t, &one)
	if one.Action.ID != created.Action.ID {
		t.Errorf("GET /servers/{id}/actions/{id} answered %+v", one.Action)
	}
	response := test.client.Get("/v1/servers/999999999/actions/1")
	if got := errorOf(t, response); response.Status != http.StatusNotFound || got.Message != "action not found" {
		t.Errorf("a missing action: %d %+v", response.Status, got)
	}
	if response := test.client.Get("/v1/servers/actions?status=bogus"); response.Status != http.StatusUnprocessableEntity {
		t.Errorf("an unknown status: %d", response.Status)
	}
}

// The create's answer carries the server's JSON in Hetzner's order of
// fields, without a datacenter (measured 2026-11-07).
func TestServerJSONHasHetznersFields(t *testing.T) {
	test := newTestLab(t)
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13"})
	response := test.client.Get(fmt.Sprintf("/v1/servers/%d", created.Server.ID))
	var raw struct {
		Server json.RawMessage `json:"server"`
	}
	response.Decode(t, &raw)
	decoder := json.NewDecoder(strings.NewReader(string(raw.Server)))
	_, _ = decoder.Token()
	var keys []string
	for decoder.More() {
		key, _ := decoder.Token()
		keys = append(keys, key.(string))
		var skip json.RawMessage
		_ = decoder.Decode(&skip)
	}
	want := []string{"id", "name", "status", "server_type", "location", "image", "iso", "primary_disk_size", "labels",
		"protection", "backup_window", "rescue_enabled", "locked", "placement_group", "public_net", "private_net",
		"load_balancers", "volumes", "included_traffic", "ingoing_traffic", "outgoing_traffic", "created"}
	if !slices.Equal(keys, want) {
		t.Errorf("the server's keys are %v", keys)
	}
}
