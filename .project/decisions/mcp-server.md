# Decision: MCP server scope

Status: decided (2026-11-21)

## Question

Which tools does the MCP server for controlling hlab offer? It was taken
from "Later" into milestone 1 (Q18). Already settled (DECISIONS.md,
2026-11-21): it runs as the subcommand `hlab mcp`, started by the MCP
client, speaks MCP over stdio and talks to hlabd through the management
pipe like the other `hlab` commands; it offers neither
`cloud start --discard-state` nor `catalog update`; deleting projects and
creating tokens are allowed, and a new token value is returned once.

## Decision

The MCP server offers the functions of the `hlab` command plus the
per-project resource overview that the management API already returns
("A+"). Hetzner resources (servers, networks, ...) are not read or changed
through MCP tools in milestone 1; agents use hcloud, OpenTofu and Ansible
against hlab for them.

Tools:

- `cloud_status`, `cloud_start`, `cloud_stop`
- `project_create`, `project_list`, `project_delete`
- `token_create`, `token_list`, `token_revoke`
- `logs`
- `ca_export`, `ca_trust`
- `version`

Details:

1. Cloud stopped: the tools behave like `hlab`. `cloud_status`, `logs`
   (read from the log files) and `version` work; all other tools return an
   MCP tool error "the cloud is not running". The MCP server itself starts
   and stays up while the cloud is stopped, so `cloud_start` is reachable.
2. `cloud_start` and `cloud_stop` wait until the cloud is `running` or
   `stopped`, or until a failed start or a refusal (`refused` with its
   problem message, returned as a tool error), with a deadline (about
   5 minutes). `project_delete` waits for its action like
   `hlab project delete`.
3. `token_create` returns the token value once in the tool result, with a
   note that it cannot be retrieved again. `token_list` never returns
   values.
4. `logs` has no follow mode; it returns the last N lines (default 200,
   at most 1000).
5. `ca_export` returns the CA certificate as PEM text instead of writing a
   file.
6. `ca_trust` takes on/off and works through the management pipe like
   `hlab ca trust`.
7. `project_list` returns, per project, the resource counts by type
   (`resources_by_type` of `GET /mgmt/v1/projects`); this is the resource
   overview.
8. Tests: unit tests with an MCP client against a fake management client,
   one case per tool plus one case for a stopped cloud; one end-to-end case
   for the wiring (`hlab mcp` against the real dev service: create a
   project, create a token, delete the project).
9. Documentation: a section `hlab mcp` in `docs/commands.md` with the tool
   list and a configuration example for Claude Code and Claude Desktop, and
   a short note in the README.
10. The repository does not check in an `.mcp.json` that configures the
    server for Claude Code; the configuration example in the documentation
    is enough.

## Reasons

- A thin layer over the existing `hlab` functions and the management
  client: no new domain logic, small test effort (one unit case per tool,
  one end-to-end case).
- Agents can already manage Hetzner resources with hcloud, OpenTofu and
  Ansible, the clients the project targets; a second Hetzner client inside
  the MCP server would duplicate them.
- The resource overview costs nothing extra, because `GET /projects`
  already returns it, and lets an agent see what a project holds without
  hcloud.
- Waiting in `cloud_start`/`cloud_stop` spares the agent its own polling.
- A checked-in `.mcp.json` would point every developer's Claude Code at
  `bin\hlab.exe` and the dev service, which is confusing while working on
  the product itself.

## Alternatives considered

- (B) Tools that read and change Hetzner resources through the project API
  (`ANY /mgmt/v1/projects/{name}/api/v1/{rest}`). Rejected for milestone 1
  as much larger (tools, schemas, validation and tests for every resource
  type and action) and bypassing the token model (the path has full
  rights). Kept as a candidate for a later version until the user removed
  it from the plans on 2026-11-24.
- One generic tool that forwards any Hetzner API call to a project.
  Rejected: cheap, but unvalidated and with full write rights.
- (C) Read-only tools. Rejected: contradicts the settled decision that
  projects may be deleted and tokens created, and leaves little to control.
- `logs` with follow mode: rejected, it does not fit request/response MCP
  tools.
- `ca_export` writing a file: rejected, PEM text in the result is simpler
  for an agent.
