# Phase 17: test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| MCP-03 | `DELETE /projects/{name}` while a deletion of that project runs answers `409 conflict` whose error object carries the `action_id` of the running deletion | unit | internal/mgmt/mgmt_test.go: TestMgmt/MCP-03 | 17-01 |
| MCP-01 | every tool does what its `hlab` command does and returns its `--output json` text; table with one case per tool: `cloud_status`, `cloud_start`, `cloud_stop`, `project_create` (derived name), `project_list` (with `resources_by_type`), `project_delete` (project gone), `token_create`, `token_list`, `token_revoke`, `logs`, `ca_export` (PEM text, no file), `ca_trust` (on and off reach `PUT /trust`), `version`; a command error comes back as a tool error with the message `hlab` prints | unit | internal/cli/mcp_test.go: TestMCP/MCP-01 | 17-02 |
| MCP-01 | `logs` returns the last N lines while the cloud runs: 200 without `lines`, N with `lines`, and `lines` 0 or 1001 is a tool error | unit | internal/cli/mcp_test.go: TestMCP/MCP-01/logs_lines | 17-02 |
| MCP-02 | with the cloud stopped, `cloud_status` (state and the refusal of `status.json`), `logs` (from the log files) and `version` work; every other tool except `cloud_start` and `cloud_stop` returns the tool error "the cloud is not running"; afterwards the same session still answers | unit | internal/cli/mcp_test.go: TestMCP/MCP-02 | 17-02 |
| MCP-03 | `cloud_start` returns only once the service is `running`, `cloud_stop` once it is `stopped` (fake service passing through `starting`/`stopping`) | unit | internal/cli/mcp_test.go: TestMCP/MCP-03/wait | 17-03 |
| MCP-03 | a refused start (`status.json` `refused` with problem) and a failed start (`stopped` with `error`) come back as a tool error with their message | unit | internal/cli/mcp_test.go: TestMCP/MCP-03/refused | 17-03 |
| MCP-03 | a `status.json` left by an earlier failed start does not end the wait of a new `cloud_start` that then reaches `running` | unit | internal/cli/mcp_test.go: TestMCP/MCP-03/stale_status | 17-03 |
| MCP-03 | with a short `CloudWait` and a service that never leaves `starting`, `cloud_start` gives up with a tool error naming the limit | unit | internal/cli/mcp_test.go: TestMCP/MCP-03/limit | 17-03 |
| MCP-03 | `project_delete` waits for its action and returns a failed action as a tool error; a `project_delete` while that deletion runs waits for its action and succeeds with it | unit | internal/cli/mcp_test.go: TestMCP/MCP-03/project_delete | 17-03 |
| MCP-03 | with a progress token, `cloud_start` and `project_delete` send progress notifications with the current state at the `Progress` interval; without a token none | unit | internal/cli/mcp_test.go: TestMCP/MCP-03/progress | 17-03 |
| MCP-03 | a client cancelling `cloud_start` and `project_delete` ends only the wait: the service is not stopped and the deletion action still finishes | unit | internal/cli/mcp_test.go: TestMCP/MCP-03/cancel | 17-03 |
| MCP-04 | `token_create` returns the value once with the note that it cannot be retrieved again; `token_list` contains no token value; no tool updates the catalog or passes `discard-state` (tool list, start parameters of the fake service, no `POST /catalog/update` reached) | unit | internal/cli/mcp_test.go: TestMCP/MCP-04 | 17-02 |
| MCP-05 | an MCP client starts `bin/hlab.exe mcp` against the running dev service and creates a project (display name beginning with the runID), creates a token and deletes the project, with a deadline per call and the deletion also in `t.Cleanup` | e2e | test/e2e/mcp_test.go: TestE2E/MCP-05 | 17-03 |
| DOC-01 | every tool of `cli.MCPTools()` is named in `docs/commands.md` (extension of the existing row) | unit | test/docs/docs_test.go: TestDocs/DOC-01 | 17-04 |

## Writers

1. cli: `internal/cli/mcp_test.go` (TestMCP, its fake service with scripted states, an in-memory client helper reusing `newHlab`, `notRunning`, `logDir` of `cli_test.go`), ~550 lines.
2. mgmt, docs and e2e: `internal/mgmt/mgmt_test.go` (TestMgmt/MCP-03), `test/docs/docs_test.go` (DOC-01 extension), `test/e2e/mcp_test.go` plus the `t.Run("MCP-05", parallel(testMCP05))` line in `test/e2e/e2e_test.go`, ~180 lines.
