package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/mgmt"
)

// mgmtTimeout limits one request to the management API, well below the
// test's limit.
const mgmtTimeout = 10 * time.Second

// noopFaultEffect lets every kind be created without acting.
type noopFaultEffect struct{}

func (noopFaultEffect) Apply(context.Context, faults.Fault) error { return nil }
func (noopFaultEffect) Undo(context.Context, faults.Fault) error  { return nil }

// newFaultHlab returns hlab against a management API with a real fault
// manager (a no-op effect for every kind, every target known) over the
// projects p and q.
func newFaultHlab(t *testing.T) (*hlab, *faults.Manager) {
	t.Helper()
	effects := map[string]faults.Effect{}
	for _, k := range []string{
		faults.KindServerCrash, faults.KindServerFreeze, faults.KindNetwork, faults.KindLatency,
		faults.KindVolumeUnavailable, faults.KindDiskSlow, faults.KindStorageBoxUnavailable,
	} {
		effects[k] = noopFaultEffect{}
	}
	mgr := faults.New(faults.Options{Effects: effects})
	t.Cleanup(func() { _ = mgr.EndAll(context.Background()) })
	h := newHlabWith(t, func(d *mgmt.Deps) { d.Faults = mgr })
	h.env.NewToken("q", false)
	return h, mgr
}

// mgmtCall sends a request to the management API of h and returns status
// and body.
func mgmtCall(t *testing.T, h *hlab, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the body of %s %s: %v", method, path, err)
		}
		r = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), mgmtTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://hlab"+mgmt.Prefix+path, r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return h.dial(ctx) },
	}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the answer of %s %s: %v", method, path, err)
	}
	return resp.StatusCode, out
}

// apiMessage sends a request the management API refuses with want and
// returns the message of its error object.
func apiMessage(t *testing.T, h *hlab, method, path string, body any, want int) string {
	t.Helper()
	status, out := mgmtCall(t, h, method, path, body)
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if status != want || json.Unmarshal(out, &e) != nil || e.Error.Message == "" {
		t.Fatalf("%s %s: status %d, body %s; want %d with an error message", method, path, status, out, want)
	}
	return e.Error.Message
}

