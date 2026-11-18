//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// testLIFE01: in the non-elevated test process, `hlab cloud stop` stops and
// `hlab cloud start` starts the dev cloud without a prompt.
func testLIFE01(t *testing.T) {
	assertNotElevated(t)
	ctx := context.Background()

	r, err := runHlab(ctx, 10*time.Minute, "cloud", "stop")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab cloud stop: %v\n%s", err, r)
	}
	waitService(t, "Stopped", 10*time.Minute)
	if err := waitCloudState("stopped", time.Minute); err != nil {
		t.Fatalf("hlab cloud status after stop: %v", err)
	}

	r, err = runHlab(ctx, 10*time.Minute, "cloud", "start")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab cloud start: %v\n%s", err, r)
	}
	waitService(t, "Running", 10*time.Minute)
	if err := waitCloudState("running", 15*time.Minute); err != nil {
		t.Fatalf("hlab cloud status after start: %v", err)
	}
}

// waitService waits until the dev service reports the given SCM state.
func waitService(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	var last string
	err := waitFor(timeout, 2*time.Second, func() (bool, error) {
		svcs, err := services(devService)
		if err != nil {
			return false, err
		}
		last = svcs[devService].State
		return last == want, nil
	})
	if err != nil {
		t.Fatalf("service %s did not reach %s (last %q): %v", devService, want, last, err)
	}
}

func assertNotElevated(t *testing.T) {
	t.Helper()
	elevated, err := isElevated()
	if err != nil {
		t.Fatal(err)
	}
	if elevated {
		t.Fatal("the test process is elevated; the requirement is about a user without administrator rights")
	}
}
