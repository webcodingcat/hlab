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
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/webcodingcat/hlab/internal/cloudstatus"
	"github.com/webcodingcat/hlab/internal/logfile"
	"github.com/webcodingcat/hlab/internal/mgmtclient"
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
	// Getenv reads an environment variable (`--hetzner-token-env`).
	Getenv func(string) string
	// ReadSecret shows prompt and reads a line from the terminal without
	// echoing it.
	ReadSecret func(prompt string) (string, error)
	// LogDir is the folder of hlabd.log, read by `hlab logs` while the
	// cloud is stopped; "" reads nothing.
	LogDir string
}

// Command is one command with its flags, for the documentation.
type Command struct {
	Path  string // without "hlab", e.g. "token create"
	Flags []string
}

// discardParam is the flag of `cloud start` and the service start
// parameter that ask hlabd to discard an incompatible state.
const discardParam = "discard-state"

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
	flags []string // value flags, required
	opts  []string // value flags, optional
	bools []string // boolean flags
	// noOutput: the command takes no --output (it prints no result).
	noOutput bool
	run      func(c *call) error
}

var commands = []command{
	{path: "cloud start", bools: []string{discardParam}, run: cloudStart},
	{path: "cloud stop", run: cloudStop},
	{path: "cloud status", run: cloudStatus},
	{path: "catalog update", opts: []string{"hetzner-token-env"}, run: catalogUpdate},
	{path: "project create", args: "<display-name>", nargs: 1, run: projectCreate},
	{path: "project list", run: projectList},
	{path: "project delete", args: "<name>", nargs: 1, run: projectDelete},
	{path: "token create", flags: []string{"project", "name"}, bools: []string{"read-only"}, run: tokenCreate},
	{path: "token list", flags: []string{"project"}, run: tokenList},
	{path: "token revoke", args: "<name>", nargs: 1, flags: []string{"project"}, run: tokenRevoke},
	{path: "ca export", args: "<file>", nargs: 1, run: caExport},
	{path: "ca trust", args: "on|off", nargs: 1, run: caTrust},
	{path: "logs", bools: []string{"follow"}, run: logs},
	{path: "version", run: printVersion},
	{path: "mcp", noOutput: true, run: serveMCP},
}

// Commands lists every command and its flags, the global --output included
// where the command takes it.
func Commands() []Command {
	out := make([]Command, 0, len(commands))
	for _, c := range commands {
		var flags []string
		for _, f := range c.flags {
			flags = append(flags, "--"+f)
		}
		for _, f := range c.opts {
			flags = append(flags, "--"+f)
		}
		for _, f := range c.bools {
			flags = append(flags, "--"+f)
		}
		if !c.noOutput {
			flags = append(flags, "--output")
		}
		out = append(out, Command{Path: c.path, Flags: flags})
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
		if errors.As(err, new(usageError)) {
			return exitUsage
		}
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
		for _, f := range c.opts {
			b.WriteString(" [--" + f + " <" + f + ">]")
		}
		for _, f := range c.bools {
			b.WriteString(" [--" + f + "]")
		}
		b.WriteString("\n")
	}
	b.WriteString("Every command except mcp accepts --output json.\n")
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
	if c.json && cmd.noOutput {
		return nil, command{}, usageError{fmt.Sprintf("hlab %s takes no --output", cmd.path)}
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
		case contains(cmd.flags, name), contains(cmd.opts, name):
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

// mgmt sends one request to the management API and decodes the answer into
// v (if not nil); a *[]byte gets the answer as it is. A failed connection
// is mgmtclient.ErrNotRunning, an error answer its message.
func (c *call) mgmt(method, path string, body, v any) error {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return err
		}
	}
	status, data, err := mgmtclient.New(c.io.Dial).Do(c.ctx, method, path, b)
	if err != nil {
		return err
	}
	if status >= 300 {
		return mgmtclient.AnswerError(status, data)
	}
	if raw, ok := v.(*[]byte); ok {
		*raw = data
		return nil
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
	var params []string
	if c.flags[discardParam] == "true" {
		params = append(params, discardParam)
	}
	if err := c.io.Service.Start(c.ctx, params...); err != nil {
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
			if zs, ok := st["shadowing_zones"].([]any); ok {
				for _, z := range zs {
					if m, ok := z.(map[string]any); ok {
						text += fmt.Sprintf("warning: zone %v (project %v) shadows a public domain\n", m["zone"], m["project"])
					}
				}
			}
			return c.out(st, text)
		}
	}
	if state == sysint.StateStopped && c.io.StatusFile != "" {
		if f, ok := cloudstatus.Read(c.io.StatusFile); ok {
			switch {
			case f.State == "refused" && f.Problem != nil:
				return c.out(f, "state: refused\nproblem: "+f.Problem.Message+"\n")
			case f.State == sysint.StateStopped && f.Error != "":
				return c.out(f, "state: stopped\nerror: "+f.Error+"\n")
			}
		}
	}
	return c.out(serviceState{state}, "state: "+state+"\n")
}

