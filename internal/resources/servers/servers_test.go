package servers_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
)

func TestServers(t *testing.T) {
	t.Run("SRV-04", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t, servers.New(servers.Deps{
			Catalog:     cat,
			PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
			Machines:    apitest.NoopMachines{},
		}))

		var nets []netip.Prefix
		for _, name := range []string{"srv-a", "srv-b"} {
			resp := env.Do(http.MethodPost, "/servers", map[string]any{
				"name":               name,
				"server_type":        "cx23",
				"image":              "ubuntu-24.04",
				"location":           "fsn1",
				"start_after_create": true,
			})
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /servers %s: status %d, want 201", name, resp.StatusCode)
			}
			var body struct {
				Server struct {
					PublicNet struct {
						IPv6 *struct {
							IP string `json:"ip"`
						} `json:"ipv6"`
					} `json:"public_net"`
				} `json:"server"`
			}
			apitest.Decode(t, resp, &body)
			if body.Server.PublicNet.IPv6 == nil {
				t.Fatalf("%s: public_net.ipv6 is null", name)
			}
			p, err := netip.ParsePrefix(body.Server.PublicNet.IPv6.IP)
			if err != nil || !p.Addr().Is6() || p.Bits() != 64 {
				t.Fatalf("%s: public_net.ipv6.ip = %q, want an IPv6 /64", name, body.Server.PublicNet.IPv6.IP)
			}
			nets = append(nets, p.Masked())
		}
		if nets[0] == nets[1] {
			t.Errorf("both servers got IPv6 %s, want distinct /64s", nets[0])
		}
	})

	t.Run("CAT-04", func(t *testing.T) {
		env := newEnv(t, apitest.NoopMachines{})
		resp := env.Do(http.MethodPost, "/servers", map[string]any{
			"name":        "arm",
			"server_type": "cax11",
			"image":       "ubuntu-24.04",
			"location":    "fsn1",
		})
		if resp.StatusCode != http.StatusPreconditionFailed {
			t.Fatalf("POST /servers cax11: status %d, want 412", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "resource_unavailable" {
			t.Errorf("POST /servers cax11: error.code %q, want resource_unavailable", code)
		}
		var list struct {
			Servers []struct {
				ID int64 `json:"id"`
			} `json:"servers"`
		}
		apitest.Decode(t, env.Do(http.MethodGet, "/servers", nil), &list)
		if len(list.Servers) != 0 {
			t.Errorf("GET /servers lists %d servers after the refused create, want none", len(list.Servers))
		}
		if acts := listActions(t, env, "/servers/actions"); len(acts) != 0 {
			t.Errorf("GET /servers/actions lists %d actions after the refused create, want none", len(acts))
		}
	})

	t.Run("API-08", func(t *testing.T) {
		machines := apitest.NewBlockingMachines()
		env := newEnv(t, machines)
		a, actA := createServer(t, env, "act-a", nil)
		b, actB := createServer(t, env, "act-b", nil)

		running := env.GetAction(actA)
		if running.Status != "running" || running.Progress >= 100 || running.Finished != nil {
			t.Errorf("create action while its work blocks: status %q, progress %d, finished %v; want running, below 100, null",
				running.Status, running.Progress, running.Finished)
		}

		machines.Release()
		done := env.WaitAction(actA)
		env.WaitAction(actB)
		if done.Status != "success" || done.Progress != 100 || done.Finished == nil {
			t.Errorf("create action after its work: status %q, progress %d, finished %v; want success, 100, set",
				done.Status, done.Progress, done.Finished)
		}

		want := rawAction(t, env, fmt.Sprintf("/actions/%d", actA))
		for _, path := range []string{"/servers/actions", fmt.Sprintf("/servers/%d/actions", a)} {
			var found map[string]any
			for _, act := range listActions(t, env, path) {
				if id, _ := act["id"].(float64); int64(id) == actA {
					found = act
				}
			}
			if !reflect.DeepEqual(found, want) {
				t.Errorf("GET %s: action %d = %v, want %v", path, actA, found, want)
			}
		}
		path := fmt.Sprintf("/servers/%d/actions/%d", a, actA)
		if got := rawAction(t, env, path); !reflect.DeepEqual(got, want) {
			t.Errorf("GET %s = %v, want %v", path, got, want)
		}

		path = fmt.Sprintf("/servers/%d/actions/%d", a, actB)
		resp := env.Do(http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s (action of server %d): status %d, want 404", path, b, resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "not_found" {
			t.Errorf("GET %s: error.code %q, want not_found", path, code)
		}

		failing := apitest.NewBlockingMachines()
		fenv := newEnv(t, failing)
		_, actF := createServer(t, fenv, "act-fail", nil)
		failing.Fail()
		failed := fenv.WaitAction(actF)
		if failed.Status != "error" || failed.Error == nil || failed.Error.Code == "" || failed.Error.Message == "" {
			t.Errorf("create action with failing work: status %q, error %+v; want error with code and message",
				failed.Status, failed.Error)
		}
	})

	t.Run("API-12", func(t *testing.T) {
		env := newEnv(t, apitest.NoopMachines{})
		id, act := createServer(t, env, "prot", nil)
		env.WaitAction(act)

		changeProtection(t, env, id, map[string]any{"delete": true})
		if p := serverProtection(t, env, id); !p.Delete {
			t.Errorf("protection after change_protection delete=true: %+v, want delete true", p)
		}
		resp := env.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
		if resp.StatusCode != http.StatusLocked {
			t.Errorf("DELETE protected server: status %d, want 423", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "protected" {
			t.Errorf("DELETE protected server: error.code %q, want protected", code)
		}
		if resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET server after refused delete: status %d, want 200", resp.StatusCode)
		}

		changeProtection(t, env, id, map[string]any{"delete": false})
		if p := serverProtection(t, env, id); p.Delete {
			t.Errorf("protection after change_protection delete=false: %+v, want delete false", p)
		}
		deleteServer(t, env, id)
	})

	t.Run("API-13", func(t *testing.T) {
		machines := apitest.NewBlockingMachines()
		env := newEnv(t, machines)
		id, act := createServer(t, env, "lock", nil)

		resp := env.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
		if resp.StatusCode != http.StatusLocked {
			t.Errorf("DELETE during create: status %d, want 423", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "locked" {
			t.Errorf("DELETE during create: error.code %q, want locked", code)
		}
		resp = env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/change_protection", id), map[string]any{"rebuild": true})
		if resp.StatusCode != http.StatusLocked {
			t.Errorf("change_protection during create: status %d, want 423", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "locked" {
			t.Errorf("change_protection during create: error.code %q, want locked", code)
		}

		machines.Release()
		env.WaitAction(act)
		changeProtection(t, env, id, map[string]any{"rebuild": true})
		deleteServer(t, env, id)
	})

	t.Run("SRV-17", func(t *testing.T) {
		env := newEnv(t, apitest.NoopMachines{})
		id, act := createServer(t, env, "console", nil)
		env.WaitAction(act)
		path := fmt.Sprintf("/servers/%d/actions", id)
		before := len(listActions(t, env, path))

		resp := env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/request_console", id), nil)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("request_console: status %d, want 422", resp.StatusCode)
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		apitest.Decode(t, resp, &body)
		if body.Error.Code != "unsupported_error" || body.Error.Message != "server console is not supported by hlab yet" {
			t.Errorf("request_console: error %+v, want unsupported_error with the SPEC message", body.Error)
		}
		if after := len(listActions(t, env, path)); after != before {
			t.Errorf("GET %s: %d actions after request_console, want %d", path, after, before)
		}
	})
}

// newEnv mounts the servers module on the shipped catalog with machines,
// plus the actions module for /actions/{id}.
func newEnv(t *testing.T, machines servers.Machines) *apitest.Env {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	return apitest.New(t, servers.New(servers.Deps{
		Catalog:     cat,
		PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
		Machines:    machines,
	}), resactions.New())
}

// createServer creates a cx23 server with the fields in extra added and
// returns its ID and the ID of its create action.
func createServer(t *testing.T, env *apitest.Env, name string, extra map[string]any) (id, action int64) {
	t.Helper()
	req := map[string]any{
		"name":        name,
		"server_type": "cx23",
		"image":       "ubuntu-24.04",
		"location":    "fsn1",
	}
	for k, v := range extra {
		req[k] = v
	}
	resp := env.Do(http.MethodPost, "/servers", req)
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

func rawAction(t *testing.T, env *apitest.Env, path string) map[string]any {
	t.Helper()
	resp := env.Do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	var body struct {
		Action map[string]any `json:"action"`
	}
	apitest.Decode(t, resp, &body)
	return body.Action
}

func listActions(t *testing.T, env *apitest.Env, path string) []map[string]any {
	t.Helper()
	resp := env.Do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	var body struct {
		Actions []map[string]any `json:"actions"`
	}
	apitest.Decode(t, resp, &body)
	return body.Actions
}

// changeProtection runs change_protection and waits for its success.
func changeProtection(t *testing.T, env *apitest.Env, id int64, body map[string]any) {
	t.Helper()
	resp := env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/change_protection", id), body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("change_protection %v: status %d, want 201", body, resp.StatusCode)
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if a := env.WaitAction(b.Action.ID); a.Status != "success" {
		t.Fatalf("change_protection %v: action %q, want success", body, a.Status)
	}
}

type protection struct {
	Delete  bool `json:"delete"`
	Rebuild bool `json:"rebuild"`
}

func serverProtection(t *testing.T, env *apitest.Env, id int64) protection {
	t.Helper()
	resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var body struct {
		Server struct {
			Protection protection `json:"protection"`
		} `json:"server"`
	}
	apitest.Decode(t, resp, &body)
	return body.Server.Protection
}

// deleteServer deletes the server, waits for the delete action and checks
// the server is gone.
func deleteServer(t *testing.T, env *apitest.Env, id int64) {
	t.Helper()
	resp := env.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if a := env.WaitAction(b.Action.ID); a.Status != "success" {
		t.Fatalf("DELETE /servers/%d: action %q, want success", id, a.Status)
	}
	if resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /servers/%d after delete: status %d, want 404", id, resp.StatusCode)
	}
}
