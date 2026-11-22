package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/webcodingcat/hlab/internal/cli"
	"github.com/webcodingcat/hlab/internal/mgmt"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// mcpCallTimeout limits one tool call, well below the test's limit.
const mcpCallTimeout = 10 * time.Second

// fastWaits are MCP options with short waits for the tests.
var fastWaits = cli.MCPOptions{
	CloudWait: 10 * time.Second,
	Poll:      5 * time.Millisecond,
	Progress:  20 * time.Millisecond,
}

// mcpSession serves cli.NewMCPServer with the IO of h (adjusted by with)
// over in-memory transports and returns the connected client session.
func (h *hlab) mcpSession(t *testing.T, opts cli.MCPOptions, clientOpts *mcp.ClientOptions, with func(*cli.IO)) *mcp.ClientSession {
	t.Helper()
	sys := cli.IO{
		Stdin:      strings.NewReader(""),
		Stdout:     &syncBuffer{},
		Stderr:     &syncBuffer{},
		Dial:       h.dial,
		IsTerminal: func() bool { return false },
		Getenv:     func(string) string { return "" },
		ReadSecret: func(string) (string, error) {
			t.Errorf("hlab mcp read a secret from the terminal")
			return "", fmt.Errorf("no terminal")
		},
	}
	if with != nil {
		with(&sys)
	}
	serverSide, clientSide := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
	defer cancel()
	ss, err := cli.NewMCPServer(sys, opts).Connect(ctx, serverSide, nil)
	if err != nil {
		t.Fatalf("connect the MCP server: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "hlab-test", Version: "1"}, clientOpts).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatalf("connect the MCP client: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Close()
	})
	return cs
}

// callTool calls the tool name with args within mcpCallTimeout; a protocol
// error fails the test.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

// texts returns the text items of a tool result.
func texts(res *mcp.CallToolResult) []string {
	var out []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out = append(out, tc.Text)
		}
	}
	return out
}

// firstText returns the first text item of a tool result, or "".
func firstText(res *mcp.CallToolResult) string {
	if tx := texts(res); len(tx) > 0 {
		return tx[0]
	}
	return ""
}

// mustSucceed fails the test when res is a tool error and returns its text.
func mustSucceed(t *testing.T, name string, res *mcp.CallToolResult) string {
	t.Helper()
	if res.IsError {
		t.Fatalf("%s: tool error %q", name, strings.Join(texts(res), "\n"))
	}
	return firstText(res)
}

// eventually waits up to 10 s until cond holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached after 10 s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// forever is the poll count of a phase that never ends.
const forever = -1

// phase is one state of a scripted service transition.
type phase struct {
	state  string
	polls  int    // State calls reporting it; forever below 0
	status string // written to the status file when the phase begins
}

// scriptedService is a service control whose Start and Stop run scripted
// phases, one State call at a time.
type scriptedService struct {
	mu         sync.Mutex
	state      string
	statusFile string
	onStart    []phase
	onStop     []phase
	queue      []phase
	begun      bool
	starts     [][]string
	stops      int
	polls      int
}

var _ sysint.ServiceControl = (*scriptedService)(nil)

func statusDoc(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

var (
	statusStarting = statusDoc(map[string]any{"state": "starting"})
	statusRunning  = statusDoc(map[string]any{"state": "running"})
	statusStopped  = statusDoc(map[string]any{"state": "stopped"})
)

// newScriptedService returns a service in state whose status file is in a
// new folder and says state, written an hour ago.
func newScriptedService(t *testing.T, state string) *scriptedService {
	t.Helper()
	s := &scriptedService{state: state, statusFile: filepath.Join(t.TempDir(), "status.json")}
	writeOldStatus(t, s.statusFile, statusDoc(map[string]any{"state": state}))
	return s
}

// writeOldStatus writes doc to path with a modification time an hour ago.
func writeOldStatus(t *testing.T, path, doc string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func (s *scriptedService) Start(_ context.Context, params ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, append([]string{}, params...))
	script := s.onStart
	if script == nil {
		script = []phase{{sysint.StateStarting, 2, statusStarting}, {sysint.StateRunning, forever, statusRunning}}
	}
	s.queue, s.begun = slices.Clone(script), false
	return nil
}

func (s *scriptedService) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops++
	script := s.onStop
	if script == nil {
		script = []phase{{sysint.StateStopping, 2, ""}, {sysint.StateStopped, forever, statusStopped}}
	}
	s.queue, s.begun = slices.Clone(script), false
	return nil
}

func (s *scriptedService) State(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.polls++
	if len(s.queue) > 0 {
		p := &s.queue[0]
		if !s.begun {
			s.begun = true
			s.state = p.state
			if p.status != "" {
				if err := os.WriteFile(s.statusFile, []byte(p.status), 0o600); err != nil {
					return "", err
				}
			}
		}
		if p.polls > 0 {
			p.polls--
			if p.polls == 0 {
				s.queue, s.begun = s.queue[1:], false
			}
		}
	}
	return s.state, nil
}

