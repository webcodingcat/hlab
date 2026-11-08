# Developing HLab

This file explains how the code is laid out, which rules hold it together and how to
work on it (Recipes). CLAUDE.md lists the rules that are easy to break,
[STYLE.md](STYLE.md) has the coding standards, and
[docs/roadmap.md](docs/roadmap.md) says what is done and what comes next. What
each part does in detail is in the files under `docs/`:

- [docs/servers.md](docs/servers.md): servers, Primary IPs, projects, tokens,
  faults.
- [docs/service.md](docs/service.md): the service's start and stop, the
  machine's side, the lab control, the CLI, the web UI.
- [docs/catalog.md](docs/catalog.md): what the lab offers.
- [docs/state.md](docs/state.md): how the state is encoded and written.
- [docs/hyperv.md](docs/hyperv.md): how HLab drives Hyper-V and the host's
  network on Windows.
- [docs/appliance.md](docs/appliance.md): the pictures, the agent and the
  appliance's build.
- [docs/tools.md](docs/tools.md): what a developer installs; lint, the tests,
  the live tests, the dev loop, the installer, the builds.

Section names in parentheses, such as (The state), point to the section of this file
that explains more; a file name in them, such as (docs/tools.md), to that file.

**Start here.** For a first change, read "A request's path" and "Layers", then
the recipe for what you add (Recipes, at the end). The other sections and
files are reference: read one when you work on its part.

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
4. Some commands take time, such as a server's `poweroff`. They run
   as an action instead. `lab.Act` checks and plans the action. The action's steps
   call `internal/system`, which drives the machine through `internal/platform`
   (Actions, Platform).

Words this file uses:

| Word | Meaning |
|---|---|
| appliance | a small Linux VM that runs `hlab-agent`; it provides the lab's network, DNS, metadata service and more |
| data plane | what the appliance does for the guests: links, routes, firewall rules, NAT, DHCP, DNS |
| picture | the complete wanted configuration of one part of the data plane; `internal/picture` builds it from the state, and the agent always applies a whole picture, never a single change |
| host link | the network between the host and the appliance, `198.18.0.0/29`; the host is `.1`, the appliance `.2`; WSL2 reaches `hlabd` on `198.18.0.1` |
| flag | an in-memory mark on a resource that an action is working on (Actions) |
| MAC set | `A` or `B`: the appliance's four MAC addresses; an update starts the new appliance with the other set (docs/service.md) |
| `want_running` | whether a server should run; a start of the lab follows it (docs/servers.md) |

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

Everything except `cmd/` and the live tests in `itest/` lives under
`internal/`. The table below goes from the user at the top to the system at
the bottom.

The table also lists packages that do not exist yet; their Job says "(later)". That
way their rule applies from their first line of code.

`internal/layers_test.go` reads this table. It fails when:

- a package imports a package of this module that its "May import" column does not
  list (it checks Linux and Windows files);
- a package is missing from the table.

The "May import" column names packages by their path below `internal/`. "anything"
means the row has no limit. Outside modules are not in it (Dependencies): so
`schema` there is HLab's own `internal/schema`, not `hcloud-go`'s.

