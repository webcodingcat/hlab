# Developing HLab

This file explains how the code is laid out, which rules hold it together and how to
work on it. CLAUDE.md lists the rules that are easy to break, [STYLE.md](STYLE.md) the
coding standards, [docs/roadmap.md](docs/roadmap.md) what is done and what comes next.

## Binaries

One Go module, `github.com/webcodingcat/hlab`, built without cgo, so that WSL2 builds
the Windows binaries too.

| Binary | Runs on | Package | Job |
|---|---|---|---|
| `hlabd` | Windows; Linux later | `cmd/hlabd` | the service: state, the APIs, lab control, web UI, platform |
| `hlab` | Linux (also WSL2), Windows | `cmd/hlab` | the user's command line, without admin rights |
| `hlab-agent` | Linux, inside the appliance only | `cmd/hlab-agent` | puts the data plane's pictures in place, metadata service, counters |

At most one `hlabd` runs per machine.

## Layers

Everything except `cmd/` lives under `internal/`. The table runs from the user (top)
to the system (bottom). Packages that do not exist yet are listed, so that their rule
holds from their first line. `internal/layers_test.go` holds the same table and fails
on every import of this module that the column "may import" does not allow, for Linux
and for Windows files, and on a package that is missing from it.

| Package | Job | May import |
|---|---|---|
| `cmd/hlabd`, `cmd/hlab`, `cmd/hlab-agent` | wiring only: flags, configuration, putting the parts together | anything |
| `internal/tray` (Windows only) | tray icon and menu | `lab` |
| `internal/cli` | the CLI's commands | `client`, `config` |
| `internal/client` | Go client of the lab control: finding the token, TCP, upload, download | `labapi` |
| `internal/api/cloud` | Cloud API: one file per resource, action tables | `lab`, `web`, `state`, `catalog`, `schema` |
| `internal/api/hetzner` | Hetzner API (Storage Boxes) | as `api/cloud` |
| `internal/api/control` | lab control `/_lab/...` | `lab`, `web`, `state`, `labapi` |
| `internal/labapi` | request and answer types of the lab control, shared with `client` | – |
| `internal/webui` | web UI: files through `go:embed`, Host and Origin checks | `web` |
| `internal/web` | HTTP basics: routes (`net/http`), Hetzner's error format, pagination, sorting, label selectors, rate limit headers, token check, `X-Correlation-Id` | `state` |
| `internal/lab` | the core: owner of the state, transactions, actions, flags, timings, triggering pictures, life cycle | `state`, `picture`, `system`, `catalog`, `config` |
| `internal/state` | records, kinds, loading, saving, migrations | – |
| `internal/picture` | pure functions from the state to the data plane's pictures and the host's DNS rules | `state`, `agentproto`, `catalog` |
| `internal/seed` | pure functions for the cloud-init data | – |
| `internal/system` | does the work and never sees the state: VMs, disks, images, host link, host DNS, appliance, agent client | `platform`, `agentproto`, `vhdx`, `imagefmt` |
| `internal/platform` | the platform interfaces and their data types | – |
| `internal/platform/windows` | Hyper-V through `pshost`, host network, NRPT, life cycle | `platform`, `pshost` |
| `internal/platform/windows/pshost` | the long-running PowerShell process: JSON over stdin and stdout | – |
| `internal/platform/linux` | the Linux platform (later) | `platform` |
| `internal/platform/fake` | fakes for unit tests and `--platform fake` | `platform` |
| `internal/platform/contract` | tests only: the contract tests of the platform interfaces, run against the fake and the real platforms | `platform` |
| `internal/agentproto` | the wire format between service and agent, picture types | – |
| `internal/agent` | the agent: puts pictures in place, announces itself, metadata service, counters | `agentproto` |
| `internal/vhdx` | the page 83 disk ID in a VHDX's metadata | – |
| `internal/imagefmt` | detecting image formats, unpacking | – |
| `internal/catalog` | server types, locations, load balancer and box types, prices, ISOs, system images; built in, a user file overrides | – |
| `internal/config` | `hlab.yaml`: ranges, time scale, overcommit, deadlines, ports | – |
| `internal/schema` | models generated from Hetzner's spec where `hcloud-go/schema` has gaps | – |
| `internal/spectest` | tests only: checking answers against Hetzner's spec | – |

