# Phase 17: MCP server `hlab mcp`

## Interfaces

```go
// internal/mgmtclient: additive field of the error answer. The 409
// conflict of DELETE /projects/{name} sets it to the running deletion's
// action (Q20); Error() stays the message.
type APIError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	ActionID int64  `json:"action_id,omitempty"`
}

// internal/cli

// MCPOptions sets the waits of `hlab mcp`; zero values take the defaults.
type MCPOptions struct {
	CloudWait time.Duration // limit of cloud_start and cloud_stop; default 5 min
	Poll      time.Duration // service state and status file reads while waiting; default 1 s
	Progress  time.Duration // interval of progress notifications; default 10 s
}

// NewMCPServer returns the MCP server with every tool of MCPTools, working
// through sys like the other commands (Dial, Service, StatusFile, LogDir).
func NewMCPServer(sys IO, opts MCPOptions) *mcp.Server // mcp = github.com/modelcontextprotocol/go-sdk/mcp

// MCPTools lists the tool names in the order the server registers them:
// cloud_status, cloud_start, cloud_stop, project_create, project_list,
// project_delete, token_create, token_list, token_revoke, logs, ca_export,
// ca_trust, version.
func MCPTools() []string
```

Tool arguments (JSON names are a stable interface; `?` is optional):
`project_create {display_name}`, `project_delete {name}`,
`token_create {project, name, read_only?}`, `token_list {project}`,
`token_revoke {project, name}`, `logs {lines?}` (integer, minimum 1,
maximum 1000, default 200, explicit input schema), `ca_trust {on}`
(boolean, required); the others take none.

Results: one `*mcp.CallToolResult` with text content only, no structured
content and no output schema. The text is what `hlab <command> --output
json` prints. Exceptions: `logs` gives the raw JSON lines one per line,
`ca_export` the PEM, `token_create` a second text item saying the value
cannot be retrieved again. A command error is a result with `IsError` and
the message `hlab` prints.

## Plans

- [x] 17-01 Conflict on project deletion names the running action
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: MCP-03 | Tests: internal/mgmt/mgmt_test.go
  - Reads: ARCHITECTURE.md "Management API (named pipe)" (`DELETE /projects/{name}`); DECISIONS.md "Q20 Conflict on project deletion names the running action"; CONTEXT.md
  - Size: ~40 lines in 2 files
  - Adjusts: -
  - [x] In `internal/mgmt/mgmt.go`, the `actions.ErrLocked` branch of the project deletion finds the running `delete_project` action of the project (`Actions.ListFor(0, projectRef, p.ID)`, status `running`) and answers `409 conflict` with `"action_id":n` in the error object; the message stays.
  - [x] Add `ActionID int64 \`json:"action_id,omitempty"\`` to `mgmtclient.APIError` in `internal/mgmtclient/mgmtclient.go`; `AnswerError` keeps it. `hlab project delete` keeps printing the conflict message.

- [x] 17-02 MCP server and its tools
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: MCP-01, MCP-02, MCP-04 | Tests: internal/cli/mcp_test.go
  - Reads: REQUIREMENTS.md "MCP server (`MCP`)"; ARCHITECTURE.md "hlab (management command)" (the `mcp` item); `decisions/mcp-server.md`; CONTEXT.md; RESEARCH.md "AddTool", "Transports and Server.Run", "Client side"
  - Size: ~380 lines in 4 files
  - Adjusts: -
  - [x] Add `github.com/modelcontextprotocol/go-sdk` v1.8.0 (`go get`, `go.mod`, `go.sum`).
  - [x] New `internal/cli/mcp.go`: `MCPOptions`, `NewMCPServer`, `MCPTools`; one handler per tool that builds a `call` with `json: true`, the request ctx and stdout captured, runs the existing command function and returns the captured text as the result (command error -> `IsError`). `logs` streams `/logs` without follow and keeps the last N lines (from `logfile.Tail(LogDir, "hlabd.log", N, false, ...)` on `ErrNotRunning`); `ca_export` returns the PEM of `/ca.pem`; `token_create` adds the note; `cloud_start`, `cloud_stop` and `project_delete` here call the existing command functions without waiting (17-03 adds the waits). Implementation name `hlab`, version `version.Version`.
  - [x] In `internal/cli/cli.go`: command `mcp` in the table, running `NewMCPServer(c.io, MCPOptions{}).Run` over `mcp.IOTransport` on `IO.Stdin`/`IO.Stdout` (stdin EOF and a nil return end with exit 0); `Commands()` lists `mcp` without `--output`, and `hlab mcp --output json` is a usage error.

- [x] 17-03 Waiting cloud and project tools
  - Wave: 2 | Depends on: 17-01, 17-02 | Review: alone
  - Requirements: MCP-03, MCP-05 | Tests: internal/cli/mcp_test.go, test/e2e/mcp_test.go
  - Reads: REQUIREMENTS.md "MCP server (`MCP`)"; ARCHITECTURE.md "hlab (management command)" (status file, `mcp` item) and "Cloud start and stop"; CONTEXT.md; RESEARCH.md "Client side" (progress, `CommandTransport`), "Tool-call timeouts of the clients"
  - Size: ~280 lines in 2 files
  - Adjusts: -
  - [x] New `internal/cli/mcp_wait.go`: `cloud_start` and `cloud_stop` read `Service.State` first. In the target state they return the status at once. A running opposite transition is waited out, then acted on, and a running transition of their own is waited for. All of this falls within `CloudWait`. Start never passes `discard-state`. After its own start, a `stopped` counts as final only if the service was seen not stopped or `status.json` is newer than the request (`os.Stat` mod time). Then `refused` (problem message) or `error` is a tool error; else the result is the `cloud_status` text. At the limit the tool error is "still not running after 5m0s" ("still not stopped after ..." for stop).
  - [x] Also in `mcp_wait.go`: `project_delete` waits with `waitAction` and `projectDeleteTimeout`, and on a `409` with `ActionID` waits for that action. Every wait ends with the request ctx (the operation goes on in hlabd). With a progress token from `req.Params.GetProgressToken()`, every `Progress` interval sends `req.Session.NotifyProgress` with the current state (`project_delete`: the action's progress). Wire the waits into the handlers in `internal/cli/mcp.go`.
  - [x] Run the MCP-05 row against the dev service (`task build`, `task e2e -- -run 'TestE2E/MCP-05'`) and repair what it shows in `hlab mcp`.

- [x] 17-04 Documentation
  - Wave: 3 | Depends on: 17-03 | Review: alone
  - Requirements: DOC-01, DOC-04, DOC-07 | Tests: test/docs/docs_test.go
  - Reads: SPEC.md "Documentation" (D1, D2); `decisions/mcp-server.md` item 9 and 10; CONTEXT.md (docs item); RESEARCH.md "Configuration for the docs", "Tool-call timeouts of the clients"; docs/commands.md; README.md
  - Size: ~150 lines in 2 files
  - Adjusts: -
  - [x] `docs/commands.md`: section `hlab mcp` covering every tool with its arguments and result; the behaviour while the cloud is stopped and the waits; the sentence on clients with a short per-call limit (Claude Desktop probably about 60 s), whose wait may be cancelled while the operation continues, so `cloud_status`/`project_list` show the state; configuration examples for Claude Code (`claude mcp add --transport stdio --scope user hlab -- <path>\hlab.exe mcp`) and Claude Desktop (`%APPDATA%\Claude\claude_desktop_config.json`).
  - [x] `README.md`: short note on the MCP server linking to the section; the "More documentation" line names the MCP tools. No `.mcp.json` is checked in.
