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
func newServeAsClient(t *testing.T, caller web.Caller, fakeLab *backend, rateLimit int) *spectest.Client {
	t.Helper()
	api := web.NewAPI(web.Options{
		Backend:   fakeLab,
		RateLimit: rateLimit,
		Logger:    slog.New(slog.DiscardHandler),
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
	client := newServeAsClient(t, web.Caller{ProjectID: project, Permission: state.PermissionReadWrite},
		&backend{faults: map[int64]state.APIFault{}}, 0)
	response := client.Do(http.MethodDelete, "/v1/ssh_keys/1", nil)
	if response.Status != http.StatusNoContent {
		t.Errorf("got %d, want 204", response.Status)
	}
	if response.Header.Get("X-Correlation-Id") == "" {
		t.Error("the answer has no X-Correlation-Id")
	}
}

func TestServeAsStillChecksThePermission(t *testing.T) {
	client := newServeAsClient(t, web.Caller{ProjectID: project, Permission: state.PermissionRead},
		&backend{faults: map[int64]state.APIFault{}}, 0)
	if response := client.Do(http.MethodDelete, "/v1/ssh_keys/1", nil); response.Status != http.StatusForbidden {
		t.Errorf("got %d, want 403", response.Status)
	}
}

// The web UI polls: it must neither use up the project's rate limit nor
// take the faults a test injected for the tools.
func TestServeAsSkipsTheRateLimitAndInjectedFaults(t *testing.T) {
	fakeLab := &backend{faults: map[int64]state.APIFault{project: {Status: 503, Count: 1}}}
	client := newServeAsClient(t, web.Caller{ProjectID: project, Permission: state.PermissionReadWrite}, fakeLab, 1)
	for range 3 {
		if response := client.Do(http.MethodDelete, "/v1/ssh_keys/1", nil); response.Status != http.StatusNoContent {
			t.Fatalf("got %d, want 204", response.Status)
		}
	}
	if fakeLab.faults[project].Count != 1 {
		t.Errorf("the fault has %d calls left, want 1", fakeLab.faults[project].Count)
	}
}
