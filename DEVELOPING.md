# Developing HLab

This file explains how the code is laid out, which rules hold it together and how to
work on it. Other files cover the rest:

- CLAUDE.md lists the rules that are easy to break.
- [STYLE.md](STYLE.md) has the coding standards.
- [docs/roadmap.md](docs/roadmap.md) says what is done and what comes next.

Section names in parentheses, such as (The state), point to the section of this file
that explains more.

## A request's path

This is what happens when you run `hcloud ssh-key create` against the lab:

1. `internal/web` receives the request on port 8080. It finds the route and checks
   the token, the rate limit and the permission. It may also inject a fault
   (The APIs).
2. The handler in `internal/api/cloud/ssh_keys.go` checks the body and calls
   `lab.Change`. `lab.Change` runs the handler's function as one transaction on the
   state (The state, Concurrency).
3. `lab` encodes the new state and passes it to `state.Writer`, which saves
   `state.json` in the background. The handler answers with Hetzner's JSON.
4. Some commands take time, such as a server's `poweroff` (from phase 1b). They run
   as an action instead. `lab.Act` checks and plans the action. The action's steps
   call `internal/system`, which drives the machine through `internal/platform`
   (Actions, Platform).

Words this file uses:

| Word | Meaning |
|---|---|
| appliance | a small Linux VM (from phase 1b) that runs `hlab-agent`; it provides the lab's network, DNS, metadata service and more |
| data plane | what the appliance does for the guests: links, routes, firewall rules, NAT, DHCP, DNS |
| picture | the complete wanted configuration of one part of the data plane; `internal/picture` builds it from the state, and the agent always applies a whole picture, never a single change |
| host link | the network between the host and the appliance, `198.18.0.0/29`; the host is `.1`, the appliance `.2`; WSL2 reaches `hlabd` on `198.18.0.1` |
| flag | an in-memory mark on a resource that an action is working on (Actions) |

## Binaries

Everything is one Go module, `github.com/webcodingcat/hlab`. It is built without cgo,
so WSL2 can build the Windows binaries too.

| Binary | Runs on | Package | Job |
|---|---|---|---|
| `hlabd` | Windows; Linux later | `cmd/hlabd` | the service: state, the APIs, lab control, web UI, platform |
| `hlab` | Linux (also WSL2), Windows | `cmd/hlab` | the user's command line; needs no admin rights |
| `hlab-agent` | Linux, only inside the appliance | `cmd/hlab-agent` | applies the data plane's pictures, runs the metadata service, keeps counters |

At most one `hlabd` runs on a machine.

## Layers

Everything except `cmd/` lives under `internal/`. The table below goes from the user
at the top to the system at the bottom.

The table also lists packages that do not exist yet; their Job says "(later)". That
way their rule applies from their first line of code.

`internal/layers_test.go` reads this table. It fails when:

- a package imports a package of this module that its "May import" column does not
  list (it checks Linux and Windows files);
- a package is missing from the table.

The "May import" column names packages by their path below `internal/`. "anything"
means the row has no limit.

| Package | Job | May import |
|---|---|---|
| `cmd/hlabd`, `cmd/hlab`, `cmd/hlab-agent` | wiring only: flags, configuration, putting the parts together | anything |
| `internal/tray` (Windows only) | (later) tray icon and menu | `lab` |
| `internal/cli` | the CLI's commands | `client`, `config` |
| `internal/client` | Go client of the lab control: finds the token, TCP, upload, download | `labapi` |
| `internal/api/cloud` | Cloud API: one file per resource, action tables | `lab`, `web`, `state`, `catalog`, `schema` |
| `internal/api/hetzner` | (later) Hetzner API (Storage Boxes) | `lab`, `web`, `state`, `catalog`, `schema` |
| `internal/api/control` | lab control `/_lab/...` | `lab`, `web`, `state`, `labapi` |
| `internal/labapi` | request and answer types of the lab control, shared with `client` | – |
| `internal/webui` | web UI: files through `go:embed`, Host and Origin checks | `web` |
| `internal/web` | HTTP basics: routes (`net/http`), Hetzner's error format, pagination, sorting, label selectors, rate limit headers, token check, `X-Correlation-Id` | `state` |
| `internal/lab` | the core: owns the state; transactions, actions, flags, timings, triggering pictures, life cycle | `state`, `picture`, `system`, `catalog`, `config` |
| `internal/state` | records, kinds, loading, saving, migrations | – |
| `internal/picture` | (later) pure functions from the state to the data plane's pictures and the host's DNS rules | `state`, `agentproto`, `catalog` |
| `internal/seed` | (later) pure functions for the cloud-init data | – |
| `internal/system` | does the work and never sees the state: VMs, disks, images, host link, host DNS, appliance, agent client | `platform`, `agentproto`, `vhdx`, `imagefmt` |
| `internal/platform` | the platform interfaces and their data types | – |
| `internal/platform/windows` | Hyper-V through `pshost`, host network, NRPT, life cycle | `platform`, `platform/windows/pshost` |
| `internal/platform/windows/pshost` | (later) the long-running PowerShell process: JSON over stdin and stdout | – |
| `internal/platform/linux` | the Linux platform; for now only its life cycle | `platform` |
| `internal/platform/fake` | fakes for unit tests and `--platform fake` | `platform` |
| `internal/platform/contract` | tests only: the contract tests of the platform interfaces, run against the fake and the real platforms | `platform` |
| `internal/agentproto` | (later) the wire format between service and agent, picture types | – |
| `internal/agent` | (later) the agent: applies pictures, announces itself, metadata service, counters | `agentproto` |
| `internal/vhdx` | (later) the page 83 disk ID in a VHDX's metadata | – |
| `internal/imagefmt` | (later) detecting image formats, unpacking | – |
| `internal/catalog` | server types, locations, load balancer and box types, prices, ISOs, system images; built in, a user file overrides | – |
| `internal/config` | `hlab.yaml`: ranges, time scale, overcommit, deadlines, ports | – |
| `internal/schema` | (later) models generated from Hetzner's spec where `hcloud-go/schema` has gaps | – |
| `internal/spectest` | tests only: checking answers against Hetzner's spec | – |

