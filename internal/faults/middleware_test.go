package faults_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/resource"
)

func init() {
	register("FAULT-03", testFault03)
}

// --- API fixture shared with ratelimit_test.go ---

// apiEnv is a fault manager (env) whose Middleware is the fault layer of an
// in-process API with the probe module (ssh_keys, counting the requests
// that reach it), a stub "servers" module under /v1 and a stub
// "storage_boxes" module under /hetzner/v1.
type apiEnv struct {
	*env
	API *apitest.Env
	// Reached counts the requests that reached a module's handler.
	Reached *atomic.Int64
}

func newAPIEnv(t *testing.T, mods ...func(*faults.Options)) *apiEnv {
	t.Helper()
	e := newEnv(t, mods...)
	reached := &atomic.Int64{}
	a := apitest.NewWith(t, apitest.Options{Faults: e.M.Middleware},
		counting{Module: apitest.ProbeModule(), reached: reached},
		counting{Module: stubModule{typ: "servers"}, reached: reached},
		countingPrefixed{counting{Module: stubModule{typ: "storage_boxes"}, reached: reached}, api.HetznerPrefix},
	)
	return &apiEnv{env: e, API: a, Reached: reached}
}

// stubModule answers GET and POST /<typ> with 200 and an empty object.
type stubModule struct{ typ string }

func (s stubModule) Type() string { return s.typ }

func (s stubModule) Routes(r resource.Registrar) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	r.Handle("GET /"+s.typ, ok)
	r.Handle("POST /"+s.typ, ok)
}

// counting wraps a module and counts the requests reaching its handlers.
type counting struct {
	resource.Module
	reached *atomic.Int64
}

// countingPrefixed is counting for a module under another prefix.
type countingPrefixed struct {
	counting
	prefix string
}

func (c countingPrefixed) Prefix() string { return c.prefix }

func (c counting) Routes(r resource.Registrar) {
	c.Module.Routes(countingRegistrar{r: r, reached: c.reached})
}

type countingRegistrar struct {
	r       resource.Registrar
	reached *atomic.Int64
}

func (c countingRegistrar) Handle(pattern string, h http.Handler) {
	c.r.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c.reached.Add(1)
		h.ServeHTTP(w, req)
	}))
}

// send serves one request straight to the router, without the description
// check (the stub modules do not answer in Hetzner's shapes). path is the
// full path ("/v1/ssh_keys", "/hetzner/v1/storage_boxes"); token "" sends
// no Authorization header. Requests other than GET and HEAD carry "{}".
func (a *apiEnv) send(t *testing.T, token, method, path string) *http.Response {
	t.Helper()
	return a.serve(t, context.Background(), "Bearer "+token, token != "", method, path)
}

// sendAuth is send with a raw Authorization header value.
func (a *apiEnv) sendAuth(t *testing.T, authorization, method, path string) *http.Response {
	t.Helper()
	return a.serve(t, context.Background(), authorization, true, method, path)
}

// proxy serves a request the way the management API's project proxy does:
// project "p" in the context (api.WithProject) and no token.
func (a *apiEnv) proxy(t *testing.T, method, path string) *http.Response {
	t.Helper()
	return a.serve(t, api.WithProject(context.Background(), a.API.Project), "", false, method, path)
}

