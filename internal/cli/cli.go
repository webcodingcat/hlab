// Package cli is the hlab management command: cloud start and stop through
// the service controller, everything else through the management API.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/webcodingcat/hlab/internal/sysint"
	"github.com/webcodingcat/hlab/internal/version"
)

// IO is what the command reads, writes and talks to.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Dial connects to the management API.
	Dial func(ctx context.Context) (net.Conn, error)
	// Service controls the hlabd service of the current profile.
	Service    sysint.ServiceControl
	IsTerminal func() bool
	// StatusFile is hlabd's status file (status.json), read by
	// `cloud status` while the service is stopped; "" skips it.
	StatusFile string
}

// Command is one command with its flags, for the documentation.
type Command struct {
	Path  string // without "hlab", e.g. "token create"
	Flags []string
}

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

type command struct {
	path  string
	args  string   // positional arguments for the usage text
	nargs int      // number of positional arguments
	flags []string // value flags
	bools []string // boolean flags
	run   func(c *call) error
}

var commands = []command{
	{path: "cloud start", run: cloudStart},
	{path: "cloud stop", run: cloudStop},
	{path: "cloud status", run: cloudStatus},
	{path: "project create", args: "<name>", nargs: 1, run: projectCreate},
	{path: "project list", run: projectList},
	{path: "project delete", args: "<name>", nargs: 1, run: projectDelete},
	{path: "token create", flags: []string{"project", "name"}, bools: []string{"read-only"}, run: tokenCreate},
	{path: "token list", flags: []string{"project"}, run: tokenList},
	{path: "token revoke", args: "<name>", nargs: 1, flags: []string{"project"}, run: tokenRevoke},
	{path: "version", run: printVersion},
}

// Commands lists every command and its flags, the global --output included.
func Commands() []Command {
	out := make([]Command, 0, len(commands))
	for _, c := range commands {
		var flags []string
		for _, f := range c.flags {
			flags = append(flags, "--"+f)
		}
		for _, f := range c.bools {
			flags = append(flags, "--"+f)
		}
		out = append(out, Command{Path: c.path, Flags: append(flags, "--output")})
	}
	return out
}

// call is one run of a command.
type call struct {
	ctx   context.Context
	io    IO
	json  bool
	args  []string
	flags map[string]string
}

// usageError is a wrong command line.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// errNotRunning is a failed connection to the management API.
var errNotRunning = errors.New("the cloud is not running")

// Run executes the command line args (without the program name) and
// returns the exit code.
func Run(ctx context.Context, args []string, sys IO) int {
	c, cmd, err := parse(args)
	if err != nil {
		_, _ = fmt.Fprintf(sys.Stderr, "hlab: %v\n\n%s", err, usage())
		return exitUsage
	}
	c.ctx = ctx
	c.io = sys
	if err := cmd.run(c); err != nil {
		_, _ = fmt.Fprintf(sys.Stderr, "hlab: %v\n", err)
		return exitError
	}
	return exitOK
}

func usage() string {
	var b strings.Builder
	b.WriteString("Usage:\n")
	for _, c := range commands {
		b.WriteString("  hlab " + c.path)
		if c.args != "" {
			b.WriteString(" " + c.args)
		}
		for _, f := range c.flags {
			b.WriteString(" --" + f + " <" + f + ">")
		}
		for _, f := range c.bools {
			b.WriteString(" [--" + f + "]")
		}
		b.WriteString("\n")
	}
	b.WriteString("Every command accepts --output json.\n")
	return b.String()
}

