# Phase 25 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| FAULT-01 | `Create` with a fake effect registered for every kind returns, per kind (table, one valid case each), the fault with IDs from 1 upwards, the project, kind, target and params as given, `created` from the clock and `ends` = created + duration, or nil without one | unit | internal/faults/manager_test.go: TestFaults/FAULT-01/create | 25-01 |
| FAULT-01 | without a seed, `api_error` and `network` with `loss_percent` below 100 get a chosen seed in 1..2^53-1; a given seed (also 2^64-1) is kept; the other kinds and `network` with 100 % have a nil seed; a seed given to them is `invalid_input` on `seed` (table) | unit | internal/faults/manager_test.go: TestFaults/FAULT-01/seed | 25-01 |
| FAULT-01 | invalid input is an `*InputError` naming the field (table): unknown kind; a kind without effect ("not available yet"); a missing target or a wrong target type per kind; a target on `api_error`/`rate_limit`; each parameter missing or out of its range (`traffic`, `loss_percent` 0/101, `delay_ms` 0/10001, `jitter_ms` above `delay_ms`, unknown `code`, `percent` 0/101, `only` other than read/write, `resource` not in `Resources`, `per_hour` 0/3601, `iops` 9); equal `from` and `to`; duration "0s", "25h", "abc"; no fault is left listed | unit | internal/faults/manager_test.go: TestFaults/FAULT-01/invalid_input | 25-01 |
| FAULT-01 | a target the lookup does not know (server, volume, storage box, location) is `ErrNotFound`; a volume that is not attached for `volume_unavailable` is an `*InputError` (table) | unit | internal/faults/manager_test.go: TestFaults/FAULT-01/unknown_target | 25-01 |
| FAULT-01 | a second fault of the same kind on the same target is `ErrConflict`, a second `api_error` or `rate_limit` in the same project too; the same kind on another target, another kind on the same target and the same kind in another project are created (table) | unit | internal/faults/manager_test.go: TestFaults/FAULT-01/conflict | 25-01 |
| FAULT-01 | `List(p)` gives only p's faults by ID, `List("")` all projects', `Count` all; `End(p, id)` ends one, an unknown ID or another project's ID is `ErrNotFound`; `EndProject` ends only that project's; `EndAll` ends all | unit | internal/faults/manager_test.go: TestFaults/FAULT-01/list_and_end | 25-01 |
| FAULT-01 | through the management API (table over the routes): `POST /projects/p/faults` answers 201 with the fault object and a chosen seed, 400 `invalid_input`, 404 `not_found` for an unknown target and an unknown project, 409 `conflict`; `GET /projects/p/faults` and `GET /faults` list; `GET /status` carries `faults`; `DELETE /projects/p/faults/{id}` 204 and 404, `DELETE /projects/p/faults` and `DELETE /faults` 204 and end them | unit | internal/mgmt/faults_test.go: TestMgmt/FAULT-01 | 25-03 |
| FAULT-02 | a fault with a duration ends when the test clock passes it: it leaves the list and its effect's `Undo` ran once; a fault without duration stays | unit | internal/faults/manager_test.go: TestFaults/FAULT-02/duration | 25-01 |
| FAULT-02 | `EndTarget(p, "server", 7)` ends every fault of p on server 7 with `Undo`, and none on another server, another target type or in another project | unit | internal/faults/manager_test.go: TestFaults/FAULT-02/target_deleted | 25-01 |
| FAULT-02 | ending by `End`, `EndProject` and `EndAll` runs `Undo` while the fault is still listed and removes it after; an `Apply` that fails refuses the fault and leaves nothing listed (table) | unit | internal/faults/manager_test.go: TestFaults/FAULT-02/undo | 25-01 |
| FAULT-02 | `DELETE /projects/p` ends p's faults (gone from `GET /faults`) and keeps those of project q | unit | internal/mgmt/faults_test.go: TestMgmt/FAULT-02 | 25-03 |
| FAULT-03 | with an `api_error` of 50 % and seed S, 200 token requests are hit at the same positions as on a fresh manager with seed S, at other positions with another seed, and roughly half are hit; 100 % hits every request; a hit answers the code with its status and a message naming the fault ID, and the handler is not reached (a hit `POST /ssh_keys` creates no key) | unit | internal/faults/middleware_test.go: TestFaults/FAULT-03/share_and_seed | 25-02 |
| FAULT-03 | each injected code answers its status and validates against the OpenAPI description (table: `server_error` 500, `service_error` 422, `conflict` 409, `locked` 423, `unavailable` 503, `invalid_input` 422 with `details.fields` []) | unit | internal/faults/middleware_test.go: TestFaults/FAULT-03/codes | 25-02 |
| FAULT-03 | filters (table): `only: read` hits GET and HEAD and never POST, `only: write` the reverse; `resource: servers` hits `/v1/servers` and not `/v1/ssh_keys`; `resource: storage_boxes` hits `/hetzner/v1/storage_boxes`; requests that do not match, interleaved, do not change which matching requests are hit | unit | internal/faults/middleware_test.go: TestFaults/FAULT-03/filters | 25-02 |
| FAULT-03 | at 100 %, requests through the project proxy context (`api.WithProject`) and token requests of another project are never hit; after `End` the project's token requests pass again | unit | internal/faults/middleware_test.go: TestFaults/FAULT-03/unaffected | 25-02 |
| FAULT-04 | without a `rate_limit` fault, token responses carry `RateLimit-Limit` 3600, `RateLimit-Remaining` 3599, 3598, ... and `RateLimit-Reset` = now + (3600 - remaining) s, refilled one per second on the test clock, at 0 never refused; a 401, an unknown path, a Hetzner API (`/hetzner/v1`) response and a proxy request carry the headers, the 401 and the proxy request with 3600 remaining and without counting | unit | internal/faults/ratelimit_test.go: TestFaults/FAULT-04/default_headers | 25-02 |
| FAULT-04 | with `per_hour` 10, ten token requests (Cloud and Hetzner API mixed) pass with `Remaining` 9 to 0, the eleventh gets `429 rate_limit_exceeded` with Limit 10, Remaining 0 and Reset now + 3600 s; refused requests do not count and draw nothing from an `api_error` of the project, a request hit by `api_error` counts; after 360 s one more passes; another project and proxy requests are not limited | unit | internal/faults/ratelimit_test.go: TestFaults/FAULT-04/limit | 25-02 |
| FAULT-04 | after the `rate_limit` fault ends, the project's responses carry the default headers again and are never refused; a new `rate_limit` starts with a full bucket | unit | internal/faults/ratelimit_test.go: TestFaults/FAULT-04/end | 25-02 |
| FAULT-12 | `hlab fault create --project p <kind>` with the flags of every kind (table, one case per kind: `--server`, `--volume`, `--storage-box`, `--from`/`--to`, `--traffic`, `--loss-percent`, `--delay-ms`, `--jitter-ms`, `--code`, `--percent`, `--only`, `--resource`, `--per-hour`, `--iops`, `--seed`, `--duration`) creates the fault the management API then lists with that target and params; the output names ID and seed, `--output json` gives the fault object | unit | internal/cli/fault_test.go: TestCLI/FAULT-12/create | 25-05 |
| FAULT-12 | `hlab fault list` prints ID, project, kind, target, params, seed and remaining time; `--project` shows only that project; `--output json` gives the fault objects | unit | internal/cli/fault_test.go: TestCLI/FAULT-12/list | 25-05 |
| FAULT-12 | `hlab fault delete --project p <id>`, `--project p --all` and `--all` end one fault, the project's and all; an unknown ID exits 1 with the API's message | unit | internal/cli/fault_test.go: TestCLI/FAULT-12/delete | 25-05 |
| FAULT-12 | usage errors exit 2 (table: create without `--project`, a non-integer `--percent`, two targets; delete without ID or `--all`, with both, ID without `--project`); API refusals exit 1 with the message (invalid parameter, conflict) | unit | internal/cli/fault_test.go: TestCLI/FAULT-12/errors | 25-05 |
| MCP-06 | `fault_create` (an `api_error` without target with seed and duration, a fault with a server target), `fault_list` with and without `project`, and `fault_delete` with `id`, with `all` and `project`, and with `all` alone give the same JSON as `hlab fault ... --output json` and change the faults the management API lists | unit | internal/cli/mcp_fault_test.go: TestMCP/MCP-06/tools | 25-06 |
| MCP-06 | arguments (table): `target` null or omitted for `api_error` accepted; `seed` omitted or null gets a chosen seed; `seed` 2^53-1 kept, 2^53 a tool error; `fault_delete` with neither or both of `id` and `all` a tool error | unit | internal/cli/mcp_fault_test.go: TestMCP/MCP-06/arguments | 25-06 |

