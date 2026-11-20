//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/test/traylaunch"
)

// The UI rows share one tray process of the e2e build (tag e2e) and its
// window. Each row runs the Playwright test of its ID in ui/frontend, which
// connects to the window over CDP (ui/frontend/tests/e2e/fixtures.ts).

var tray struct {
	once sync.Once
	mu   sync.Mutex
	proc *traylaunch.Tray
	err  error
}

// sharedTray builds and starts the e2e tray once, for all UI rows.
func sharedTray(t *testing.T) *traylaunch.Tray {
	t.Helper()
	tray.once.Do(func() {
		p, err := startTray()
		tray.mu.Lock()
		tray.proc, tray.err = p, err
		tray.mu.Unlock()
	})
	if tray.err != nil {
		t.Fatalf("e2e tray: %v", tray.err)
	}
	select {
	case <-tray.proc.Done():
		t.Fatalf("the e2e tray has ended (%v); its log: %s", tray.proc.Err(), tray.proc.Log)
	default:
	}
	return tray.proc
}

// startTray builds cmd/hlab-tray with tag e2e next to the other dev
// binaries and starts it with a user data folder of this run.
func startTray() (*traylaunch.Tray, error) {
	exe := filepath.Join(harness.root, "bin", "hlab-tray-e2e.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := traylaunch.Build(ctx, harness.root, exe); err != nil {
		return nil, err
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return traylaunch.Start(ctx, exe, filepath.Join(harness.tmp, "webview"))
}

// releaseTray ends the e2e tray if a UI row started it.
func releaseTray(t *testing.T) {
	t.Helper()
	tray.mu.Lock()
	p := tray.proc
	tray.proc = nil
	tray.mu.Unlock()
	if p != nil {
		p.Stop()
	}
}

// runSpec runs the Playwright tests named id against the shared tray and
// logs each spec's result (list reporter). A skipped spec fails the row. The
// values in secrets are given to the test through env and never logged.
func runSpec(t *testing.T, id string, timeout time.Duration, env []string, secrets ...string) {
	t.Helper()
	p := sharedTray(t)
	frontend := filepath.Join(harness.root, "ui", "frontend")
	environ := append(os.Environ(),
		"HLAB_E2E_CDP_PORT="+strconv.Itoa(p.Port),
		"HLAB_E2E_HLAB="+harness.hlab,
	)
	environ = append(environ, env...)

	start := time.Now()
	t.Logf("%s: start %s (deadline %s)", id, start.Format(time.TimeOnly), timeout)
	r, err := command(context.Background(), timeout, environ, "", "node",
		filepath.Join(frontend, "node_modules", "@playwright", "test", "cli.js"), "test",
		"--config", filepath.Join(frontend, "playwright.config.ts"),
		"--project", "e2e", "--grep", id, "--reporter", "list")
	t.Logf("%s: end after %s", id, time.Since(start).Round(time.Second))
	for _, s := range secrets {
		if s != "" {
			r = redact(r, s)
		}
	}
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("playwright test --grep %s: %v\n%s", id, err, r)
	}
	t.Logf("%s: playwright output:\n%s", id, r.Stdout)
	if skipped.MatchString(r.Stdout) {
		t.Fatalf("playwright test --grep %s skipped specs", id)
	}
}

// skipped matches the skipped count in Playwright's summary.
var skipped = regexp.MustCompile(`(?m)^\s*\d+ skipped`)

// testUI01: through the tray menu handlers the cloud stops and starts
// again; the tray state and the top bar pill follow, and hlab cloud status
// agrees.
func testUI01(t *testing.T) {
	// A failed run must not leave the cloud stopped for the rows after it.
	t.Cleanup(func() {
		if s, err := cloudState(); err == nil && s == "running" {
			return
		}
		if r, err := runHlab(context.Background(), 10*time.Minute, "cloud", "start"); err != nil {
			t.Errorf("hlab cloud start after UI-01: %v\n%s", err, r)
			return
		}
		if err := waitCloudState("running", 15*time.Minute); err != nil {
			t.Errorf("after UI-01: %v", err)
		}
	})
	runSpec(t, "UI-01", 40*time.Minute, nil)
}

// testUI03: the settings view changes every key of settings.toml; the
// original file is restored afterwards.
func testUI03(t *testing.T) {
	path := settingsPath()
	orig, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read %s: %v", path, err)
	}
	t.Cleanup(func() {
		var err error
		if existed {
			err = os.WriteFile(path, orig, 0o644)
		} else {
			err = os.Remove(path)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("restore %s: %v", path, err)
		}
	})
	runSpec(t, "UI-03", 10*time.Minute, []string{"HLAB_E2E_SETTINGS=" + path})
}

// testUI04: the server types view lists the catalog, adds and removes an
// own type that the project's API serves, and starts the catalog update
// with the token of a usable hcloud context or, without one, an invalid
// token.
func testUI04(t *testing.T) {
	e := sharedEnv(t)
	env := []string{"HLAB_E2E_API_TOKEN=" + e.token}
	acc, why := usableHetzner(context.Background())
	if why != "" {
		t.Logf("no usable hcloud context (%s); UI-04 checks Hetzner's refusal of an invalid token", why)
	} else {
		env = append(env, tokenVar+"="+acc.token)
	}
	runSpec(t, "UI-04", 20*time.Minute, env, e.token, acc.token)
}

