# Phase 17 research: MCP server `hlab mcp` (as of 2026-11-22)

## go-sdk version, Go requirement, license
- v1.8.0 exists (published 2026-09-14), and it is the latest stable release. v1.9.0-pre.1 (2026-11-17) is a pre-release and is not needed. Releases are frequent, so the project is maintained. Source: https://github.com/modelcontextprotocol/go-sdk/releases
- v1.8.0 go.mod says `go 1.25.0`. The project uses go 1.27, so this is compatible. Source: https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.8.0/go.mod
- Direct dependencies: golang-jwt/jwt/v5, google/jsonschema-go, segmentio/encoding, yosida95/uritemplate/v3, golang.org/x/oauth2, x/time, x/tools, google/go-cmp. All of them land in go.sum.
- License: the LICENSE file is Apache-2.0 plus a transition notice. New code is Apache-2.0. Contributions whose authors did not consent to relicensing stay MIT. Documentation is CC-BY-4.0. So the SDK is Apache-2.0 or MIT, and both are permissive. Source: https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.8.0/LICENSE
- v1.8.0 changes from earlier versions: it removed the MCPGODEBUG flags `seterroroverwrite`, `enableoriginverification`, `disablecontenttypecheck` and `disablelocalhostprotection`. `notifications/cancelled` is now sent asynchronously by default. None of this affects stdio or in-memory use.

## AddTool
- Signature: `func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out])`. It does not return an error. A bad schema panics at registration.
- Handler type: `type ToolHandlerFor[In, Out any] func(ctx context.Context, request *CallToolRequest, input In) (result *CallToolResult, output Out, _ error)`.
- Low-level variant: `Server.AddTool(t *Tool, h ToolHandler)` with `type ToolHandler func(context.Context, *CallToolRequest) (*CallToolResult, error)`. There the SDK does no validation, and a returned error becomes a protocol error.
- Returned error: with `ToolHandlerFor`, a plain error is converted by `errRes.SetError(err)` into a `CallToolResult` with `IsError=true`. Its Content is a TextContent with the error text, and the call itself returns no protocol error. The exception is an error of type `*jsonrpc.Error`, which is returned as a protocol error.
- Invalid input (schema validation or unmarshal failure) also becomes a tool error with `IsError=true`. The text is `validating "arguments": ...`.
- A handler may return a nil `*CallToolResult`. The SDK then fills it in: `StructuredContent` is set from the output, and if `Content` is nil it gets a TextContent holding the JSON of the output.
- `Out = any`: no output schema is generated (the check is `reflect.TypeFor[Out]() != reflect.TypeFor[any]()`). Unconfirmed: whether StructuredContent is still filled when the `any` value is non-nil. By the code read, a non-nil output sets `StructuredContent` and the text Content. If you need an exact result, return your own `*CallToolResult` with `Content` and `StructuredContent` set.
- With `Out` a concrete struct, the output schema is inferred and the output is validated. Content is the JSON text.
- Tag syntax is documented in google/jsonschema-go `For`:
  - The `jsonschema:"text"` tag value is the property description. It must not start with `WORD=`, which is reserved.
  - Fields with `json:",omitempty"` or `json:",omitzero"` are optional. All other fields are required.
  - The property name comes from the `json` tag, and `json:"-"` omits the field. Structs get additionalProperties false.
  - Example: `Name string \`json:"name" jsonschema:"project name"\``, and `Limit int \`json:"limit,omitempty" jsonschema:"max results"\``.
- Sources: https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.8.0/mcp/tool.go, https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.8.0/mcp/server.go, https://pkg.go.dev/github.com/google/jsonschema-go/jsonschema#For

## Transports and Server.Run
- `StdioTransport{MaxLineLength int}` is newline-delimited JSON on os.Stdin and os.Stdout. `Connect` is `newIOConnLimited(rwc{os.Stdin, nopCloserWriter{os.Stdout}}, ...)`.
- Arbitrary reader and writer: `&mcp.IOTransport{Reader: io.ReadCloser, Writer: io.WriteCloser, MaxLineLength: n}`. Both fields are Closers, so wrap with `io.NopCloser` where needed. This is not a new type per case. Pass the io streams of the `hlab mcp` command's own in and out to it, so tests can drive it without a subprocess.
- Server.Run: `func (s *Server) Run(ctx context.Context, t Transport) error`. The transport must be persistent. It blocks until the client closes the connection or ctx is cancelled. On ctx cancel it closes the session and returns `ctx.Err()`.
- At stdin EOF, Run returns nil. Read from the source: jsonrpc2 `Connection.Wait` ignores an `io.EOF` read error and returns the closer's error, which is normally nil. This was not run, so the main session should still treat `io.EOF` as a normal end.
- `mcp.NewInMemoryTransports()` returns two `*InMemoryTransport` connected to each other. Connect the server side first (`server.Connect(ctx, t1, nil)`), because the client initializes while connecting.
- `mcp.LoggingTransport` wraps another transport and writes RPC logs to an io.Writer (handy when debugging tests).
- stdout is the protocol channel. The server must write nothing else to it. Use stderr or slog to stderr.
- Sources: https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.8.0/mcp/transport.go, https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.8.0/internal/jsonrpc2/conn.go

