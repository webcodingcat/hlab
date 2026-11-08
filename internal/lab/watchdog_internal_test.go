package lab

import (
	"log/slog"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// The watchdog looks at a VM without the mutex. An action that started and
// ended meanwhile makes what it saw out of date: settleServer then leaves
// the server alone. An internal test, as the window cannot be hit from
// outside on purpose.
func TestSettleServerIgnoresWhatAnActionChangedMeanwhile(t *testing.T) {
	l, err := Open(Options{DataDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)}, system.New(fake.New().Platform()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var id int64
	err = l.Change(func(tx *state.Tx) error {
		id = state.NewID(tx, state.Servers)
		state.Put(tx, state.Servers, &state.Server{ID: id, Status: state.ServerRunning, WantRunning: true})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := serverRef(id)
	l.mu.Lock()
	seen := l.flagged[ref]
	l.flagged[ref]++ // a poweron came and went while the watchdog looked
	l.mu.Unlock()

	l.settleServer(id, false, seen)
	if server, _ := Current(l, state.Servers, id); server.Status != state.ServerRunning || !server.WantRunning {
		t.Errorf("an out-of-date look changed the server: %s, wanting to run %v", server.Status, server.WantRunning)
	}
	l.settleServer(id, false, seen+1)
	if server, _ := Current(l, state.Servers, id); server.Status != state.ServerOff || server.WantRunning {
		t.Errorf("a current look left the server %s, wanting to run %v; want off", server.Status, server.WantRunning)
	}
}
