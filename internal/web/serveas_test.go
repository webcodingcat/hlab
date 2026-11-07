package web_test

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/webcodingcat/hlab/internal/spectest"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// newServeAsClient returns a client without a token of an API that serves
// as the caller.
func newServeAsClient(t *testing.T, caller web.Caller) *spectest.Client {
	t.Helper()
	api := web.NewAPI(web.Options{
		Backend: &backend{faults: map[int64]state.APIFault{}},
		Logger:  slog.New(slog.DiscardHandler),
	})
	api.Handle("DELETE /ssh_keys/{id}", func(req *web.Request) (int, any, error) {
		if req.Caller != caller {
			t.Errorf("the handler got the caller %+v, want %+v", req.Caller, caller)
		}
		return http.StatusNoContent, nil, nil
	})
	return spectest.New(t, api.ServeAs(caller), spectest.Cloud)
}

func TestServeAsAdmitsTheCallerWithoutAToken(t *testing.T) {
	client := newServeAsClient(t, web.Caller{ProjectID: project, Permission: state.PermissionReadWrite})
	response := client.Do(http.MethodDelete, "/v1/ssh_keys/1", nil)
	if response.Status != http.StatusNoContent {
		t.Errorf("got %d, want 204", response.Status)
	}
	if response.Header.Get("X-Correlation-Id") == "" {
		t.Error("the answer has no X-Correlation-Id")
	}
}

func TestServeAsStillChecksThePermission(t *testing.T) {
	client := newServeAsClient(t, web.Caller{ProjectID: project, Permission: state.PermissionRead})
	if response := client.Do(http.MethodDelete, "/v1/ssh_keys/1", nil); response.Status != http.StatusForbidden {
		t.Errorf("got %d, want 403", response.Status)
	}
}