// parse finds the command and splits its flags and arguments.
func parse(args []string) (*call, command, error) {
	c := &call{flags: map[string]string{}}
	var words []string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			words = append(words, a)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if name == "output" {
			if !hasValue {
				if i+1 >= len(args) {
					return nil, command{}, usageError{"--output needs a value"}
				}
				i++
				value = args[i]
			}
			if value != "json" {
				return nil, command{}, usageError{fmt.Sprintf("unknown output format %q", value)}
			}
			c.json = true
			continue
		}
		rest = append(rest, args[i])
		if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			rest = append(rest, args[i+1])
			i++
		}
	}
	cmd, n, ok := find(words)
	if !ok {
		if len(words) == 0 {
			return nil, command{}, usageError{"no command given"}
		}
		return nil, command{}, usageError{fmt.Sprintf("unknown command %q", strings.Join(words, " "))}
	}
	c.args = words[n:]
	// rest holds flags with the word after each; bool flags give that word back.
	for i := 0; i < len(rest); i++ {
		name, value, hasValue := strings.Cut(strings.TrimPrefix(rest[i], "--"), "=")
		switch {
		case contains(cmd.bools, name):
			if hasValue {
				return nil, command{}, usageError{fmt.Sprintf("--%s takes no value", name)}
			}
			c.flags[name] = "true"
			if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") {
				c.args = append(c.args, rest[i+1])
				i++
			}
		case contains(cmd.flags, name):
			if !hasValue {
				if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "--") {
					return nil, command{}, usageError{fmt.Sprintf("--%s needs a value", name)}
				}
				i++
				value = rest[i]
			}
			c.flags[name] = value
		default:
			return nil, command{}, usageError{fmt.Sprintf("unknown flag --%s for hlab %s", name, cmd.path)}
		}
	}
	if len(c.args) != cmd.nargs {
		return nil, command{}, usageError{fmt.Sprintf("hlab %s takes %d argument(s)", cmd.path, cmd.nargs)}
	}
	for _, f := range cmd.flags {
		if c.flags[f] == "" {
			return nil, command{}, usageError{fmt.Sprintf("hlab %s needs --%s", cmd.path, f)}
		}
	}
	return c, cmd, nil
}

// find returns the command named by the first words and how many words it
// uses.
func find(words []string) (command, int, bool) {
	for _, c := range commands {
		p := strings.Fields(c.path)
		if len(words) >= len(p) && strings.Join(words[:len(p)], " ") == c.path {
			return c, len(p), true
		}
	}
	return command{}, 0, false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// out writes v as JSON, or text for humans.
func (c *call) out(v any, text string) error {
	if c.json {
		enc := json.NewEncoder(c.io.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	_, err := fmt.Fprint(c.io.Stdout, text)
	return err
}

// apiError is an error answer of the management API.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e apiError) Error() string { return e.Message }

// mgmt sends one request to the management API and decodes the answer into
// v (if not nil).
func (c *call) mgmt(method, path string, body, v any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, "http://hlab/mgmt/v1"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	var dialFailed atomic.Bool
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := c.io.Dial(ctx)
			if err != nil {
				dialFailed.Store(true)
			}
			return conn, err
		},
	}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		if dialFailed.Load() {
			return errNotRunning
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error apiError `json:"error"`
		}
		if json.Unmarshal(data, &e) != nil || e.Error.Message == "" {
			return fmt.Errorf("management API answered %s", resp.Status)
		}
		return e.Error
	}
	if v != nil && len(data) > 0 {
		return json.Unmarshal(data, v)
	}
	return nil
}

func pathEscape(s string) string { return url.PathEscape(s) }

type serviceState struct {
	State string `json:"state"`
}

func cloudStart(c *call) error {
	if err := c.io.Service.Start(c.ctx); err != nil {
		return err
	}
	return c.out(serviceState{"starting"}, "the cloud is starting\n")
}

func cloudStop(c *call) error {
	if err := c.io.Service.Stop(c.ctx); err != nil {
		return err
	}
	return c.out(serviceState{"stopping"}, "the cloud is stopping\n")
}

