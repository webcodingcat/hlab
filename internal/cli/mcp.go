package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/webcodingcat/hlab/internal/version"
)

// MCPOptions sets the waits of `hlab mcp`; zero values take the defaults.
type MCPOptions struct {
	CloudWait time.Duration // limit of cloud_start and cloud_stop; default 5 min
	Poll      time.Duration // service state and status file reads while waiting; default 1 s
	Progress  time.Duration // interval of progress notifications; default 10 s
}

// MCP defaults.
const (
	defaultCloudWait = 5 * time.Minute
	defaultPoll      = time.Second
	defaultProgress  = 10 * time.Second
	defaultLogLines  = 200
)

// tokenNote is the second text item of token_create.
const tokenNote = "The token value above is shown only once and cannot be retrieved again; store it now."

// mcpServer holds what the tool handlers work with.
type mcpServer struct {
	sys  IO
	opts MCPOptions
}

// No arguments.
type noArgs struct{}

type projectCreateArgs struct {
	DisplayName string `json:"display_name" jsonschema:"display name of the new project; the project name is derived from it"`
}

type projectDeleteArgs struct {
	Name string `json:"name" jsonschema:"name of the project to delete"`
}

type tokenCreateArgs struct {
	Project  string `json:"project" jsonschema:"name of the project"`
	Name     string `json:"name" jsonschema:"name of the new token"`
	ReadOnly bool   `json:"read_only,omitempty" jsonschema:"whether the token may only read"`
}

type tokenListArgs struct {
	Project string `json:"project" jsonschema:"name of the project"`
}

type tokenRevokeArgs struct {
	Project string `json:"project" jsonschema:"name of the project"`
	Name    string `json:"name" jsonschema:"name of the token to revoke"`
}

type logsArgs struct {
	Lines int `json:"lines,omitempty"`
}

type caTrustArgs struct {
	On bool `json:"on" jsonschema:"true adds the HLab CA to the host's trust stores, false removes it"`
}

// logsSchema is the explicit input schema of logs. It names the default
// in the description only: a "default" keyword makes go-sdk v1.8.0 panic on
// a call without arguments (it applies defaults to a nil map).
func logsSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"lines": {
				Type:        "integer",
				Description: "number of lines from the end of hlabd.log (default 200)",
				Minimum:     jsonschema.Ptr(1.0),
				Maximum:     jsonschema.Ptr(float64(logLines)),
			},
		},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

// mcpTool is one tool of the server.
type mcpTool struct {
	name string
	add  func(srv *mcp.Server, s *mcpServer, name string)
}

// tool returns an mcpTool whose handler h gets typed arguments.
func tool[In any](name, desc string, schema *jsonschema.Schema, h func(s *mcpServer, ctx context.Context, req *mcp.CallToolRequest, in In) *mcp.CallToolResult) mcpTool {
	return mcpTool{name: name, add: func(srv *mcp.Server, s *mcpServer, name string) {
		t := &mcp.Tool{Name: name, Description: desc}
		if schema != nil {
			t.InputSchema = schema
		}
		mcp.AddTool(srv, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			return h(s, ctx, req, in), nil, nil
		})
	}}
}

