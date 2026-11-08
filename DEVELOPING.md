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

Everything except `cmd/` and the live tests in `itest/` lives under `internal/`. The table below goes from the user
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
| `internal/picture` | pure functions from the state to the data plane's pictures and the host's DNS rules | `state`, `agentproto`, `catalog` |
| `internal/seed` | (later) pure functions for the cloud-init data | – |
| `internal/system` | does the work and never sees the state: VMs, disks, images, host link, host DNS, appliance, agent client | `platform`, `agentproto`, `vhdx`, `imagefmt` |
| `internal/platform` | the platform interfaces and their data types | – |
| `internal/platform/windows` | Hyper-V through `pshost`, host network, NRPT, life cycle | `platform`, `platform/windows/pshost` |
| `internal/platform/windows/pshost` | long-running PowerShell processes: JSON over stdin and stdout | – |
| `internal/platform/linux` | the Linux platform; for now only its life cycle | `platform` |
| `internal/platform/fake` | fakes for unit tests and `--platform fake` | `platform` |
| `internal/platform/contract` | tests only: the contract tests of the platform interfaces, run against the fake and the real platforms | `platform` |
| `internal/agentproto` | the wire format between service and agent, picture types | – |
| `internal/agent` | the agent (Linux only): applies pictures, announces its addresses, metadata service; counters (later) | `agentproto` |
| `internal/vhdx` | (later) the page 83 disk ID in a VHDX's metadata | – |
| `internal/imagefmt` | (later) detecting image formats, unpacking | – |
| `internal/catalog` | server types, locations, load balancer and box types, prices, ISOs, system images; built in, a user file overrides | – |
| `internal/config` | `hlab.yaml`: ranges, time scale, overcommit, deadlines, ports | – |
| `internal/schema` | (later) models generated from Hetzner's spec where `hcloud-go/schema` has gaps | – |
| `internal/spectest` | tests only: checking answers against Hetzner's spec | – |
| `itest` | tests only: the live tests (build tag `live`) | – |

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
- **Transactions.** A `state.Tx` makes a deep copy of a record the first time it
  hands it out (`state.Get`, `All`, `Find`); of the kind's table it copies only the
  map from IDs to records. The records you get from a Tx are its copies, so
  changing one in place changes the Tx. The Tx notes the ID of every record it
  copied, put or deleted. The changes become part of a state only through
  `tx.Result()`; a Tx that is dropped changes nothing. A Tx that changes one of a
  thousand actions takes about 20 µs (`BenchmarkTxOnAThousandActions`); the
  copied map grows with the kind.
- **`All` copies every record of the kind**, and the change then compares and
  encodes all of them anew. To scan a large kind such as actions in a `Change`,
  use `state.IDs`, which copies nothing, and `state.Get` for the records it
  changes.
- **Reads copy nothing.** `state.Read` starts a read-only Tx. Its records are the
  state's own, and calling Put, Delete or NewID on it panics. `lab.View` uses it, so
  the token check on every request copies nothing. That check uses `state.Find`,
  which does not sort as `state.All` does.
- **Only a real change is saved.** Because records can change in place,
  `tx.Changed()` compares each record the Tx noted with the state the Tx started
  from (`reflect.DeepEqual`). A `Change` that only read, or set a value to what it
  already was, saves nothing. A real change encodes the whole state while holding
  the mutex and passes it to the writer.
- **Encoding reuses each record's JSON.** `state.Encode` keeps the JSON of every
  record in the state it encoded; `tx.Result()` drops it for the records the Tx
  noted. So a change encodes only its own records, and `Encode` puts the file
  together from the rest. It writes by hand what `json.MarshalIndent` would write
  for the whole file, which `TestEncodeWritesWhatMarshalIndentWrites` checks. This
  relies on the rule that a state's records never change.
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
- `internal/platform/windows` builds only for Windows. It has the life cycle (The
  service) and the hypervisor (Hyper-V, below); `HostNet`, `HostDNS` and the agent
  dialer return `platform.ErrNotImplemented` for now. `internal/platform/linux` has
  only the Linux life cycle so far.
- A method joins an interface with the phase that first calls it. Still to come:
  freezing a running VM's disk for snapshots (phase 3a), counters for metrics
  (phase 4), the serial console (phase 6a).
