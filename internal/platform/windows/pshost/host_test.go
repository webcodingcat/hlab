//go:build windows

package pshost_test

import (
	"context"
	_ "embed"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/platform/windows/pshost"
)

//go:embed testdata/commands.ps1
var commands string

// newHost returns a Host for the test commands and closes it after the test.
func newHost(t *testing.T, processes int) *pshost.Host {
	t.Helper()
	host := pshost.New(commands, pshost.Options{Processes: processes})
	t.Cleanup(func() { _ = host.Close() })
	return host
}

// pid returns the process that runs the next call.
func pid(t *testing.T, host *pshost.Host) int {
	t.Helper()
	var got int
	if err := host.Call(t.Context(), "pid", nil, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCallReturnsTheResult(t *testing.T) {
	host := newHost(t, 1)
	type value struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	sent := value{Name: "grüße €", Count: 3}
	var got value
	if err := host.Call(t.Context(), "echo", sent, &got); err != nil {
		t.Fatal(err)
	}
	if got != sent {
		t.Fatalf("echo returned %+v, want %+v", got, sent)
	}
}

func TestListsOfOneOrNoneStayLists(t *testing.T) {
	host := newHost(t, 1)
	for _, count := range []int{0, 1, 3} {
		var got []int
		if err := host.Call(t.Context(), "list", map[string]int{"count": count}, &got); err != nil {
			t.Fatal(err)
		}
		if count == 0 && len(got) != 0 || count > 0 && len(got) != count {
			t.Errorf("list of %d returned %v", count, got)
		}
	}
}

func TestFailedCommandsReturnTheirKind(t *testing.T) {
	host := newHost(t, 1)
	err := host.Call(t.Context(), "fail", map[string]string{"kind": "not_found", "message": "VM web"}, nil)
	var failure *pshost.Error
	if !errors.As(err, &failure) || failure.Kind != "not_found" || failure.Message != "VM web" {
		t.Fatalf("fail returned %#v", err)
	}

	err = host.Call(t.Context(), "cmdlet", nil, nil)
	if !errors.As(err, &failure) || failure.Kind != "" || failure.Message == "" {
		t.Fatalf("a failed cmdlet returned %#v", err)
	}

	err = host.Call(t.Context(), "missing", nil, nil)
	if !errors.As(err, &failure) || failure.Kind != "unknown_command" {
		t.Fatalf("an unknown command returned %#v", err)
	}
}

func TestStrayOutputDoesNotDisturbTheAnswer(t *testing.T) {
	host := newHost(t, 1)
	var got string
	if err := host.Call(t.Context(), "noise", nil, &got); err != nil {
		t.Fatal(err)
	}
	if got != "quiet" {
		t.Fatalf("noise returned %q, want quiet", got)
	}
}

func TestACrashedProcessIsReplaced(t *testing.T) {
	host := newHost(t, 1)
	before := pid(t, host)
	if err := host.Call(t.Context(), "exit", nil, nil); !errors.Is(err, pshost.ErrCrashed) {
		t.Fatalf("exit returned %v, want ErrCrashed", err)
	}
	if after := pid(t, host); after == before {
		t.Fatal("the crashed process still answers")
	}
}

func TestACanceledCallEndsAtOnce(t *testing.T) {
	host := newHost(t, 1)
	before := pid(t, host)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := host.Call(ctx, "sleep", map[string]int{"seconds": 60}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sleep returned %v, want DeadlineExceeded", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the canceled call took %s", took)
	}
	if after := pid(t, host); after == before {
		t.Fatal("the process of the canceled call still answers")
	}
}

func TestCallsRunInSeveralProcesses(t *testing.T) {
	host := newHost(t, 2)
	// warm up both processes, so that the timing below leaves out the start
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _ = host.Call(t.Context(), "sleep", map[string]int{"seconds": 0}, nil) })
	}
	wg.Wait()

	started := time.Now()
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { errs <- host.Call(t.Context(), "sleep", map[string]int{"seconds": 2}, nil) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(started); took > 3500*time.Millisecond {
		t.Fatalf("two calls of 2 s took %s: they did not run side by side", took)
	}
}

func TestABrokenScriptFailsTheCall(t *testing.T) {
	host := pshost.New("'not a hashtable'", pshost.Options{})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Call(t.Context(), "echo", nil, nil); err == nil {
		t.Fatal("a script without commands loaded")
	}
}

func TestCallAfterCloseFails(t *testing.T) {
	host := newHost(t, 1)
	pid(t, host)
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if err := host.Call(t.Context(), "pid", nil, nil); !errors.Is(err, pshost.ErrClosed) {
		t.Fatalf("Call after Close returned %v, want ErrClosed", err)
	}
}
