package agentclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/store"
)

func TestStorageClient(t *testing.T) {
	t.Run("SBOX-08/apply-deadline", func(t *testing.T) {
		st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })

		// The fake storage agent never answers the first apply; it notes
		// when hlabd gives that request up. Later applies are answered.
		var applies atomic.Int32
		abandoned := make(chan struct{})
		stop := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut || r.URL.Path != "/v1/storage" {
				http.NotFound(w, r)
				return
			}
			if applies.Add(1) == 1 {
				select {
				case <-r.Context().Done():
					close(abandoned)
				case <-stop:
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Generation":2}`))
		}))
		t.Cleanup(srv.Close)
		t.Cleanup(func() { close(stop) })

		c := NewStorage(Options{Addr: strings.TrimPrefix(srv.URL, "http://"), Secret: "s", Store: st})
		c.applyTimeout = 200 * time.Millisecond

		// The caller's context has no deadline: the apply's own one ends it.
		result := make(chan error, 1)
		go func() { result <- c.Push(context.Background(), agentapi.Storage{}) }()
		select {
		case err = <-result:
		case <-time.After(10 * time.Second):
			t.Fatal("Push still running 10 s after an apply deadline of 200 ms")
		}
		if err == nil {
			t.Fatal("Push = nil, want an error for the apply past its deadline")
		}
		select {
		case <-abandoned:
		case <-time.After(5 * time.Second):
			t.Error("the request of the expired apply is still open at the agent")
		}

		// The expired apply holds no lock: the next apply goes through.
		go func() { result <- c.Push(context.Background(), agentapi.Storage{}) }()
		select {
		case err = <-result:
		case <-time.After(10 * time.Second):
			t.Fatal("second Push still running 10 s after the first one expired: lock still held")
		}
		if err != nil {
			t.Errorf("second Push = %v, want nil from an agent that answers", err)
		}
	})
}