| Package | Job | May import |
|---|---|---|
| `cmd/hlabd`, `cmd/hlab`, `cmd/hlab-agent` | wiring only: flags, configuration, putting the parts together | anything |
| `internal/tray` (Windows only) | (later) tray icon and menu | `lab` |
| `internal/cli` | the CLI's commands | `client`, `config`, `labapi` |
| `internal/client` | Go client of the lab control: finds the token, TCP, upload, download | `labapi` |
| `internal/api/cloud` | Cloud API: one file per resource, action tables | `lab`, `web`, `state`, `catalog`, `schema` |
| `internal/api/hetzner` | (later) Hetzner API (Storage Boxes) | `lab`, `web`, `state`, `catalog`, `schema` |
| `internal/api/control` | lab control `/_lab/...` | `lab`, `web`, `state`, `labapi` |
| `internal/labapi` | request and answer types of the lab control, shared with `client` | – |
| `internal/webui` | web UI: files through `go:embed`, Host and Origin checks | `web` |
| `internal/web` | HTTP basics: routes (`net/http`), Hetzner's error format, pagination, sorting, label selectors, rate limit headers, token check, `X-Correlation-Id` | `state` |
| `internal/lab` | the core: owns the state; transactions, actions, flags, timings, triggering pictures, life cycle | `state`, `picture`, `seed`, `system`, `catalog`, `config` |
| `internal/state` | records, kinds, loading, saving, migrations | – |
| `internal/picture` | pure functions from the state to the data plane's pictures and the host's DNS rules | `state`, `agentproto`, `catalog`, `seed` |
| `internal/seed` | pure functions for the cloud-init seed (NoCloud) | – |
| `internal/sshkey` | the lab's SSH key (Ed25519) in OpenSSH's private key format and as a line of authorized_keys | – |
| `internal/system` | does the work and never sees the state: VMs, disks, images, host link, host DNS, appliance with its watchdog, agent client | `platform`, `agentproto`, `vhdx`, `imagefmt` |
| `internal/platform` | the platform interfaces and their data types | – |
| `internal/platform/windows` | Hyper-V through `pshost`, host network, NRPT, life cycle | `platform`, `platform/windows/pshost` |
| `internal/platform/windows/pshost` | long-running PowerShell processes: JSON over stdin and stdout | – |
| `internal/platform/linux` | the Linux platform; for now only its life cycle | `platform` |
| `internal/platform/fake` | fakes for unit tests and `--platform fake`, with a fake agent | `platform`, `agentproto` |
| `internal/platform/contract` | tests only: the contract tests of the platform interfaces, run against the fake and the real platforms | `platform` |
| `internal/agentproto` | the wire format between service and agent, picture types | – |
| `internal/agent` | the agent (Linux only): applies pictures, announces its addresses, metadata service; counters (later) | `agentproto` |
| `internal/vhdx` | (later) the page 83 disk ID in a VHDX's metadata | – |
| `internal/imagefmt` | disk image formats without outside tools: reading qcow2, the footer of a fixed VHD; (later) detecting formats, unpacking | – |
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

- **Kinds.** Each kind of record (`state.Projects`, `state.Tokens`,
  `state.SSHKeys`, `state.Actions`, `state.Servers`, `state.PrimaryIPs`) is a
  `state.Kind[R]`. It knows its record type `R` and its name in the file.
  The lab's own records, which the API does not show, are in `state.Lab`
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
  changes; to look up one record, `state.Find` or `state.ByName`. A small kind
  (servers in the room check) may use `All`.
- **Reads copy nothing.** `state.Read` starts a read-only Tx. Its records are the
  state's own, and calling Put, Delete or NewID on it panics. `lab.View` uses it, so
  the token check on every request copies nothing. That check uses `state.Find`,
  which does not sort as `state.All` does.
- **Only a real change is saved.** Because records can change in place,
  `tx.Changed()` compares each record the Tx noted with the state the Tx started
  from (`reflect.DeepEqual`). A `Change` that only read, or set a value to what it
  already was, saves nothing. A real change encodes the whole state while holding
  the mutex and passes it to the writer.
- **Saving.** A real change encodes the state under the mutex, reusing the
  JSON of the records it did not touch, and `state.Writer` writes
  `state.json` in the background, atomically, retrying on failure
  ([docs/state.md](docs/state.md)). This relies on the rule that a state's
  records never change.
- **Limits.** A project's `Limits` field set to zero means "no limit". So a limit
  added later needs no migration.
- **Versions.** The file has a version (`currentVersion` in `file.go`). An older
  file is migrated when it is loaded, one version at a time: `migrations[n]` lifts
  version `n` to `n+1` on the raw JSON. The old file is kept as `state.json.v<n>`.
  A newer file, or a kind this code does not know, stops the start, because saving
  would lose data.