- A NIC's port mode (isolated, promiscuous, access VLAN, trunk) is set only through
  `NICSpec.Port`, when `CreateVM` or `AttachNIC` adds the NIC, and never changes;
  the Linux platform (phase 8) must set it there too. If a later phase needs to
  change a port, the method comes back through the recipe "A new platform method".
- `lab` reaches the platform only through `system`, which never sees the state.

## Hyper-V

The Windows hypervisor (`internal/platform/windows/hyperv.go`) runs the commands of
`hyperv.ps1` in long-running PowerShell processes (`pshost`). It needs administrator
rights, which `hlabd` has, or the group Hyper-V Administrators, which the live tests
use.

**pshost** keeps up to four `powershell.exe` (Windows PowerShell 5.1, which every
Windows 11 has and which loads the Hyper-V module natively). Starting one and
loading the module takes most of a second; a command then takes about 30 ms.

Why a pool of a fixed size and not one process: commands of one process run one
after another, and some take minutes. While `Convert-VHD` flattens a 40 GB disk for
a snapshot, a single process would hold up every `VMState`, `StartVM` and
`CreateVM` of the lab. The pool stays simple: a fixed size, no priorities; a call
waits only when all four are busy.

- The processes start on demand; commands in one process run one after another, so
  a slow command (`Convert-VHD`) holds up only its own process.
- The loop (`pshost/host.ps1`) gets the caller's script as its first line. The
  script returns a hashtable from command names to script blocks, so only those
  commands can run.
- A command fails with a kind by throwing `"<kind>: <message>"`, such as
  `"not_found: VM web"`. `hyperv.go` turns the kinds `not_found`, `exists`,
  `running` and `not_running` into the platform's errors; a cmdlet's error has no
  kind.
- A command returns a list with a leading comma (`, @(...)`); PowerShell would turn
  a list of one into the item, and an empty list into nothing.
- Answers start with the character 0x1E. Anything else on stdout (`Write-Host`,
  `[Console]::WriteLine`) is logged at debug level and skipped.
- A canceled call kills its process, and so does a process that ends by itself
  (`pshost.ErrCrashed`, with the end of its error output). The next call starts a
  new one. Hyper-V may still finish what the killed command had started, so a
  caller looks at the VM again rather than assume nothing happened.
- The processes run with `CREATE_NO_WINDOW`, so `hlabd` without a console opens no
  window.

**What Hyper-V does differently**, and how `hyperv.ps1` deals with it:

- A VM is generation 2 with Secure Boot and the template "Microsoft UEFI
  Certificate Authority"; the official cloud images boot with it unchanged.
- Memory is static, as on Hetzner: with dynamic memory the guest would see only the
  start value.
- At every VM, `AutomaticStopAction` is `ShutDown` and `AutomaticStartAction` is
  `Nothing`: a stopping host shuts the guests down, and HLab starts them itself.
- Disks sit on SCSI controller 0, a slot is its location; the system disk is slot
  0 and the first boot device, so the guest sees it as `sda`. A Gen2 VM takes a DVD
  drive only while it is off, so every VM gets an empty one on controller 1 when it
  is created; `SetISO` only fills or empties it.
- `New-VM` adds a NIC of its own; `CreateVM` removes it and adds the spec's NICs
  with their MAC addresses.
- Ports: servers' public NICs are isolated in the private VLAN 10/11 and see only
  the appliance, which is promiscuous; private NICs are access ports, the
  appliance's private NIC a trunk for VLANs 2–4094.
- Hyper-V has no ACPI button. `ShutdownVM` asks the guest's shutdown service
  (`Msvm_ShutdownComponent.InitiateShutdown`) and does not wait; a guest without
  it ignores the request, as a guest without ACPI does on Hetzner. `ResetVM` is
  `Restart-VM -Type Reset`.
- States: `Saved` and `Hibernated` count as off, `Pausing` and `Resuming` as
  paused, everything else that is not off as running. The heartbeat counts when
  Hyper-V reports `Ok…`.
- `Get-VM -Name` takes wildcards, so names are escaped. `Get-VM` without a name
  reads every VM (about 10 ms each); only `FindVMs` does that, when HLab starts.
