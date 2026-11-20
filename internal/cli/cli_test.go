package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/cli"
	"github.com/webcodingcat/hlab/internal/mgmt"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// hlab runs the management command against env's store through the
// management API, served on a loopback listener in place of the pipe.
type hlab struct {
	env  *apitest.Env
	dial func(ctx context.Context) (net.Conn, error)
}

func newHlab(t *testing.T) *hlab {
	t.Helper()
	return newHlabWith(t, nil)
}

// newHlabWith is newHlab with the management API's deps adjusted by with.
func newHlabWith(t *testing.T, with func(*mgmt.Deps)) *hlab {
	t.Helper()
	env := apitest.New(t, apitest.ProbeModule())
	deps := mgmt.Deps{
		Store:   env.Store,
		API:     env.Router,
		Status:  func() mgmt.Status { return mgmt.Status{State: "running"} },
		Actions: env.Actions,
	}
	if with != nil {
		with(&deps)
	}
	srv := httptest.NewServer(mgmt.New(deps))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	return &hlab{env: env, dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}}
}

// run executes `hlab args...` and returns exit code, stdout and stderr.
func (h *hlab) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return h.runWith(t, nil, args...)
}

// runWith is run with the IO adjusted by with.
func (h *hlab) runWith(t *testing.T, with func(*cli.IO), args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr syncBuffer
	code := h.runOut(context.Background(), t, &stdout, &stderr, with, args...)
	return code, stdout.String(), stderr.String()
}

// runOut runs `hlab args...` with ctx, writing to stdout and stderr, and
// returns the exit code.
func (h *hlab) runOut(ctx context.Context, t *testing.T, stdout, stderr *syncBuffer, with func(*cli.IO), args ...string) int {
	t.Helper()
	sys := cli.IO{
		Stdin:      strings.NewReader(""),
		Stdout:     stdout,
		Stderr:     stderr,
		Dial:       h.dial,
		IsTerminal: func() bool { return false },
		Getenv:     func(string) string { return "" },
		ReadSecret: func(string) (string, error) {
			t.Errorf("hlab %s read a secret from the terminal", strings.Join(args, " "))
			return "", errors.New("no terminal")
		},
	}
	if with != nil {
		with(&sys)
	}
	return cli.Run(ctx, args, sys)
}

// syncBuffer is a bytes.Buffer safe for a command writing while the test
// reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// createToken runs `hlab token create --project p args...` and returns the
// one token of 64 characters it prints.
func (h *hlab) createToken(t *testing.T, args ...string) string {
	t.Helper()
	out := h.mustRun(t, append([]string{"token", "create", "--project", "p"}, args...)...)
	runs := regexp.MustCompile(`[A-Za-z0-9]{33,}`).FindAllString(out, -1)
	if len(runs) != 1 || len(runs[0]) != 64 {
		t.Fatalf("token create printed %q, want exactly one token of 64 characters [A-Za-z0-9]", out)
	}
	return runs[0]
}

func (h *hlab) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errOut := h.run(t, args...)
	if code != 0 {
		t.Fatalf("hlab %s: exit %d, stderr %q", strings.Join(args, " "), code, errOut)
	}
	return out
}

func listed(out, name string) bool {
	return regexp.MustCompile(`(?m)(^|\s|")` + regexp.QuoteMeta(name) + `($|\s|")`).MatchString(out)
}