// apiFaults returns the faults the management API lists at path.
func apiFaults(t *testing.T, h *hlab, path string) []faults.Fault {
	t.Helper()
	status, out := mgmtCall(t, h, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200 (%s)", path, status, out)
	}
	var body struct {
		Faults []faults.Fault `json:"faults"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("GET %s: %v\n%s", path, err, out)
	}
	return body.Faults
}

func faultIDList(fs []faults.Fault) []int64 {
	ids := []int64{}
	for _, f := range fs {
		ids = append(ids, f.ID)
	}
	return ids
}

// decodeFault decodes the fault a create returned, as {"fault": {...}} or
// the bare fault object.
func decodeFault(t *testing.T, what, text string) faults.Fault {
	t.Helper()
	var wrapped struct {
		Fault *faults.Fault `json:"fault"`
	}
	if err := json.Unmarshal([]byte(text), &wrapped); err == nil && wrapped.Fault != nil {
		return *wrapped.Fault
	}
	var f faults.Fault
	if err := json.Unmarshal([]byte(text), &f); err != nil || f.ID == 0 {
		t.Fatalf("%s returned %q, want a fault object (%v)", what, text, err)
	}
	return f
}

// createFault runs `hlab fault create args... --output json` and returns
// the created fault.
func createFault(t *testing.T, h *hlab, args ...string) faults.Fault {
	t.Helper()
	all := append(append([]string{"fault", "create"}, args...), "--output", "json")
	return decodeFault(t, "hlab "+strings.Join(all, " "), h.mustRun(t, all...))
}

// sameJSON reports whether a and b encode to the same JSON.
func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(ja, jb)
}

func seedOf(seed uint64) *uint64 { return &seed }

func testFaultCommands(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		h, mgr := newFaultHlab(t)
		for _, c := range []struct {
			kind   string
			args   []string
			target *faults.Target
			params map[string]any
			seed   *uint64       // nil: none or chosen
			ends   time.Duration // 0: none
		}{
			{faults.KindServerCrash, []string{"--server", "7"},
				&faults.Target{Type: faults.TargetServer, ID: 7}, map[string]any{}, nil, 0},
			{faults.KindServerFreeze, []string{"--server", "7"},
				&faults.Target{Type: faults.TargetServer, ID: 7}, map[string]any{}, nil, 0},
			{faults.KindNetwork, []string{"--server", "7", "--traffic", "public", "--loss-percent", "30", "--seed", "99"},
				&faults.Target{Type: faults.TargetServer, ID: 7},
				map[string]any{"traffic": "public", "loss_percent": float64(30)}, seedOf(99), 0},
			{faults.KindLatency, []string{"--from", "fsn1", "--to", "hel1", "--delay-ms", "100", "--jitter-ms", "20"},
				&faults.Target{Type: faults.TargetLocations, From: "fsn1", To: "hel1"},
				map[string]any{"delay_ms": float64(100), "jitter_ms": float64(20)}, nil, 0},
			{faults.KindAPIError, []string{"--code", "service_error", "--percent", "50", "--only", "write",
				"--resource", "servers", "--seed", "18446744073709551615", "--duration", "5m"},
				nil, map[string]any{"code": "service_error", "percent": float64(50), "only": "write", "resource": "servers"},
				seedOf(18446744073709551615), 5 * time.Minute},
			{faults.KindRateLimit, []string{"--per-hour", "10"},
				nil, map[string]any{"per_hour": float64(10)}, nil, 0},
			{faults.KindVolumeUnavailable, []string{"--volume", "3"},
				&faults.Target{Type: faults.TargetVolume, ID: 3}, map[string]any{}, nil, 0},
			{faults.KindDiskSlow, []string{"--server", "8", "--iops", "100"},
				&faults.Target{Type: faults.TargetServer, ID: 8}, map[string]any{"iops": float64(100)}, nil, 0},
			{faults.KindStorageBoxUnavailable, []string{"--storage-box", "5"},
				&faults.Target{Type: faults.TargetStorageBox, ID: 5}, map[string]any{}, nil, 0},
		} {
			t.Run(c.kind, func(t *testing.T) {
				if err := mgr.EndAll(context.Background()); err != nil {
					t.Fatalf("end the faults of the last case: %v", err)
				}
				args := append([]string{"fault", "create", "--project", "p", c.kind}, c.args...)
				out := h.mustRun(t, args...)
				listed := apiFaults(t, h, "/projects/p/faults")
				if len(listed) != 1 {
					t.Fatalf("hlab %s: the management API lists %d faults of p, want 1", strings.Join(args, " "), len(listed))
				}
				f := listed[0]
				if f.Kind != c.kind || !reflect.DeepEqual(f.Target, c.target) || !reflect.DeepEqual(f.Params, c.params) {
					t.Errorf("hlab %s: listed kind %q, target %+v, params %v; want %q, %+v, %v",
						strings.Join(args, " "), f.Kind, f.Target, f.Params, c.kind, c.target, c.params)
				}
				if c.seed != nil && (f.Seed == nil || *f.Seed != *c.seed) {
					t.Errorf("hlab %s: listed seed %v, want %d", strings.Join(args, " "), f.Seed, *c.seed)
				}
				switch {
				case c.ends == 0 && f.Ends != nil:
					t.Errorf("hlab %s: listed ends %v, want null", strings.Join(args, " "), f.Ends)
				case c.ends != 0 && (f.Ends == nil || f.Ends.Sub(f.Created) != c.ends):
					t.Errorf("hlab %s: listed created %v, ends %v; want ends %s after created", strings.Join(args, " "), f.Created, f.Ends, c.ends)
				}
				if !regexp.MustCompile(`\b` + strconv.FormatInt(f.ID, 10) + `\b`).MatchString(out) {
					t.Errorf("hlab %s printed %q, want the ID %d", strings.Join(args, " "), out, f.ID)
				}
				if f.Seed != nil && !strings.Contains(out, strconv.FormatUint(*f.Seed, 10)) {
					t.Errorf("hlab %s printed %q, want the seed %d", strings.Join(args, " "), out, *f.Seed)
				}

				if err := mgr.EndAll(context.Background()); err != nil {
					t.Fatalf("end the fault: %v", err)
				}
				got := createFault(t, h, append([]string{"--project", "p", c.kind}, c.args...)...)
				listed = apiFaults(t, h, "/projects/p/faults")
				if len(listed) != 1 || !sameJSON(t, got, listed[0]) {
					t.Errorf("hlab %s --output json gave %+v, want the fault the management API lists %+v", strings.Join(args, " "), got, listed)
				}
			})
		}
	})

	t.Run("list", func(t *testing.T) {
		h, _ := newFaultHlab(t)
		apiErr := createFault(t, h, "--project", "p", faults.KindAPIError, "--code", "service_error", "--percent", "50",
			"--seed", "12345", "--duration", "1h")
		crash := createFault(t, h, "--project", "p", faults.KindServerCrash, "--server", "7")
		limit := createFault(t, h, "--project", "q", faults.KindRateLimit, "--per-hour", "10")

		out := h.mustRun(t, "fault", "list")
		lines := nonEmptyLines(out)
		if len(lines) == 0 || !regexp.MustCompile(`^ID\s+PROJECT\s+KIND\s+TARGET\s+PARAMS\s+SEED\s+REMAINING\s*$`).MatchString(lines[0]) {
			t.Errorf("fault list header is not ID, PROJECT, KIND, TARGET, PARAMS, SEED, REMAINING:\n%s", out)
		}
		row := func(out string, id int64) string {
			for _, l := range nonEmptyLines(out) {
				if regexp.MustCompile(`^\s*` + strconv.FormatInt(id, 10) + `\s`).MatchString(l) {
					return l
				}
			}
			return ""
		}
		for _, c := range []struct {
			id   int64
			want []string
		}{
			{apiErr.ID, []string{`\bp\b`, `\bapi_error\b`, `\bservice_error\b`, `\b50\b`, `\b12345\b`, `\b(1h|59m)`}},
			{crash.ID, []string{`\bp\b`, `\bserver_crash\b`, `\b7\b`}},
			{limit.ID, []string{`\bq\b`, `\brate_limit\b`, `\b10\b`}},
		} {
			l := row(out, c.id)
			if l == "" {
				t.Errorf("fault list has no row for fault %d:\n%s", c.id, out)
				continue
			}
			for _, w := range c.want {
				if !regexp.MustCompile(w).MatchString(l) {
					t.Errorf("fault list row of fault %d %q does not match %s", c.id, l, w)
				}
			}
		}

		out = h.mustRun(t, "fault", "list", "--project", "q")
		if row(out, limit.ID) == "" || row(out, apiErr.ID) != "" || row(out, crash.ID) != "" {
			t.Errorf("fault list --project q does not show only fault %d of q:\n%s", limit.ID, out)
		}

		for _, c := range []struct {
			args []string
			path string
		}{
			{[]string{"fault", "list", "--output", "json"}, "/faults"},
			{[]string{"fault", "list", "--project", "q", "--output", "json"}, "/projects/q/faults"},
		} {
			jsonOut := h.mustRun(t, c.args...)
			var got struct {
				Faults []faults.Fault `json:"faults"`
			}
			if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
				t.Fatalf("hlab %s: %v\n%s", strings.Join(c.args, " "), err, jsonOut)
			}
			if want := apiFaults(t, h, c.path); !sameJSON(t, got.Faults, want) {
				t.Errorf("hlab %s gave %s, want the faults of GET %s %+v", strings.Join(c.args, " "), jsonOut, c.path, want)
			}
		}
	})

	t.Run("delete", func(t *testing.T) {
		h, _ := newFaultHlab(t)
		a := createFault(t, h, "--project", "p", faults.KindServerCrash, "--server", "7")
		b := createFault(t, h, "--project", "p", faults.KindServerFreeze, "--server", "7")
		c := createFault(t, h, "--project", "p", faults.KindRateLimit, "--per-hour", "10")
		d := createFault(t, h, "--project", "q", faults.KindRateLimit, "--per-hour", "10")

		h.mustRun(t, "fault", "delete", "--project", "p", strconv.FormatInt(a.ID, 10))
		if got, want := faultIDList(apiFaults(t, h, "/faults")), []int64{b.ID, c.ID, d.ID}; !slices.Equal(got, want) {
			t.Errorf("after fault delete --project p %d the API lists %v, want %v", a.ID, got, want)
		}
		h.mustRun(t, "fault", "delete", "--project", "p", "--all")
		if got, want := faultIDList(apiFaults(t, h, "/faults")), []int64{d.ID}; !slices.Equal(got, want) {
			t.Errorf("after fault delete --project p --all the API lists %v, want %v", got, want)
		}
		e := createFault(t, h, "--project", "p", faults.KindRateLimit, "--per-hour", "10")
		h.mustRun(t, "fault", "delete", "--all")
		if got := faultIDList(apiFaults(t, h, "/faults")); len(got) != 0 {
			t.Errorf("after fault delete --all the API lists %v (%d and %d created), want none", got, d.ID, e.ID)
		}

		msg := apiMessage(t, h, http.MethodDelete, "/projects/p/faults/99", nil, http.StatusNotFound)
		if code, _, errOut := h.run(t, "fault", "delete", "--project", "p", "99"); code != 1 || !strings.Contains(errOut, msg) {
			t.Errorf("fault delete of the unknown ID 99: exit %d, stderr %q; want exit 1 and the API's message %q", code, errOut, msg)
		}
	})

	t.Run("errors", func(t *testing.T) {
		h, _ := newFaultHlab(t)
		kept := createFault(t, h, "--project", "p", faults.KindRateLimit, "--per-hour", "10")
		id := strconv.FormatInt(kept.ID, 10)
		for _, c := range []struct {
			name string
			args []string
			// apiBody is the request body the API refuses (exit 1 with its
			// message); nil for a usage error (exit 2).
			apiBody map[string]any
			status  int
		}{
			{name: "create without project", args: []string{"fault", "create", faults.KindAPIError, "--code", "server_error", "--percent", "10"}},
			{name: "non-integer percent", args: []string{"fault", "create", "--project", "p", faults.KindAPIError, "--code", "server_error", "--percent", "ten"}},
			{name: "two targets", args: []string{"fault", "create", "--project", "p", faults.KindServerCrash, "--server", "7", "--volume", "3"}},
			{name: "delete without id or all", args: []string{"fault", "delete", "--project", "p"}},
			{name: "delete with id and all", args: []string{"fault", "delete", "--project", "p", id, "--all"}},
			{name: "delete id without project", args: []string{"fault", "delete", id}},
			{name: "invalid parameter", args: []string{"fault", "create", "--project", "p", faults.KindAPIError, "--code", "server_error", "--percent", "101"},
				apiBody: map[string]any{"kind": faults.KindAPIError, "params": map[string]any{"code": "server_error", "percent": 101}},
				status:  http.StatusBadRequest},
			{name: "conflict", args: []string{"fault", "create", "--project", "p", faults.KindRateLimit, "--per-hour", "5"},
				apiBody: map[string]any{"kind": faults.KindRateLimit, "params": map[string]any{"per_hour": 5}},
				status:  http.StatusConflict},
		} {
			t.Run(c.name, func(t *testing.T) {
				wantCode, msg := 2, ""
				if c.apiBody != nil {
					wantCode = 1
					msg = apiMessage(t, h, http.MethodPost, "/projects/p/faults", c.apiBody, c.status)
				}
				code, _, errOut := h.run(t, c.args...)
				if code != wantCode || !strings.Contains(errOut, msg) {
					t.Errorf("hlab %s: exit %d, stderr %q; want exit %d with %q", strings.Join(c.args, " "), code, errOut, wantCode, msg)
				}
				if got := faultIDList(apiFaults(t, h, "/faults")); !slices.Equal(got, []int64{kept.ID}) {
					t.Errorf("after hlab %s the API lists %v, want only fault %d", strings.Join(c.args, " "), got, kept.ID)
				}
			})
		}
	})
}
