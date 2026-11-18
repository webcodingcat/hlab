//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

// testPROJ05: in the non-elevated test process, `hlab project create/list`
// and `hlab token create` succeed with exit code 0 and no prompt (stdin is
// the null device and every command has a deadline).
func testPROJ05(t *testing.T) {
	assertNotElevated(t)
	// The shared setup runs `hlab project create` and `hlab token create`
	// and fails unless both exit with 0.
	e := sharedEnv(t)
	ctx := context.Background()

	r, err := runHlab(ctx, 2*time.Minute, "project", "list")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab project list: %v\n%s", err, r)
	}
	if !strings.Contains(r.Stdout, e.project) {
		t.Errorf("hlab project list does not show %q:\n%s", e.project, r.Stdout)
	}

	r, err = runHlab(ctx, 2*time.Minute, "token", "create", "--project", e.project, "--name", "proj-05", "--read-only")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab token create: %v (exit %d)\nstderr: %s", err, r.ExitCode, r.Stderr)
	}
	if strings.TrimSpace(r.Stdout) == "" {
		t.Error("hlab token create printed nothing")
	}
}