## Client side
- `mcp.NewClient(&mcp.Implementation{Name:..., Version:...}, nil)`.
- `client.Connect(ctx, transport, nil) (*ClientSession, error)`. It performs the initialize handshake.
- `cs.CallTool(ctx, &mcp.CallToolParams{Name: "x", Arguments: map[string]any{...}}) (*CallToolResult, error)`. `Arguments` is `any` and must be marshalable to JSON.
- A tool error is a nil Go error with `res.IsError == true` and the message in `res.Content[0].(*mcp.TextContent).Text`. A protocol error (unknown tool, a low-level handler error, `*jsonrpc.Error`) is a non-nil error from CallTool. `res.GetError()` always returns nil on a client, so check `IsError`.
- `StructuredContent` arrives on the client as decoded JSON (`any`, i.e. maps).
- `mcp.CommandTransport{Command: exec.Command(...), TerminateDuration: 5s default}`. `Close` closes the child's stdin, waits up to TerminateDuration, then sends SIGTERM, then kills. This fits an e2e test of the real `hlab mcp` binary: stdin EOF has to end the server.
- Progress: the client sets `Meta: mcp.Meta{"progressToken": ...}` in CallToolParams. The server handler reads `req.Params.GetProgressToken()` and calls `req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: n, Total: t, Message: m})`. `NotifyProgress` is `func (ss *ServerSession) NotifyProgress(ctx context.Context, params *ProgressNotificationParams) error`. Unconfirmed: the exact field names of ProgressNotificationParams (the pkg.go.dev page was truncated).
- Source: https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.8.0/mcp

## Tool-call timeouts of the clients
- Claude Code (official docs, https://code.claude.com/docs/en/mcp):
  - The wall-clock limit per call is `MCP_TOOL_TIMEOUT`, or a per-server `timeout` field in ms (min 1000). Default when unset is about 28 hours. Progress notifications do not extend the per-server `timeout`.
  - Idle timeout: a call with no response and no progress notification for the idle window aborts with an error. The window is 5 min for HTTP, SSE and similar, and 30 min for stdio. Override with `CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT` (ms, 0 disables). A 5-minute cloud wait is below the stdio idle window. A 30-minute project deletion is at the edge of it, so the server should send progress notifications about every 10 to 30 seconds. The docs say a progress notification counts against the idle check.
  - Claude Code moves an MCP call in the main conversation to a background task after 2 minutes. Change with `CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS` (0 disables). The result still arrives, but the session is not blocked.
  - `MCP_TIMEOUT` is the server startup timeout.
- Claude Desktop: no official documentation found. Community issue reports (not confirmed at the source) say Desktop cancels stdio tool calls at about 60 s with -32001, that it ignores `MCP_TOOL_TIMEOUT` and a `timeout` key in the config, and that this is not configurable. Whether progress notifications reset the 60-second timer in Desktop is unconfirmed. If Desktop is a target, assume long calls (5 or 30 minutes) can fail there unless the tool returns early. Sending progress notifications is the only mitigation known. Sources: https://claudeissues.com/issue/63379-bug-desktop-app-cancels-stdio-mcp-tool-calls-at-60s-and-ignores-mcp-tool-timeout and https://claudeissues.com/issue/22542-feature-make-mcp-tool-execution-timeout-configurable-in-claude-desktop (the first page was not fetchable, so this rests on the search summaries).

## Configuration for the docs
- Claude Code (official): `claude mcp add [options] <name> -- <command> [args...]`. The `--` separates Claude's options from the server command. Scopes are set with `-s` or `--scope`: `local` (default, stored in `~/.claude.json`, only you in this project), `project` (written to `.mcp.json` in the project root, shared), `user` (`~/.claude.json`, all projects). `--env KEY=value` can be repeated, and `--transport stdio` is the default for a command. Put another option between `--env` and the name.
  - Example: `claude mcp add --transport stdio --scope user hlab -- C:\Program Files\HLab\bin\hlab.exe mcp` (adjust the path to the installed `hlab.exe`).
  - `.mcp.json` shape: `{"mcpServers": {"hlab": {"command": "C:\\...\\hlab.exe", "args": ["mcp"], "env": {}}}}`. A `timeout` (ms) can be added per server.
  - Windows closes stdin of a stdio server and ends the process tree 300 ms later if it is still running. So `hlab mcp` should exit promptly at stdin EOF.
- `cmd /c`: not found in the official page for a real .exe. The `cmd /c` wrapper is the usual advice for `npx`, which is a .cmd shim. A changelog entry says Claude Code removed a "Windows requires cmd /c wrapper" warning because it fired wrongly. So a plain .exe needs no `cmd /c`. This is not stated as a rule in the docs, so the docs example should be tested once by the user. (Unconfirmed.)
- Claude Desktop (official, https://modelcontextprotocol.io/docs/develop/connect-local-servers): file `%APPDATA%\Claude\claude_desktop_config.json` (Settings, Developer, Edit Config creates it). Shape: `{"mcpServers": {"hlab": {"command": "C:\\Program Files\\HLab\\bin\\hlab.exe", "args": ["mcp"]}}}`. Use absolute paths with doubled backslashes. Restart Desktop fully after editing. Logs are in `%APPDATA%\Claude\logs` (`mcp.log`, `mcp-server-<name>.log` with the stderr of the server). Optional `env` object.

## Open points
- Out = any: exact StructuredContent behavior (see above).
- Desktop timeout and progress behavior (community reports only).
- The ProgressNotificationParams field names.
- Server.Run at EOF returns nil (read from source, not executed).