// set puts the service into state at once, ending any script.
func (s *scriptedService) set(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.queue, s.begun = state, nil, false
}

// finish ends the running script with the phase state lasting forever.
func (s *scriptedService) finish(state, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue, s.begun = []phase{{state, forever, status}}, false
}

func (s *scriptedService) current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *scriptedService) pollCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.polls
}

func (s *scriptedService) stopCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops
}

func (s *scriptedService) takeStarts() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	got := s.starts
	s.starts = nil
	return got
}

// with returns the IO adjustment using s as service and its status file.
func (s *scriptedService) with(logDir string) func(*cli.IO) {
	return func(sys *cli.IO) {
		sys.Service = s
		sys.StatusFile = s.statusFile
		sys.LogDir = logDir
	}
}

// whenRunning makes the management pipe of h reachable only while svc is
// running, like the pipe of the real service.
func whenRunning(h *hlab, svc *scriptedService) *hlab {
	return &hlab{env: h.env, dial: func(ctx context.Context) (net.Conn, error) {
		if svc.current() != sysint.StateRunning {
			return notRunning(ctx)
		}
		return h.dial(ctx)
	}}
}

// gateDeleter deletes servers once gate is closed; with fail it fails.
type gateDeleter struct {
	gate  chan struct{}
	fail  bool
	mu    sync.Mutex
	calls int
}

func (d *gateDeleter) Delete(ctx context.Context, _ int64) error {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	select {
	case <-d.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	if d.fail {
		return fmt.Errorf("disk is in use")
	}
	return nil
}

func (d *gateDeleter) called() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls > 0
}

// projectWithServer creates the project name with one server of id.
func (h *hlab) projectWithServer(t *testing.T, name string, id int64) {
	t.Helper()
	h.mustRun(t, "project", "create", name)
	err := h.env.Store.Tx(func(tx store.Tx) error {
		p, err := store.ProjectByName(tx, name)
		if err != nil {
			return err
		}
		return store.Put(tx, store.Meta{Kind: servers.Kind, ID: id, ProjectID: p.ID, Name: "s"}, map[string]any{})
	})
	if err != nil {
		t.Fatalf("add a server to %s: %v", name, err)
	}
}

// projectListed reports whether `hlab project list --output json` lists name.
func (h *hlab) projectListed(t *testing.T, name string) bool {
	t.Helper()
	var list struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	out := h.mustRun(t, "project", "list", "--output", "json")
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("project list --output json: %v\n%s", err, out)
	}
	for _, p := range list.Projects {
		if p.Name == name {
			return true
		}
	}
	return false
}

// sniffDial returns a dial whose connections close seen when a response
// with status 409 is read.
func sniffDial(dial func(context.Context) (net.Conn, error), seen chan struct{}) func(context.Context) (net.Conn, error) {
	var once sync.Once
	return func(ctx context.Context) (net.Conn, error) {
		c, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		return &sniffConn{Conn: c, saw: func() { once.Do(func() { close(seen) }) }}, nil
	}
}

type sniffConn struct {
	net.Conn
	saw func()
}

func (c *sniffConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if bytes.Contains(p[:n], []byte(" 409 ")) {
		c.saw()
	}
	return n, err
}

// progressLog records the progress notifications a client receives.
type progressLog struct {
	mu    sync.Mutex
	notes []*mcp.ProgressNotificationParams
}

func (l *progressLog) options() *mcp.ClientOptions {
	return &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.notes = append(l.notes, req.Params)
	}}
}

// forToken returns the notifications for token.
func (l *progressLog) forToken(token string) []*mcp.ProgressNotificationParams {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*mcp.ProgressNotificationParams
	for _, n := range l.notes {
		if fmt.Sprint(n.ProgressToken) == token {
			out = append(out, n)
		}
	}
	return out
}

func (l *progressLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.notes)
}

// toolArgs returns valid arguments for the tool name against project p.
func toolArgs(name string) map[string]any {
	switch name {
	case "project_create":
		return map[string]any{"display_name": "Fresh Project"}
	case "project_delete":
		return map[string]any{"name": "doomed"}
	case "token_create":
		return map[string]any{"project": "p", "name": "fresh"}
	case "token_list":
		return map[string]any{"project": "p"}
	case "token_revoke":
		return map[string]any{"project": "p", "name": "old"}
	case "ca_trust":
		return map[string]any{"on": true}
	}
	return nil
}

// asyncCall runs a tool call with ctx in the background.
type asyncCall struct {
	done chan struct{}
	res  *mcp.CallToolResult
	err  error
}

func startCall(ctx context.Context, cs *mcp.ClientSession, params *mcp.CallToolParams) *asyncCall {
	c := &asyncCall{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		c.res, c.err = cs.CallTool(ctx, params)
	}()
	return c
}