- Disks are dynamic VHDX with 1 MB blocks, as Microsoft advises for Linux guests,
  or differencing VHDX on a parent. Hyper-V cannot use files in WSL2's file system.
- `qemu-img` cannot open a differencing VHDX; `FlattenDisk` (`Convert-VHD`) writes
  one with its parents into a plain one first.
- The seed is an ISO 9660 with Joliet, labeled `cidata`, written with IMAPI2.
  Joliet keeps the long names `meta-data` and `network-config`. PowerShell cannot
  read the IStream that IMAPI2 returns, so a few lines of C# (`Add-Type`, once per
  process) write it to the file.

## Pictures and the agent

The data plane lives in the appliance, a small Debian 13 VM. hlabd never sends it a
change, only whole pictures; the agent inside makes the appliance match each one.

**Pictures** (`internal/picture`) are pure functions of a `picture.Lab`: plain
values the lab takes from its state and settings (the host link, the MAC set, the
servers). Until the servers come (1b-4) the lab has none. Each picture carries the
Lab's generation. Their output is compared with golden files in
`internal/picture/testdata`; `go test ./internal/picture -update` writes them anew,
and the diff shows what changed.

| Picture | What | The agent |
|---|---|---|
| `links` | the four NICs by MAC (`link`, `public`, `private`, `uplink`), VRFs, VLANs, addresses, routes, sysctls | netlink: adds what is missing, removes what the picture lacks |
| `ruleset` | the whole nftables ruleset as text | `nft -f`: all or nothing |
| `dhcp` | dnsmasq's whole configuration | writes it, checks it with `dnsmasq --test`, restarts `hlab-dnsmasq` if it changed |
| `metadata` | the metadata service's answers per server, with the server's addresses | serves them on `169.254.169.254:80` |

**The address plan** as the pictures use it: the host link `198.18.0.0/29` and
`2001:db8:0:ffff::/64` (host `.1`/`::1`, appliance `.2`/`::2`, `.3` the source for
ACME checks); the servers see Hetzner's gateways `172.31.1.1` (on-link to their
`/32`) and `fe80::1`, and the resolvers `198.18.0.2` and `2001:db8:0:ffff::2`. The
appliance's MAC addresses are `02:48:4c:0a:00:0<n>` (set A) or `02:48:4c:0b:00:0<n>`
(set B), n = NIC 1 to 4; an update starts the new appliance with the other set.

**The ruleset's fixed part**, before any firewall (`ruleset.nft.tmpl`):

- Servers reach the host (`198.18.0.1`, `2001:db8:0:ffff::1`) only on TCP 8080
  and 8081; the lab control on 8090 stays closed to them.
- The uplink reaches the Internet only: private and link-local ranges, the
  Default Switch's and the LAN's among them, are rejected, so no server reaches the
  Windows host or its LAN. So is the lab's own range `198.18.0.0/15`: an address
  nobody holds would otherwise match the uplink's default route and leave the lab.
  The appliance needs no routing table of its own for the uplink: only `public`
  may forward to it. IPv6 to the uplink is rejected (the Default Switch has none),
  and so is IPv4 from servers without Internet.
- No rule depends on conntrack seeing a connection start. Between host and servers
  and among servers nothing is tracked; the answers to the host's connections are
  `established`, which conntrack also gives a running TCP connection after an
  update of the appliance.
- The servers reach DHCP, the metadata service and DNS on the appliance; the host
  reaches all of it.

**Redirects stay off** on every interface (sysctl in the image and per interface in
the `links` picture): a router that sends ICMP redirects teaches servers to reach
each other past the firewalls.

**The agent** (`internal/agent`, Linux only):

- It answers hlabd on vsock port 5000 (`agentproto.Port`): `GET /v1/status` (version
  and the generation of each picture applied) and `PUT /v1/pictures/<name>`. It
  applies one picture at a time. 204 means applied; 400 a body that is not the
  picture (also unknown fields, which come from a newer hlabd); 422 that it refused
  the picture and nothing changed (its own `Check`, nft or dnsmasq refused it); 500
  that applying failed part way, and hlabd sends the picture again.
