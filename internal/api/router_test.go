package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resource"
)

// writeModule offers POST /ssh_keys and PUT /ssh_keys/{id}; a request that
// reaches a handler answers server_error, so a malformed body that is not
// refused before shows up as a wrong status.
type writeModule struct{}

func (writeModule) Type() string { return "ssh_keys" }

func (writeModule) Routes(r resource.Registrar) {
	reached := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.Error(w, "server_error", "handler reached", nil)
	})
	r.Handle("POST /ssh_keys", reached)
	r.Handle("PUT /ssh_keys/{id}", reached)
}

// hetznerModule is a module of the Hetzner API: it offers
// GET /storage_boxes and GET /storage_boxes/{id} under /hetzner/v1, both
// answering 200 with an empty object.
type hetznerModule struct{}

func (hetznerModule) Type() string { return "storage_boxes" }

func (hetznerModule) Prefix() string { return "/hetzner/v1" }

func (hetznerModule) Routes(r resource.Registrar) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	r.Handle("GET /storage_boxes", ok)
	r.Handle("GET /storage_boxes/{id}", ok)
}

// serveRaw sends a request with token straight to the router, bypassing
// the description check, and returns the response.
func serveRaw(t *testing.T, env *apitest.Env, method, path, body string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, "http://api.hetzner.internal"+path, r)
	req.Header.Set("Authorization", "Bearer "+env.Token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	env.Router.ServeHTTP(rec, req)
	return rec.Result()
}

// testRouter is TestAPI/API-14.
func testRouter(t *testing.T) {
	env := apitest.New(t, writeModule{})

	t.Run("unknown path", func(t *testing.T) {
		// Paths unknown to the description cannot be validated against it,
		// so these requests go to the router directly.
		paths := []string{
			"/v1/no_such_resource",
			"/v1/ssh_keys/1/no_such_part",
			"/",
			"/no_such_path",
			"/v2/ssh_keys",
		}
		for _, p := range paths {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://api.hetzner.internal"+p, nil)
			req.Header.Set("Authorization", "Bearer "+env.Token)
			rec := httptest.NewRecorder()
			env.Router.ServeHTTP(rec, req)
			resp := rec.Result()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s: status %d, want 404", p, resp.StatusCode)
			}
			if code := apitest.ErrorCode(t, resp); code != "not_found" {
				t.Errorf("GET %s: error code %q, want not_found", p, code)
			}
		}
	})

	t.Run("hetzner_prefix", func(t *testing.T) {
		env := apitest.New(t, writeModule{}, hetznerModule{})
		for _, p := range []string{"/hetzner/v1/storage_boxes", "/hetzner/v1/storage_boxes/1"} {
			if resp := serveRaw(t, env, http.MethodGet, p, ""); resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s: status %d, want 200 from the Hetzner module", p, resp.StatusCode)
			}
		}
		unknown := []struct{ method, path, body string }{
			{http.MethodGet, "/hetzner/v1/no_such_resource", ""},
			{http.MethodGet, "/hetzner/v1/storage_boxes/1/no_such_part", ""},
			{http.MethodGet, "/hetzner/v1", ""},
			{http.MethodGet, "/hetzner/v2/storage_boxes", ""},
			// A /v1 route is not reachable under /hetzner/v1 ...
			{http.MethodPost, "/hetzner/v1/ssh_keys", `{"name":"k"}`},
			{http.MethodPut, "/hetzner/v1/ssh_keys/1", `{"name":"k"}`},
			// ... nor a /hetzner/v1 route under /v1.
			{http.MethodGet, "/v1/storage_boxes", ""},
			{http.MethodGet, "/v1/storage_boxes/1", ""},
		}
		for _, u := range unknown {
			resp := serveRaw(t, env, u.method, u.path, u.body)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s: status %d, want 404", u.method, u.path, resp.StatusCode)
			}
			if code := apitest.ErrorCode(t, resp); code != "not_found" {
				t.Errorf("%s %s: error code %q, want not_found", u.method, u.path, code)
			}
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		requests := []struct{ method, path string }{
			{http.MethodPost, "/ssh_keys"},
			{http.MethodPut, "/ssh_keys/1"},
		}
		bodies := []string{`{"name":`, `not json`}
		for _, r := range requests {
			for _, b := range bodies {
				resp := env.For(t).Do(r.method, r.path, []byte(b))
				if resp.StatusCode != http.StatusBadRequest {
					t.Errorf("%s %s with %q: status %d, want 400", r.method, r.path, b, resp.StatusCode)
				}
				if code := apitest.ErrorCode(t, resp); code != "json_error" {
					t.Errorf("%s %s with %q: error code %q, want json_error", r.method, r.path, b, code)
				}
			}
		}
	})
}