DOC-01, DOC-03 and DOC-04 keep their tests in `test/docs/docs_test.go`
(DOC-01 picks up the `hlab fault` commands and flags through
`cli.Commands()` and the tools through `cli.MCPTools()`); DOC-07 is
manual. The wiring of 25-04 (manager per cloud start, `EndAll` at cloud
stop, `EndTarget` in the deletes of servers, volumes and storage boxes)
is glue and has no row.

## Writers

1. Faults, part 1 (runs first): `internal/faults/helpers_test.go` (fake
   `scheduler.Clock` with manual advance, recording fake `Effect`, a
   `TargetLookup` over a fixed set, a manager constructor with effects for
   every kind; shared with part 2) and `internal/faults/manager_test.go`
   (rows FAULT-01 create to list_and_end, FAULT-02 duration, target_deleted,
   undo), ~500 lines.
2. Faults, part 2: `internal/faults/middleware_test.go` and
   `internal/faults/ratelimit_test.go` (rows FAULT-03, FAULT-04) on
   `apitest.NewWith` with `apitest.ProbeModule()` and the part 1 helpers,
   ~450 lines.
3. Management API: `internal/mgmt/faults_test.go` with its registration in
   `TestMgmt` (`internal/mgmt/mgmt_test.go`), rows FAULT-01 and FAULT-02,
   a real `faults.Manager` with no-op effects for every kind, ~200 lines.
4. CLI and MCP: `internal/cli/fault_test.go` (registered in `TestCLI` in
   `internal/cli/cli_test.go`, the harness `newHlabWith` with
   `mgmt.Deps.Faults` set) and `internal/cli/mcp_fault_test.go` (registered
   in `TestMCP` in `internal/cli/mcp_test.go`), plus `toolArgs` entries for
   `fault_create`, `fault_list` and `fault_delete` in
   `internal/cli/mcp_test.go` so the loops of MCP-01, MCP-02 and MCP-04
   call them with valid arguments, ~500 lines.