What the layers mean:

- **`api/*` decides.** It checks requests, changes the state and starts actions. It
  reaches the system only through `lab`, never through `platform`.
- **`system` does the work and never sees the state.** It gets IDs, values and
  pictures.
- **`picture` and `seed` are pure functions.** The same state always gives the same
  output, and the output can be compared as text (golden files).
- **`agent` decides nothing.** It applies whole pictures and reports states.

The layer test checks production code only. Tests may import fakes and helpers.

## The state

`internal/state` holds the records. `internal/lab` owns them.

- **Kinds.** Each kind of record (`state.Projects`, `state.Tokens`, `state.SSHKeys`,
  `state.Actions`) is a `state.Kind[R]`. It knows its record type `R` and its name in
  the file. The lab's own records, which the API does not show, are in `state.Lab`
  (read them with `tx.Lab()`). Records hold plain data: no interfaces and no
  unexported fields.
- **Projects.** A record that belongs to a project has a `RecordProject` method
  (interface `state.Owned`). `state.DeleteProject` removes the project, its records
  of every kind and its faults. So a new kind is deleted with its project, and
  nobody has to remember to add it.
- **IDs.** Each kind has one counter, shared by all projects, as on Hetzner. It
  starts at `state.FirstID` (1 000 000). Catalog entries carry Hetzner's real IDs,
  which are lower, so no record shares an ID with a catalog entry. An ID is never
  reused and never changes.
- **Transactions.** A `state.Tx` makes a deep copy of a kind the first time it uses
  it (`state.Get`, `All`, `Put`, `Delete`, `NewID`). The records you get from a Tx
  are its copies, so changing one in place changes the Tx. The changes become part
  of a state only through `tx.Result()`; a Tx that is dropped changes nothing.
  Copying a kind with a thousand actions takes about a millisecond
  (`BenchmarkTxOnAThousandActions`).
- **Reads copy nothing.** `state.Read` starts a read-only Tx. Its records are the
  state's own, and calling Put, Delete or NewID on it panics. `lab.View` uses it, so
  the token check on every request copies nothing. That check uses `state.Find`,
  which does not sort as `state.All` does.
- **Only a real change is saved.** Because records can change in place,
  `tx.Changed()` compares each kind the Tx copied with the state the Tx started from
  (`reflect.DeepEqual`). A `Change` that only read, or set a value to what it
  already was, saves nothing. A real change encodes the whole state while holding
  the mutex and passes it to the writer. If that ever shows up in a profile, encode
  only the kinds that changed.
- **The file.** The state is `state.json` in the data folder. Its keys are
  `version`, `saved`, `next_ids`, `kinds` and `lab`. Each kind is a list sorted by
  ID, so diffs stay readable. Names are `snake_case`, times are RFC 3339 in UTC.
- **The writer.** `state.Writer` writes the file in the background:
  - It writes only the newest content it was given. Content is written once, even
    if a second wake-up follows; on Windows, replacing a file while someone reads it
    fails with "access denied".
  - It writes `state.json.tmp`, calls fsync and then moves the file over
    `state.json`. On Windows it uses `MoveFileExW` with `MOVEFILE_WRITE_THROUGH`,
    which `os.Rename` does not set. So `state.json` is never a half-written file.
  - When a write fails, it logs the error and tries again by itself: after 1 s, then
    each time twice as long, up to 30 s. A virus scanner or a reader may hold the
    file for a moment. `Flush` reports the error of the last try.
  - After `Lab.Close`, a change (from a request that outlived the stop) is logged
    and dropped.
