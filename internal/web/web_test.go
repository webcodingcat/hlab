package web_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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
	api.Handle("GET /actions/{id}", func(r *http.Request, _ web.Caller) (int, any, error) {
		id, err := web.PathID(r, "action")
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]any{"action": map[string]any{
			"id": id, "command": "start_server", "status": "success", "progress": 100,
			"started": "2026-11-07T10:00:00Z", "finished": "2026-11-07T10:00:01Z",
			"resources": []any{}, "error": nil,
		}}, nil
	})
	api.Handle("DELETE /ssh_keys/{id}", func(*http.Request, web.Caller) (int, any, error) {
		return http.StatusNoContent, nil, nil
	})
	api.Handle("GET /ssh_keys/{id}", func(*http.Request, web.Caller) (int, any, error) {
		return 0, nil, errors.New("the disk is full")
	})
	client := spectest.New(t, api, spectest.Cloud)
	client.Token = readWrite
	return client, fakeLab
}

// errorCode returns the error code of an answer, "" if it has none.
func errorCode(t *testing.T, response *spectest.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	response.Decode(t, &body)
	return body.Error.Code
}

// Hetzner's API: 401 without or with an unknown token, 403 for a read token
// on a request that changes something.
func TestTokenCheck(t *testing.T) {
	client, _ := newAPI(t, web.Options{})
	for name, test := range map[string]struct {
		token, method string
		status        int
		code          string
	}{
		"no token":             {"", http.MethodGet, 401, "unauthorized"},
		"an unknown token":     {"guess", http.MethodGet, 401, "unauthorized"},
		"a read token reads":   {readOnly, http.MethodGet, 200, ""},
		"a read token deletes": {readOnly, http.MethodDelete, 403, "forbidden"},
		"read_write deletes":   {readWrite, http.MethodDelete, 204, ""},
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
			if test.code != "" && errorCode(t, response) != test.code {
				t.Errorf("code %q, want %q", errorCode(t, response), test.code)
			}
		})
	}
}

// hcloud-go adds the correlation ID to its error texts, so errors need one
// as well.
func TestEveryAnswerHasACorrelationID(t *testing.T) {
	client, _ := newAPI(t, web.Options{})
	ok := client.Get("/v1/actions/1000000")
	client.Token = ""
	refused := client.Get("/v1/actions/1000000")
	for _, response := range []*spectest.Response{ok, refused} {
		if response.Header.Get("X-Correlation-Id") == "" {
			t.Errorf("the answer %d has no X-Correlation-Id", response.Status)
		}
	}
	if ok.Header.Get("X-Correlation-Id") == refused.Header.Get("X-Correlation-Id") {
		t.Error("two answers share their correlation ID")
	}
}

func TestErrorsUseHetznersFormat(t *testing.T) {
	client, _ := newAPI(t, web.Options{})
	for name, test := range map[string]struct {
		method, path string
		status       int
		code         string
	}{
		"an unknown route":      {http.MethodGet, "/v1/nothing", 404, "not_found"},
		"a path without the v1": {http.MethodGet, "/actions/1", 404, "not_found"},
		"an ID that is no ID":   {http.MethodGet, "/v1/actions/abc", 404, "not_found"},
		"another method":        {http.MethodPut, "/v1/actions/1000000", 405, "method_not_allowed"},
		"a failure of the lab":  {http.MethodGet, "/v1/ssh_keys/1000000", 500, "server_error"},
	} {
		t.Run(name, func(t *testing.T) {
			response := client.Do(test.method, test.path, nil)
			if response.Status != test.status || errorCode(t, response) != test.code {
				t.Errorf("answered %d %s, want %d %s", response.Status, response.Body, test.status, test.code)
			}
			var body map[string]map[string]json.RawMessage
			response.Decode(t, &body)
			if _, found := body["error"]["details"]; !found {
				t.Errorf("the error has no details: %s", response.Body)
			}
		})
	}
}

func TestInvalidInputNamesTheFirstField(t *testing.T) {
	err := web.InvalidInput(
		web.Field{Name: "name", Messages: []string{"is too long", "is taken"}},
		web.Field{Name: "labels", Messages: []string{"is invalid"}},
	)
	if err.Status != 422 || err.Code != "invalid_input" || err.Message != "invalid input in field 'name': is too long" {
		t.Errorf("the error is %+v", err)
	}
}

func TestAPIFaultFailsTheNextCalls(t *testing.T) {
	client, fakeLab := newAPI(t, web.Options{})
	fakeLab.faults[project] = state.APIFault{Status: 503, Count: 2, DelayMS: 20}

	started := time.Now()
	first := client.Get("/v1/actions/1000000")
	if first.Status != 503 || errorCode(t, first) != "unavailable" {
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
