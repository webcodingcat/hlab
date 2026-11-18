package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