- **Limits.** A project's `Limits` field set to zero means "no limit". So a limit
  added later needs no migration.
- **Versions.** The file has a version (`currentVersion` in `file.go`). An older
  file is migrated when it is loaded, one version at a time: `migrations[n]` lifts
  version `n` to `n+1` on the raw JSON. The old file is kept as `state.json.v<n>`.
  A newer file, or a kind this code does not know, stops the start, because saving
  would lose data.
- **Not in the state:** disks, images, metrics and logs (they have their own files);
  action flags (memory only); tokens in clear text (only their hash is kept).

## Concurrency

- A mutex in `lab` guards the state, the flags and the running actions. Only code
  that holds the mutex takes records from the state. The mutex is never held during
  a call into `system`.
- Every change runs in `lab.Change(func(tx *state.Tx) error)`. If the function
  returns an error, the old state stays; otherwise the new state is saved. Reads run
  in `lab.View`, which changes nothing.
- **A record may outlive its Tx, but only to be read.** A handler may build its
  answer from a record after `Change` or `View` returned, without the mutex. It must
  never change that record: from `View` it is the state's own record, and from
  `Change` the state shares it.
- **Across a call into the system, keep IDs, not records.** Look the records up
  again afterwards with `lab.Current(l, kind, id)`. It returns the record to read,
  or `state.ErrNotFound` if it is gone. It is a function, not a method, because Go
  methods cannot have type parameters.
- The slow part of an action runs in a goroutine with a `context.Context`. Closing
  the lab (`Lab.Close`) and resetting a project (`Lab.StopProjectActions`) cancel
  that context.
- `lab.Flush()` waits until the newest state is on disk. `Lab.Close` calls it.
- `go test -race` runs on Linux, in `bin/dev test` and in CI.

## Actions

Hetzner's API runs slow commands as actions. This is how the lab does it
(`internal/lab/actions.go`):

- **Tables.** Each resource has a `lab.ActionTable`. It holds the resource's type as
  actions name it (`server`), the message for 423 answers, and one `lab.ActionSpec`
  per Hetzner command. A spec maps the command (`poweroff`) to Hetzner's action name
  (`stop_server`).
- **`Lab.Act`** runs one row of a table in one transaction:
  1. If the resource is busy, it refuses with `lab.LockedError`; the API answers
     that with 423 `locked`.
  2. It calls the row's `Plan`, which checks the request and makes the quick
     changes.
  3. It creates the action. An action without steps is finished at once.

  If any of this fails, nothing changes.
- **Flags.** While an action's steps run, the action flags its resource and the
  resources in the plan's `Locks`. Flags live in memory only, because after a
  restart no action is running. A resource has at most one flag. A row marked
  `Unflagged` (such as `change_protection`) neither sets nor checks flags. Note that
  `locked` in Hetzner's server JSON is an administrative lock, not a flag.
- **Steps.** The slow part runs in a goroutine. It waits Hetzner's timings with
  `Lab.Sleep(ctx, lab.PowerOff)`, scaled by the time scale. The time scale is 0 in
  unit tests; tests that check timings use their own `lab.Clock`.
  - If the steps fail, the plan's `Undo` takes back the quick changes and the
    action ends with `error`.
  - If the steps are cancelled, nothing is undone: a reset removes everything
    anyway, and a stopping lab keeps its state.
- **Restart.** Actions that were running when HLab stopped end with an error when it
  starts again.
- **Size.** Each project keeps its newest `lab.MaxFinishedActions` (1 000) finished
  actions.

## Projects, tokens, faults

These are the lab's own operations (`internal/lab/projects.go`, `tokens.go`,
`faults.go`). The lab control (`/_lab/...`) and the CLI call them. They report
errors as `lab.ErrInvalid`, `lab.ErrExists` and `state.ErrNotFound`.

- **Projects** have a name, limits and Internet access.
  - The name must be a DNS label, because it appears in URLs of the lab control and
    in log file names.
  - A limit of zero means no limit; a negative limit is refused.
  - Internet access is on by default.
  - `Lab.DeleteProject` first removes the records. Its tokens are gone then, so no
    new request is admitted. Then it cancels the running actions, which find their
    actions gone.
  - A request admitted just before the delete may still try to create something.
    So every create calls `lab.CheckProject` inside its `Change` (`Lab.Act` does
    so itself); the API answers `lab.ErrProjectGone` as a deleted token
    (`web.TokenInvalid`).
- **Tokens** are 64 letters and digits, like Hetzner's, with the permission `read`
  or `read_write`. Names are unique per project, so the CLI can refer to a token by
  name. `Lab.CreateToken` returns the token only once. The state keeps its SHA-256
  (`state.HashToken`) and its first four characters, to tell tokens apart.