- `links` is not atomic, but every step makes a state the next picture can start
  from. Its order: rename the NICs (a NIC must be down for that), VRFs, VLANs,
  sysctls, links up, addresses, routes. Its routes carry the protocol number 72, so
  the agent removes only its own. The `uplink` is networkd's: the agent only names
  it.
- **Announcing.** Whenever a device's carrier comes up, and after each `links`
  picture, the agent sends each of the device's announced addresses twice: a
  gratuitous ARP for IPv4, an unsolicited neighbor advertisement (override) for
  IPv6. After an update the new appliance has new MAC addresses; without this the
  neighbors keep the old ones for seconds.
- **The metadata service** tells servers apart by the request's source address.
  It listens with `IP_FREEBIND`, because `169.254.169.254` on `lo` comes with the
  first `links` picture.
- It decides nothing: what the appliance does comes from the pictures. After a
  restart it has none and reports none; hlabd sends all of them again.

## The appliance

`bin/dev appliance` builds `build/appliance/hlab-appliance.vhdx` with the agent of
the working tree (`appliance/build.sh`). It needs no root:

1. `mmdebstrap --mode=unshare` builds Debian 13 (trixie with its updates and
   security updates) as a tarball. It installs only Debian packages, puts in
   `appliance/files` and the agent, and runs `appliance/customize.sh`.
2. `mkfs.ext4 -d` fills the root file system from the tarball; `mkfs.vfat` and
   mtools build the EFI partition with Debian's shim and grub, signed for the
   "Microsoft UEFI Certificate Authority", which the VMs' Secure Boot template
   trusts. grub boots `/vmlinuz` of the root file system.
3. `sfdisk` puts both into a 4 GB disk with a GPT, and `qemu-img` writes it as a
   dynamic VHDX with 1 MB blocks.

What is in it:

