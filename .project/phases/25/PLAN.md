# Phase 25: Fault model, API errors and rate limits

## Interfaces

New package `internal/faults` (25-01, middleware in 25-02). Projects are
identified by name. Methods that run or undo effects take a context; this
adds a `ctx` to the signatures in ARCHITECTURE.md "faults" (recorded in
DECISIONS.md).

```go
package faults

// Kinds.
const (
	KindServerCrash           = "server_crash"
	KindServerFreeze          = "server_freeze"
	KindNetwork               = "network"
	KindLatency               = "latency"
	KindAPIError              = "api_error"
	KindRateLimit             = "rate_limit"
	KindVolumeUnavailable     = "volume_unavailable"
	KindDiskSlow              = "disk_slow"
	KindStorageBoxUnavailable = "storage_box_unavailable"
)

// MaxChosenSeed is the largest seed hlab chooses, and the largest the MCP
// tool accepts (2^53-1).
const MaxChosenSeed = 1<<53 - 1

// Target types.
const (
	TargetServer     = "server"
	TargetVolume     = "volume"
	TargetStorageBox = "storage_box"
	TargetLocations  = "locations"
)

type Target struct {
	Type string `json:"type"`
	ID   int64  `json:"id,omitempty"`   // server, volume, storage_box
	From string `json:"from,omitempty"` // locations
	To   string `json:"to,omitempty"`
}

// Fault is the fault object of the management API (JSON as in
// ARCHITECTURE.md "Fault object"; params {} when a kind has none).
type Fault struct {
	ID      int64          `json:"id"`
	Project string         `json:"project"`
	Kind    string         `json:"kind"`
	Target  *Target        `json:"target"`
	Params  map[string]any `json:"params"`
	Seed    *uint64        `json:"seed"`
	Created time.Time      `json:"created"`
	Ends    *time.Time     `json:"ends"`
}

// Request is the body of POST /mgmt/v1/projects/{name}/faults.
type Request struct {
	Kind     string         `json:"kind"`
	Target   *Target        `json:"target"`
	Params   map[string]any `json:"params"`
	Seed     *uint64        `json:"seed"`
	Duration string         `json:"duration"` // Go duration, "" = none
}

// Spec is a validated-to-be request; Duration 0 = none.
type Spec struct {
	Kind     string
	Target   *Target
	Params   map[string]any
	Seed     *uint64
	Duration time.Duration
}

// Spec parses Duration; an unparsable one is an *InputError on "duration".
func (r Request) Spec() (Spec, error)

// InputError is invalid input (management API: 400 invalid_input).
type InputError struct{ Field, Message string }
func (e *InputError) Error() string

// ErrNotFound: unknown fault ID in the project, or unknown target
// (wrapped with a message naming it). ErrConflict: a fault of the same
// kind on the same target (for api_error and rate_limit: in the project).
var ErrNotFound, ErrConflict error

// Effect makes a kind act; Apply runs at create (a failure refuses the
// fault), Undo when the fault ends, before it leaves the list.
type Effect interface {
	Apply(ctx context.Context, f Fault) error
	Undo(ctx context.Context, f Fault) error
}

// TargetLookup checks that t exists in project: ErrNotFound (wrapped) for
// an unknown server, volume, storage box or location, an *InputError for a
// volume that is not attached when kind is volume_unavailable.
type TargetLookup func(ctx context.Context, project, kind string, t Target) error

type Options struct {
	Clock scheduler.Clock // nil = scheduler.RealClock()
	Lookup TargetLookup   // nil = every target exists
	// Effects per kind. api_error and rate_limit are always available
	// (their effect is Middleware); any other kind without an entry is
	// refused with an *InputError "kind ... is not available yet".
	Effects map[string]Effect
	// Resources are the first path segments api_error's "resource" may
	// name (api.Router.Resources()); nil = any.
	Resources []string
}

func New(o Options) *Manager
func (m *Manager) Create(ctx context.Context, project string, s Spec) (Fault, error)
func (m *Manager) List(project string) []Fault // "" = all projects; by ID
func (m *Manager) Count() int
func (m *Manager) End(ctx context.Context, project string, id int64) error
func (m *Manager) EndProject(ctx context.Context, project string) error
func (m *Manager) EndAll(ctx context.Context) error
func (m *Manager) EndTarget(ctx context.Context, project, targetType string, id int64) error

// Middleware is the fault layer of the api chain (25-02): rate_limit and
// api_error of the request's project for requests marked api.ByToken,
// and the RateLimit-* headers of the project's bucket.
func (m *Manager) Middleware(next http.Handler) http.Handler
```