- **The env token.** `Lab.EnvToken` returns a project's `read_write` token named
  `hlab-env`. It returns the same token on every call and after a restart, so
  `hlab env` gives every shell the same token, in WSL2 and Windows alike.
  - The state cannot show a token a second time, so the clear text is kept in
    `env-tokens.json` in the data folder (`lab.EnvTokenFile`).
  - That file is written like the control token and readable by the same users.
    This adds no risk: whoever can read the control token can create tokens anyway.
  - If the token is gone (deleted, or the file was lost), a new one is made. The
    file keeps only projects that still exist.
- **API faults.** `Lab.SetAPIFault` makes a project's next n API calls fail with a
  given status, each after a delay. `Lab.TakeAPIFault` counts one call against the
  fault. The counter is in the state (`state.Lab.Faults`), so it survives a restart.
  A call that finds no fault saves nothing.

## The APIs

`internal/web` holds what both of Hetzner's APIs have in common. `internal/api/cloud`
registers its handlers on a `web.API`, one file per resource; `api/hetzner` will do
the same later.

- **The order of the checks.** Hetzner checks a request in this order, as measured
  on the real API (`compat/requirements.md`). The lab does the same:
  1. The route (`API.ServeHTTP`). A path that no route takes is 404 `not_found`
     "api route not found", even without a token. The same happens when a path
     segment named `id` or `*_id` is not a valid ID.
  2. The bearer token (`API.admit`). A missing or unknown token is 401
     `unauthorized`, without details.
  3. From here on, every answer has an `X-Correlation-Id` header.
  4. The rate limit: per project, with `RateLimit-*` headers, 429 when used up. The
     actions endpoints share a separate budget that is twice as large. They are
     `/v1/actions…`, `/v1/servers/actions…`, `/v1/servers/{id}/actions…` and the
     same paths for every other resource.
  5. The permission. A `read` token gets 403 `forbidden` for any method except GET
     and HEAD.
  6. An injected fault, if the project has one.
  7. The method. A known path with a method it does not take is 405
     `method_not_allowed`, with an `Allow` header. Otherwise the handler runs.
- **Handlers** are `web.HandlerFunc`. A handler gets a `*web.Request`, which holds
  the HTTP request, the `Caller` (project and permission) and the answer's `Header`.
  It returns a status and a body, which the API writes as JSON, or an error.
  - A `*web.Error` is sent as it is, with Hetzner's code.
  - Any other error is a failure of the lab. It is logged and answered as 500
    `server_error`.
  - Errors have the shape `{"error": {"code", "message", "details"}}`. Hetzner's
    `details` differ from error to error: missing, `null` or `{}`. The constructors
    in `internal/web/errors.go` send what Hetzner sends, so handlers use them instead
    of building an `Error` by hand.
  - The lab control is the exception: its errors carry the lab's own texts, so it
    builds them itself.
- **The web package reaches the lab** only through the interface `web.Backend`
  (`TokenByHash`, `TakeAPIFault`), which `*lab.Lab` implements. This keeps the layer
  rule: `web` may import only `state`.
- **Lists** use three helpers:
  - `web.Paginate` handles `page`, `per_page`, `meta.pagination` (with `null` for
    pages that do not exist) and the `Link` header. Hetzner announced that it would
    drop `Link` but still sends it.
  - `web.Sort` sorts by the fields a resource allows (`web.SortKeys`).
  - `web.ParseSelector` parses `label_selector`.
- **Request bodies.**
  - `web.DecodeJSON` decodes a body into a request type with pointer fields, so a
    missing or null field can be told apart from an empty one.
  - Broken JSON is 422 `json_error` with the decoder's message. JSON of the wrong
    shape is 422 `"failed to parse json"`. Unknown fields are ignored, as Hetzner
    ignores them, and so is anything after the first JSON value. `DecodeJSON`
    takes an `http.Request`, so the lab control uses it too.
  - The handler then checks the fields with a `web.Check` (`Required`, `Length`,
    `Labels`, `Fail`). A PUT uses `web.Check{Update: true}`, because Hetzner's texts
    differ there. The handler returns `check.Err()`: one `invalid_input` error that
    lists every bad field with Hetzner's messages.
  - A value that another resource of the project already has is `web.Uniqueness`
    (409 `uniqueness_error`).
  - All these texts were measured on the real API (`compat/requirements.md`).
  - The request schemas of the spec are not used at run time. The spec check is for
    tests only.
- **JSON types** come from `hcloud-go/v2/hcloud/schema`. Those are the types the CLI
  and the provider decode with. Where `schema` cannot express what the real API
  sends, HLab has its own type, named `<thing>Answer` or `<thing>Request`:
  - `web.Pagination`, because pages may be `null`;
  - the pricing, because `schema` still has `traffic` and a `datacenter` per Primary
    IP price;
  - server types, load balancer types and images, because `schema` still sends
    `deprecated`;
  - the SSH key requests, to tell a missing field from an empty one.