// catalogUpdate sends the Hetzner token once to hlabd, which updates the
// server type catalog with it. The token comes from the variable named by
// --hetzner-token-env, else from the hidden prompt on a terminal; it is
// never printed.
func catalogUpdate(c *call) error {
	var token string
	switch name := c.flags["hetzner-token-env"]; {
	case name != "":
		token = strings.TrimSpace(c.io.Getenv(name))
		if token == "" {
			return usageError{fmt.Sprintf("the variable %s named by --hetzner-token-env is empty or not set", name)}
		}
	case c.io.IsTerminal != nil && c.io.IsTerminal():
		s, err := c.io.ReadSecret("Hetzner API token: ")
		if err != nil {
			return fmt.Errorf("read the token: %w", err)
		}
		token = strings.TrimSpace(s)
		if token == "" {
			return usageError{"no token entered; pass it in an environment variable named with --hetzner-token-env"}
		}
	default:
		return usageError{"no terminal to ask for the Hetzner token; pass it in an environment variable named with --hetzner-token-env"}
	}
	var body struct {
		ServerTypes int `json:"server_types"`
	}
	if err := c.mgmt(http.MethodPost, "/catalog/update", map[string]string{"hetzner_token": token}, &body); err != nil {
		return err
	}
	return c.out(body, fmt.Sprintf("server type catalog updated: %d server types\n", body.ServerTypes))
}