// wait waits up to mcpCallTimeout for the call to return.
func (c *asyncCall) wait(t *testing.T, what string) (*mcp.CallToolResult, error) {
	t.Helper()
	select {
	case <-c.done:
		return c.res, c.err
	case <-time.After(mcpCallTimeout):
		t.Fatalf("%s: no answer after %s", what, mcpCallTimeout)
		return nil, nil
	}
}

func (c *asyncCall) returned() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func TestMCP(t *testing.T) {
	t.Run("MCP-01", func(t *testing.T) {
		const pem = "-----BEGIN CERTIFICATE-----\nTUlJQ2FDQQ==\n-----END CERTIFICATE-----\n"
		const apiLine = `{"time":"2026-11-19T10:00:00Z","level":"INFO","msg":"served by the api"}`
		const fileLine = `{"time":"2026-11-19T11:00:00Z","level":"WARN","msg":"read from the file"}`
		settingsPath := filepath.Join(t.TempDir(), "settings.toml")
		if err := os.WriteFile(settingsPath, []byte("[logging]\nlevel = \"info\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var trustMu sync.Mutex
		var trusted []bool
		h := newHlabWith(t, func(d *mgmt.Deps) {
			d.CACert = func() []byte { return []byte(pem) }
			d.LogDir = logDir(t, apiLine)
			d.SettingsPath = settingsPath
			d.Trust = func(_ context.Context, on bool) error {
				trustMu.Lock()
				defer trustMu.Unlock()
				trusted = append(trusted, on)
				return nil
			}
		})
		svc := newScriptedService(t, sysint.StateRunning)
		with := svc.with(logDir(t, fileLine))
		cs := h.mcpSession(t, fastWaits, nil, with)

		// jsonOf runs `hlab args... --output json` with the same IO.
		jsonOf := func(t *testing.T, args ...string) string {
			t.Helper()
			code, out, errOut := h.runWith(t, with, append(args, "--output", "json")...)
			if code != 0 {
				t.Fatalf("hlab %s --output json: exit %d, stderr %q", strings.Join(args, " "), code, errOut)
			}
			return strings.TrimSpace(out)
		}
		isJSON := func(t *testing.T, name, text string) {
			t.Helper()
			if !json.Valid([]byte(text)) {
				t.Errorf("%s returned %q, want JSON", name, text)
			}
		}

		for _, c := range []struct {
			name string
			run  func(t *testing.T)
		}{
			{"cloud_status", func(t *testing.T) {
				got := mustSucceed(t, "cloud_status", callTool(t, cs, "cloud_status", nil))
				if want := jsonOf(t, "cloud", "status"); strings.TrimSpace(got) != want {
					t.Errorf("cloud_status returned %q, want the output of hlab cloud status --output json %q", got, want)
				}
			}},
			{"cloud_start", func(t *testing.T) {
				svc.set(sysint.StateStopped)
				svc.takeStarts()
				got := mustSucceed(t, "cloud_start", callTool(t, cs, "cloud_start", nil))
				isJSON(t, "cloud_start", got)
				if starts := svc.takeStarts(); len(starts) != 1 || len(starts[0]) != 0 {
					t.Errorf("cloud_start started the service with parameters %q, want once without parameters", starts)
				}
			}},
			{"cloud_stop", func(t *testing.T) {
				svc.set(sysint.StateRunning)
				defer svc.set(sysint.StateRunning)
				got := mustSucceed(t, "cloud_stop", callTool(t, cs, "cloud_stop", nil))
				isJSON(t, "cloud_stop", got)
				if n := svc.stopCount(); n != 1 {
					t.Errorf("cloud_stop stopped the service %d times, want once", n)
				}
			}},
			{"project_create", func(t *testing.T) {
				got := mustSucceed(t, "project_create", callTool(t, cs, "project_create", map[string]any{"display_name": "Web Coding Cat"}))
				var p struct {
					Name        string `json:"name"`
					DisplayName string `json:"display_name"`
				}
				if err := json.Unmarshal([]byte(got), &p); err != nil {
					t.Fatalf("project_create returned %q: %v", got, err)
				}
				if p.Name != "web-coding-cat" || p.DisplayName != "Web Coding Cat" {
					t.Errorf("project_create returned name %q, display name %q; want web-coding-cat, \"Web Coding Cat\"", p.Name, p.DisplayName)
				}
				if !h.projectListed(t, "web-coding-cat") {
					t.Errorf("web-coding-cat is not listed after project_create")
				}
			}},
			{"project_list", func(t *testing.T) {
				err := h.env.Store.Tx(func(tx store.Tx) error {
					return store.Put(tx, store.Meta{Kind: servers.Kind, ID: 7001, ProjectID: h.env.Project.ID, Name: "s"}, map[string]any{})
				})
				if err != nil {
					t.Fatalf("add a server to p: %v", err)
				}
				got := mustSucceed(t, "project_list", callTool(t, cs, "project_list", nil))
				if want := jsonOf(t, "project", "list"); strings.TrimSpace(got) != want {
					t.Errorf("project_list returned %q, want the output of hlab project list --output json %q", got, want)
				}
				var list struct {
					Projects []struct {
						Name            string         `json:"name"`
						ResourcesByType map[string]int `json:"resources_by_type"`
					} `json:"projects"`
				}
				if err := json.Unmarshal([]byte(got), &list); err != nil {
					t.Fatalf("project_list returned %q: %v", got, err)
				}
				found := false
				for _, p := range list.Projects {
					if p.Name == "p" {
						found = true
						if p.ResourcesByType["servers"] != 1 {
							t.Errorf("project_list: resources_by_type of p is %v, want servers 1", p.ResourcesByType)
						}
					}
				}
				if !found {
					t.Errorf("project_list does not list p: %s", got)
				}
			}},
			{"project_delete", func(t *testing.T) {
				h.mustRun(t, "project", "create", "gone")
				got := mustSucceed(t, "project_delete", callTool(t, cs, "project_delete", map[string]any{"name": "gone"}))
				isJSON(t, "project_delete", got)
				if h.projectListed(t, "gone") {
					t.Errorf("project gone is still listed after project_delete returned")
				}
			}},
			{"token_create", func(t *testing.T) {
				got := mustSucceed(t, "token_create", callTool(t, cs, "token_create", map[string]any{"project": "p", "name": "mcp-reader", "read_only": true}))
				var tok struct {
					Name     string `json:"name"`
					ReadOnly bool   `json:"read_only"`
					Token    string `json:"token"`
				}
				if err := json.Unmarshal([]byte(got), &tok); err != nil {
					t.Fatalf("token_create returned %q: %v", got, err)
				}
				if tok.Name != "mcp-reader" || !tok.ReadOnly || len(tok.Token) != 64 {
					t.Errorf("token_create returned name %q, read-only %v, a value of %d characters; want mcp-reader, read-only, 64",
						tok.Name, tok.ReadOnly, len(tok.Token))
				}
				if resp := h.env.DoAuth("Bearer "+tok.Token, http.MethodGet, "/ssh_keys", nil); resp.StatusCode != http.StatusOK {
					t.Errorf("token of token_create: GET status %d, want 200", resp.StatusCode)
				}
			}},
			{"token_list", func(t *testing.T) {
				h.createToken(t, "--name", "listed")
				got := mustSucceed(t, "token_list", callTool(t, cs, "token_list", map[string]any{"project": "p"}))
				if want := jsonOf(t, "token", "list", "--project", "p"); strings.TrimSpace(got) != want {
					t.Errorf("token_list returned %q, want the output of hlab token list --output json %q", got, want)
				}
			}},
			{"token_revoke", func(t *testing.T) {
				tok := h.createToken(t, "--name", "revoked")
				mustSucceed(t, "token_revoke", callTool(t, cs, "token_revoke", map[string]any{"project": "p", "name": "revoked"}))
				if resp := h.env.DoAuth("Bearer "+tok, http.MethodGet, "/ssh_keys", nil); resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("token after token_revoke: status %d, want 401", resp.StatusCode)
				}
			}},
			{"logs", func(t *testing.T) {
				got := mustSucceed(t, "logs", callTool(t, cs, "logs", nil))
				want := nonEmptyLines(jsonOf(t, "logs"))
				if lines := nonEmptyLines(got); !slices.Equal(lines, want) || !slices.Equal(lines, []string{apiLine}) {
					t.Errorf("logs returned %q, want the raw lines of hlab logs --output json %q (the served line)", lines, want)
				}
			}},
			{"ca_export", func(t *testing.T) {
				if got := mustSucceed(t, "ca_export", callTool(t, cs, "ca_export", nil)); got != pem {
					t.Errorf("ca_export returned %q, want the served PEM %q", got, pem)
				}
			}},
			{"ca_trust", func(t *testing.T) {
				mustSucceed(t, "ca_trust on", callTool(t, cs, "ca_trust", map[string]any{"on": true}))
				mustSucceed(t, "ca_trust off", callTool(t, cs, "ca_trust", map[string]any{"on": false}))
				trustMu.Lock()
				defer trustMu.Unlock()
				if !slices.Equal(trusted, []bool{true, false}) {
					t.Errorf("PUT /trust received %v, want [true false]", trusted)
				}
			}},
			{"version", func(t *testing.T) {
				got := mustSucceed(t, "version", callTool(t, cs, "version", nil))
				if want := jsonOf(t, "version"); strings.TrimSpace(got) != want {
					t.Errorf("version returned %q, want the output of hlab version --output json %q", got, want)
				}
			}},
			{"command error", func(t *testing.T) {
				res := callTool(t, cs, "project_create", map[string]any{"display_name": "p"})
				got := strings.TrimSpace(firstText(res))
				if !res.IsError || got == "" {
					t.Fatalf("project_create of the existing project p: IsError %v, text %q; want a tool error with a message", res.IsError, got)
				}
				code, _, errOut := h.runWith(t, with, "project", "create", "p")
				if code != 1 || !strings.Contains(errOut, got) {
					t.Errorf("tool error %q is not the message hlab project create p prints (exit %d, stderr %q)", got, code, errOut)
				}
			}},
		} {
			t.Run(c.name, c.run)
		}

		t.Run("logs_lines", func(t *testing.T) {
			var all []string
			for i := range 1200 {
				all = append(all, fmt.Sprintf(`{"time":"2026-11-19T10:00:00Z","level":"INFO","msg":"line","n":%d}`, i))
			}
			h := newHlabWith(t, func(d *mgmt.Deps) { d.LogDir = logDir(t, all...) })
			svc := newScriptedService(t, sysint.StateRunning)
			cs := h.mcpSession(t, fastWaits, nil, svc.with(logDir(t, "{}")))

			for _, c := range []struct {
				name string
				args map[string]any
				want []string
			}{
				{"default", nil, all[1000:]},
				{"5", map[string]any{"lines": 5}, all[1195:]},
				{"1000", map[string]any{"lines": 1000}, all[200:]},
			} {
				got := nonEmptyLines(mustSucceed(t, "logs "+c.name, callTool(t, cs, "logs", c.args)))
				if !slices.Equal(got, c.want) {
					t.Errorf("logs %s: %d lines, first %q; want the last %d lines", c.name, len(got), got[:min(1, len(got))], len(c.want))
				}
			}
			for _, n := range []int{0, 1001} {
				if res := callTool(t, cs, "logs", map[string]any{"lines": n}); !res.IsError {
					t.Errorf("logs with lines %d: no tool error", n)
				}
			}
		})
	})

	t.Run("MCP-02", func(t *testing.T) {
		const message = "the state was written by a newer HLab version; start with hlab cloud start --discard-state to discard it"
		const fileLine = `{"time":"2026-11-19T11:00:00Z","level":"WARN","msg":"read from the file"}`
		svc := newScriptedService(t, sysint.StateStopped)
		writeOldStatus(t, svc.statusFile, statusDoc(map[string]any{
			"state":   "refused",
			"problem": map[string]any{"code": "state_incompatible", "message": message, "fatal": true},
		}))
		down := &hlab{dial: notRunning}
		cs := down.mcpSession(t, fastWaits, nil, svc.with(logDir(t, fileLine)))

		got := mustSucceed(t, "cloud_status", callTool(t, cs, "cloud_status", nil))
		if !strings.Contains(got, "refused") || !strings.Contains(got, message) {
			t.Errorf("cloud_status with the cloud stopped returned %q, want the state refused and %q", got, message)
		}
		if lines := nonEmptyLines(mustSucceed(t, "logs", callTool(t, cs, "logs", nil))); !slices.Equal(lines, []string{fileLine}) {
			t.Errorf("logs with the cloud stopped returned %q, want the line of the log file %q", lines, fileLine)
		}
		mustSucceed(t, "version", callTool(t, cs, "version", nil))

		working := []string{"cloud_status", "cloud_start", "cloud_stop", "logs", "version"}
		for _, name := range cli.MCPTools() {
			if slices.Contains(working, name) {
				continue
			}
			res := callTool(t, cs, name, toolArgs(name))
			if !res.IsError || !strings.Contains(firstText(res), "the cloud is not running") {
				t.Errorf("%s with the cloud stopped: IsError %v, text %q; want the tool error \"the cloud is not running\"",
					name, res.IsError, firstText(res))
			}
		}
		mustSucceed(t, "version after the tool errors", callTool(t, cs, "version", nil))
	})

	t.Run("MCP-03", func(t *testing.T) {
		t.Run("wait", func(t *testing.T) {
			svc := newScriptedService(t, sysint.StateStopped)
			svc.onStart = []phase{{sysint.StateStarting, 5, statusStarting}, {sysint.StateRunning, forever, statusRunning}}
			svc.onStop = []phase{{sysint.StateStopping, 5, ""}, {sysint.StateStopped, forever, statusStopped}}
			h := whenRunning(newHlab(t), svc)
			with := svc.with(t.TempDir())
			cs := h.mcpSession(t, fastWaits, nil, with)
			statusJSON := func() string {
				code, out, errOut := h.runWith(t, with, "cloud", "status", "--output", "json")
				if code != 0 {
					t.Fatalf("hlab cloud status --output json: exit %d, stderr %q", code, errOut)
				}
				return strings.TrimSpace(out)
			}

			got := mustSucceed(t, "cloud_start", callTool(t, cs, "cloud_start", nil))
			if state := svc.current(); state != sysint.StateRunning {
				t.Errorf("cloud_start returned while the service was %s, want running", state)
			}
			if want := statusJSON(); strings.TrimSpace(got) != want {
				t.Errorf("cloud_start returned %q, want the cloud status %q", got, want)
			}

			got = mustSucceed(t, "cloud_stop", callTool(t, cs, "cloud_stop", nil))
			if state := svc.current(); state != sysint.StateStopped {
				t.Errorf("cloud_stop returned while the service was %s, want stopped", state)
			}
			if want := statusJSON(); strings.TrimSpace(got) != want {
				t.Errorf("cloud_stop returned %q, want the cloud status %q", got, want)
			}
		})

		t.Run("refused", func(t *testing.T) {
			const refusal = "the state was written by a newer HLab version (state version 7, this version reads 1)"
			const failure = "switch hlab-dev cannot be created: access denied"
			for _, c := range []struct {
				name, status, message string
			}{
				{"refused", statusDoc(map[string]any{
					"state":   "refused",
					"problem": map[string]any{"code": "state_incompatible", "message": refusal, "fatal": true},
				}), refusal},
				{"failed", statusDoc(map[string]any{"state": "stopped", "error": failure}), failure},
			} {
				t.Run(c.name, func(t *testing.T) {
					svc := newScriptedService(t, sysint.StateStopped)
					svc.onStart = []phase{{sysint.StateStarting, 3, statusStarting}, {sysint.StateStopped, forever, c.status}}
					cs := whenRunning(newHlab(t), svc).mcpSession(t, fastWaits, nil, svc.with(t.TempDir()))
					res := callTool(t, cs, "cloud_start", nil)
					if !res.IsError || !strings.Contains(firstText(res), c.message) {
						t.Errorf("cloud_start of a %s start: IsError %v, text %q; want a tool error with %q",
							c.name, res.IsError, firstText(res), c.message)
					}
				})
			}
		})

		t.Run("stale_status", func(t *testing.T) {
			const earlier = "an earlier start failed"
			svc := newScriptedService(t, sysint.StateStopped)
			writeOldStatus(t, svc.statusFile, statusDoc(map[string]any{"state": "stopped", "error": earlier}))
			// The service controller still reports stopped for a while after
			// the start request.
			svc.onStart = []phase{
				{sysint.StateStopped, 5, ""},
				{sysint.StateStarting, 5, statusStarting},
				{sysint.StateRunning, forever, statusRunning},
			}
			cs := whenRunning(newHlab(t), svc).mcpSession(t, fastWaits, nil, svc.with(t.TempDir()))
			got := mustSucceed(t, "cloud_start", callTool(t, cs, "cloud_start", nil))
			if state := svc.current(); state != sysint.StateRunning {
				t.Errorf("cloud_start returned while the service was %s, want running", state)
			}
			if strings.Contains(got, earlier) {
				t.Errorf("cloud_start returned the earlier failure: %q", got)
			}
		})

		t.Run("limit", func(t *testing.T) {
			const limit = 200 * time.Millisecond
			svc := newScriptedService(t, sysint.StateStopped)
			svc.onStart = []phase{{sysint.StateStarting, forever, statusStarting}}
			opts := fastWaits
			opts.CloudWait = limit
			cs := whenRunning(newHlab(t), svc).mcpSession(t, opts, nil, svc.with(t.TempDir()))
			res := callTool(t, cs, "cloud_start", nil)
			if want := "still not running after " + limit.String(); !res.IsError || !strings.Contains(firstText(res), want) {
				t.Errorf("cloud_start of a service never leaving starting: IsError %v, text %q; want a tool error with %q",
					res.IsError, firstText(res), want)
			}
		})

		t.Run("project_delete", func(t *testing.T) {
			t.Run("failed", func(t *testing.T) {
				deleter := &gateDeleter{gate: make(chan struct{}), fail: true}
				close(deleter.gate)
				h := newHlabWith(t, func(d *mgmt.Deps) {
					d.Deleters = map[string]resource.Deleter{servers.Kind: deleter}
				})
				h.projectWithServer(t, "stuck", 8001)
				cs := h.mcpSession(t, fastWaits, nil, nil)
				res := callTool(t, cs, "project_delete", map[string]any{"name": "stuck"})
				got := strings.TrimSpace(firstText(res))
				if !res.IsError || got == "" {
					t.Fatalf("project_delete of a failing deletion: IsError %v, text %q; want a tool error", res.IsError, got)
				}
				if !h.projectListed(t, "stuck") {
					t.Errorf("stuck is no longer listed after its failed deletion")
				}
				if code, _, errOut := h.run(t, "project", "delete", "stuck"); code != 1 || !strings.Contains(errOut, got) {
					t.Errorf("tool error %q is not the message hlab project delete prints (exit %d, stderr %q)", got, code, errOut)
				}
			})

			t.Run("running", func(t *testing.T) {
				deleter := &gateDeleter{gate: make(chan struct{})}
				h := newHlabWith(t, func(d *mgmt.Deps) {
					d.Deleters = map[string]resource.Deleter{servers.Kind: deleter}
				})
				h.projectWithServer(t, "slow", 8002)
				conflict := make(chan struct{})
				sniffed := &hlab{env: h.env, dial: sniffDial(h.dial, conflict)}
				cs := sniffed.mcpSession(t, fastWaits, nil, nil)
				args := map[string]any{"name": "slow"}

				first := startCall(context.Background(), cs, &mcp.CallToolParams{Name: "project_delete", Arguments: args})
				eventually(t, "the deletion of slow runs", deleter.called)
				second := startCall(context.Background(), cs, &mcp.CallToolParams{Name: "project_delete", Arguments: args})
				select {
				case <-conflict:
				case <-time.After(mcpCallTimeout):
					t.Fatalf("the second project_delete got no 409 conflict within %s", mcpCallTimeout)
				}
				if first.returned() || second.returned() {
					t.Errorf("a project_delete returned while the deletion still runs")
				}
				close(deleter.gate)
				for _, c := range []struct {
					name string
					call *asyncCall
				}{{"first", first}, {"second", second}} {
					res, err := c.call.wait(t, c.name+" project_delete")
					if err != nil {
						t.Fatalf("%s project_delete: %v", c.name, err)
					}
					mustSucceed(t, c.name+" project_delete", res)
				}
				if h.projectListed(t, "slow") {
					t.Errorf("slow is still listed after both project_delete calls returned")
				}
			})
		})

		t.Run("progress", func(t *testing.T) {
			deleter := &gateDeleter{gate: make(chan struct{})}
			base := newHlabWith(t, func(d *mgmt.Deps) {
				d.Deleters = map[string]resource.Deleter{servers.Kind: deleter}
			})
			base.projectWithServer(t, "tracked", 8003)
			base.projectWithServer(t, "quiet", 8004)
			svc := newScriptedService(t, sysint.StateStopped)
			svc.onStart = []phase{{sysint.StateStarting, forever, statusStarting}}
			h := whenRunning(base, svc)
			var log progressLog
			cs := h.mcpSession(t, fastWaits, log.options(), svc.with(t.TempDir()))

			// With a token: cloud_start.
			params := &mcp.CallToolParams{Name: "cloud_start"}
			params.SetProgressToken("start-1")
			call := startCall(context.Background(), cs, params)
			eventually(t, "two progress notifications of cloud_start", func() bool { return len(log.forToken("start-1")) >= 2 })
			svc.finish(sysint.StateRunning, statusRunning)
			res, err := call.wait(t, "cloud_start with a progress token")
			if err != nil {
				t.Fatalf("cloud_start with a progress token: %v", err)
			}
			mustSucceed(t, "cloud_start with a progress token", res)
			for _, n := range log.forToken("start-1") {
				if !strings.Contains(n.Message, sysint.StateStarting) {
					t.Errorf("progress notification of cloud_start has message %q, want the state %s", n.Message, sysint.StateStarting)
				}
			}

			// With a token: project_delete.
			params = &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"name": "tracked"}}
			params.SetProgressToken("delete-1")
			call = startCall(context.Background(), cs, params)
			eventually(t, "two progress notifications of project_delete", func() bool { return len(log.forToken("delete-1")) >= 2 })
			close(deleter.gate)
			res, err = call.wait(t, "project_delete with a progress token")
			if err != nil {
				t.Fatalf("project_delete with a progress token: %v", err)
			}
			mustSucceed(t, "project_delete with a progress token", res)

			// Without a token: none, while both wait many progress intervals.
			before := log.count()
			deleter.gate = make(chan struct{})
			svc.set(sysint.StateStopped)
			// The deletion runs on a session whose pipe stays reachable while
			// the fake service is stopped again for cloud_start (MCP-02: a
			// stopped cloud refuses project_delete).
			ungated := base.mcpSession(t, fastWaits, log.options(), nil)
			del := startCall(context.Background(), ungated, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"name": "quiet"}})
			eventually(t, "the deletion of quiet runs", func() bool {
				deleter.mu.Lock()
				defer deleter.mu.Unlock()
				return deleter.calls >= 2
			})
			start := startCall(context.Background(), cs, &mcp.CallToolParams{Name: "cloud_start"})
			polls := svc.pollCount()
			eventually(t, "cloud_start waits 20 polls", func() bool { return svc.pollCount() >= polls+20 })
			svc.finish(sysint.StateRunning, statusRunning)
			if res, err := start.wait(t, "cloud_start without a progress token"); err != nil {
				t.Fatalf("cloud_start without a progress token: %v", err)
			} else {
				mustSucceed(t, "cloud_start without a progress token", res)
			}
			close(deleter.gate)
			if res, err := del.wait(t, "project_delete without a progress token"); err != nil {
				t.Fatalf("project_delete without a progress token: %v", err)
			} else {
				mustSucceed(t, "project_delete without a progress token", res)
			}
			if n := log.count() - before; n != 0 {
				t.Errorf("calls without a progress token got %d progress notifications, want none", n)
			}
		})

		t.Run("cancel", func(t *testing.T) {
			deleter := &gateDeleter{gate: make(chan struct{})}
			base := newHlabWith(t, func(d *mgmt.Deps) {
				d.Deleters = map[string]resource.Deleter{servers.Kind: deleter}
			})
			base.projectWithServer(t, "leaving", 8005)
			cs := base.mcpSession(t, fastWaits, nil, nil)

			// project_delete: the deletion finishes after the cancel.
			ctx, cancel := context.WithCancel(context.Background())
			call := startCall(ctx, cs, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"name": "leaving"}})
			eventually(t, "the deletion of leaving runs", deleter.called)
			cancel()
			if _, err := call.wait(t, "cancelled project_delete"); err == nil {
				t.Errorf("cancelled project_delete returned no error")
			}
			close(deleter.gate)
			eventually(t, "leaving is deleted after the cancelled project_delete", func() bool { return !base.projectListed(t, "leaving") })

			// cloud_start: the service keeps starting after the cancel.
			svc := newScriptedService(t, sysint.StateStopped)
			svc.onStart = []phase{{sysint.StateStarting, forever, statusStarting}}
			cs = whenRunning(base, svc).mcpSession(t, fastWaits, nil, svc.with(t.TempDir()))
			ctx, cancel = context.WithCancel(context.Background())
			defer cancel()
			call = startCall(ctx, cs, &mcp.CallToolParams{Name: "cloud_start"})
			eventually(t, "the service is starting", func() bool { return svc.current() == sysint.StateStarting })
			cancel()
			if _, err := call.wait(t, "cancelled cloud_start"); err == nil {
				t.Errorf("cancelled cloud_start returned no error")
			}
			callTool(t, cs, "cloud_status", nil)
			if n := svc.stopCount(); n != 0 {
				t.Errorf("the service was stopped %d times after cloud_start was cancelled, want never", n)
			}
			if state := svc.current(); state != sysint.StateStarting {
				t.Errorf("the service is %s after cloud_start was cancelled, want still starting", state)
			}
		})
	})

	t.Run("MCP-04", func(t *testing.T) {
		var catMu sync.Mutex
		catalogUpdates := 0
		base := newHlabWith(t, func(d *mgmt.Deps) {
			d.UpdateCatalog = func(context.Context, string) error {
				catMu.Lock()
				defer catMu.Unlock()
				catalogUpdates++
				return nil
			}
		})
		svc := newScriptedService(t, sysint.StateRunning)
		h := whenRunning(base, svc)
		cs := h.mcpSession(t, fastWaits, nil, svc.with(logDir(t, "{}")))

		res := callTool(t, cs, "token_create", map[string]any{"project": "p", "name": "once"})
		mustSucceed(t, "token_create", res)
		items := texts(res)
		var tok struct {
			Token string `json:"token"`
		}
		if len(items) != 2 {
			t.Fatalf("token_create returned %d text items, want the token and a note", len(items))
		}
		if err := json.Unmarshal([]byte(items[0]), &tok); err != nil || len(tok.Token) != 64 {
			t.Fatalf("token_create returned %q, want JSON with a value of 64 characters (%v)", items[0], err)
		}
		if strings.Count(strings.Join(items, "\n"), tok.Token) != 1 {
			t.Errorf("token_create result shows the value %d times, want once", strings.Count(strings.Join(items, "\n"), tok.Token))
		}
		if !strings.Contains(items[1], "cannot be retrieved again") {
			t.Errorf("token_create note %q does not say the value cannot be retrieved again", items[1])
		}
		if got := mustSucceed(t, "token_list", callTool(t, cs, "token_list", map[string]any{"project": "p"})); strings.Contains(got, tok.Token) {
			t.Errorf("token_list shows the token value: %q", got)
		}

		ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
		defer cancel()
		listed, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		var names []string
		for _, tool := range listed.Tools {
			names = append(names, tool.Name)
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatalf("input schema of %s: %v", tool.Name, err)
			}
			if strings.Contains(tool.Name, "catalog") || strings.Contains(strings.ToLower(tool.Name+string(schema)), "discard") {
				t.Errorf("tool %s offers a catalog update or discarding the state (schema %s)", tool.Name, schema)
			}
		}
		if want := cli.MCPTools(); !slices.Equal(names, want) {
			t.Errorf("the server lists the tools %q, want %q", names, want)
		}

		// Every tool once: none updates the catalog or discards the state.
		h.mustRun(t, "project", "create", "doomed")
		h.createToken(t, "--name", "old")
		for _, name := range cli.MCPTools() {
			if name == "cloud_start" {
				svc.set(sysint.StateStopped)
			}
			callTool(t, cs, name, toolArgs(name))
			if name == "cloud_stop" {
				svc.set(sysint.StateRunning)
			}
		}
		starts := svc.takeStarts()
		if len(starts) == 0 {
			t.Errorf("cloud_start did not start the stopped service")
		}
		for _, params := range starts {
			if slices.Contains(params, "discard-state") {
				t.Errorf("a tool started the service with parameters %q, want without discard-state", params)
			}
		}
		catMu.Lock()
		defer catMu.Unlock()
		if catalogUpdates != 0 {
			t.Errorf("POST /catalog/update was reached %d times, want never", catalogUpdates)
		}
	})
}