What the layers mean:

- **`api/*` decides:** it checks requests, changes the state and starts actions. It
  reaches the system only through `lab`, never through `platform`.
- **`system` does the work and never sees the state.** It gets IDs, values and
  pictures.
- **`picture` and `seed` are pure functions:** the same state gives the same output,
  comparable as text (golden files).
- **`agent` decides nothing:** it puts pictures in place as a whole and reports states.

The layer test checks production code only: tests may import fakes and helpers.

## The state

`internal/state` holds the records; `internal/lab` owns them.

- **Kinds.** Each kind (`state.Projects`, `state.Tokens`, `state.SSHKeys`,
  `state.Actions`) is a `state.Kind[R]` with its record type `R` and its name in the
  file. The lab's own records, which the API does not show, are `state.Lab`
  (`tx.Lab()`). Records hold plain data: no interfaces, no unexported fields.
- **Projects.** A record that belongs to a project has a `RecordProject` method
  (`state.Owned`); `state.DeleteProject` removes the project, every such record of
  every kind and the project's faults, so that a new kind goes with its project
  without anyone remembering to.
- **IDs.** One counter per kind, global over all projects as on Hetzner, starting at
  `state.FirstID` (1 000 000) so that no record shares an ID with a catalog entry,
  which carries Hetzner's real ID. An ID is never reused and never changes.
- **Transactions.** A `state.Tx` copies each kind deeply on first use (`state.Get`,
  `All`, `Put`, `Delete`, `NewID`); records from a Tx are its copies. Only
  `tx.Result()` makes the changes part of a state, so a Tx that is dropped changes
  nothing. Copying a kind of a thousand actions takes about a millisecond
  (`BenchmarkTxOnAThousandActions`).
- **The file.** `state.json` in the data folder: `version`, `saved`, `next_ids`,
  `kinds` (each a list sorted by ID, so that diffs stay readable) and `lab`;
  `snake_case` names and RFC 3339 times in UTC. `state.Writer` writes it in the
  background, only the newest content: `state.json.tmp`, fsync, then over
  `state.json` (on Windows `MoveFileExW` with `MOVEFILE_WRITE_THROUGH`, which
  `os.Rename` leaves out). A file cut off halfway is never `state.json`. A failed
  write is logged and tried again on its own, after 1 s and then twice as long each
  time, up to 30 s (a virus scanner may hold the file for a moment); `Flush`
  reports the error of the last try.
- **Limits.** A project's `Limits` field of zero means no limit, so that a limit
  added later needs no migration.
- **Versions.** The file has a version (`currentVersion` in `file.go`). An older file
  is migrated on load, one version at a time (`migrations[n]` lifts `n` to `n+1` on
  the raw JSON), and stays as `state.json.v<n>`. A newer file, or a kind this code
  does not know, stops the start: saving would lose data.
- **Not in the state:** disks, images, metrics, logs (their own files); action flags
  (memory only); tokens in clear text (only their hash).

## Concurrency

- A mutex in `lab` guards the state, the flags and the running actions. Only code
  that holds it reads or changes records. It is never held across a call into
  `system`.
- Every change runs in `lab.Change(func(tx *state.Tx) error)`: if the function returns
  an error, the old state stays; otherwise the new one is saved. Reads run in
  `lab.View`, which keeps nothing.
- **Across a call into the system, code keeps IDs, not records**, and looks them up
  again afterwards: `lab.Current(l, kind, id)` (a function: Go methods take no type
  parameters) returns a copy, or `state.ErrNotFound` when the record is gone.
- The slow part of an action runs as a goroutine with a `context.Context`, which
  closing the lab (`Lab.Close`) and resetting a project (`Lab.StopProjectActions`)
  cancel.
- `lab.Flush()` waits until the newest state is on disk; `Lab.Close` flushes.
- `go test -race` runs on Linux, in `bin/dev test` and in CI.

## Actions

The pattern Hetzner's API shows, as the lab runs it (`internal/lab/actions.go`):