func cloudStatus(c *call) error {
	state, err := c.io.Service.State(c.ctx)
	if err != nil {
		return err
	}
	if state == sysint.StateRunning {
		var st map[string]any
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer cancel()
		sub := *c
		sub.ctx = ctx
		if err := sub.mgmt(http.MethodGet, "/status", nil, &st); err == nil {
			text := fmt.Sprintf("state: %v\nversion: %v\n", st["state"], st["version"])
			if ps, ok := st["problems"].([]any); ok {
				for _, p := range ps {
					if m, ok := p.(map[string]any); ok {
						text += fmt.Sprintf("problem: %v\n", m["message"])
					}
				}
			}
			return c.out(st, text)
		}
	}
	if state == sysint.StateStopped && c.io.StatusFile != "" {
		if r, ok := refusedStatus(c.io.StatusFile); ok {
			return c.out(r, "state: refused\nproblem: "+r.Problem.Message+"\n")
		}
	}
	return c.out(serviceState{state}, "state: "+state+"\n")
}

// refused is the status file of a cloud that refused to start.
type refused struct {
	State   string `json:"state"`
	Problem struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Fatal   bool   `json:"fatal"`
	} `json:"problem"`
}

// refusedStatus reads the status file and reports whether the last start
// was refused; a missing or unreadable file is no refusal.
func refusedStatus(path string) (refused, bool) {
	var r refused
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &r) != nil {
		return refused{}, false
	}
	return r, r.State == "refused" && r.Problem.Message != ""
}

func projectCreate(c *call) error {
	var p map[string]any
	if err := c.mgmt(http.MethodPost, "/projects", map[string]string{"name": c.args[0]}, &p); err != nil {
		return err
	}
	return c.out(p, "project "+c.args[0]+" created\n")
}

func projectList(c *call) error {
	var body struct {
		Projects []struct {
			Name      string    `json:"name"`
			Created   time.Time `json:"created"`
			Resources int       `json:"resources"`
		} `json:"projects"`
	}
	if err := c.mgmt(http.MethodGet, "/projects", nil, &body); err != nil {
		return err
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tRESOURCES\tCREATED")
	for _, p := range body.Projects {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\n", p.Name, p.Resources, p.Created.Local().Format(time.DateTime))
	}
	_ = tw.Flush()
	return c.out(body, b.String())
}

func projectDelete(c *call) error {
	var body struct {
		ActionID int64 `json:"action_id"`
	}
	if err := c.mgmt(http.MethodDelete, "/projects/"+pathEscape(c.args[0]), nil, &body); err != nil {
		return err
	}
	return c.out(body, "project "+c.args[0]+" deleted\n")
}

func tokenCreate(c *call) error {
	req := map[string]any{"name": c.flags["name"], "read_only": c.flags["read-only"] == "true"}
	var t struct {
		Name     string    `json:"name"`
		ReadOnly bool      `json:"read_only"`
		Created  time.Time `json:"created"`
		Token    string    `json:"token"`
	}
	if err := c.mgmt(http.MethodPost, "/projects/"+pathEscape(c.flags["project"])+"/tokens", req, &t); err != nil {
		return err
	}
	// The token is printed once, alone on its line; it cannot be shown again.
	return c.out(t, t.Token+"\n")
}

func tokenList(c *call) error {
	var body struct {
		Tokens []struct {
			Name     string    `json:"name"`
			ReadOnly bool      `json:"read_only"`
			Created  time.Time `json:"created"`
		} `json:"tokens"`
	}
	if err := c.mgmt(http.MethodGet, "/projects/"+pathEscape(c.flags["project"])+"/tokens", nil, &body); err != nil {
		return err
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tACCESS\tCREATED")
	for _, t := range body.Tokens {
		access := "read/write"
		if t.ReadOnly {
			access = "read-only"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, access, t.Created.Local().Format(time.DateTime))
	}
	_ = tw.Flush()
	return c.out(body, b.String())
}

func tokenRevoke(c *call) error {
	path := "/projects/" + pathEscape(c.flags["project"]) + "/tokens/" + pathEscape(c.args[0])
	if err := c.mgmt(http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	return c.out(map[string]string{}, "token "+c.args[0]+" revoked\n")
}

func printVersion(c *call) error {
	return c.out(map[string]string{"version": version.Version}, "hlab "+version.Version+"\n")
}
