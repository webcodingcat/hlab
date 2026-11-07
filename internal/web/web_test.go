package web_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/spectest"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// The tests' tokens.
const (
	readWrite = "read-write-token"
	readOnly  = "read-only-token"
	project   = 1_000_000
)

// backend is the lab as the API sees it: two tokens of one project and its
// faults.
type backend struct {
	mu     sync.Mutex
	faults map[int64]state.APIFault
}

func (b *backend) TokenByHash(hash string) (state.Token, bool) {
	switch hash {
	case state.HashToken(readWrite):
		return state.Token{ProjectID: project, Permission: state.PermissionReadWrite}, true
	case state.HashToken(readOnly):
		return state.Token{ProjectID: project, Permission: state.PermissionRead}, true
	}
	return state.Token{}, false
}

func (b *backend) TakeAPIFault(projectID int64) (state.APIFault, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fault := b.faults[projectID]
	if fault.Count == 0 {
		return state.APIFault{}, false
	}
	fault.Count--
	b.faults[projectID] = fault
	return fault, true
}

// newAPI returns an API with the routes the tests use, all of the Cloud API's
// spec, and a client of it with the read-write token.
func newAPI(t *testing.T, opts web.Options) (*spectest.Client, *backend) {
	t.Helper()
	fakeLab := &backend{faults: map[int64]state.APIFault{}}
	opts.Backend = fakeLab
	opts.Logger = slog.New(slog.DiscardHandler)
	api := web.NewAPI(opts)
	api.Handle("GET /actions/{id}", func(req *web.Request) (int, any, error) {
		return http.StatusOK, map[string]any{"action": map[string]any{
			"id": web.PathID(req, "id"), "command": "start_server", "status": "success", "progress": 100,
			"started": "2026-11-07T10:00:00Z", "finished": "2026-11-07T10:00:01Z",
			"resources": []any{}, "error": nil,
		}}, nil
	})
	api.Handle("POST /servers/{id}/actions/poweroff", func(*web.Request) (int, any, error) {
		return 0, nil, web.NotFound("server")
	})
	api.Handle("DELETE /ssh_keys/{id}", func(*web.Request) (int, any, error) {
		return http.StatusNoContent, nil, nil
	})
	api.Handle("GET /ssh_keys/{id}", func(*web.Request) (int, any, error) {
		return 0, nil, errors.New("the disk is full")
	})
	client := spectest.New(t, api, spectest.Cloud)
	client.Token = readWrite
	return client, fakeLab
}

// newRequest returns a GET request as a handler gets it.
func newRequest(target string) *web.Request {
	return &web.Request{Request: httptest.NewRequest(http.MethodGet, target, nil), Header: http.Header{}}
}

// errorOf returns the error of an answer: code, message and the raw details,
// nil if there are none.
func errorOf(t *testing.T, response *spectest.Response) (code, message string, details json.RawMessage) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	response.Decode(t, &body)
	return body.Error.Code, body.Error.Message, body.Error.Details
}

// Hetzner's API (measured 2026-11-07): 401 without or with an unknown token,
// with no details; 403 for a read token on a request that changes something.
func TestTokenCheck(t *testing.T) {
	client, _ := newAPI(t, web.Options{})
	for name, test := range map[string]struct {
		token, method string
		status        int
		message       string
		details       string
	}{
		"no token":             {"", http.MethodGet, 401, "token is required", ""},
		"an unknown token":     {"guess", http.MethodGet, 401, "the token you have provided is invalid", ""},
		"a read token reads":   {readOnly, http.MethodGet, 200, "", ""},
		"a read token deletes": {readOnly, http.MethodDelete, 403, "permission denied", "{}"},
		"read_write deletes":   {readWrite, http.MethodDelete, 204, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			client.Token = test.token
			path := "/v1/actions/1000000"
			if test.method == http.MethodDelete {
				path = "/v1/ssh_keys/1000000"
			}
			response := client.Do(test.method, path, nil)
			if response.Status != test.status {
				t.Fatalf("status %d, want %d: %s", response.Status, test.status, response.Body)
			}
			if test.message == "" {
				return
			}
			if _, message, details := errorOf(t, response); message != test.message || string(details) != test.details {
				t.Errorf("message %q with details %s, want %q with %q", message, details, test.message, test.details)
			}
		})
	}
}