func TestCLI(t *testing.T) {
	t.Run("PROJ-01", func(t *testing.T) {
		h := newHlab(t)

		h.mustRun(t, "project", "create", "alpha")
		if out := h.mustRun(t, "project", "list"); !listed(out, "alpha") {
			t.Errorf("project list after create does not show alpha:\n%s", out)
		}
		if code, _, _ := h.run(t, "project", "create", "alpha"); code != 1 {
			t.Errorf("duplicate project create: exit %d, want 1", code)
		}
		h.mustRun(t, "project", "delete", "alpha")
		deadline := time.Now().Add(10 * time.Second)
		for {
			out := h.mustRun(t, "project", "list")
			if !listed(out, "alpha") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("project alpha still listed 10 s after delete:\n%s", out)
			}
			time.Sleep(20 * time.Millisecond)
		}

		// Without a reachable management pipe every command except cloud fails.
		down := &hlab{dial: func(context.Context) (net.Conn, error) {
			return nil, errors.New("open \\\\.\\pipe\\hlab-dev: The system cannot find the file specified.")
		}}
		for _, args := range [][]string{
			{"project", "create", "beta"},
			{"project", "list"},
			{"project", "delete", "beta"},
			{"token", "create", "--project", "p", "--name", "t"},
			{"token", "list", "--project", "p"},
			{"token", "revoke", "--project", "p", "t"},
		} {
			code, _, errOut := down.run(t, args...)
			if code != 1 || !strings.Contains(errOut, "the cloud is not running") {
				t.Errorf("hlab %s with the cloud stopped: exit %d, stderr %q; want exit 1 and \"the cloud is not running\"",
					strings.Join(args, " "), code, errOut)
			}
		}
	})

	t.Run("PROJ-02", func(t *testing.T) {
		h := newHlab(t)
		rw := h.createToken(t, "--name", "writer")
		ro := h.createToken(t, "--name", "reader", "--read-only")

		if out := h.mustRun(t, "token", "list", "--project", "p"); strings.Contains(out, rw) || strings.Contains(out, ro) {
			t.Errorf("token list shows a token value:\n%s", out)
		}

		key := map[string]any{"name": "k", "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIProbeKeyForTestsOnly k"}
		for _, c := range []struct {
			name, token string
			wantPost    int
		}{
			{"read/write", rw, http.StatusCreated},
			{"read-only", ro, http.StatusForbidden},
		} {
			if resp := h.env.DoAuth("Bearer "+c.token, http.MethodGet, "/ssh_keys", nil); resp.StatusCode != http.StatusOK {
				t.Errorf("%s token: GET status %d, want 200", c.name, resp.StatusCode)
			}
			resp := h.env.DoAuth("Bearer "+c.token, http.MethodPost, "/ssh_keys", key)
			if resp.StatusCode != c.wantPost {
				t.Errorf("%s token: POST status %d, want %d", c.name, resp.StatusCode, c.wantPost)
			}
			if c.wantPost == http.StatusForbidden {
				if code := apitest.ErrorCode(t, resp); code != "token_readonly" {
					t.Errorf("read-only token: POST error code %q, want token_readonly", code)
				}
			}
		}
	})

	t.Run("PROJ-03", func(t *testing.T) {
		h := newHlab(t)
		gone := h.createToken(t, "--name", "gone")
		kept := h.createToken(t, "--name", "kept", "--read-only")

		out := h.mustRun(t, "token", "list", "--project", "p")
		if !listed(out, "gone") || !listed(out, "kept") {
			t.Errorf("token list does not show gone and kept:\n%s", out)
		}
		jsonOut := h.mustRun(t, "token", "list", "--project", "p", "--output", "json")
		for _, o := range []string{out, jsonOut} {
			if strings.Contains(o, gone) || strings.Contains(o, kept) {
				t.Errorf("token list shows a token value:\n%s", o)
			}
		}
		var list struct {
			Tokens []struct {
				Name     string `json:"name"`
				ReadOnly bool   `json:"read_only"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal([]byte(jsonOut), &list); err != nil {
			t.Fatalf("token list --output json: %v\n%s", err, jsonOut)
		}
		readOnly := map[string]bool{}
		for _, tok := range list.Tokens {
			readOnly[tok.Name] = tok.ReadOnly
		}
		if ro, ok := readOnly["gone"]; !ok || ro {
			t.Errorf("token gone: listed %v, read-only %v; want listed read/write", ok, ro)
		}
		if ro, ok := readOnly["kept"]; !ok || !ro {
			t.Errorf("token kept: listed %v, read-only %v; want listed read-only", ok, ro)
		}

		if resp := h.env.DoAuth("Bearer "+gone, http.MethodGet, "/ssh_keys", nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("token gone before revoke: status %d, want 200", resp.StatusCode)
		}
		h.mustRun(t, "token", "revoke", "--project", "p", "gone")
		resp := h.env.DoAuth("Bearer "+gone, http.MethodGet, "/ssh_keys", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("revoked token: status %d, want 401", resp.StatusCode)
		} else if code := apitest.ErrorCode(t, resp); code != "unauthorized" {
			t.Errorf("revoked token: error code %q, want unauthorized", code)
		}
		if resp := h.env.DoAuth("Bearer "+kept, http.MethodGet, "/ssh_keys", nil); resp.StatusCode != http.StatusOK {
			t.Errorf("other token of the project after revoke: status %d, want 200", resp.StatusCode)
		}
	})

	t.Run("CERT-05", func(t *testing.T) {
		const pem = "-----BEGIN CERTIFICATE-----\nTUlJQ2FDQQ==\n-----END CERTIFICATE-----\n"
		h := newHlabWith(t, func(d *mgmt.Deps) {
			d.CACert = func() []byte { return []byte(pem) }
		})
		file := filepath.Join(t.TempDir(), "hlab-ca.pem")
		h.mustRun(t, "ca", "export", file)
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read the exported file: %v", err)
		}
		if string(got) != pem {
			t.Errorf("hlab ca export wrote %q, want the served PEM %q", got, pem)
		}
		if code, _, errOut := h.run(t, "ca", "export"); code != 2 {
			t.Errorf("hlab ca export without a file: exit %d, want 2; stderr %q", code, errOut)
		}
	})

	t.Run("CAT-03", func(t *testing.T) {
		const secret = "hz-Cl1TokenValue-0123456789abcdefghijklmnopqrstuvwxyz"
		var mu sync.Mutex
		var sent []string
		h := newHlabWith(t, func(d *mgmt.Deps) {
			d.UpdateCatalog = func(_ context.Context, token string) error {
				mu.Lock()
				defer mu.Unlock()
				sent = append(sent, token)
				return nil
			}
		})
		takeSent := func() []string {
			mu.Lock()
			defer mu.Unlock()
			s := sent
			sent = nil
			return s
		}

		for _, c := range []struct {
			name     string
			args     []string
			env      string // value of HLAB_TEST_HZ
			terminal bool
			prompt   bool // the hidden prompt answers secret
			wantCode int
		}{
			{name: "variable", args: []string{"--hetzner-token-env", "HLAB_TEST_HZ"}, env: secret},
			{name: "variable on a terminal", args: []string{"--hetzner-token-env", "HLAB_TEST_HZ"}, env: secret, terminal: true},
			{name: "hidden prompt", terminal: true, prompt: true},
			{name: "no terminal", wantCode: 2},
			{name: "empty variable", args: []string{"--hetzner-token-env", "HLAB_TEST_HZ"}, wantCode: 2},
		} {
			prompts := 0
			args := append([]string{"catalog", "update"}, c.args...)
			code, out, errOut := h.runWith(t, func(sys *cli.IO) {
				sys.IsTerminal = func() bool { return c.terminal }
				sys.Getenv = func(name string) string {
					if name == "HLAB_TEST_HZ" {
						return c.env
					}
					return ""
				}
				if c.prompt {
					sys.ReadSecret = func(string) (string, error) {
						prompts++
						return secret, nil
					}
				}
			}, args...)
			got := takeSent()
			if code != c.wantCode {
				t.Errorf("%s: exit %d, want %d; stderr %q", c.name, code, c.wantCode, errOut)
			}
			if c.prompt && prompts != 1 {
				t.Errorf("%s: hidden prompt read %d times, want once", c.name, prompts)
			}
			if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
				t.Errorf("%s: output shows the token", c.name)
			}
			if c.wantCode == 0 {
				if len(got) != 1 || got[0] != secret {
					t.Errorf("%s: catalog update received %d tokens, want the token once", c.name, len(got))
				}
				continue
			}
			if len(got) != 0 {
				t.Errorf("%s: catalog update called %d times, want none", c.name, len(got))
			}
			if !strings.Contains(errOut, "--hetzner-token-env") {
				t.Errorf("%s: stderr %q does not name --hetzner-token-env", c.name, errOut)
			}
		}
	})

	t.Run("LIFE-08", func(t *testing.T) {
		svc := &fakeService{state: sysint.StateStopped}
		down := &hlab{dial: notRunning}
		withService := func(sys *cli.IO) { sys.Service = svc }

		if code, _, errOut := down.runWith(t, withService, "cloud", "start", "--discard-state"); code != 0 {
			t.Fatalf("hlab cloud start --discard-state: exit %d, stderr %q", code, errOut)
		}
		if got := svc.takeStarts(); len(got) != 1 || !slices.Equal(got[0], []string{"discard-state"}) {
			t.Errorf("hlab cloud start --discard-state started the service with parameters %q, want once with [discard-state]", got)
		}
		if code, _, errOut := down.runWith(t, withService, "cloud", "start"); code != 0 {
			t.Fatalf("hlab cloud start: exit %d, stderr %q", code, errOut)
		}
		if got := svc.takeStarts(); len(got) != 1 || slices.Contains(got[0], "discard-state") {
			t.Errorf("hlab cloud start started the service with parameters %q, want once without discard-state", got)
		}

		const message = "the state was written by a newer HLab version (state version 7, this version reads 1); " +
			"start with hlab cloud start --discard-state to discard it"
		status := filepath.Join(t.TempDir(), "status.json")
		doc, err := json.Marshal(map[string]any{
			"state":   "refused",
			"problem": map[string]any{"code": "state_incompatible", "message": message, "fatal": true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(status, doc, 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, errOut := down.runWith(t, func(sys *cli.IO) {
			sys.Service = svc
			sys.StatusFile = status
		}, "cloud", "status")
		if code != 0 {
			t.Fatalf("hlab cloud status after a refused start: exit %d, stderr %q", code, errOut)
		}
		if !strings.Contains(out, "refused") || !strings.Contains(out, message) {
			t.Errorf("hlab cloud status after a refused start does not show the state refused and the warning %q:\n%s", message, out)
		}
	})

	t.Run("LIFE-09", func(t *testing.T) {
		const apiLine = `{"time":"2026-11-19T10:00:00Z","level":"INFO","msg":"served by the api","profile":"hlab-dev"}`
		const fileLine = `{"time":"2026-11-19T11:00:00Z","level":"WARN","msg":"read from the file","vm":"hlab-dev-server-3"}`
		const laterLine = `{"time":"2026-11-19T12:00:00Z","level":"ERROR","msg":"written later","id":"7"}`
		apiDir := logDir(t, apiLine)
		fileDir := logDir(t, fileLine)
		running := newHlabWith(t, func(d *mgmt.Deps) { d.LogDir = apiDir })
		stopped := &hlab{dial: notRunning}
		// While running, IO.LogDir holds another line than the API serves,
		// so the output shows which source was read.
		withRunning := func(sys *cli.IO) {
			sys.Service = &fakeService{state: sysint.StateRunning}
			sys.LogDir = fileDir
		}
		withStopped := func(sys *cli.IO) {
			sys.Service = &fakeService{state: sysint.StateStopped}
			sys.LogDir = fileDir
		}

		for _, c := range []struct {
			name  string
			h     *hlab
			with  func(*cli.IO)
			line  string
			human string // the line in human form after the time
		}{
			{"running", running, withRunning, apiLine, "INFO served by the api profile=hlab-dev"},
			{"stopped", stopped, withStopped, fileLine, "WARN read from the file vm=hlab-dev-server-3"},
		} {
			code, out, errOut := c.h.runWith(t, c.with, "logs")
			if code != 0 {
				t.Errorf("%s: hlab logs: exit %d, stderr %q", c.name, code, errOut)
			}
			lines := nonEmptyLines(out)
			if len(lines) != 1 || !strings.HasSuffix(lines[0], " "+c.human) || strings.HasPrefix(lines[0], c.human) {
				t.Errorf("%s: hlab logs printed %q, want one line: the time, then %q", c.name, out, c.human)
			}
			code, out, errOut = c.h.runWith(t, c.with, "logs", "--output", "json")
			if code != 0 {
				t.Errorf("%s: hlab logs --output json: exit %d, stderr %q", c.name, code, errOut)
			}
			if lines := nonEmptyLines(out); len(lines) != 1 || lines[0] != c.line {
				t.Errorf("%s: hlab logs --output json printed %q, want the raw line %q", c.name, out, c.line)
			}
		}

		for _, c := range []struct {
			name string
			h    *hlab
			with func(*cli.IO)
			dir  string
			line string
		}{
			{"running", running, withRunning, apiDir, apiLine},
			{"stopped", stopped, withStopped, fileDir, fileLine},
		} {
			ctx, cancel := context.WithCancel(context.Background())
			var stdout, stderr syncBuffer
			done := make(chan int, 1)
			go func() {
				done <- c.h.runOut(ctx, t, &stdout, &stderr, c.with, "logs", "--follow", "--output", "json")
			}()
			waitOutput(t, c.name+": hlab logs --follow before the new line", &stdout, c.line)
			appendLine(t, c.dir, laterLine)
			waitOutput(t, c.name+": hlab logs --follow after a line was written", &stdout, laterLine)
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: hlab logs --follow did not end 10 s after its context was cancelled", c.name)
			}
		}
	})
}

// notRunning dials the management pipe of a stopped cloud.
func notRunning(context.Context) (net.Conn, error) {
	return nil, errors.New("open \\\\.\\pipe\\hlab-dev: The system cannot find the file specified.")
}

// fakeService is a service control that records its start parameters.
type fakeService struct {
	mu     sync.Mutex
	state  string
	starts [][]string
}

var _ sysint.ServiceControl = (*fakeService)(nil)

func (s *fakeService) Start(_ context.Context, params ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, append([]string{}, params...))
	return nil
}

func (s *fakeService) Stop(context.Context) error { return nil }

func (s *fakeService) State(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

func (s *fakeService) takeStarts() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	got := s.starts
	s.starts = nil
	return got
}

// logDir returns a new folder holding hlabd.log with lines.
func logDir(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, l := range lines {
		appendLine(t, dir, l)
	}
	return dir
}

func appendLine(t *testing.T, dir, line string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "hlabd.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// waitOutput waits up to 10 s until out holds want as a line.
func waitOutput(t *testing.T, what string, out *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Contains(nonEmptyLines(out.String()), want) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: output %q has no line %q after 10 s", what, out.String(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
