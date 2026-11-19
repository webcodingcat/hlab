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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/cli"
	"github.com/webcodingcat/hlab/internal/mgmt"
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
	var stdout, stderr bytes.Buffer
	sys := cli.IO{
		Stdin:      strings.NewReader(""),
		Stdout:     &stdout,
		Stderr:     &stderr,
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
	code := cli.Run(context.Background(), args, sys)
	return code, stdout.String(), stderr.String()
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
}