- **Tables.** Each resource has a `lab.ActionTable`: its type as actions name it
  (`server`), the 423 message, and one `lab.ActionSpec` per Hetzner command
  (`poweroff`) with Hetzner's action name (`stop_server`).
- **`Lab.Act`** runs one row in one transaction: it refuses a busy resource
  (`lab.LockedError`, which the API answers with 423 `locked`), calls the row's
  `Plan` (it checks the request and makes the quick changes), creates the action and,
  without steps, finishes it at once. If anything fails, nothing changes.
- **Flags.** While an action's steps run, it flags its resource and the plan's
  `Locks`. Flags live in memory only: a restart has no running actions. One flag per
  resource; an `Unflagged` row (such as `change_protection`) neither takes nor checks
  it. `locked` in Hetzner's server JSON is an administrative lock, not a flag.
- **Steps.** The slow part runs in a goroutine and waits Hetzner's timings with
  `Lab.Sleep(ctx, lab.PowerOff)`, scaled by the time scale (0 in the unit tests;
  tests that check timings use their own `lab.Clock`). When the steps fail, the
  plan's `Undo` takes back the quick changes and the action ends with `error`; when
  they are cancelled, nothing is undone (a reset removes everything, a stopping lab
  keeps its state).
- **Restart.** Actions that were running when HLab stopped end with an error when it
  starts again.
- **Size.** Each project keeps its newest `lab.MaxFinishedActions` (1 000) finished
  actions.

## Projects, tokens, faults

The lab's own requests (`internal/lab/projects.go`, `tokens.go`, `faults.go`); the
lab control (`/_lab/...`) and the CLI call them. They report `lab.ErrInvalid`,
`lab.ErrExists` and `state.ErrNotFound`.

- **Projects** have a name that is a DNS label (it appears in URLs of the lab control
  and in log file names), limits (zero is no limit, negative is refused) and
  Internet access, on by default. `Lab.DeleteProject` removes the records first, so
  that no new action can start (its tokens are gone), then cancels the running
  actions, which find their actions gone.
- **Tokens** are 64 letters and digits, like Hetzner's, with `read` or `read_write`;
  names are unique per project so that the CLI can name one. `Lab.CreateToken`
  returns the token once; the state keeps its SHA-256 (`state.HashToken`) and its
  first four characters to tell tokens apart.
- **API faults:** `Lab.SetAPIFault` makes a project's next n API calls fail with a
  status, each after a delay; `Lab.TakeAPIFault` counts a call against it. The
  counter is in the state (`state.Lab.Faults`), so a restart keeps it; a call that
  finds no fault saves nothing.

## The APIs

`internal/web` holds what both of Hetzner's APIs share; `internal/api/cloud` (and
later `api/hetzner`) registers its handlers on a `web.API`, one file per resource.

- **Order of the checks**, as measured on Hetzner's API (`compat/requirements.md`):
  1. the route (`API.ServeHTTP`): a path no route takes is 404 `not_found` "api
     route not found", before the token; so is a wildcard named `id` or `*_id`
     whose value is no ID;
  2. the bearer token (`API.admit`): 401 `unauthorized`, without details;
  3. from here on every answer has an `X-Correlation-Id`;
  4. the rate limit (per project, `RateLimit-*` headers, 429); the actions endpoints
     (`/v1/actions…`) have a budget of their own, twice the size;
  5. the permission: a `read` token gets 403 `forbidden` for anything but GET and
     HEAD;
  6. an injected fault;
  7. a known path with another method is 405 `method_not_allowed` with `Allow`;
     else the handler runs.