// testUI05: the projects view creates a project and a token, revokes the
// token and deletes the project.
func testUI05(t *testing.T) {
	runSpec(t, "UI-05", 10*time.Minute, nil)
}

// testUI02: the project console's sections, one spec each. SSH Keys and
// Servers run in a project of their own: an SSH key is created, labelled and
// deleted; a server is created through the form, renamed, powered off and
// deleted, with the type, location and image of the other server rows.
// Volumes, Load Balancers, Networks, Firewalls, Floating IPs, Primary IPs,
// DNS, Certificates and Placement Groups share one project: each creates a
// resource through its form, labels and deletes it; ISOs lists the API's
// ISOs. The limit is about twice the last full run (1m26s).
func testUI02(t *testing.T) {
	runSpec(t, "UI-02", 3*time.Minute, []string{
		"HLAB_E2E_SERVER_TYPE=" + serverType,
		"HLAB_E2E_SERVER_LOCATION=" + serverLocation,
		"HLAB_E2E_SERVER_IMAGE=" + serverImage,
	})
}

// testUI10: the Administration ISOs view lists the catalog and adds an own
// ISO by URL and one by a local .iso file (a small file of this run), edits
// and removes them; a project of its own checks /isos. The spec removes
// every own ISO it added. The limit is generous until the first full run.
func testUI10(t *testing.T) {
	file := filepath.Join(harness.tmp, "ui10-own.iso")
	if err := os.WriteFile(file, []byte("hlab e2e UI-10: not a real ISO image\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	t.Cleanup(func() { _ = os.Remove(file) })
	runSpec(t, "UI-10", 10*time.Minute, []string{"HLAB_E2E_ISO_FILE=" + file})
}

// testUI06: the logs view shows the service log and follows it.
func testUI06(t *testing.T) {
	runSpec(t, "UI-06", 10*time.Minute, nil)
}

// hetznerAccount is what a usable hcloud context gives: its token and the
// names of the real server types.
type hetznerAccount struct {
	token string
	types map[string]bool
}

// usableHetzner checks for a usable hcloud context (SPEC "Accounts and
// secrets"): named by HLAB_HCLOUD_CONTEXT (default HLab), listed by
// `hcloud context list`, pointing at the real API, listing server types and
// holding a token. It returns why the context is not usable, or "". The
// token is taken through hcloud itself and never printed or written.
func usableHetzner(ctx context.Context) (hetznerAccount, string) {
	name := os.Getenv("HLAB_HCLOUD_CONTEXT")
	if name == "" {
		name = "HLab"
	}
	// hcloud never sees HCLOUD_TOKEN, HCLOUD_ENDPOINT or HCLOUD_CONTEXT; the
	// context is always selected by name.
	var real []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HCLOUD_TOKEN", "HCLOUD_ENDPOINT", "HCLOUD_CONTEXT":
			continue
		}
		real = append(real, kv)
	}
	hcloud := func(timeout time.Duration, args ...string) (result, error) {
		return command(ctx, timeout, real, "", harness.hcloud, append([]string{"--context", name}, args...)...)
	}

	r, err := command(ctx, time.Minute, real, "", harness.hcloud, "context", "list", "-o", "noheader", "-o", "columns=name")
	if err != nil {
		return hetznerAccount{}, fmt.Sprintf("hcloud context list: %v; stderr: %s", err, r.Stderr)
	}
	listed := false
	for _, line := range strings.Split(r.Stdout, "\n") {
		if strings.TrimSpace(line) == name {
			listed = true
		}
	}
	if !listed {
		return hetznerAccount{}, fmt.Sprintf("%q is not listed by hcloud context list (set HLAB_HCLOUD_CONTEXT)", name)
	}
	r, err = hcloud(time.Minute, "config", "get", "endpoint")
	if err != nil || strings.TrimSpace(r.Stdout) != realEndpoint {
		return hetznerAccount{}, fmt.Sprintf("endpoint of %q is %q, want %s (%v)", name, strings.TrimSpace(r.Stdout), realEndpoint, err)
	}
	r, err = hcloud(2*time.Minute, "server-type", "list", "-o", "json")
	if err != nil {
		return hetznerAccount{}, fmt.Sprintf("%q: hcloud server-type list: %v; stderr: %s", name, err, r.Stderr)
	}
	types, err := typeNames(r.Stdout)
	if err != nil || len(types) == 0 {
		return hetznerAccount{}, fmt.Sprintf("%q: the real API listed no server types (%v)", name, err)
	}
	r, err = hcloud(time.Minute, "config", "get", "token", "--allow-sensitive")
	token := strings.TrimSpace(r.Stdout)
	if err != nil || token == "" {
		// Never show stdout here: it may hold the token.
		return hetznerAccount{}, fmt.Sprintf("%q: hcloud config get token gave no token (%v; stderr: %s)", name, err, r.Stderr)
	}
	return hetznerAccount{token: token, types: types}, ""
}
