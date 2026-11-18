package cli_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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
	env := apitest.New(t, apitest.ProbeModule())
	srv := httptest.NewServer(mgmt.New(mgmt.Deps{
		Store:  env.Store,
		API:    env.Router,
		Status: func() mgmt.Status { return mgmt.Status{State: "running"} },
	}))
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
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), args, cli.IO{
		Stdin:      strings.NewReader(""),
		Stdout:     &stdout,
		Stderr:     &stderr,
		Dial:       h.dial,
		IsTerminal: func() bool { return false },
	})
	return code, stdout.String(), stderr.String()
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
		alnum := regexp.MustCompile(`[A-Za-z0-9]{33,}`)

		create := func(args ...string) string {
			t.Helper()
			out := h.mustRun(t, append([]string{"token", "create", "--project", "p"}, args...)...)
			runs := alnum.FindAllString(out, -1)
			if len(runs) != 1 || len(runs[0]) != 64 {
				t.Fatalf("token create printed %q, want exactly one token of 64 characters [A-Za-z0-9]", out)
			}
			return runs[0]
		}
		rw := create("--name", "writer")
		ro := create("--name", "reader", "--read-only")

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
}
