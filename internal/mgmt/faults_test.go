package mgmt_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/mgmt"
)

// faultServer is the only server the fault target lookup knows, in
// project p.
const faultServer = 7

// noopEffect lets every kind be created without acting.
type noopEffect struct{}

func (noopEffect) Apply(context.Context, faults.Fault) error { return nil }
func (noopEffect) Undo(context.Context, faults.Fault) error  { return nil }

// newFaultsMgmt returns the management API with a real fault manager that
// has a no-op effect for every kind, over projects p and q.
func newFaultsMgmt(t *testing.T) (*apitest.Env, http.Handler) {
	t.Helper()
	env := apitest.New(t, apitest.ProbeModule())
	env.NewToken("q", false)
	effects := map[string]faults.Effect{}
	for _, k := range []string{
		faults.KindServerCrash, faults.KindServerFreeze, faults.KindNetwork, faults.KindLatency,
		faults.KindVolumeUnavailable, faults.KindDiskSlow, faults.KindStorageBoxUnavailable,
	} {
		effects[k] = noopEffect{}
	}
	mgr := faults.New(faults.Options{
		Effects: effects,
		Lookup: func(_ context.Context, project, _ string, tg faults.Target) error {
			if project == "p" && tg.Type == faults.TargetServer && tg.ID == faultServer {
				return nil
			}
			return fmt.Errorf("%s %d: %w", tg.Type, tg.ID, faults.ErrNotFound)
		},
	})
	h := mgmt.New(mgmt.Deps{
		Store:   env.Store,
		API:     env.Router,
		Status:  func() mgmt.Status { return mgmt.Status{State: "running"} },
		Actions: env.Actions,
		Faults:  mgr,
	})
	return env, h
}

// listFaults returns the faults GET path lists.
func listFaults(t *testing.T, h http.Handler, path string) []faults.Fault {
	t.Helper()
	rec := do(t, h, http.MethodGet, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200 (%s)", path, rec.Code, rec.Body.String())
	}
	var body struct {
		Faults []faults.Fault `json:"faults"`
	}
	decode(t, rec, &body)
	return body.Faults
}

func faultIDs(fs []faults.Fault) []int64 {
	ids := make([]int64, 0, len(fs))
	for _, f := range fs {
		ids = append(ids, f.ID)
	}
	return ids
}