Additions to `internal/api` (`HasStatus` in 25-01, the rest in 25-02):

```go
type Deps struct {
	Store   *store.Store
	Actions *actions.Manager
	// Faults wraps the chain after token auth (faults.Manager.Middleware);
	// nil = none.
	Faults func(http.Handler) http.Handler
}
func ByToken(ctx context.Context) bool  // set only by token auth, never by WithProject
func HasStatus(code string) bool        // code is in the status table
func (r *Router) Resources() []string   // first path segments of the mounted modules, sorted
// Every response under /v1 and /hetzner/v1 gets RateLimit-Limit 3600,
// RateLimit-Remaining 3600, RateLimit-Reset <now> before auth; the fault
// layer overwrites them for token requests.
```

`internal/apitest` (25-02): `NewWith(t, Options{Faults: func(http.Handler) http.Handler}, mods...)`;
`New` stays as it is.

`internal/mgmt` (25-03): `Deps.Faults *faults.Manager` (nil leaves the
fault routes out and `faults` 0); `Status.Faults int` (`json:"faults"`),
filled by the status handler from `Deps.Faults.Count()`.

## Plans

- [x] 25-01 Fault manager
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FAULT-01, FAULT-02 | Tests: internal/faults/manager_test.go, internal/faults/helpers_test.go
  - Reads: REQUIREMENTS FAULT-01, FAULT-02; ARCHITECTURE "faults (phase 25)", "Management API (named pipe)" the fault rows and "Fault object"; decisions/fault-injection.md; DECISIONS "2026-11-24 Fault interfaces", "2026-11-24 One fault per kind and target, with fixed parameter ranges"; phases/25/CONTEXT.md
  - Size: ~400 lines in 4 files
  - Adjusts: none
  - [x] `internal/faults/fault.go`: the types, constants, `Request.Spec`, `InputError`, `ErrNotFound`, `ErrConflict`, `Effect`, `TargetLookup`, `Options` of "Interfaces".
  - [x] `internal/faults/kinds.go`: validation per kind of target and params with the ranges of the fault object table (`traffic`, `loss_percent`, `delay_ms`, `jitter_ms`, `code` via `api.HasStatus` (added here in `internal/api/errors.go`), `percent`, `only`, `resource` against `Options.Resources`, `per_hour`, `iops`), duration 1 s to 24 h, from != to; seed only for `api_error` and `network` with `loss_percent` below 100 (otherwise `*InputError` on `seed`); kinds without effect refused as not available yet; numbers in params normalized to int64.
  - [x] `internal/faults/manager.go`: `Manager` with mutex, IDs from 1, `Create` (validate, lookup, conflict, chosen seed in 1..MaxChosenSeed from crypto/rand, Apply, timer on `Clock.After` with a stop channel), `List`, `Count`, `End`, `EndProject`, `EndAll`, `EndTarget` (Undo before removal; Undo errors joined and logged, the fault still ends).