func (a *apiEnv) serve(t *testing.T, ctx context.Context, authorization string, withAuth bool, method, path string) *http.Response {
	t.Helper()
	var body *strings.Reader
	if method != http.MethodGet && method != http.MethodHead {
		body = strings.NewReader(`{}`)
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(ctx, method, "http://api.hetzner.internal"+path, body)
	if body.Len() > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if withAuth {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	a.API.Router.ServeHTTP(rec, req)
	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// hitCode is the code the share, filter and isolation tests inject: no
// module of apiEnv ever answers 409 itself.
const hitCode = "conflict"

func isHit(resp *http.Response) bool { return resp.StatusCode == http.StatusConflict }

// hits sends n GET /v1/ssh_keys with p's token and reports which were hit.
func (a *apiEnv) hits(t *testing.T, n int) []bool {
	t.Helper()
	out := make([]bool, n)
	for i := range out {
		out[i] = isHit(a.send(t, a.API.Token, http.MethodGet, "/v1/ssh_keys"))
	}
	return out
}

func countTrue(bs []bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// errorBody decodes Hetzner's error object.
type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// --- FAULT-03 ---

func testFault03(t *testing.T) {
	t.Run("share_and_seed", func(t *testing.T) {
		const n = 200
		run := func(s uint64) []bool {
			a := newAPIEnv(t)
			spec := apiErrorSpec(hitCode, 50)
			spec.Seed = seed(s)
			a.create(t, "p", spec)
			return a.hits(t, n)
		}
		first := run(4711)
		if again := run(4711); !slices.Equal(first, again) {
			t.Errorf("seed 4711: hits differ on a fresh manager:\n%v\n%v", first, again)
		}
		if other := run(4712); slices.Equal(first, other) {
			t.Errorf("seeds 4711 and 4712 hit the same %d requests", n)
		}
		if c := countTrue(first); c < 60 || c > 140 {
			t.Errorf("50 %%: %d of %d requests hit, want roughly half", c, n)
		}

		a := newAPIEnv(t)
		f := a.create(t, "p", apiErrorSpec(hitCode, 100))
		if c := countTrue(a.hits(t, 20)); c != 20 {
			t.Errorf("100 %%: %d of 20 requests hit, want all", c)
		}
		before := a.Reached.Load()
		resp := a.API.Do(http.MethodPost, "/ssh_keys", map[string]any{"name": "k", "public_key": "ssh-ed25519 AAAA k"})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("hit POST /ssh_keys: status %d, want 409", resp.StatusCode)
		}
		var body errorBody
		apitest.Decode(t, resp, &body)
		if body.Error.Code != hitCode {
			t.Errorf("hit: error code %q, want %q", body.Error.Code, hitCode)
		}
		idRe := regexp.MustCompile(`\b` + fmt.Sprint(f.ID) + `\b`)
		if !strings.Contains(strings.ToLower(body.Error.Message), "fault") || !idRe.MatchString(body.Error.Message) {
			t.Errorf("hit: message %q does not name fault %d", body.Error.Message, f.ID)
		}
		if got := a.Reached.Load(); got != before {
			t.Errorf("hit POST /ssh_keys reached the handler (%d calls), want none", got-before)
		}
	})

	t.Run("codes", func(t *testing.T) {
		cases := []struct {
			code   string
			status int
		}{
			{"server_error", 500},
			{"service_error", 422},
			{"conflict", 409},
			{"locked", 423},
			{"unavailable", 503},
			{"invalid_input", 422},
		}
		for _, c := range cases {
			t.Run(c.code, func(t *testing.T) {
				a := newAPIEnv(t)
				a.create(t, "p", apiErrorSpec(c.code, 100))
				// Do validates the answer against the OpenAPI description.
				resp := a.API.For(t).Do(http.MethodGet, "/ssh_keys", nil)
				if resp.StatusCode != c.status {
					t.Errorf("status %d, want %d", resp.StatusCode, c.status)
				}
				var body errorBody
				apitest.Decode(t, resp, &body)
				if body.Error.Code != c.code {
					t.Errorf("error code %q, want %q", body.Error.Code, c.code)
				}
				if c.code == "invalid_input" {
					fields, ok := body.Error.Details["fields"].([]any)
					if !ok || len(fields) != 0 {
						t.Errorf("details %v, want fields []", body.Error.Details)
					}
				}
			})
		}
	})

	t.Run("filters", func(t *testing.T) {
		type req struct {
			method, path string
			hit          bool
		}
		cases := []struct {
			name     string
			params   map[string]any
			requests []req
		}{
			{"only_read", map[string]any{"only": "read"}, []req{
				{http.MethodGet, "/v1/ssh_keys", true},
				{http.MethodHead, "/v1/ssh_keys", true},
				{http.MethodPost, "/v1/ssh_keys", false},
			}},
			{"only_write", map[string]any{"only": "write"}, []req{
				{http.MethodGet, "/v1/ssh_keys", false},
				{http.MethodHead, "/v1/ssh_keys", false},
				{http.MethodPost, "/v1/ssh_keys", true},
			}},
			{"resource_servers", map[string]any{"resource": "servers"}, []req{
				{http.MethodGet, "/v1/servers", true},
				{http.MethodGet, "/v1/ssh_keys", false},
			}},
			{"resource_storage_boxes", map[string]any{"resource": "storage_boxes"}, []req{
				{http.MethodGet, "/hetzner/v1/storage_boxes", true},
				{http.MethodGet, "/v1/ssh_keys", false},
			}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				a := newAPIEnv(t)
				spec := apiErrorSpec(hitCode, 100)
				for k, v := range c.params {
					spec.Params[k] = v
				}
				a.create(t, "p", spec)
				for _, r := range c.requests {
					resp := a.send(t, a.API.Token, r.method, r.path)
					if isHit(resp) != r.hit {
						t.Errorf("%s %s: status %d, hit %v, want hit %v", r.method, r.path, resp.StatusCode, isHit(resp), r.hit)
					}
				}
			})
		}

		t.Run("interleaved_non_matching", func(t *testing.T) {
			const n = 100
			run := func(interleave bool) []bool {
				a := newAPIEnv(t)
				spec := apiErrorSpec(hitCode, 50)
				spec.Params["only"] = "read"
				spec.Params["resource"] = "servers"
				spec.Seed = seed(99)
				a.create(t, "p", spec)
				out := make([]bool, n)
				for i := range out {
					if interleave {
						for _, r := range []struct{ method, path string }{
							{http.MethodGet, "/v1/ssh_keys"},
							{http.MethodPost, "/v1/servers"},
							{http.MethodGet, "/hetzner/v1/storage_boxes"},
						} {
							if resp := a.send(t, a.API.Token, r.method, r.path); isHit(resp) {
								t.Errorf("non-matching %s %s was hit", r.method, r.path)
							}
						}
					}
					out[i] = isHit(a.send(t, a.API.Token, http.MethodGet, "/v1/servers"))
				}
				return out
			}
			plain, mixed := run(false), run(true)
			if !slices.Equal(plain, mixed) {
				t.Errorf("non-matching requests changed the hits:\nalone       %v\ninterleaved %v", plain, mixed)
			}
		})
	})

	t.Run("unaffected", func(t *testing.T) {
		a := newAPIEnv(t)
		f := a.create(t, "p", apiErrorSpec(hitCode, 100))
		qToken := a.API.NewToken("q", false)
		for i := range 5 {
			if resp := a.proxy(t, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusOK {
				t.Errorf("proxy request %d: status %d, want 200", i, resp.StatusCode)
			}
			if resp := a.proxy(t, http.MethodPost, "/v1/servers"); resp.StatusCode != http.StatusOK {
				t.Errorf("proxy write %d: status %d, want 200", i, resp.StatusCode)
			}
			if resp := a.send(t, qToken, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusOK {
				t.Errorf("project q request %d: status %d, want 200", i, resp.StatusCode)
			}
		}
		if resp := a.send(t, a.API.Token, http.MethodGet, "/v1/ssh_keys"); !isHit(resp) {
			t.Fatalf("project p token request: status %d, want the injected 409", resp.StatusCode)
		}
		if err := a.M.End(t.Context(), "p", f.ID); err != nil {
			t.Fatalf("End: %v", err)
		}
		for i := range 5 {
			if resp := a.send(t, a.API.Token, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusOK {
				t.Errorf("after End, request %d: status %d, want 200", i, resp.StatusCode)
			}
		}
	})
}