func testMgmtFaultRoutes(t *testing.T) {
	_, h := newFaultsMgmt(t)

	wantIDs := func(path string, want ...int64) func(t *testing.T) {
		return func(t *testing.T) {
			if got := faultIDs(listFaults(t, h, path)); !slices.Equal(got, want) {
				t.Errorf("GET %s lists faults %v, want %v", path, got, want)
			}
		}
	}
	wantStatusFaults := func(want int) func(t *testing.T) {
		return func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/status")
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /status: status %d, want 200", rec.Code)
			}
			var st mgmt.Status
			decode(t, rec, &st)
			if st.Faults != want {
				t.Errorf("GET /status faults %d, want %d", st.Faults, want)
			}
		}
	}
	created := func(check func(t *testing.T, f faults.Fault)) func(t *testing.T, rec *httptest.ResponseRecorder) {
		return func(t *testing.T, rec *httptest.ResponseRecorder) {
			var body struct {
				Fault faults.Fault `json:"fault"`
			}
			decode(t, rec, &body)
			check(t, body.Fault)
		}
	}

	// The steps build on each other in order.
	for _, tc := range []struct {
		name         string
		method, path string
		body         string
		status       int
		code         string // error code, if any
		check        func(t *testing.T, rec *httptest.ResponseRecorder)
		after        func(t *testing.T)
	}{
		{
			name: "create api_error with chosen seed", method: http.MethodPost, path: "/projects/p/faults",
			body:   `{"kind":"api_error","params":{"code":"server_error","percent":20,"only":"write"},"duration":"5m"}`,
			status: http.StatusCreated,
			check: created(func(t *testing.T, f faults.Fault) {
				if f.ID != 1 || f.Project != "p" || f.Kind != faults.KindAPIError || f.Target != nil {
					t.Errorf("fault id %d, project %q, kind %q, target %v; want 1, p, api_error, null", f.ID, f.Project, f.Kind, f.Target)
				}
				if f.Params["code"] != "server_error" || f.Params["percent"] != float64(20) || f.Params["only"] != "write" {
					t.Errorf("fault params %v, want code server_error, percent 20, only write", f.Params)
				}
				if f.Seed == nil || *f.Seed < 1 || *f.Seed > faults.MaxChosenSeed {
					t.Errorf("fault seed %v, want one chosen in 1..2^53-1", f.Seed)
				}
				if f.Ends == nil || !f.Ends.Equal(f.Created.Add(5*time.Minute)) {
					t.Errorf("fault created %v, ends %v; want ends 5m after created", f.Created, f.Ends)
				}
			}),
		},
		{
			name: "create server_crash on a server", method: http.MethodPost, path: "/projects/p/faults",
			body:   fmt.Sprintf(`{"kind":"server_crash","target":{"type":"server","id":%d}}`, faultServer),
			status: http.StatusCreated,
			check: created(func(t *testing.T, f faults.Fault) {
				if f.ID != 2 || f.Kind != faults.KindServerCrash || f.Target == nil ||
					f.Target.Type != faults.TargetServer || f.Target.ID != faultServer {
					t.Errorf("fault id %d, kind %q, target %v; want 2, server_crash, server %d", f.ID, f.Kind, f.Target, faultServer)
				}
				if f.Seed != nil || f.Ends != nil {
					t.Errorf("fault seed %v, ends %v; want both null", f.Seed, f.Ends)
				}
			}),
		},
		{
			name: "invalid input", method: http.MethodPost, path: "/projects/p/faults",
			body: `{"kind":"no_such_kind"}`, status: http.StatusBadRequest, code: "invalid_input",
		},
		{
			name: "unknown target", method: http.MethodPost, path: "/projects/p/faults",
			body:   fmt.Sprintf(`{"kind":"server_freeze","target":{"type":"server","id":%d}}`, faultServer+1),
			status: http.StatusNotFound, code: "not_found",
		},
		{
			name: "unknown project", method: http.MethodPost, path: "/projects/nope/faults",
			body:   `{"kind":"rate_limit","params":{"per_hour":10}}`,
			status: http.StatusNotFound, code: "not_found",
		},
		{
			name: "conflict", method: http.MethodPost, path: "/projects/p/faults",
			body:   `{"kind":"api_error","params":{"code":"conflict","percent":50}}`,
			status: http.StatusConflict, code: "conflict",
		},
		{
			name: "create rate_limit in q", method: http.MethodPost, path: "/projects/q/faults",
			body: `{"kind":"rate_limit","params":{"per_hour":10}}`, status: http.StatusCreated,
			check: created(func(t *testing.T, f faults.Fault) {
				if f.ID != 3 || f.Project != "q" || f.Kind != faults.KindRateLimit {
					t.Errorf("fault id %d, project %q, kind %q; want 3, q, rate_limit", f.ID, f.Project, f.Kind)
				}
			}),
		},
		{
			name: "list project", method: http.MethodGet, path: "/projects/p/faults", status: http.StatusOK,
			after: wantIDs("/projects/p/faults", 1, 2),
		},
		{
			name: "list all", method: http.MethodGet, path: "/faults", status: http.StatusOK,
			after: wantIDs("/faults", 1, 2, 3),
		},
		{
			name: "status", method: http.MethodGet, path: "/status", status: http.StatusOK,
			after: wantStatusFaults(3),
		},
		{
			name: "delete one", method: http.MethodDelete, path: "/projects/p/faults/2", status: http.StatusNoContent,
			after: wantIDs("/faults", 1, 3),
		},
		{
			name: "delete unknown", method: http.MethodDelete, path: "/projects/p/faults/2",
			status: http.StatusNotFound, code: "not_found",
		},
		{
			name: "delete project's", method: http.MethodDelete, path: "/projects/p/faults", status: http.StatusNoContent,
			after: wantIDs("/faults", 3),
		},
		{
			name: "delete all", method: http.MethodDelete, path: "/faults", status: http.StatusNoContent,
			after: func(t *testing.T) {
				wantIDs("/faults")(t)
				wantStatusFaults(0)(t)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doBody(t, h, tc.method, tc.path, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("%s %s %s: status %d, want %d (%s)", tc.method, tc.path, tc.body, rec.Code, tc.status, rec.Body.String())
			}
			if tc.code != "" {
				if got := mgmtErrorCode(t, rec); got != tc.code {
					t.Errorf("%s %s %s: error code %q, want %q", tc.method, tc.path, tc.body, got, tc.code)
				}
			}
			if tc.check != nil {
				tc.check(t, rec)
			}
			if tc.after != nil {
				tc.after(t)
			}
		})
	}
}

func testMgmtFaultsProjectDeleted(t *testing.T) {
	_, h := newFaultsMgmt(t)
	for _, req := range []struct{ project, body string }{
		{"p", `{"kind":"api_error","params":{"code":"server_error","percent":100}}`},
		{"p", fmt.Sprintf(`{"kind":"server_crash","target":{"type":"server","id":%d}}`, faultServer)},
		{"q", `{"kind":"rate_limit","params":{"per_hour":10}}`},
	} {
		if rec := doBody(t, h, http.MethodPost, "/projects/"+req.project+"/faults", req.body); rec.Code != http.StatusCreated {
			t.Fatalf("POST /projects/%s/faults %s: status %d, want 201 (%s)", req.project, req.body, rec.Code, rec.Body.String())
		}
	}

	action := deleteProject(t, h, "p")
	if status := waitMgmtAction(t, h, action); status != "success" {
		t.Fatalf("deletion of p ended %q, want success", status)
	}
	left := listFaults(t, h, "/faults")
	if len(left) != 1 || left[0].Project != "q" || left[0].Kind != faults.KindRateLimit {
		t.Errorf("GET /faults after deleting p lists %v, want only q's rate_limit", left)
	}
}