- [x] 25-02 API fault layer: api_error, rate limits and headers
  - Wave: 2 | Depends on: 25-01 | Review: alone
  - Requirements: FAULT-03, FAULT-04 | Tests: internal/faults/middleware_test.go, internal/faults/ratelimit_test.go
  - Reads: REQUIREMENTS FAULT-03, FAULT-04, API-10; ARCHITECTURE "api", "faults (phase 25)" the `api_error`/`rate_limit` paragraph; research/fault-injection.md section 1; DECISIONS "2026-11-24 RateLimit headers on every API response", "2026-11-24 Fault interfaces"; phases/25/CONTEXT.md
  - Size: ~330 lines in 4 files
  - Adjusts: none (`apitest.New` keeps its signature)
  - [x] `internal/api/router.go`, `internal/api/context.go`: `Deps.Faults` applied after `authenticate` and before `checkJSON`; the `ByToken` mark set only on the token path; default `RateLimit-*` headers set in `underPrefix` for every request under a prefix; `Router.Resources()` from the mounted modules' `Type()`.
  - [x] `internal/faults/middleware.go`: for `api.ByToken` requests of a project: the bucket (the `rate_limit` fault's: capacity N, refill N/3600 per second, full at creation; otherwise the default 3600, refill 1/s, never refusing, floor 0) with lazy refill from `Clock.Now()`; a refused request answers `429 rate_limit_exceeded` and does not count; then `api_error` with filters `only` (GET/HEAD are reads) and `resource` (first segment after `/v1` or `/hetzner/v1`), one `IntN(100) < percent` draw per matching request on the fault's `rand.New(rand.NewPCG(seed, seed))` under the lock; a hit answers `api.Error(code, "<code> injected by HLab fault <id>", details)` with `details.fields: []` for `invalid_input`; `RateLimit-Limit`, `RateLimit-Remaining` (floor of tokens) and `RateLimit-Reset` (Unix seconds, now + ceil of the time until full) on every answer. A `rate_limit` fault's bucket is dropped when it ends; the default bucket is kept per project.
  - [x] `internal/apitest/apitest.go`: `Options` and `NewWith` passing `Faults` into `api.Deps`.

- [x] 25-03 Management API for faults
  - Wave: 2 | Depends on: 25-01 | Review: alone
  - Requirements: FAULT-01, FAULT-02 | Tests: internal/mgmt/faults_test.go, internal/mgmt/mgmt_test.go (registration)
  - Reads: REQUIREMENTS FAULT-01, FAULT-02; ARCHITECTURE "Management API (named pipe)" (table, "Fault object", errors, `mgmt.Deps`); DECISIONS "2026-11-24 Fault interfaces"; phases/25/CONTEXT.md
  - Size: ~220 lines in 2 files
  - Adjusts: none
  - [x] `internal/mgmt/faults.go`: handlers for `GET /faults`, `DELETE /faults`, `GET|POST|DELETE /projects/{name}/faults`, `DELETE /projects/{name}/faults/{id}`; unknown project `404 not_found`; body decoding with unknown fields refused; `*faults.InputError` to `400 invalid_input` (field in the message), `ErrNotFound` to `404 not_found`, `ErrConflict` to `409 conflict`, others `500 server_error`.
  - [x] `internal/mgmt/mgmt.go`: `Deps.Faults`, the routes (only when set), `Status.Faults` from `Count()`, and `EndProject` in `deleteProject` right after the token revocation.

- [x] 25-04 Faults in the cloud
  - Wave: 3 | Depends on: 25-02, 25-03 | Review: alone
  - Requirements: FAULT-01, FAULT-02, FAULT-03, FAULT-04 (wiring) | Tests: none of its own (glue; the rows of 25-01 to 25-03 cover the behaviour)
  - Reads: ARCHITECTURE "faults (phase 25)", "Cloud start and stop"; phases/25/CONTEXT.md "Technical choices"; `internal/cloud/cloud.go` `start`, `modules`, `serveManagement`, `teardown`
  - Size: ~140 lines in 6 files
  - Adjusts: none
  - [x] `internal/cloud/cloud.go`, `internal/cloud/faults.go`: a new `faults.Manager` at every start (before the router), with `Lookup` (server, volume and storage box in the project through the store, volume attached for `volume_unavailable`, locations from the catalog) and `Resources` from the router; `api.Deps.Faults = mgr.Middleware`; `mgmt.Deps.Faults`; `EndAll` in `teardown` before the API servers shut down, skipped at a host shutdown; the manager dropped after stop.
  - [x] `internal/resources/servers/servers.go`, `internal/resources/volumes/volumes.go`, `internal/resources/storageboxes/storageboxes.go`, `internal/resources/storageboxes/boxes.go`: an optional `Deps.EndFaults func(ctx context.Context, project, targetType string, id int64) error` (nil = none), called when a server, volume or storage box is deleted, before its resources go; set by the cloud to `mgr.EndTarget`.

- [x] 25-05 `hlab fault` commands
  - Wave: 3 | Depends on: 25-03 | Review: alone
  - Requirements: FAULT-12 | Tests: internal/cli/fault_test.go, internal/cli/cli_test.go (registration)
  - Reads: REQUIREMENTS FAULT-12; ARCHITECTURE "hlab (management command)" (command list, output, exit codes), "Management API (named pipe)" fault rows and "Fault object"; phases/25/CONTEXT.md; `internal/cli/cli.go` `parse`, `commands`, `cloudStatus`
  - Size: ~330 lines in 2 files
  - Adjusts: none
  - [x] `internal/cli/cli.go`: command entries for `fault create` (one argument `<kind>`, required `--project`, optional target, parameter, `--seed`, `--duration` flags), `fault list` (optional `--project`), `fault delete` (optional `<id>`, optional `--project`, bool `--all`); the parser learns optional positional arguments (a minimum and maximum count) without changing the existing commands' behaviour; `cloud status` prints `faults active: N` when N > 0.
  - [x] `internal/cli/fault.go`: `faultCreate` (flags to a `faults.Request`; non-integer values and a target given twice are usage errors with exit 2), `faultList` (table ID, PROJECT, KIND, TARGET, PARAMS, SEED, REMAINING; `--output json` the fault objects), `faultDelete` (`--project p <id>`, `--project p --all`, `--all`; any other combination exit 2).

- [x] 25-06 MCP fault tools
  - Wave: 4 | Depends on: 25-05 | Review: alone
  - Requirements: MCP-06 | Tests: internal/cli/mcp_fault_test.go, internal/cli/mcp_test.go (registration and `toolArgs`)
  - Reads: REQUIREMENTS MCP-01, MCP-02, MCP-06; ARCHITECTURE "hlab (management command)" the `mcp` paragraph; phases/25/RESEARCH.md "MCP go-sdk"; DECISIONS "2026-11-22 Default of logs lines applied by the handler", "2026-11-22 MCP tools listed by name"; phases/25/CONTEXT.md
  - Size: ~200 lines in 2 files
  - Adjusts: none (the tool-list loops of MCP-01, MCP-02 and MCP-04 take the new tools through `cli.MCPTools()`; their `toolArgs` entries come with the test map)
  - [x] `internal/cli/mcp_fault.go`: argument structs (`target *faultTarget` omitempty, `params map[string]any` omitempty, `seed *uint64`, `duration` omitempty; `fault_delete` with `project` omitempty, `id *int64`, `all bool`), hand-written input schemas with `maximum: 9007199254740991` on `seed` and no `default`, handlers running the 25-05 command functions with JSON output through `s.command`; `fault_delete` with neither or both of `id` and `all` is a tool error.
  - [x] `internal/cli/mcp.go`: the three tools in `mcpTools`, in name order.

- [x] 25-07 Documentation
  - Wave: 5 | Depends on: 25-01, 25-02, 25-03, 25-04, 25-05, 25-06 | Review: alone
  - Requirements: DOC-01, DOC-03, DOC-04, DOC-07 | Tests: test/docs/docs_test.go (existing DOC-01, DOC-03, DOC-04)
  - Reads: SPEC "Documentation" (D1, D2, D4, D6) and "Accounts and secrets" (hlab-docs context); REQUIREMENTS DOC-01 to DOC-07; phases/25/CONTEXT.md; phases/25/RESEARCH.md "Hetzner client libraries"; decisions/fault-injection.md
  - Size: ~250 lines in 4 files
  - Adjusts: none
  - [x] `docs/commands.md`: `hlab fault create|list|delete` with every flag and an example; the MCP tools `fault_create`, `fault_list`, `fault_delete` with their arguments and the seed limit of 2^53-1; `hlab cloud status` shows active faults.
  - [x] `docs/guide.md`: section "Fault injection" (what a fault is, lifetime, seeds, that the tray console is never affected), with examples for `api_error` (`service_error`, and `conflict` that clients retry) and `rate_limit` (`hcloud server list`, headers), which client retries which codes (RESEARCH.md); later kinds named as coming.
  - [x] `docs/deviations.md`: hlab never limits requests without a `rate_limit` fault; `docs/development/api-coverage.md` and `docs/development/components.md`: the `RateLimit-*` headers on every response, the management API's fault routes, the package `internal/faults`.

## Close

- Unit suite (`task test`) and linter (`task lint`) over the whole project: green on b9644e9.
- End-to-end: no row of this phase is end-to-end; as a smoke check of the changed API chain, cloud stop and delete paths ran `task e2e -- -v -run 'TestE2E/(LIFE-01|SRV-01|VOL-02|SBOX-03)$'`, green on b9644e9.
- Verifier: no blockers, no decisions, no backlog.
