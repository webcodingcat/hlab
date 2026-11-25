package storageagent

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeCommandEnv makes the test binary act as a fake command: with the
// value "hang" it sleeps far longer than any test deadline.
const fakeCommandEnv = "HLAB_STORAGEAGENT_FAKE_COMMAND"

func TestMain(m *testing.M) {
	if os.Getenv(fakeCommandEnv) == "hang" {
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// durationRE matches a Go duration such as "200ms", "1.5s" or "2m0s".
var durationRE = regexp.MustCompile(`\d+(\.\d+)?(ns|µs|us|ms|s|m|h)\b`)

func TestRun(t *testing.T) {
	t.Run("SBOX-08/command-deadline", func(t *testing.T) {
		t.Setenv(fakeCommandEnv, "hang")
		old := commandTimeout
		commandTimeout = 200 * time.Millisecond
		t.Cleanup(func() { commandTimeout = old })

		name := os.Args[0]
		type outcome struct {
			out string
			err error
		}
		result := make(chan outcome, 1)
		start := time.Now()
		// The caller's context has no deadline: the command's own one ends it.
		go func() {
			out, err := run(context.Background(), name, "fake-arg")
			result <- outcome{out, err}
		}()
		var got outcome
		select {
		case got = <-result:
		case <-time.After(10 * time.Second):
			t.Fatal("run still waiting 10 s after a command deadline of 200 ms: the command was not ended")
		}
		took := time.Since(start)
		if got.err == nil {
			t.Fatalf("run = nil after %v, want an error for the command past its deadline", took)
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Errorf("run error %q, want one wrapping context.DeadlineExceeded", got.err)
		}
		msg := got.err.Error()
		if !strings.Contains(msg, name) {
			t.Errorf("run error %q does not name the command %q", msg, name)
		}
		if !durationRE.MatchString(strings.ReplaceAll(msg, name, "")) {
			t.Errorf("run error %q does not give the command's duration", msg)
		}
	})
}
