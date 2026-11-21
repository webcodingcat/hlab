package mgmt_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/mgmt"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/loadbalancers"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/settings"
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

// call is one entry of a callLog: event is "start" or "done".
type call struct {
	kind  string
	id    int64
	event string
}

// callLog records the Delete calls of several kinds in one order.
type callLog struct {
	mu    sync.Mutex
	calls []call
}

func (l *callLog) add(c call) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, c)
}

func (l *callLog) entries() []call {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

// deleter returns a Deleter for kind that records its start and end in l.
func (l *callLog) deleter(kind string) resource.Deleter {
	return logDeleter{log: l, kind: kind}
}

type logDeleter struct {
	log  *callLog
	kind string
}

func (d logDeleter) Delete(_ context.Context, id int64) error {
	d.log.add(call{kind: d.kind, id: id, event: "start"})
	d.log.add(call{kind: d.kind, id: id, event: "done"})
	return nil
}

func TestMgmt(t *testing.T) {
	t.Run("PROJ-04", func(t *testing.T) {
		cat, err := catalog.Load("../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t,
			servers.New(apitest.ServersDeps(t, cat, platformtest.NewFake())),
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

		t.Run("networks_last", func(t *testing.T) {
			env := apitest.New(t,
				servers.New(apitest.ServersDeps(t, cat, platformtest.NewFake())),
				resactions.New(),
			)
			log := &callLog{}
			h := mgmt.New(mgmt.Deps{
				Store:   env.Store,
				API:     env.Router,
				Status:  func() mgmt.Status { return mgmt.Status{State: "running"} },
				Actions: env.Actions,
				Deleters: map[string]resource.Deleter{
					servers.Kind:       log.deleter(servers.Kind),
					loadbalancers.Kind: log.deleter(loadbalancers.Kind),
					networks.Kind:      log.deleter(networks.Kind),
				},
			})

			auth := "Bearer " + env.Token
			srv := createServer(t, env, auth, "n-1")
			const netID, lbID = 900001, 900002
			err := env.Store.Tx(func(tx store.Tx) error {
				if err := store.Put(tx, store.Meta{Kind: networks.Kind, ID: netID, ProjectID: env.Project.ID, Name: "n-net"},
					map[string]any{"id": netID, "name": "n-net", "ip_range": "10.0.0.0/16"}); err != nil {
					return err
				}
				return store.Put(tx, store.Meta{Kind: loadbalancers.Kind, ID: lbID, ProjectID: env.Project.ID, Name: "n-lb"},
					map[string]any{"id": lbID, "name": "n-lb"})
			})
			if err != nil {
				t.Fatalf("store network and load balancer rows: %v", err)
			}

			action := deleteProject(t, h, "p")
			if status := waitMgmtAction(t, h, action); status != "success" {
				t.Fatalf("deletion of p ended %q, want success", status)
			}
			calls := log.entries()
			lastOther, firstNet := -1, -1
			seen := map[string]bool{}
			for i, c := range calls {
				seen[c.kind+":"+c.event] = true
				switch {
				case c.kind == networks.Kind && c.event == "start" && firstNet < 0:
					firstNet = i
				case c.kind != networks.Kind && c.event == "done":
					lastOther = i
				}
			}
			for _, want := range []string{servers.Kind, loadbalancers.Kind, networks.Kind} {
				if !seen[want+":start"] || !seen[want+":done"] {
					t.Errorf("Deleter for %s was not called to completion; calls %v", want, calls)
				}
			}
			if firstNet < lastOther {
				t.Errorf("networks Delete started before every server and load balancer was deleted (server %d); calls %v", srv, calls)
			}
		})
	})

	t.Run("CERT-04", func(t *testing.T) {
		env := apitest.New(t, apitest.ProbeModule())
		path := filepath.Join(t.TempDir(), "settings.toml")
		const initial = "[cloud]\nstop_behaviour = \"shutdown\"\n\n[logging]\nlevel = \"debug\"\n"
		if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
			t.Fatalf("write settings file: %v", err)
		}
		var mu sync.Mutex
		var calls []bool
		var trustErr error
		h := mgmt.New(mgmt.Deps{
			Store:        env.Store,
			API:          env.Router,
			Status:       func() mgmt.Status { return mgmt.Status{State: "running"} },
			Actions:      env.Actions,
			SettingsPath: path,
			Trust: func(_ context.Context, on bool) error {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, on)
				return trustErr
			},
		})
		takeCalls := func() []bool {
			mu.Lock()
			defer mu.Unlock()
			c := calls
			calls = nil
			return c
		}
		load := func() settings.Settings {
			t.Helper()
			s, err := settings.Load(path)
			if err != nil {
				t.Fatalf("load settings file after PUT /trust: %v", err)
			}
			return s
		}

		for _, on := range []bool{true, false} {
			rec := doBody(t, h, http.MethodPut, "/trust", fmt.Sprintf(`{"on":%t}`, on))
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT /trust on=%t: status %d, want 200 (%s)", on, rec.Code, rec.Body.String())
			}
			var body struct {
				On *bool `json:"on"`
			}
			decode(t, rec, &body)
			if body.On == nil || *body.On != on {
				t.Errorf("PUT /trust on=%t answered %s, want {\"on\":%t}", on, rec.Body.String(), on)
			}
			if got := takeCalls(); !slices.Equal(got, []bool{on}) {
				t.Errorf("PUT /trust on=%t: Trust calls %v, want [%t]", on, got, on)
			}
			s := load()
			if s.Certificates.TrustCA != on {
				t.Errorf("PUT /trust on=%t: settings trust_ca = %t, want %t", on, s.Certificates.TrustCA, on)
			}
			if s.Cloud.StopBehaviour != "shutdown" || s.Logging.Level != "debug" {
				t.Errorf("PUT /trust on=%t: stop_behaviour %q, level %q; want the file's shutdown and debug kept",
					on, s.Cloud.StopBehaviour, s.Logging.Level)
			}
		}

		// A failing Trust answers server_error and leaves the file as it is.
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read settings file: %v", err)
		}
		mu.Lock()
		trustErr = errors.New("store access denied")
		mu.Unlock()
		rec := doBody(t, h, http.MethodPut, "/trust", `{"on":true}`)
		if code := mgmtErrorCode(t, rec); code != "server_error" {
			t.Errorf("PUT /trust with a failing Trust: status %d, code %q, want server_error", rec.Code, code)
		}
		if got := takeCalls(); !slices.Equal(got, []bool{true}) {
			t.Errorf("PUT /trust with a failing Trust: Trust calls %v, want [true]", got)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read settings file: %v", err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("PUT /trust with a failing Trust changed the settings file:\n%s\nwant\n%s", after, before)
		}

		// A body without on is invalid input and calls nothing.
		rec = doBody(t, h, http.MethodPut, "/trust", `{}`)
		if code := mgmtErrorCode(t, rec); code != "invalid_input" {
			t.Errorf("PUT /trust {}: status %d, code %q, want invalid_input", rec.Code, code)
		}
		if got := takeCalls(); len(got) != 0 {
			t.Errorf("PUT /trust {}: Trust calls %v, want none", got)
		}
	})

	t.Run("CERT-05", func(t *testing.T) {
		env := apitest.New(t, apitest.ProbeModule())
		const pem = "-----BEGIN CERTIFICATE-----\nTUlJQ2FDQQ==\n-----END CERTIFICATE-----\n"
		h := mgmt.New(mgmt.Deps{
			Store:   env.Store,
			API:     env.Router,
			Status:  func() mgmt.Status { return mgmt.Status{State: "running"} },
			Actions: env.Actions,
			CACert:  func() []byte { return []byte(pem) },
		})
		rec := do(t, h, http.MethodGet, "/ca.pem")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /mgmt/v1/ca.pem: status %d, want 200", rec.Code)
		}
		if got := rec.Body.String(); got != pem {
			t.Errorf("GET /mgmt/v1/ca.pem returned %q, want the PEM of CACert %q", got, pem)
		}
	})

	t.Run("LIFE-09", func(t *testing.T) {
		logDir := t.TempDir()
		const line = `{"time":"2026-11-19T10:00:00Z","level":"INFO","msg":"before the stop"}` + "\n"
		if err := os.WriteFile(filepath.Join(logDir, "hlabd.log"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		env := apitest.New(t, apitest.ProbeModule())
		done := make(chan struct{})
		srv := httptest.NewServer(mgmt.New(mgmt.Deps{
			Store:   env.Store,
			API:     env.Router,
			Status:  func() mgmt.Status { return mgmt.Status{State: "running"} },
			Actions: env.Actions,
			LogDir:  logDir,
			Done:    done,
		}))
		t.Cleanup(srv.Close)

		resp, err := srv.Client().Get(srv.URL + mgmt.Prefix + "/logs?follow=1")
		if err != nil {
			t.Fatalf("GET /mgmt/v1/logs?follow=1: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /mgmt/v1/logs?follow=1: status %d, want 200", resp.StatusCode)
		}
		lines := make(chan string, 100)
		ended := make(chan struct{})
		go func() {
			defer close(ended)
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				lines <- sc.Text()
			}
		}()
		select {
		case got := <-lines:
			if got+"\n" != line {
				t.Errorf("first followed line %q, want %q", got, line)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no log line from the follower within 5 s")
		}

		close(done)
		// The shutdown timeout of the management server is 10 s; the
		// follower must end well before.
		select {
		case <-ended:
		case got := <-lines:
			t.Fatalf("line %q after Done closed, want the stream to end", got)
		case <-time.After(2 * time.Second):
			t.Fatal("log follower still streaming 2 s after Done closed")
		}
	})

	t.Run("PROJ-06", func(t *testing.T) {
		env := apitest.New(t, apitest.ProbeModule())
		h := mgmt.New(mgmt.Deps{
			Store:   env.Store,
			API:     env.Router,
			Status:  func() mgmt.Status { return mgmt.Status{State: "running"} },
			Actions: env.Actions,
		})

		rec := doBody(t, h, http.MethodPost, "/projects", `{"display_name":" Web Coding Cat "}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /projects: status %d, want 201 (%s)", rec.Code, rec.Body.String())
		}
		var created mgmt.Project
		decode(t, rec, &created)
		if created.Name != "web-coding-cat" || created.DisplayName != "Web Coding Cat" {
			t.Errorf("created project %q with display name %q, want web-coding-cat with \"Web Coding Cat\"",
				created.Name, created.DisplayName)
		}

		// One server and one snapshot of the project.
		err := env.Store.Tx(func(tx store.Tx) error {
			p, err := store.ProjectByName(tx, "web-coding-cat")
			if err != nil {
				return err
			}
			if err := store.Put(tx, store.Meta{Kind: servers.Kind, ID: 9001, ProjectID: p.ID, Name: "s"}, map[string]any{}); err != nil {
				return err
			}
			return store.Put(tx, store.Meta{Kind: "images", ID: 9002, ProjectID: p.ID}, map[string]any{"type": "snapshot"})
		})
		if err != nil {
			t.Fatalf("add resources: %v", err)
		}
		listed, ok := listProjects(t, h)["web-coding-cat"]
		if !ok {
			t.Fatal("GET /projects does not list web-coding-cat")
		}
		if listed.DisplayName != "Web Coding Cat" {
			t.Errorf("listed display name %q, want \"Web Coding Cat\"", listed.DisplayName)
		}
		if want := map[string]int{"servers": 1, "snapshots": 1}; !maps.Equal(listed.ResourcesByType, want) {
			t.Errorf("listed resources_by_type %v, want %v", listed.ResourcesByType, want)
		}

		for _, tc := range []struct {
			name, body string
			status     int
			code       string
			message    string // a part of the error message, if any
		}{
			{"invalid display name", `{"display_name":""}`, http.StatusBadRequest, "invalid_input", ""},
			{"only name", `{"name":"other"}`, http.StatusBadRequest, "invalid_input", ""},
			{"empty derived name", `{"display_name":"!!!"}`, http.StatusBadRequest, "invalid_input", ""},
			{"derived name taken", `{"display_name":"web coding cat"}`, http.StatusConflict, "conflict", "web-coding-cat"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rec := doBody(t, h, http.MethodPost, "/projects", tc.body)
				if rec.Code != tc.status {
					t.Fatalf("POST /projects %s: status %d, want %d (%s)", tc.body, rec.Code, tc.status, rec.Body.String())
				}
				var body struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				decode(t, rec, &body)
				if body.Error.Code != tc.code {
					t.Errorf("POST /projects %s: error code %q, want %q", tc.body, body.Error.Code, tc.code)
				}
				if !strings.Contains(body.Error.Message, tc.message) {
					t.Errorf("POST /projects %s: message %q does not name %q", tc.body, body.Error.Message, tc.message)
				}
			})
		}
	})
}

// doBody sends a request with a JSON body to the management API.
func doBody(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, mgmt.Prefix+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// mgmtErrorCode returns the error code of a management API error answer.
func mgmtErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decode(t, rec, &body)
	return body.Error.Code
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
