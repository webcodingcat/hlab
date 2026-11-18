package mgmt_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/mgmt"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/store"
)

// fakeDeleter records the servers it deletes. Delete blocks until gate is
// closed and fails for the IDs in fail.
type fakeDeleter struct {
	gate  chan struct{}
	mu    sync.Mutex
	calls []int64
	fail  map[int64]bool
}

func (d *fakeDeleter) Delete(ctx context.Context, id int64) error {
	d.mu.Lock()
	d.calls = append(d.calls, id)
	d.mu.Unlock()
	select {
	case <-d.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail[id] {
		return errors.New("disk is in use")
	}
	return nil
}

func (d *fakeDeleter) take() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.calls
	d.calls = nil
	slices.Sort(c)
	return c
}

func (d *fakeDeleter) setFail(id int64, fail bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail[id] = fail
}

func TestMgmt(t *testing.T) {
	t.Run("PROJ-04", func(t *testing.T) {
		cat, err := catalog.Load("../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t,
			servers.New(servers.Deps{
				Catalog:     cat,
				PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
				Machines:    apitest.NoopMachines{},
			}),
			sshkeys.New(),
			resactions.New(),
		)
		deleter := &fakeDeleter{gate: make(chan struct{}), fail: map[int64]bool{}}
		h := mgmt.New(mgmt.Deps{
			Store:    env.Store,
			API:      env.Router,
			Status:   func() mgmt.Status { return mgmt.Status{State: "running"} },
			Actions:  env.Actions,
			Deleters: map[string]resource.Deleter{servers.Kind: deleter},
		})

		tokenP := "Bearer " + env.Token
		tokenQ := "Bearer " + env.NewToken("q", false)
		tokenR := "Bearer " + env.NewToken("r", false)
		p1 := createServer(t, env, tokenP, "p-1")
		p2 := createServer(t, env, tokenP, "p-2")
		resp := env.DoAuth(tokenP, http.MethodPost, "/ssh_keys", map[string]any{
			"name":       "p-key",
			"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA42AvjmOSeFhkGqi7LL8SMihzTLFoX9Kd9sDKhkOtDv p",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /ssh_keys in p: status %d, want 201", resp.StatusCode)
		}
		q1 := createServer(t, env, tokenQ, "q-1")
		r1 := createServer(t, env, tokenR, "r-1")

		// Successful deletion of p.
		action := deleteProject(t, h, "p")
		if resp := env.DoAuth(tokenP, http.MethodGet, "/servers", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token of p while its deletion runs: status %d, want 401", resp.StatusCode)
		}
		if status := mgmtAction(t, h, action); status != "running" {
			t.Errorf("GET /mgmt/v1/actions/%d while Delete blocks: status %q, want running", action, status)
		}
		close(deleter.gate)
		if status := waitMgmtAction(t, h, action); status != "success" {
			t.Fatalf("deletion of p ended %q, want success", status)
		}
		if got, want := deleter.take(), sorted(p1, p2); !slices.Equal(got, want) {
			t.Errorf("Deleter calls for p = %v, want each of %v once", got, want)
		}
		projects := listProjects(t, h)
		if _, ok := projects["p"]; ok {
			t.Errorf("p is still listed after its deletion")
		}
		if _, ok := projects["q"]; !ok {
			t.Errorf("q is no longer listed after deleting p")
		}
		err = env.Store.Tx(func(tx store.Tx) error {
			n, err := store.CountResources(tx, env.Project.ID)
			if err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("project p keeps %d resource rows, want none", n)
			}
			toks, err := store.ListTokens(tx, env.Project.ID)
			if err != nil {
				return err
			}
			if len(toks) != 0 {
				t.Errorf("project p keeps %d tokens, want none", len(toks))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("read store: %v", err)
		}
		if resp := env.DoAuth(tokenQ, http.MethodGet, fmt.Sprintf("/servers/%d", q1), nil); resp.StatusCode != http.StatusOK {
			t.Errorf("server of q after deleting p: status %d, want 200", resp.StatusCode)
		}

		// A failing Delete keeps r; a second delete finishes it.
		deleter.setFail(r1, true)
		action = deleteProject(t, h, "r")
		if status := waitMgmtAction(t, h, action); status != "error" {
			t.Fatalf("deletion of r with a failing Delete ended %q, want error", status)
		}
		if got := deleter.take(); !slices.Equal(got, []int64{r1}) {
			t.Errorf("Deleter calls for r = %v, want [%d]", got, r1)
		}
		if r, ok := listProjects(t, h)["r"]; !ok || r.Resources < 1 {
			t.Errorf("r after the failed deletion: listed %v, resources %d; want listed with its server", ok, r.Resources)
		}
		deleter.setFail(r1, false)
		action = deleteProject(t, h, "r")
		if status := waitMgmtAction(t, h, action); status != "success" {
			t.Fatalf("second deletion of r ended %q, want success", status)
		}
		if _, ok := listProjects(t, h)["r"]; ok {
			t.Errorf("r is still listed after the second deletion")
		}
		if got := deleter.take(); slices.Contains(got, q1) {
			t.Errorf("Deleter was called for q's server %d", q1)
		}
	})
}

func sorted(ids ...int64) []int64 {
	slices.Sort(ids)
	return ids
}

// createServer creates a server with the token auth and waits for its
// create action.
func createServer(t *testing.T, env *apitest.Env, auth, name string) int64 {
	t.Helper()
	resp := env.DoAuth(auth, http.MethodPost, "/servers", map[string]any{
		"name":        name,
		"server_type": "cx23",
		"image":       "ubuntu-24.04",
		"location":    "fsn1",
	})
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
	env.WaitActionAuth(auth, body.Action.ID)
	return body.Server.ID
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, mgmt.Prefix+path, bytes.NewReader(nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func deleteProject(t *testing.T, h http.Handler, name string) int64 {
	t.Helper()
	rec := do(t, h, http.MethodDelete, "/projects/"+name)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("DELETE /projects/%s: status %d, want 202 (%s)", name, rec.Code, rec.Body.String())
	}
	var body struct {
		ActionID int64 `json:"action_id"`
	}
	decode(t, rec, &body)
	return body.ActionID
}

func mgmtAction(t *testing.T, h http.Handler, id int64) string {
	t.Helper()
	rec := do(t, h, http.MethodGet, fmt.Sprintf("/actions/%d", id))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /mgmt/v1/actions/%d: status %d, want 200", id, rec.Code)
	}
	var body struct {
		Action struct {
			Status string `json:"status"`
		} `json:"action"`
	}
	decode(t, rec, &body)
	return body.Action.Status
}

func waitMgmtAction(t *testing.T, h http.Handler, id int64) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status := mgmtAction(t, h, id)
		if status != "running" {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("management action %d still running after 10 s", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func listProjects(t *testing.T, h http.Handler) map[string]mgmt.Project {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /mgmt/v1/projects: status %d, want 200", rec.Code)
	}
	var body struct {
		Projects []mgmt.Project `json:"projects"`
	}
	decode(t, rec, &body)
	out := map[string]mgmt.Project{}
	for _, p := range body.Projects {
		out[p.Name] = p
	}
	return out
}