- **Lists handle `sort` differently** on Hetzner, as measured. Most refuse an
  unknown value (`web.Sort`). Locations ignore an unknown value. Server types answer
  it with `service_error`. Load balancer types ignore `sort` completely. Each
  handler says which behaviour it follows.
- **Datacenters** are gone from Hetzner's API. Their endpoints answer 410
  `deprecated_api_endpoint`, and no answer names a datacenter.
- **Without a token.** `API.ServeAs(caller)` answers as a caller that the lab
  control vouches for, such as the web UI acting for a project. The permission check
  still applies. The rate limit and injected faults do not: they exist for the tools
  under test, and the web UI's polling would use them up.

## The service

`cmd/hlabd` puts the parts together (`service.go`). It may import anything; the
packages below it follow the layers.

- **Start** (`start`) does these steps in order:
  1. Read `hlab.yaml` (`internal/config`). A missing file or key takes the default.
     An unknown key or a value out of range stops the start, and the error names the
     file.
  2. Load the catalog.
  3. `lab.Open`. It takes the data folder for itself through `Lifecycle.Lock`, so
     only one HLab runs on a machine.
  4. Open the three ports.
  5. Create the project `default` if the lab is new (`Lab.Fresh`: there was no state
     file). This comes after the ports, so a port that is already taken leaves a new
     lab untouched.
  6. Write a new control token through `Lifecycle.WriteSecret`.

  The ports listen on the host link's address; with `--platform fake` they listen
  on `127.0.0.1`. A port of 0 takes any free port (the tests do that), and the status
  reports the ports in use.
- **Ports:** the Cloud API on 8080, the Hetzner API on 8081 (with the Storage Box
  routes), and 8090 for the lab control under `/_lab` and the web UI.
- **Stop.** The service stops on `POST /_lab/shutdown`, on
  `Lifecycle.StopRequests` (Ctrl+C, SIGTERM) or when a server fails. Then:
  1. The servers let running requests finish (up to 10 s).
  2. The control token file is removed, so the CLI knows the lab is gone.
  3. `Lab.Close` writes the state.
- **`--platform fake`** uses the fake machine but this machine's real life cycle:
  the lock, the token file and the signals are real. So only one HLab runs, and the
  CLI finds it.
- **The life cycles:**
  - Windows holds the mutex `Global\HLab`. It writes the path of its data folder to
    `%ProgramData%\HLab\instance`, so a second start can say in its error message
    which folder is in use. It writes the token with its own ACL: local users may
    read it.
  - Linux holds an flock on `/run/lock/hlab.lock`; that file contains the path of
    the data folder. It writes the token with mode `0640` and the group `hlab`, if
    the machine has that group.

## Lab control, CLI and web UI