- **Not in the state:** disks, images, metrics and logs (they have their own files);
  action flags (memory only); tokens in clear text (only their hash is kept).
  A server's root password is in it, in clear text: Hetzner shows it only
  once, but the lab's metadata service and seed hand it to the guest.

## Concurrency

- A mutex in `lab` guards the state, the flags and the running actions. Only code
  that holds the mutex takes records from the state. The mutex is never held during
  a call into `system`.
- **The machine's fields share the lab's mutex.** `lab.Machine` (Actions) has
  no mutex of its own: its fields (the appliance, the host's resources, the
  image loads, the watchdog's notes) are read and changed only while holding
  the lab's mutex. The watchdog and the room check read them together with
  the state and the flags, and a second mutex would add a lock order to
  keep. Two kinds of lock are held across calls to the system and are
  therefore separate: `Machine.pictureMu`, which keeps the pictures in order,
  and the image cache's load lock per image (`Machine.imageLoads`). Neither
  is ever taken while holding the lab's mutex.
- Every change runs in `lab.Change(func(tx *state.Tx) error)`. If the function
  returns an error, the old state stays; otherwise the new state is saved. Reads run
  in `lab.View`, which changes nothing. A `View` whose function always returns
  nil is called as `_ = l.View(...)`; one whose function can fail returns its
  error.
- **A record may outlive its Tx, but only to be read.** A handler may build its
  answer from a record after `Change` or `View` returned, without the mutex. It must
  never change that record: from `View` it is the state's own record, and from
  `Change` the state shares it.
- **Across a call into the system, keep IDs, not records.** Look the records up
  again afterwards with `lab.Current(l, kind, id)`. `Current` returns the record
  to read, or `state.ErrNotFound` if it is gone. It is a function, not a method,
  because Go methods cannot have type parameters. The one exception to the rule
  is `resumeServers` at the start, which reads the records it took once and
  says why next to it.
- The slow part of an action runs in a goroutine with a `context.Context`. Closing
  the lab (`Lab.Close`) and deleting a project (`Lab.StopProjectActions`) cancel
  that context. A closed lab starts no action (`lab.ErrClosed`, 503), and
  `Close` waits for every goroutine of the actions, of
  `Machine.UpdatePicturesLater` and of `Lab.DeleteProject`'s removals.
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
     that with 423 `locked`. A table marked `Queued` (the servers') refuses
     nothing: the new action waits until the one queued before it on the
     resource is done, as Hetzner runs a server's actions one after another.
  2. It calls the row's `Plan`, which checks the request and makes the quick
     changes.
  3. It creates the action. An action without steps is finished at once.

  If any of this fails, nothing changes.
- **`Lab.Create`** records a new resource and runs its first rows in the same
  transaction (a server's `create` and `poweron`, a Primary IP's `assign`),
  so a resource never stays without the action that makes it. If the record
  or a row's plan fails, nothing changes. The API maps the errors of both
  with `actError`.
- **Flags.** While an action's steps run or wait in a queue, the action flags
  its resource and the resources in the plan's `Locks`. Flags live in memory
  only, because after a restart no action is running; they count the actions
  that hold them. A row marked `Unflagged` (such as `change_protection`)
  neither sets nor checks flags. `Lab.ChangeIdle` is a `Change` that refuses a
  flagged resource, for changes that are no action (deleting a Primary IP
  while it is assigned). Note that `locked` in Hetzner's server JSON is an
  administrative lock, not a flag.

  | Row or table | Its resource flagged? | Busy resource | `Locks` busy |
  |---|---|---|---|
  | plain table | yes | 423 `locked` | 423 `locked` |
  | `Queued` table | yes | waits behind the action before it | 423 `locked` |
  | `Unflagged` row | no | runs alongside | 423 `locked` |

  An action that locks a resource of a `Queued` table (a Primary IP's
  assign locks its server) also heads that resource's queue: the
  resource's own actions sent meanwhile wait behind it. Hetzner's answer
  there is not measured.
- **Steps.** The slow part runs in a goroutine. It waits Hetzner's timings with
  `Lab.Sleep(ctx, lab.PowerOff)`, scaled by the time scale. The time scale is 0 in
  unit tests; tests that check timings use their own `lab.Clock`.
  - If the steps fail, the plan's `Undo` takes back the quick changes and the
    action ends with `error`, both in one change. A plan marked `UndoIfLast`
    undoes only if no action was queued behind it on the resource: the later
    plans have set again what it would take back (`want_running`).
  - The watchdog looks at a VM without the mutex, so it acts only if no
    action flagged the server meanwhile: each round notes the servers it
    looks at, an action's flag marks them, and the notes go with the round
    (`Machine.watchRound`).
  - If the steps are cancelled, nothing is undone: a reset removes everything
    anyway, and a stopping lab keeps its state.
- **Where an action's parts live.** `api/*` decides and `lab` does the work
  (Layers), so for a resource `thing` (a Primary IP: `primary_ip`):

  | File | What |
  |---|---|
  | `internal/api/cloud/things.go` | the endpoints and the JSON |
  | `internal/api/cloud/thing_actions.go` | the action table `thingActions(l)` and each row's `plan<Command>`: its checks, quick changes and `Undo` |
  | `internal/lab/things.go` | the steps: methods of `*lab.Machine` |

  - A step method is named after what it does (`PowerOnServer`,
    `ChangePrimaryIP`), takes the context and IDs, and does the whole slow
    part, Hetzner's wait (`Lab.Sleep`) included, so that the timing sits
    with the work.
  - **Every step is a `Machine` method,** also one that only sends the
    pictures and waits, as for Floating IPs, firewalls and networks: the
    pictures go through the machine (`Machine.UpdatePictures`).
  - A plan's `Steps` calls exactly one step method and nothing else
    (`Steps: l.Machine().ChangePrimaryIP` or a closure that passes the
    IDs). The one exception is an action that changes nothing on the
    machine and only waits Hetzner's time: its `Steps` calls `l.Sleep`
    itself (an unassign of a Primary IP that has no server) and says so.
  - What the plans of several resources share stays in `api/cloud`, with the
    resource whose records it changes (`releaseServer` in
    `server_actions.go`).
  - `Lab` keeps the state, the actions and their flags, projects, tokens,
    faults and the time; `Machine` has everything that calls `system` for
    VMs, files, the appliance and its pictures.
- **Restart.** Actions that were running when HLab stopped end with an error when it
  starts again.
- **Size.** Each project keeps its newest `lab.MaxFinishedActions` (1 000) finished
  actions.

## Behaviour that changes must keep

What each part does is in its own file: servers, Primary IPs, projects and
tokens in [docs/servers.md](docs/servers.md); the service, the machine's side,
the lab control and the CLI in [docs/service.md](docs/service.md); the
catalog in [docs/catalog.md](docs/catalog.md). These rules hold across them:

- **Hetzner first.** Answers, status codes, error texts and timings follow the
  real API as measured; each such choice says so next to it ("measured
  2026-11-08") and in `compat/requirements.md`.
- **Creates are whole.** A create records its resource and plans its first
  actions in one `Lab.Create`; every create checks its project
  (`lab.CheckProject`, which `Lab.Act` and `Lab.Create` call). A failed
  create removes what its steps made. Known gap until phase 2a-1: a
  server whose create fails stays in the appliance's pictures until the
  next change sends them (docs/roadmap.md, Known gaps).
- **Names are unique per project**, looked up with `state.ByName`.
- **`want_running` changes only in an action's plan** (and in the create's
  record, and by the watchdog for a guest that shut itself down); steps
  leave it, so queued actions keep the order they were sent in.
- **Room is checked where the records are:** memory and cores in the
  transaction (`Machine.CheckRoom`), the disk before it (`Machine.CheckDisk`, which
  asks the system). The reserves come from `hlab.yaml` (`room`).
- **A server's status** is set by its actions' steps while they run, by the
  watchdog when no action works on it, and by the lab's start, which makes
  every status follow its VM.
- **One server's trouble never stops the lab:** a server that cannot come
  back at a start stays off; cleaning files only logs its failures.
- **The service opens its ports after the machine's side is up,** and sets
  up a new lab (the project `default`) after the ports.
- **The catalog holds only what the lab can run** (x86, the newest image
  versions), with Hetzner's real IDs and names; a user file in the data
  folder overrides it.
- **The lab control's errors carry the lab's own texts** in Hetzner's
  format; the Cloud API's errors come from the `web` constructors.

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
  - A value that another resource of the project already has is 409
    `uniqueness_error`. Its details differ as measured: SSH keys name the field
    (`web.Uniqueness`), servers send `{}` (`web.Refusal`), Primary IPs `null`
    (`web.RefusalNull`).
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
  - the SSH key, server and Primary IP requests, to tell a missing field from
    an empty one;
  - the server answer, to keep Hetzner's order of fields and what `schema`
    lacks.
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
  service), the hypervisor, the host network and the NRPT rules
  (docs/hyperv.md) and the agent dialer (hv_sock).
  `internal/platform/linux` has only the Linux life cycle so far.
- A method joins an interface with the phase that first calls it. Still to come:
  freezing a running VM's disk for snapshots (phase 3a), counters for metrics
  (phase 4), the serial console (phase 6a).
- A NIC's port mode (plain, isolated, promiscuous, access VLAN, trunk) is set only through
  `NICSpec.Port`, when `CreateVM` or `AttachNIC` adds the NIC, and never changes;
  the Linux platform (phase 8) must set it there too. If a later phase needs to
  change a port, the method comes back through the recipe "A new platform method".
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
| `github.com/vishvananda/netlink` | the agent sets links, VLANs, VRFs, addresses and routes, and watches the carrier |
| `github.com/mdlayher/vsock` | the agent listens on AF_VSOCK, which hv_sock reaches from Windows |
| `github.com/Microsoft/go-winio` | hlabd dials the agent over hv_sock on Windows, without cgo |

Developer tools live in a separate module, `tools/go.mod`, so their dependencies
stay out of `go.mod`. Run them with `go tool -modfile=tools/go.mod <tool>`.

| Tool | Why |
|---|---|
| `github.com/golangci/golangci-lint/v2` | the linters of STYLE.md, staticcheck among them |
| `mvdan.cc/sh/v3` (`shfmt`) | formats the shell scripts |

No HTTP router (the patterns of `net/http` are enough), no ORM, no database.

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
   `web.DecodeJSON` and check it with a `web.Check`. A create without an
   action calls `lab.CheckProject` in its `Change` and answers its error with
   `web.TokenInvalid()`; a create with actions runs `Lab.Create`, which checks
   the project itself, and maps its errors with `actError`.
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

**A new action.** Its parts go where the rule says (Actions, Where an
action's parts live). Add a row to the resource's `lab.ActionTable` in
`internal/api/cloud/<thing>_actions.go` with Hetzner's command and action name.

- `Plan` (`plan<Command>`, in the same file) checks the request and makes the
  quick changes.
- `Steps` calls one step method of `*lab.Machine` in `internal/lab/<thing>s.go`
  (such as `Machine.PowerOffServer`), which calls `system` and waits with
  `m.lab.Sleep`, using a timing from `internal/lab/clock.go` that was
  measured on Hetzner; `api/*` never reaches `system` itself. The method keeps
  IDs across its calls and looks records up again with `lab.Current`. A
  resource whose steps only change the data plane calls
  `m.UpdatePictures(ctx)` and then sleeps (`Machine.ChangePrimaryIP`).
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