func projectCreate(c *call) error {
	var raw []byte
	if err := c.mgmt(http.MethodPost, "/projects", map[string]string{"display_name": c.args[0]}, &raw); err != nil {
		return err
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	return c.out(json.RawMessage(raw), "project "+p.Name+" created\n")
}

func projectList(c *call) error {
	var raw []byte
	if err := c.mgmt(http.MethodGet, "/projects", nil, &raw); err != nil {
		return err
	}
	var body struct {
		Projects []struct {
			Name        string    `json:"name"`
			DisplayName string    `json:"display_name"`
			Created     time.Time `json:"created"`
			Resources   int       `json:"resources"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tRESOURCES\tCREATED\tDISPLAY NAME")
	for _, p := range body.Projects {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", p.Name, p.Resources, p.Created.Local().Format(time.DateTime), p.DisplayName)
	}
	_ = tw.Flush()
	return c.out(json.RawMessage(raw), b.String())
}

func projectDelete(c *call) error {
	var body struct {
		ActionID int64 `json:"action_id"`
	}
	if err := c.mgmt(http.MethodDelete, "/projects/"+pathEscape(c.args[0]), nil, &body); err != nil {
		return err
	}
	if err := c.waitAction(body.ActionID, projectDeleteTimeout); err != nil {
		return fmt.Errorf("deleting project %s failed: %w", c.args[0], err)
	}
	return c.out(body, "project "+c.args[0]+" deleted\n")
}

// projectDeleteTimeout bounds the wait for a project deletion, which
// deletes VMs and disks one after another.
const projectDeleteTimeout = 30 * time.Minute

// actionPoll is the interval between two reads of a running action.
const actionPoll = 200 * time.Millisecond

// waitAction polls the management action id until it is no longer running
// and returns its error, if any; it gives up after timeout.
func (c *call) waitAction(id int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var body struct {
			Action struct {
				Status string               `json:"status"`
				Error  *mgmtclient.APIError `json:"error"`
			} `json:"action"`
		}
		if err := c.mgmt(http.MethodGet, fmt.Sprintf("/actions/%d", id), nil, &body); err != nil {
			return err
		}
		switch body.Action.Status {
		case "success":
			return nil
		case "error":
			if body.Action.Error != nil {
				return body.Action.Error
			}
			return errors.New("the action ended with an error")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("still running after %s (action %d)", timeout, id)
		}
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-time.After(actionPoll):
		}
	}
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

// caExport writes the hlab CA certificate (PEM) to the file of the argument.
func caExport(c *call) error {
	var pem []byte
	if err := c.mgmt(http.MethodGet, "/ca.pem", nil, &pem); err != nil {
		return err
	}
	if err := os.WriteFile(c.args[0], pem, 0o644); err != nil {
		return err
	}
	return c.out(map[string]string{"file": c.args[0]}, "HLab CA written to "+c.args[0]+"\n")
}

// caTrust adds the hlab CA to the host's trust stores (on) or removes it
// (off).
func caTrust(c *call) error {
	var on bool
	switch c.args[0] {
	case "on":
		on = true
	case "off":
	default:
		return usageError{fmt.Sprintf("hlab ca trust takes on or off, not %q", c.args[0])}
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := c.mgmt(http.MethodPut, "/trust", map[string]bool{"on": on}, &body); err != nil {
		return err
	}
	text := "the HLab CA is no longer trusted on this host\n"
	if body.On {
		text = "the HLab CA is trusted on this host\n"
	}
	return c.out(body, text)
}

func printVersion(c *call) error {
	return c.out(map[string]string{"version": version.Version}, "HLab "+version.Version+"\n")
}

// logLines is the number of lines `hlab logs` shows before following.
const logLines = 1000

// logs shows the last lines of hlabd.log, through the management API while
// the cloud runs and from the files while it is stopped; --follow goes on
// with new lines until the command is interrupted.
func logs(c *call) error {
	follow := c.flags["follow"] == "true"
	path := "/logs"
	if follow {
		path += "?follow=1"
	}
	err := mgmtclient.New(c.io.Dial).Stream(c.ctx, path, c.logLine)
	switch {
	case err == nil || c.ctx.Err() != nil:
		return nil
	case errors.Is(err, mgmtclient.ErrNotRunning) && c.io.LogDir != "":
		err = logfile.Tail(c.ctx, c.io.LogDir, "hlabd.log", logLines, follow, c.logLine)
		if c.ctx.Err() != nil {
			return nil
		}
		return err
	default:
		return err
	}
}

// logLine prints one line of hlabd.log: raw with --output json, else as
// "time LEVEL msg key=value...".
func (c *call) logLine(line []byte) error {
	line = bytes.TrimRight(line, "\r\n")
	if !c.json {
		if text, ok := humanLogLine(line); ok {
			line = []byte(text)
		}
	}
	_, err := fmt.Fprintf(c.io.Stdout, "%s\n", line)
	return err
}

// humanLogLine turns a JSON log record into "time LEVEL msg key=value...",
// keeping the order of the keys; false for a line that is no JSON object.
func humanLogLine(line []byte) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", false
	}
	var head [3]string // time, level, msg
	var attrs []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return "", false
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			value = string(raw)
		}
		switch key {
		case "time":
			if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
				value = t.Local().Format(time.DateTime)
			}
			head[0] = value
		case "level":
			head[1] = value
		case "msg":
			head[2] = value
		default:
			if value == "" || strings.ContainsAny(value, " \"=") {
				value = strconv.Quote(value)
			}
			attrs = append(attrs, key+"="+value)
		}
	}
	return strings.Join(append(head[:], attrs...), " "), true
}