- **Packages:** the cloud kernel (Hyper-V's drivers, signed), systemd, nftables,
  dnsmasq (only `dnsmasq-base`: the agent runs it), knot, unbound, haproxy, pebble,
  openssh-server, rsync, borgbackup, btrfs-progs, qemu-utils.
- **Running:** `hlab-agent` before `sysinit.target`, after the modules (`hv_sock`,
  `vrf`, `8021q`) and the boot ruleset; it must not wait for the network, which it
  sets up itself. networkd only for the uplink (DHCP, matched by both MAC sets,
  `ManageForeignRoutes=no`). unbound as the lab's resolver on `198.18.0.2` and
  `2001:db8:0:ffff::2`. `hlab-dnsmasq` once the agent has its configuration.
- **Off until their phase:** knot, haproxy, sshd (its host keys are removed;
  each appliance makes its own in phase 5), and `systemd-networkd-wait-online`.
  pebble has no unit of its own.
- **Boot ruleset** (`/etc/nftables.conf`): forwards nothing until the first
  `ruleset` picture.
- Nobody logs in: root is locked, hlabd reaches the agent over hv_sock.

## Dependencies

As few as possible, each with its reason here.

| Module | Why |
|---|---|
| `golang.org/x/tools` (only `go/packages`) | the layer test reads the imports of every package, for Linux and Windows |
| `github.com/hetznercloud/hcloud-go/v2` (only `hcloud/schema`) | the JSON types of the APIs, as the CLI and the provider decode them |
| `github.com/getkin/kin-openapi` (only `openapi3`, in `spectest`) | checks the answers against Hetzner's OpenAPI 3.1 specs |
| `go.yaml.in/yaml/v4` | reads `hlab.yaml`, the catalog and the YAML files of `compat/`; `kin-openapi` needs it anyway |
| `github.com/spf13/cobra` | the CLI's commands, flags and help, as the hcloud CLI has them |
| `github.com/vishvananda/netlink` | the agent sets links, VLANs, VRFs, addresses and routes, and watches the carrier |
| `github.com/mdlayher/vsock` | the agent listens on AF_VSOCK, which hv_sock reaches from Windows |

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
| Docker | WSL2 (Docker Desktop with WSL integration) or Linux | `bin/dev test --agent` |
| mmdebstrap, uidmap, debian-archive-keyring, e2fsprogs (1.47.1 or newer) with libarchive (`libarchive13t64`), dosfstools, mtools, fdisk, qemu-utils | WSL2, Linux (`apt install …`); an entry for the user in `/etc/subuid` and `/etc/subgid` (Ubuntu creates it) | `bin/dev appliance` |
| Hyper-V, the group Hyper-V Administrators | Windows 11 Pro (Live tests) | `bin/dev itest` |

golangci-lint and shfmt need no install: `go tool` builds them from `tools/go.mod`
the first time they run. In WSL2, `bin/dev lint` runs PSScriptAnalyzer in pwsh if it
is installed, otherwise in Windows PowerShell through WSL interop. `bin/dev.ps1 lint`
skips shellcheck and yamllint; they are Linux tools, and CI's Linux job runs them.

**Windows tests from WSL2.** `bin/dev test --windows` compiles each package's tests
with `GOOS=windows go test -c`. It runs the resulting `.exe` through WSL interop in
the package's directory, and the exit code and output come back to WSL2. Windows
needs no Go for this. Tests that need the go command, such as the layer test, are
skipped there; CI's Windows job has Go and runs them.

**Live tests.** `bin/dev itest [--run <pattern>]` runs the tests in `itest/`, which
build only with the tag `live`, against the real platform. For now that is the
contract test of the hypervisor against Hyper-V (`internal/platform/contract`).

- It compiles them with `GOOS=windows go test -c -tags live` and runs the `.exe`
  through WSL interop, as `test --windows` does; `bin/dev.ps1 itest` runs them on
  Windows with Go.
- Hyper-V needs administrator rights or the group Hyper-V Administrators (SID
  `S-1-5-32-578`; its name depends on the language of Windows). The live tests run
  without elevation, so the developer is in that group. Once, as administrator:
  `Add-LocalGroupMember -SID S-1-5-32-578 -Member $env:USERNAME`, then sign out and
  in again. `whoami /groups` shows the group.
- Disks go to a temporary folder on the Windows disk: Hyper-V cannot use files in
  WSL2's file system.
- The tests remove what they create. Their VMs and disks are named
  `hlab-contract-…` and their VMs carry the notes `hlab-contract`; after an aborted
  run, `Get-VM hlab-contract-* | Stop-VM -TurnOff -Force -Passthru | Remove-VM
  -Force` removes the VMs.
- The live tests stay out of CI: GitHub's Windows runners have no nested Hyper-V.
  The contract test of the hypervisor takes about 30 s.

**Agent tests.** The tests of `internal/agent` with the build tag `netns` change
links, addresses, routes, nftables and sysctls for real, so they run in a network
namespace of their own, never on the developer's machine as it is:

- `bin/dev test --agent` builds them and runs them in a container without network
  (`--network none`) with `NET_ADMIN` and `NET_RAW`. `/proc/sys` is read-only in a
  container unless `systempaths=unconfined`; `NET_ADMIN` still limits writes to the
  container's own `net.*`. The image (`internal/agent/testdata/Dockerfile`) has
  nft, dnsmasq, ip and busybox's `udhcpc`, which plays a server asking for its
  address.
- `bin/dev test --agent --host` runs them with sudo in a new namespace
  (`unshare --net`); CI's job `agent` does that on `ubuntu-latest`.
- They take a few seconds. The NICs are veth pairs: the agent renames one end by its
  MAC address, the test watches the other.
- The tests without the tag (the API, the metadata service) run with `bin/dev test`.

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
contract tests, to the fake and to every platform; on Windows, its command goes into
`hyperv.ps1`. A platform that does not have it yet returns `ErrNotImplemented`. Run
the contract tests live (`bin/dev itest`).

**A new picture** (such as `dns` in phase 2b):

1. Add its name and type with a `Check` to `internal/agentproto`; `Check` refuses
   what the agent could not apply.
2. Add its function to `internal/picture` and a golden file to its tests
   (`go test ./internal/picture -update`).
3. Add its case to `putPicture` in `internal/agent` and a test in
   `netns_test.go` that applies it for real. What a picture needs on the
   appliance (packages, units) goes into `appliance/`.

**A command for later phases.** `bin/dev` and `bin/dev.ps1` list every command in
their usage text. A command that does not exist yet says "not yet implemented".