// Hetzner's API sends a correlation ID once the token is checked, not for an
// unknown route or a missing token. hcloud-go adds it to its error texts.
func TestCorrelationIDOnceTheTokenIsChecked(t *testing.T) {
	client, _ := newAPI(t, web.Options{})
	ok := client.Get("/v1/actions/1000000")
	forbidden := client.Do(http.MethodPut, "/v1/actions/1000000", nil)
	unknownRoute := client.Get("/v1/nothing")
	client.Token = ""
	noToken := client.Get("/v1/actions/1000000")

	for name, response := range map[string]*spectest.Response{"200": ok, "405": forbidden} {
		if response.Header.Get("X-Correlation-Id") == "" {
			t.Errorf("the answer %s has no X-Correlation-Id", name)
		}
	}
	for name, response := range map[string]*spectest.Response{"unknown route": unknownRoute, "no token": noToken} {
		if id := response.Header.Get("X-Correlation-Id"); id != "" {
			t.Errorf("the answer to %s has the X-Correlation-Id %s", name, id)
		}
	}
	if ok.Header.Get("X-Correlation-Id") == forbidden.Header.Get("X-Correlation-Id") {
		t.Error("two answers share their correlation ID")
	}
}

// Hetzner's API (measured 2026-11-07): an unknown route is refused before the
// token is checked, a known route with another method only after it.
func TestErrorsUseHetznersFormat(t *testing.T) {
	client, _ := newAPI(t, web.Options{})
	for name, test := range map[string]struct {
		token, method, path string
		status              int
		code, message       string
	}{
		"an unknown route":         {"", http.MethodGet, "/v1/nothing", 404, "not_found", "api route not found"},
		"a path without the v1":    {"", http.MethodGet, "/actions/1", 404, "not_found", "api route not found"},
		"an ID that is no ID":      {"", http.MethodGet, "/v1/actions/abc", 404, "not_found", "api route not found"},
		"another method, no token": {"", http.MethodPut, "/v1/actions/1000000", 401, "unauthorized", "token is required"},
		"another method":           {readWrite, http.MethodPut, "/v1/actions/1000000", 405, "method_not_allowed", "method not allowed"},
		"a failure of the lab":     {readWrite, http.MethodGet, "/v1/ssh_keys/1000000", 500, "server_error", "the lab failed: the disk is full"},
	} {
		t.Run(name, func(t *testing.T) {
			client.Token = test.token
			response := client.Do(test.method, test.path, nil)
			code, message, details := errorOf(t, response)
			if response.Status != test.status || code != test.code || message != test.message {
				t.Errorf("answered %d %s, want %d %s %q", response.Status, response.Body, test.status, test.code, test.message)
			}
			if test.status != 401 && string(details) != "null" {
				t.Errorf("the details are %s, want null", details)
			}
		})
	}
}

func TestMethodNotAllowedNamesTheMethods(t *testing.T) {
	client, _ := newAPI(t, web.Options{})
	if allow := client.Do(http.MethodPut, "/v1/actions/1000000", nil).Header.Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow is %q, want Hetzner's %q", allow, "GET, HEAD")
	}
}

func TestInvalidInputListsItsFields(t *testing.T) {
	err := web.InvalidInput("invalid sort value", web.Field{Name: "sort", Messages: []string{"value 'x' is not allowed"}})
	details, _ := json.Marshal(err.Details)
	if err.Status != 422 || err.Code != "invalid_input" || string(details) != `{"fields":[{"name":"sort","messages":["value 'x' is not allowed"]}]}` {
		t.Errorf("the error is %+v with details %s", err, details)
	}
	if bare := web.InvalidInput("invalid integer value"); bare.Details != nil {
		t.Errorf("an error without fields has the details %v", bare.Details)
	}
}

func TestAPIFaultFailsTheNextCalls(t *testing.T) {
	client, fakeLab := newAPI(t, web.Options{})
	fakeLab.faults[project] = state.APIFault{Status: 503, Count: 2, DelayMS: 20}

	started := time.Now()
	first := client.Get("/v1/actions/1000000")
	if code, _, _ := errorOf(t, first); first.Status != 503 || code != "unavailable" {
		t.Errorf("the first call answered %d %s", first.Status, first.Body)
	}
	if waited := time.Since(started); waited < 20*time.Millisecond {
		t.Errorf("the first call took %v, want at least the delay of 20ms", waited)
	}
	if second := client.Get("/v1/actions/1000000"); second.Status != 503 {
		t.Errorf("the second call answered %d", second.Status)
	}
	if third := client.Get("/v1/actions/1000000"); third.Status != 200 {
		t.Errorf("the third call answered %d", third.Status)
	}
}
