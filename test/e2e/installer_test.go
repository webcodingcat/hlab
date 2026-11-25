//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// installerVersion is the version the INST-01 bundle is built with.
const installerVersion = "0.0.1"

// burnError matches an error line of a Burn log ("[pid:tid][time]e000: ...").
var burnError = regexp.MustCompile(`\]e000[:\s]`)

// testINST01: the setup bundle built by tools/installer starts its
// bootstrapper application without elevation: `/layout` into a temporary
// folder exits 0 and its log has no e000 line.
func testINST01(t *testing.T) {
	ctx := context.Background()

	// TestMain builds the frontend with `task build`; build it here only if
	// its output is missing, as the install harness does.
	if notExist(filepath.Join(harness.root, "ui", "frontend", "dist")) {
		if r, err := command(ctx, 15*time.Minute, nil, "", "task", "frontend:build"); err != nil {
			t.Fatalf("task frontend:build: %v\n%s", err, r)
		}
	}

	dir, err := os.MkdirTemp(harness.tmp, "inst01-")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "build")
	if r, err := command(ctx, 20*time.Minute, nil, "", "go", "run", "./tools/installer", "-out", out, installerVersion); err != nil {
		t.Fatalf("go run ./tools/installer -out %s %s: %v\n%s", out, installerVersion, err, r)
	}
	exe := filepath.Join(out, "hlab-setup-"+installerVersion+".exe")
	if notExist(exe) {
		t.Fatalf("tools/installer %s wrote no %s", installerVersion, exe)
	}

	layout := filepath.Join(dir, "layout")
	logFile := filepath.Join(dir, "layout.log")
	r, runErr := command(ctx, 5*time.Minute, nil, "", exe, "/layout", layout, "/quiet", "/log", logFile)
	if runErr != nil && r.ExitCode < 0 {
		t.Fatalf("%s /layout: %v\n%s", filepath.Base(exe), runErr, r)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("%s /layout wrote no log (exit %#x): %v", filepath.Base(exe), uint32(r.ExitCode), err)
	}
	var errLines []string
	for _, line := range strings.Split(string(data), "\n") {
		if burnError.MatchString(line) {
			errLines = append(errLines, strings.TrimSpace(line))
		}
	}
	if r.ExitCode != 0 || len(errLines) > 0 {
		t.Fatalf("%s /layout exited %#x; e000 lines of %s:\n%s",
			filepath.Base(exe), uint32(r.ExitCode), logFile, strings.Join(errLines, "\n"))
	}
	if notExist(layout) {
		t.Fatalf("%s /layout exited 0 but wrote no %s", filepath.Base(exe), layout)
	}
}