- **Handlers** are `web.HandlerFunc`: they get a `*web.Request` (the request, the
  `Caller` with project and permission, and the answer's `Header`) and return a
  status and a body, which the API writes as JSON, or an error. A `*web.Error` is
  answered as it is, with Hetzner's code; any other error is a failure of the lab,
  logged and answered as 500 `server_error`. Errors have the shape
  `{"error": {"code", "message", "details"}}`. Hetzner's `details` differ by error
  (none, `null` or `{}`); the constructors in `internal/web/errors.go` send what
  Hetzner sends, so handlers use them rather than building an `Error`.
- **The web package reaches the lab** only through `web.Backend` (`TokenByHash`,
  `TakeAPIFault`), which `*lab.Lab` implements: `web` may import only `state`.
- **Lists:** `web.Paginate` (`page`, `per_page`, `meta.pagination` with `null` for
  pages that are not there, and the `Link` header, which Hetzner announced to drop
  but still sends), `web.Sort` with the fields a resource allows (`web.SortKeys`),
  `web.ParseSelector` for `label_selector`.
- **Request bodies** are decoded into the request types of `schema`
  (`schema.SSHKeyCreateRequest`) with `web.DecodeJSON`: a body that is no JSON object
  is 400 `json_error`, a field of the wrong type 422 `invalid_input` for that field,
  unknown fields are ignored as Hetzner ignores them. The handler then checks its
  fields with a `web.Check` (`Required`, `Labels` with Hetzner's label rules, `Fail`)
  and returns `check.Err()`: one `invalid_input` that lists every field, its message
  naming the first ("invalid input in field 'name': is required"), as in Hetzner's
  documentation. A value another resource of the project has already is
  `web.Uniqueness` (409 `uniqueness_error`). The request schemas of the spec are not
  used at run time: the spec check is for tests only.
- **JSON types** come from `hcloud-go/v2/hcloud/schema`, the types the CLI and the
  provider decode with; HLab has its own where `schema` cannot say what the real API
  sends (`web.Pagination`, whose pages may be `null`; the pricing, where `schema`
  still has `traffic` and a `datacenter` per Primary IP price).
- **Datacenters** are gone from Hetzner's API: their endpoints answer 410
  `deprecated_api_endpoint`, and no answer names a datacenter.

## The catalog

`internal/catalog` holds what Hetzner offers: locations, server, load balancer and
Storage Box types, prices, system images and ISOs, each with Hetzner's real ID and
name, because the tools check them (`compat/requirements.md`).

- **Built in and changed by the user.** The built-in catalog is
  `internal/catalog/catalog.yaml`, compiled in with `go:embed`. `catalog.Load`
  reads a `catalog.yaml` in the data folder over it: each key the user's file names
  replaces the built-in value, a list as a whole. Unknown keys, doubled IDs or
  names, values the API does not allow and unknown locations stop the start, naming
  the file.
- **Hetzner's shape.** The keys are those of Hetzner's JSON, so that answers of the
  real API become catalog entries (Recipes). The differences: a server type's
  locations name the location only (the ID comes from `locations`); an ISO without
  `architecture` fits every architecture; images are system images, always
  `available`, without labels.
- **Only what the lab runs:** x86 server types (Hyper-V on an x86 host runs no ARM
  guests), and each system image only in its newest version (Debian 13, Ubuntu
  26.04).
- **The lab owns it.** `lab.Options.Catalog` takes a loaded catalog (nil is the
  built-in one); `Lab.Catalog()` returns it. It does not change while the lab runs,
  so reading it needs no lock. Lists are sorted by ID, as Hetzner lists them.

## Spec check and compat/

`compat/` holds what HLab must be compatible with:

| File | What |
|---|---|
| `versions.yaml` | the tools and their versions; the specs and when they were fetched |
| `specs/cloud.spec.json`, `specs/hetzner.spec.json` | Hetzner's OpenAPI specs |
| `spec-deviations.yaml` | where the real API differs from the spec, with the reason |
| `requirements.md` | what the tools need, from their source code, each with its phase and test |
| `exceptions.yaml` | what never works against HLab, with the reason |

- **Every answer of every API test is checked** against the spec:
  `spectest.New(t, api, spectest.Cloud)` returns a client that sends requests to the
  handler in the test's process and fails the test on an answer the spec does not
  allow (status, content type or JSON body; errors of paths the spec lacks must
  still have Hetzner's error format). It uses `kin-openapi` with JSON Schema
  2020-12, as the specs are OpenAPI 3.1.
- **Deviations** change the spec before the check: each replaces the schema at a JSON
  pointer (`*` matches any key) with `{}` or a given value, or adds a key the spec
  lacks (a media type such as `text/plain`), and takes a replaced property out of
  `required`. A pointer that matches nothing fails the tests.
- **Hetzner before the spec:** where the real API and the spec differ, HLab follows
  the API and records a deviation; what the tools need is in `requirements.md`.
- `bin/dev spec-diff` compares the specs with Hetzner's current ones; CI runs it
  weekly (`.github/workflows/spec-diff.yml`).

## Platform

`internal/platform` holds four interfaces: `Hypervisor`, `HostNet`, `HostDNS` and
`Lifecycle`. They take plain values and report errors that callers tell apart with
`errors.Is` (`platform.ErrNotFound`, `ErrExists`, `ErrRunning`, …).

- `internal/platform/contract` says how every platform behaves; the unit tests run it
  against `internal/platform/fake`, the live tests against the real platform.
- The fake keeps everything in memory. Tests make calls fail (`FailNext`) or wait
  (`Delay`) and read the calls it got (`Calls`).
- `internal/platform/windows` builds for Windows only; for now each method reports
  `platform.ErrNotImplemented`.
- `lab` reaches the platform only through `system`, which never sees the state.

## Dependencies

As few as possible, each with its reason here.

| Module | Why |
|---|---|
| `golang.org/x/tools` (only `go/packages`) | the layer test reads the imports of every package, for Linux and Windows |
| `github.com/hetznercloud/hcloud-go/v2` (only `hcloud/schema`) | the JSON types of the APIs, as the CLI and the provider decode them |
| `github.com/getkin/kin-openapi` (only `openapi3`, in `spectest`) | checks the answers against Hetzner's OpenAPI 3.1 specs |
| `go.yaml.in/yaml/v4` | reads the catalog and the YAML files of `compat/`; `kin-openapi` needs it anyway |

Tools, in the separate module `tools/go.mod` so their dependencies stay out of
`go.mod` (run with `go tool -modfile=tools/go.mod <tool>`):

| Tool | Why |
|---|---|
| `github.com/golangci/golangci-lint/v2` | the linters of STYLE.md, staticcheck among them |
| `mvdan.cc/sh/v3` (`shfmt`) | formats the shell scripts |

No HTTP router (the patterns of `net/http` suffice), no ORM, no database.

## Tools

`bin/dev` (Bash, for Linux and WSL2) and `bin/dev.ps1` (PowerShell, for Windows) are
the only entry points; CI runs them as a developer does.

What a developer installs:

| What | Where | Why |
|---|---|---|
| Go | `go.mod` names the version; with `GOTOOLCHAIN=auto` (the default) any newer Go fetches it | building, testing |
| gcc | Linux, WSL2 (`apt install gcc`) | only for the race detector of `bin/dev test`; the binaries use no cgo |
| shellcheck, yamllint | Linux, WSL2 (`apt install shellcheck yamllint`) | `bin/dev lint` |
| PSScriptAnalyzer | Windows PowerShell or pwsh: `Install-Module PSScriptAnalyzer -Scope CurrentUser` | `bin/dev lint`, `bin/dev.ps1 lint` |

golangci-lint and shfmt need no install: `go tool` builds them from `tools/go.mod` on
first use. In WSL2, `bin/dev lint` runs PSScriptAnalyzer in pwsh if it is installed,
else in Windows PowerShell through WSL interop. `bin/dev.ps1 lint` leaves shellcheck
and yamllint out (they are Linux tools; CI's Linux job runs them).

**Windows tests from WSL2.** `bin/dev test --windows` compiles each package's tests
with `GOOS=windows go test -c` and runs the `.exe` through WSL interop in the
package's directory; exit code and output come back to WSL2. Windows needs no Go for
that. Tests that need the go command (the layer test) skip there; CI's Windows job
has Go and runs them.

**Builds.** `bin/dev build` builds with `CGO_ENABLED=0` and `-trimpath`, and sets
`main.version` from `git describe`. `hlabd --version` (and the others) print it.

## Recipes

**A new package**: add its row to the layer table above and to `allowedImports` in
`internal/layers_test.go`, with the same "may import" column, before its first import.

**A new dependency**: `go get` it, and add a row with the reason to the dependency
table. A developer tool goes into `tools/go.mod` (`go get -tool`, run there).

**A new kind**: add its record type with a `RecordID` method to
`internal/state/records.go` (JSON names in `snake_case`, timestamps as `time.Time`),
a `Kind` variable to `state.go` and the variable to `kinds`. A record that belongs
to a project gets a `RecordProject` method too, so that it goes with its project.
An older state file without the kind loads as before; no migration is needed.

**A new endpoint**: add its handler to the resource's file in `internal/api/cloud`
(a new resource: a new file, registered in `cloud.Register`) with
`api.Handle("GET /things/{id}", ...)`; the path is the spec's, without `/v1`, and
`web.PathID(req, "id")` reads an ID of it. Build the answer from `hcloud-go`'s
`schema` types; read a body with `web.DecodeJSON` and check it with a `web.Check`;
refuse with the `web` errors (`web.NotFound`, `web.Uniqueness`, …) and Hetzner's
codes and messages. A list filters, sorts (`web.Sort` with the spec's sort values)
and answers with `listJSON`, which paginates. Its tests send every request through a
`spectest` client, and a comment notes where a test checks Hetzner's behaviour;
an answer measured on the real API goes into `compat/requirements.md`.

**A new reading resource from the catalog** (such as `storage_box_types` in the
Hetzner API): the catalog has its entries already, or gets a list and a type in
`internal/catalog/catalog.go` with the keys of Hetzner's JSON, checks in `check.go`
(unique IDs and names, enums of the spec, known locations) and its entries in
`catalog.yaml`. The API gets a file with `register<Things>(api, l.Catalog())`, a
list with `byName` and `listJSON`, a single endpoint that answers `web.NotFound`,
and a `<thing>JSON` that builds the `schema` type. Add it to
`TestCatalogResourcesListAndGet`.

**Updating the catalog** (Hetzner changes prices, adds a type or a new image
version): with a read token of a real project, fetch the lists
(`curl -H "Authorization: Bearer $HCLOUD_TOKEN"
"https://api.hetzner.cloud/v1/server_types?per_page=50"`, likewise `locations`,
`load_balancer_types`, `isos`, `images?type=system&architecture=x86`, `pricing`) and
take the values into `internal/catalog/catalog.yaml`: the same keys; x86 types only;
a server type's locations by name, without `id`; the price lists of
`server_types` and `load_balancer_types` from the types themselves, not from
`pricing`; of the images only the newest version of each system. Storage Box types
come from `https://api.hetzner.com/v1/storage_box_types`. Run `bin/dev test`; the
catalog's check names what does not fit.

**A spec deviation** (the real API answers what the spec does not allow): add an
entry to `compat/spec-deviations.yaml` with the pointer into the spec, the value if
not `{}`, and the reason: how the real API behaves and how that was found out.

**A requirement** (a tool needs something of the API): add a row to
`compat/requirements.md` with the source (a file in the tool), the phase and the
unit test that checks it; something that never works goes into
`compat/exceptions.yaml` instead.

**A new version of a tool**: raise it in `compat/versions.yaml`; read the tool's
diff (`hcloud-go/schema`, tests, examples) for new requirements and exceptions; take
the specs over (below); run the smoke tests.

**New specs from Hetzner**: `bin/dev spec-diff --update` replaces `compat/specs`;
set the date in `compat/versions.yaml`, read `git diff compat/specs`, fix the
deviations that no longer match (the tests say which) and the API where the spec
shows a change of the real API.

**A new migration** (any change that an older state file does not load into as it
is: a renamed or moved field, a new meaning): raise `currentVersion` in
`internal/state/file.go`, add `migrations[old]` that lifts the raw JSON one version,
and a test that loads a sample file of the old version from `testdata/`.

**A new action**: add a row to its resource's `lab.ActionTable` with Hetzner's
command and name. `Plan` checks the request and makes the quick changes; `Steps`
does the slow part through `l.System()`, keeps IDs across those calls and waits with
`l.Sleep` (a timing from `internal/lab/clock.go`, measured on Hetzner); `Undo` takes
back the quick changes. Its tests use the fake platform and, for timings, a test
clock.

**A new platform method**: add it to the interface in `internal/platform`, to the
contract tests, to the fake and to every platform (`ErrNotImplemented` until it is
there).

**A command for later phases**: `bin/dev` and `bin/dev.ps1` name every command in
their usage text; a command that is not there yet says "not yet implemented".