// mcpTools are the tools in the order the server lists them (by name).
var mcpTools = []mcpTool{
	tool("ca_export", "Return the HLab CA certificate as PEM text.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) *mcp.CallToolResult {
			var pem []byte
			c := &call{ctx: ctx, io: s.sys, json: true, flags: map[string]string{}}
			if err := c.mgmt(http.MethodGet, "/ca.pem", nil, &pem); err != nil {
				return errorResult(err)
			}
			return textResult(string(pem))
		}),
	tool("ca_trust", "Add the HLab CA to the host's trust stores (on true) or remove it (on false), like hlab ca trust.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in caTrustArgs) *mcp.CallToolResult {
			arg := "off"
			if in.On {
				arg = "on"
			}
			return s.command(ctx, caTrust, []string{arg}, nil)
		}),
	tool("cloud_start", "Start the cloud (the hlabd service) and wait until it runs (at most 5 minutes); returns the cloud status.", nil,
		func(s *mcpServer, ctx context.Context, req *mcp.CallToolRequest, _ noArgs) *mcp.CallToolResult {
			return s.cloudStartWait(ctx, req)
		}),
	tool("cloud_status", "Return the state of the cloud, like hlab cloud status.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) *mcp.CallToolResult {
			return s.command(ctx, cloudStatus, nil, nil)
		}),
	tool("cloud_stop", "Stop the cloud (the hlabd service) and wait until it is stopped (at most 5 minutes); returns the cloud status.", nil,
		func(s *mcpServer, ctx context.Context, req *mcp.CallToolRequest, _ noArgs) *mcp.CallToolResult {
			return s.cloudStopWait(ctx, req)
		}),
	tool("logs", "Return the last lines of hlabd.log as JSON lines, one per line.", logsSchema(),
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in logsArgs) *mcp.CallToolResult {
			n := in.Lines
			if n == 0 {
				n = defaultLogLines
			}
			res := s.command(ctx, logs, nil, nil)
			if res.IsError {
				return res
			}
			lines := strings.Split(strings.TrimRight(res.Content[0].(*mcp.TextContent).Text, "\n"), "\n")
			if len(lines) == 1 && lines[0] == "" {
				lines = nil
			}
			lines = lines[max(0, len(lines)-n):]
			text := strings.Join(lines, "\n")
			if text != "" {
				text += "\n"
			}
			return textResult(text)
		}),
	tool("project_create", "Create a project with a display name, like hlab project create.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in projectCreateArgs) *mcp.CallToolResult {
			return s.command(ctx, projectCreate, []string{in.DisplayName}, nil)
		}),
	tool("project_delete", "Delete a project with all its resources and wait until the deletion has ended, like hlab project delete.", nil,
		func(s *mcpServer, ctx context.Context, req *mcp.CallToolRequest, in projectDeleteArgs) *mcp.CallToolResult {
			return s.projectDeleteWait(ctx, req, in.Name)
		}),
	tool("project_list", "List the projects with their resource counts per type.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) *mcp.CallToolResult {
			return s.command(ctx, projectList, nil, nil)
		}),
	tool("token_create", "Create an API token of a project; the value is returned only once.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in tokenCreateArgs) *mcp.CallToolResult {
			flags := map[string]string{"project": in.Project, "name": in.Name}
			if in.ReadOnly {
				flags["read-only"] = "true"
			}
			res := s.command(ctx, tokenCreate, nil, flags)
			if !res.IsError {
				res.Content = append(res.Content, &mcp.TextContent{Text: tokenNote})
			}
			return res
		}),
	tool("token_list", "List the API tokens of a project, without their values.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in tokenListArgs) *mcp.CallToolResult {
			return s.command(ctx, tokenList, nil, map[string]string{"project": in.Project})
		}),
	tool("token_revoke", "Revoke an API token of a project.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in tokenRevokeArgs) *mcp.CallToolResult {
			return s.command(ctx, tokenRevoke, []string{in.Name}, map[string]string{"project": in.Project})
		}),
	tool("version", "Return the version of hlab.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) *mcp.CallToolResult {
			return s.command(ctx, printVersion, nil, nil)
		}),
}

// MCPTools lists the tool names in the order the server lists them.
func MCPTools() []string {
	names := make([]string, 0, len(mcpTools))
	for _, t := range mcpTools {
		names = append(names, t.name)
	}
	return names
}

// NewMCPServer returns the MCP server with every tool of MCPTools, working
// through sys like the other commands (Dial, Service, StatusFile, LogDir).
func NewMCPServer(sys IO, opts MCPOptions) *mcp.Server {
	if opts.CloudWait <= 0 {
		opts.CloudWait = defaultCloudWait
	}
	if opts.Poll <= 0 {
		opts.Poll = defaultPoll
	}
	if opts.Progress <= 0 {
		opts.Progress = defaultProgress
	}
	s := &mcpServer{sys: sys, opts: opts}
	srv := mcp.NewServer(&mcp.Implementation{Name: "hlab", Version: version.Version}, nil)
	for _, t := range mcpTools {
		t.add(srv, s, t.name)
	}
	return srv
}

// command runs the command function run with JSON output and returns what
// it prints as the result; its error is a tool error.
func (s *mcpServer) command(ctx context.Context, run func(*call) error, args []string, flags map[string]string) *mcp.CallToolResult {
	var out bytes.Buffer
	sys := s.sys
	sys.Stdout = &out
	if flags == nil {
		flags = map[string]string{}
	}
	if err := run(&call{ctx: ctx, io: sys, json: true, args: args, flags: flags}); err != nil {
		return errorResult(err)
	}
	return textResult(out.String())
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func errorResult(err error) *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res
}

// serveMCP is the command `hlab mcp`: the MCP server on stdin and stdout
// until stdin ends or ctx is done.
func serveMCP(c *call) error {
	t := &mcp.IOTransport{Reader: io.NopCloser(c.io.Stdin), Writer: nopWriteCloser{c.io.Stdout}}
	err := NewMCPServer(c.io, MCPOptions{}).Run(c.ctx, t)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