- **The lab control** lives in `internal/api/control`; its types are in
  `internal/labapi`. Its routes:
  - `GET /_lab/status` and `POST /_lab/shutdown`;
  - projects: `GET|POST /_lab/projects`, `DELETE /_lab/projects/{name}`;
  - tokens: `GET|POST /_lab/projects/{name}/tokens`, `DELETE …/tokens/{token}`,
    where `{token}` is the token's name;
  - the env token: `POST /_lab/projects/{name}/env-token`. It is a POST because the
    first call creates the token;
  - the Cloud API for a project: `/_lab/projects/{name}/cloud/v1/...`. It runs the
    Cloud API's handlers through `API.ServeAs` with `read_write`.

  It accepts the control token (`Authorization: Bearer`) or whatever
  `Options.Admit` vouches for (the web UI's cookie). Errors have Hetzner's format
  (`web.WriteError`): the lab's `ErrInvalid` is 422 `invalid_input`, `ErrExists` is
  409 `uniqueness_error`, `state.ErrNotFound` is 404 `not_found`.
- **The client** (`internal/client`) finds the token in `HLAB_TOKEN_FILE`. Without
  it, it reads `control.token` in the default data folder:
  - `C:\ProgramData\HLab` on Windows;
  - `/mnt/c/ProgramData/HLab` in WSL2 (detected through `WSL_DISTRO_NAME`);
  - `/var/lib/hlab` on Linux.

  It calls the address in `HLAB_CONTROL`, or else `http://198.18.0.1:8090`, the host
  link that Windows and WSL2 share. If there is no token file or no answer, it
  returns `client.ErrNotRunning`. An error answer becomes a `*client.Error` with
  its status and code, so the CLI can give a hint (for 401: the token file
  belongs to another `hlabd`).
- **The CLI** (`internal/cli`, cobra) has one file per group of commands; each
  command calls the client. `hlab env` prints the endpoints and the project's env
  token for sh (`export`) or PowerShell (`$env:`), in single quotes.
- **The web UI** (`internal/webui`) is `index.html`, `ui.js` and `ui.css` in
  `files/`, built in with `go:embed`, without a build step.
  - `UI.Guard` runs before everything else on port 8090. The Host header must be
    one of the port's addresses (or `localhost` for a loopback address); this
    prevents DNS rebinding.
  - A change (any method except GET, HEAD, OPTIONS) needs an `Origin` header of the
    page itself or, without `Origin`, a token; this prevents CSRF.
  - The page sets a random cookie (`HttpOnly`, `SameSite=Strict`, new at each
    start). The lab control accepts it instead of the token (`UI.Admits`), so the
    browser never holds a token.
  - The page reads the catalog through the project's Cloud API route.
- **Who reaches port 8090.** The cookie grants the same rights as the control token,
  to anyone who loads the page. That is the same group of people:
  - Port 8090 listens on `127.0.0.1` and the host link, which only the host and
    WSL2 reach.
  - The control token is readable for the host's local users anyway.
  - Guests never reach the port: the appliance's ruleset lets them reach the host
    link only on 8080 and 8081 (phase 1b).

## The catalog

`internal/catalog` holds what Hetzner offers: locations, server types, load balancer
types, Storage Box types, prices, system images and ISOs. Each entry has Hetzner's
real ID and name, because the tools check them (`compat/requirements.md`).

- **Built in, and the user can change it.** The built-in catalog is
  `internal/catalog/catalog.yaml`, compiled in with `go:embed`. `catalog.Load` then
  reads a `catalog.yaml` from the data folder on top of it. Each key in the user's
  file replaces the built-in value; a list is replaced as a whole. The start stops,
  naming the file, on unknown keys, duplicate IDs or names, values the API does not
  allow, and unknown locations.
- **Hetzner's shape.** The keys are those of Hetzner's JSON, so answers of the real
  API can be copied into the catalog (Recipes). The differences:
  - a server type's locations give only the location's name; the ID comes from
    `locations`;
  - an ISO without `architecture` fits every architecture;
  - images are system images, always `available`, without labels.

  Prices keep all decimals, as `GET /pricing` shows them. `GET /load_balancer_types`
  rounds them to four decimals, as Hetzner does.
- **Only what the lab can run:**
  - x86 server types and ISOs, because Hyper-V on an x86 host runs no ARM guests;
  - each system image only in its newest version (Debian 13, Ubuntu 26.04).

  A server type is `available` wherever it is not deprecated. On Hetzner the flag
  also reflects current capacity, which the lab does not copy.
- **The lab owns it.** `lab.Options.Catalog` takes a loaded catalog (nil means the
  built-in one), and `Lab.Catalog()` returns it. It does not change while the lab
  runs, so reading it needs no lock. Lists are sorted by ID, as Hetzner sorts them.

## Spec check and compat/

`compat/` holds what HLab must be compatible with:

| File | What |
|---|---|
| `versions.yaml` | the tools and their versions; the specs and when they were fetched |
| `specs/cloud.spec.json`, `specs/hetzner.spec.json` | Hetzner's OpenAPI specs |
| `spec-deviations.yaml` | where the real API differs from the spec, with the reason |
| `requirements.md` | what the tools need, from their source code, each with its phase and test |
| `exceptions.yaml` | what never works against HLab, with the reason |

- **Every answer of every API test is checked against the spec.**
  `spectest.New(t, api, spectest.Cloud)` returns a client that sends requests to the
  handler inside the test's process. The client fails the test on an answer the spec
  does not allow: wrong status, content type or JSON body. Errors of paths that the
  spec does not list must still have Hetzner's error format. The check uses
  `kin-openapi` with JSON Schema 2020-12, because the specs are OpenAPI 3.1.
- **Deviations** change the spec before the check. Each deviation does one of these:
  - replaces the schema at a JSON pointer with `{}` or a given value (`*` in the
    pointer matches any key), and takes a replaced property out of `required`;
  - adds a key the spec lacks, such as a media type like `text/plain`.

  A pointer that matches nothing fails the tests.
- **Hetzner before the spec.** Where the real API and the spec differ, HLab follows
  the API and records a deviation. What the tools need is in `requirements.md`.
- `bin/dev spec-diff` compares the specs with Hetzner's current ones. CI runs it
  weekly (`.github/workflows/spec-diff.yml`).

## Platform

`internal/platform` holds four interfaces: `Hypervisor`, `HostNet`, `HostDNS` and
`Lifecycle`. They take plain values. Their errors can be told apart with `errors.Is`
(`platform.ErrNotFound`, `ErrExists`, `ErrRunning`, …).

- `internal/platform/contract` defines how every platform must behave. The unit
  tests run it against `internal/platform/fake`, the live tests against the real
  platform.
- The fake keeps everything in memory. Tests can make calls fail (`FailNext`) or
  wait (`Delay`), and read the calls it received (`Calls`).
- `internal/platform/windows` builds only for Windows. For now it has the life cycle
  (The service); every other method returns `platform.ErrNotImplemented`.
  `internal/platform/linux` has only the Linux life cycle so far.
- `lab` reaches the platform only through `system`, which never sees the state.

## Dependencies

As few as possible, each with its reason here.

| Module | Why |
|---|---|
| `golang.org/x/tools` (only `go/packages`) | the layer test reads the imports of every package, for Linux and Windows |
| `github.com/hetznercloud/hcloud-go/v2` (only `hcloud/schema`) | the JSON types of the APIs, as the CLI and the provider decode them |
| `github.com/getkin/kin-openapi` (only `openapi3`, in `spectest`) | checks the answers against Hetzner's OpenAPI 3.1 specs |
| `go.yaml.in/yaml/v4` | reads `hlab.yaml`, the catalog and the YAML files of `compat/`; `kin-openapi` needs it anyway |
| `github.com/spf13/cobra` | the CLI's commands, flags and help, as the hcloud CLI has them |

Developer tools live in a separate module, `tools/go.mod`, so their dependencies
stay out of `go.mod`. Run them with `go tool -modfile=tools/go.mod <tool>`.

| Tool | Why |
|---|---|
| `github.com/golangci/golangci-lint/v2` | the linters of STYLE.md, staticcheck among them |
| `mvdan.cc/sh/v3` (`shfmt`) | formats the shell scripts |

No HTTP router (the patterns of `net/http` are enough), no ORM, no database.

## Tools

`bin/dev` (Bash, for Linux and WSL2) and `bin/dev.ps1` (PowerShell, for Windows) are
the only entry points. CI runs them the same way a developer does.

What a developer installs:

| What | Where | Why |
|---|---|---|
| Go | `go.mod` names the version; with `GOTOOLCHAIN=auto` (the default) any newer Go fetches it | building, testing |
| gcc | Linux, WSL2 (`apt install gcc`) | only for the race detector of `bin/dev test`; the binaries use no cgo |
| shellcheck, yamllint | Linux, WSL2 (`apt install shellcheck yamllint`) | `bin/dev lint` |
| PSScriptAnalyzer | Windows PowerShell or pwsh: `Install-Module PSScriptAnalyzer -Scope CurrentUser` | `bin/dev lint`, `bin/dev.ps1 lint` |
| hcloud CLI, OpenTofu | Linux, WSL2, in the versions of `compat/versions.yaml` | `bin/dev smoke` |

golangci-lint and shfmt need no install: `go tool` builds them from `tools/go.mod`
the first time they run. In WSL2, `bin/dev lint` runs PSScriptAnalyzer in pwsh if it
is installed, otherwise in Windows PowerShell through WSL interop. `bin/dev.ps1 lint`
skips shellcheck and yamllint; they are Linux tools, and CI's Linux job runs them.

**Windows tests from WSL2.** `bin/dev test --windows` compiles each package's tests
with `GOOS=windows go test -c`. It runs the resulting `.exe` through WSL interop in
the package's directory, and the exit code and output come back to WSL2. Windows
needs no Go for this. Tests that need the go command, such as the layer test, are
skipped there; CI's Windows job has Go and runs them.

**Smoke tests.** At the end of a phase, `bin/dev smoke` runs the real tools against
the lab, from Linux or WSL2:

1. It starts `hlabd --platform fake` with a fresh data folder on free ports.
2. It runs the hcloud CLI with the commands of the phases so far.
3. It runs OpenTofu (`apply`, `plan`, `destroy`) on every `examples/tofu-<phase>`.

The smoke tests pass when nothing fails and the `plan` after `apply` shows no
changes. Each phase adds its commands to `smoke` in `bin/dev` and its configuration
as `examples/tofu-<phase>`. The smoke tests need the provider from the OpenTofu
registry, so they stay out of CI.

**Builds.** `bin/dev build` builds with `CGO_ENABLED=0` and `-trimpath`, and sets
`main.version` from `git describe`. `hlabd --version` (and the same flag of the other
binaries) prints it.

## Recipes

**A new package.** Add its row to the layer table above before anything imports it.
`internal/layers_test.go` reads the table from this file.

**A new dependency.** `go get` it and add a row with the reason to the dependency
table. A developer tool goes into `tools/go.mod` instead (run `go get -tool` there).

**A new kind.**

1. Add its record type with a `RecordID` method to `internal/state/records.go`
   (JSON names in `snake_case`, timestamps as `time.Time`).
2. Add a `Kind` variable to `state.go`, and add the variable to `kinds`.
3. If the record belongs to a project, give it a `RecordProject` method too, so it
   is deleted with its project.

An older state file without the kind still loads; no migration is needed.

**A new endpoint.**

1. Add its handler to the resource's file in `internal/api/cloud`. A new resource
   gets a new file, registered in `cloud.Register`.
2. Register it with `api.Handle("GET /things/{id}", ...)`. The path is the spec's
   path without `/v1`. `web.PathID(req, "id")` reads an ID from it.
3. Build the answer from `hcloud-go`'s `schema` types. Read a body with
   `web.DecodeJSON` and check it with a `web.Check`. A create calls
   `lab.CheckProject` in its `Change` and answers its error with
   `web.TokenInvalid()`.
4. Refuse requests with the `web` errors (`web.NotFound`, `web.Uniqueness`, …), with
   Hetzner's codes and messages.
5. A list filters, sorts (`web.Sort` with the spec's sort values) and answers with
   `listJSON`, which paginates.
6. Its tests send every request through a `spectest` client. A comment notes where a
   test checks Hetzner's behaviour. An answer measured on the real API goes into
   `compat/requirements.md`.

**A new read-only resource from the catalog**, such as `storage_box_types` in the
Hetzner API:

1. The catalog may have its entries already. If not, add a list and a type in
   `internal/catalog/catalog.go` with the keys of Hetzner's JSON, checks in
   `check.go` (unique IDs and names, the spec's enums, known locations) and the
   entries in `catalog.yaml`.
2. In the API, add a file with `register<Things>(api, l.Catalog())`. It has a list
   endpoint using `byName` and `listJSON`, a single-item endpoint that answers
   `web.NotFound` for unknown IDs, and a `<thing>JSON` function that builds the
   `schema` type.
3. Add the resource to `TestCatalogResourcesListAndGet`.

**Updating the catalog**, when Hetzner changes prices, adds a type or releases a new
image version:

1. With a read token of a real project, fetch the lists, for example:

   ```sh
   curl -H "Authorization: Bearer $HCLOUD_TOKEN" \
     "https://api.hetzner.cloud/v1/server_types?per_page=50"
   ```

   Fetch the same way `locations`, `load_balancer_types`, `isos` (all pages),
   `images?type=system&architecture=x86`, `pricing`, and
   `https://api.hetzner.com/v1/storage_box_types`.
2. Copy the values into `internal/catalog/catalog.yaml`, with the same keys:
   - only x86 server types and ISOs;
   - a server type's locations by name, without `id`, and `available` wherever the
     type is not deprecated;
   - server type prices from `server_types`, load balancer type prices from
     `pricing` (all decimals);
   - only the newest version of each system image.
3. Run `bin/dev test`. The catalog's check names whatever does not fit.

**A spec deviation**, when the real API answers something the spec does not allow:
add an entry to `compat/spec-deviations.yaml` with the pointer into the spec, the
value if it is not `{}`, and the reason: how the real API behaves and how that was
found out.

**A requirement**, when a tool needs something from the API: add a row to
`compat/requirements.md` with the source (a file in the tool), the phase and the
unit test that checks it. Something that never works goes into
`compat/exceptions.yaml` instead.

**A new version of a tool.** Raise it in `compat/versions.yaml`. Read the tool's
diff (`hcloud-go/schema`, tests, examples) for new requirements and exceptions. Take
over the new specs (next recipe), then run the smoke tests.

**New specs from Hetzner.** `bin/dev spec-diff --update` replaces `compat/specs`.
Then:

1. Set the date in `compat/versions.yaml`.
2. Read `git diff compat/specs`.
3. Fix the deviations that no longer match; the tests say which.
4. Change the API where the spec shows that the real API changed.

**A new migration** is needed for any change that an older state file cannot load
into as it is, such as a renamed or moved field, or a field with a new meaning:

1. Raise `currentVersion` in `internal/state/file.go`.
2. Add `migrations[old]`, which lifts the raw JSON by one version.
3. Add a test that loads a sample file of the old version from `testdata/`.

**A new action.** Add a row to the resource's `lab.ActionTable` with Hetzner's
command and action name.

- `Plan` checks the request and makes the quick changes.
- `Steps` does the slow part through `l.System()`. It keeps IDs across those calls
  and waits with `l.Sleep`, using a timing from `internal/lab/clock.go` that was
  measured on Hetzner.
- `Undo` takes back the quick changes.

Its tests use the fake platform and, for timings, a test clock.

**A new route of the lab control.**

1. Put its request and answer types into `internal/labapi`.
2. Put the handler into `internal/api/control`, registered with
   `c.handle("GET /things", ...)`; it returns a status and a body.
3. Add a method to `internal/client` and, if users need it, a command to
   `internal/cli`.
4. Test it in `internal/api/control`. A new command also gets a test in
   `cmd/hlabd/service_test.go`, which runs the CLI against `hlabd` inside the test's
   process.

**A new setting in `hlab.yaml`.** Add a field with its YAML key to `config.Config`,
its default to `config.Default` and its range to `check`. If users are meant to set
it, add it to the example in the README.

**A new platform method.** Add it to the interface in `internal/platform`, to the
contract tests, to the fake and to every platform. A platform that does not have it
yet returns `ErrNotImplemented`.

**A command for later phases.** `bin/dev` and `bin/dev.ps1` list every command in
their usage text. A command that does not exist yet says "not yet implemented".
