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
| `internal/system` | does the work and never sees the state: VMs, disks, images, host link, host DNS, appliance, agent client | `platform`, `agentproto`, `vhdx`, `imagefmt` |
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
     that with 423 `locked`. A table marked `Queued` (the servers') refuses
     nothing: the new action waits until the one queued before it on the
     resource is done, as Hetzner runs a server's actions one after another.
  2. It calls the row's `Plan`, which checks the request and makes the quick
     changes.
  3. It creates the action. An action without steps is finished at once.

  If any of this fails, nothing changes.
- **Flags.** While an action's steps run or wait in a queue, the action flags
  its resource and the resources in the plan's `Locks`. Flags live in memory
  only, because after a restart no action is running; they count the actions
  that hold them. A row marked `Unflagged` (such as `change_protection`)
  neither sets nor checks flags. `Lab.ChangeIdle` is a `Change` that refuses a
  flagged resource, for changes that are no action (deleting a Primary IP
  while it is assigned). Note that `locked` in Hetzner's server JSON is an
  administrative lock, not a flag.
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

## Servers and Primary IPs

The handlers in `internal/api/cloud/servers.go` and `primary_ips.go` decide;
`internal/lab/servers.go` does the slow part with IDs, through `system`.

- **Create** (13.4 of the plan): the handler records the server with its
  Primary IPs (status `initializing`) in one `Change`, then runs the table's
  rows `create` (`create_server`) and, unless `start_after_create` is false,
  `poweron` (`start_server`, the answer's `next_actions`). `Lab.BuildServer`
  takes the system image from the cache, makes the differencing disk, the
  seed and the VM, and sends the appliance the pictures with the server. If
  it fails, the action's undo removes the record and its new Primary IPs.
- **Delete** is the row `delete` (`delete_server`): the record goes at once,
  as on Hetzner, with its Primary IPs that have `auto_delete`; the others
  stay, unassigned. The steps remove the VM, the disk and the seed.
- **Power.** `poweron`, `poweroff`, `shutdown`, `reboot` and `reset` set the
  statuses Hetzner shows (`starting`, `stopping`, …) and wait its times. A
  reboot asks the guest to shut down (Hyper-V has no ACPI reboot), turns it
  off after a minute and starts it; a reboot or reset of an off server starts
  it, as on Hetzner.
- **The watchdog** looks at the running servers' VMs every 2 s: a guest that
  shut itself down leaves its server `off`, and it no longer wants to run.
- **`want_running`** is what a start of the lab follows: a server whose VM
  runs is taken over (hlabd had crashed); one whose VM is off gets its NIC on
  `HLab-Public` again (detached and attached, as a stop of the lab removed the
  switch) and starts if it wants to run. A server without a VM gets a new one
  first (`Lab.restoreServer`): with its disk as it is, if the disk is there
  (an uninstall kept the data folder but removed the VMs); built anew if
  there is no disk (hlabd's end cut its create short). `system.CreateServer`
  keeps a disk that is there and never removes it on a failure. A stop shuts
  every server down, all at once, and turns it off after `shutdown_timeout`
  (`hlab.yaml`, 60 s); `want_running` stays.
- **Room.** Memory is static, so the servers that want to run may take the
  host's memory but a reserve (25 %, at least 6 GB) and the appliance's 1 GB,
  and four virtual cores per thread (`Lab.CheckRoom`, from
  `Hypervisor.HostResources`). Beyond that, a create or a poweron is Hetzner's
  412 `resource_unavailable`. Without the machine (the API's unit tests)
  there is always room.
- **Addresses** come from `198.19.0.0/16` (IPv4, without `.0` and `.255`) and
  `2001:db8:1::/48` (one `/64` per IPv6), in order: `state.Lab.Pools` holds
  where the next search starts, so a released address comes back only once the
  pool has gone round (`lab.NewAddress`).
- **Names and MACs.** A server's VM is `hlab-server-<id>`, its public NIC has
  the MAC `96:00:` and its ID in 32 bits; its disk is
  `disks/servers/<id>.vhdx`, its seed `disks/seeds/<id>.iso`.
- **The lab's key.** `hlabd` keeps an SSH key in the data folder, `lab-key`
  (Ed25519 in OpenSSH's format, `internal/sshkey`, made at the first start,
  readable like the control token). Every server's root takes its public key
  besides the server's own keys, from the seed and the metadata service
  (`picture.ServerKeys`); `hlab ssh` logs in with it. A server created
  before the key existed does not have it.
- **The seed** (`internal/seed`) is what cloud-init reads on Hyper-V, where the
  SMBIOS vendor cannot say "Hetzner": `meta-data` (instance ID, host name,
  keys), `user-data`, `vendor-data` (root's login, the same text the metadata
  service answers) and `network-config` version 2: IPv4 by DHCP and IPv6 as
  `::1` of the `/64` with `fe80::1`, as on Hetzner, always with the lab's
  resolvers. Its files are JSON, which is YAML too: cloud-init's YAML 1.1
  parser reads an unquoted MAC of digits as a number.

**The image cache.** The catalog names each system image's source: the
official qcow2 file (Debian's genericcloud, Ubuntu's cloud image) and its
list of checksums. The first server of an image loads it
(`system.LoadImage`): download, checksum, `internal/imagefmt` reads the
qcow2 in Go and writes the disk as a fixed VHD (the raw disk and a 512-byte
footer), and `FlattenDisk` (`Convert-VHD`) makes it a dynamic VHDX,
`images/cache/<sha256>.vhdx`. `state.Lab.Images` records it by the image's
name; one load per image runs at a time. Converting through the appliance
(`qemu-img`) is for imports and exports (3b). A cached image whose source in
the catalog changed is loaded anew; each start of the lab removes the files
that neither a cache entry nor a server's disk needs, and what a load cut
short left.

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
  4. `Lab.StartMachine` (The machine's side, below): the host link, the
     appliance and its pictures. Ctrl+C ends a start that waits for them.
  5. Open the ports.
  6. Create the project `default` if the lab is new (`Lab.Fresh`: there was no state
     file). This comes after the ports, so a port that is already taken leaves a new
     lab untouched.
  7. Write a new control token through `Lifecycle.WriteSecret`.

  On Windows the three ports listen on the host link's address `198.18.0.1`, and
  the lab control also on `127.0.0.1:8090`; with `--platform fake` all listen on
  `127.0.0.1`. A port of 0 takes any free port (the tests do that), and the status
  reports the ports in use.
- **Ports:** the Cloud API on 8080, the Hetzner API on 8081 (with the Storage Box
  routes), and 8090 for the lab control under `/_lab` and the web UI.
- **Stop** (`close`). The service stops on `POST /_lab/shutdown`, on
  `Lifecycle.StopRequests` (Ctrl+C, SIGTERM) or when a server fails. Then:
  1. The status says `stopping`; every new request gets 503 `unavailable`, as
     Hetzner's API answers while it is down, but for the lab control's status and
     shutdown. The requests that came before finish (up to 10 s).
  2. `Lab.Close`: the actions end, the appliance shuts down (30 s, then off), the
     host's DNS rules and its link go, and the state is written. The lab control
     still answers meanwhile.
  3. The servers stop, and the control token file is removed, so the CLI knows
     the lab is gone.
- **`--platform fake`** uses the fake machine but this machine's real life cycle:
  the lock, the token file and the signals are real. So only one HLab runs, and the
  CLI finds it. The fake machine has the appliance's release disk and a fake agent
  (`fake.Agent`), so `StartMachine` runs there as well.
- **The appliance's release disk** is `hlab-appliance.vhdx` next to `hlabd`, or
  `--appliance <path>`. It stays as it is: the appliance runs on a differencing
  disk of its own in `<data>/appliance`.

### The machine's side

`Lab.StartMachine` does what 13.1 of the plan calls the start on the machine;
`Lab.Close` undoes it. `system` does each step, `lab` only builds the pictures.

1. `system.ConnectHost`: `HostNet.CheckRanges` refuses to start if a route or an
   address outside the lab's switches overlaps `198.18.0.0/15` or
   `2001:db8::/32`, naming the range and the interface. Then `HostNet.SetUp`
   creates or takes over the switches `HLab-Link` (internal, with the host's NIC:
   `198.18.0.1/29`, `2001:db8:0:ffff::1/64`, routes of both ranges through the
   appliance) and `HLab-Public` (private).
2. `system.RemoveStrayVMs`: the lab's VMs (notes `hlab`) that are neither the
   appliance nor a server of the state go; their disks stay.
3. `Appliance.Start`: an appliance that runs and whose agent answers is taken
   over (after a crash of hlabd); otherwise its VM and its differencing disk
   are made anew and started. Then every picture goes to the agent, and the
   watchdog starts.
4. `system.SetHostDNS`: the host's DNS rules; until the zones (2b) only the
   rules of an earlier run go.
5. The servers come back as they want (Servers and Primary IPs), and their
   watchdog starts.

If a step fails, the host link goes again. A crash of hlabd leaves everything
running (13.3); the next start takes it over.

**The watchdog** checks every 2 s that the appliance's VM runs, its heartbeat
answers and its agent answers; on the way it sends the pictures that the agent
lacks (an agent that restarted alone). After three failed checks in a row, or
at once when the VM is off or gone, it turns the VM off and on again with the
same MAC set and sends every picture. A picture that the agent refused (422) is
not sent again until its generation changes.
- **The life cycles:**
  - Windows holds the mutex `Global\HLab`. It writes the path of its data folder to
    `%ProgramData%\HLab\instance`, so a second start can say in its error message
    which folder is in use and the CLI finds the lab's token. It writes the token
    with its own ACL: local users may read it.
  - Linux holds an flock on `/run/lock/hlab.lock`; that file contains the path of
    the data folder. It writes the token with mode `0640` and the group `hlab`, if
    the machine has that group.
- **The log** goes to stderr as text and to `logs/hlabd.log` in the data folder
  as JSON lines (`cmd/hlabd/logfile.go`): hlabd mostly runs without a console
  (the start menu's minimized window, `hlab start`, the dev loop's task). A
  start moves a log of 10 MB or more to `hlabd.log.1`.

## Lab control, CLI and web UI

- **The lab control** lives in `internal/api/control`; its types are in
  `internal/labapi`. Its routes:
  - `GET /_lab/status` and `POST /_lab/shutdown`;
  - projects: `GET|POST /_lab/projects`, `DELETE /_lab/projects/{name}`;
  - tokens: `GET|POST /_lab/projects/{name}/tokens`, `DELETE …/tokens/{token}`,
    where `{token}` is the token's name;
  - the env token: `POST /_lab/projects/{name}/env-token`. It is a POST because the
    first call creates the token;
  - `GET /_lab/projects/{name}/servers/{server}/ssh`, for `hlab ssh`: the
    server's address (Primary IPv4, else the `::1` of its IPv6 network), the
    user `root` and the lab's private key;
  - the Cloud API for a project: `/_lab/projects/{name}/cloud/v1/...`. It runs the
    Cloud API's handlers through `API.ServeAs` with `read_write`.

  It accepts the control token (`Authorization: Bearer`) or whatever
  `Options.Admit` vouches for (the web UI's cookie). Errors have Hetzner's format
  (`web.WriteError`): the lab's `ErrInvalid` is 422 `invalid_input`, `ErrExists` is
  409 `uniqueness_error`, `state.ErrNotFound` is 404 `not_found`.
- **The client** (`internal/client`) finds the token in `HLAB_TOKEN_FILE`. Without
  it, it reads `control.token` in the data folder that the instance file names
  (`client.DataDir`), so it finds the dev loop's lab in `C:\HLabDev\data` as
  well as an installed one:
  - `C:\ProgramData\HLab\instance` on Windows;
  - `/mnt/c/ProgramData/HLab/instance` in WSL2 (detected through
    `WSL_DISTRO_NAME`), with the folder turned into its `/mnt/<drive>` path;
  - without the file, or for a folder not on a drive, the default folder;
    on Linux always `/var/lib/hlab`.

  It calls the address in `HLAB_CONTROL`, or else `http://198.18.0.1:8090`, the host
  link that Windows and WSL2 share. If there is no token file or no answer, it
  returns `client.ErrNotRunning`. A connection gives up after 2 s: once the lab
  has stopped, `198.18.0.1` is gone, and from WSL2 a connection to it would hang
  until the request's timeout of 30 s. An error answer becomes a `*client.Error` with
  its status and code, so the CLI can give a hint (for 401: the token file
  belongs to another `hlabd`).
- **The CLI** (`internal/cli`, cobra) has one file per group of commands; each
  command calls the client. `hlab env` prints the endpoints and the project's env
  token for sh (`export`) or PowerShell (`$env:`), in single quotes.
  - `hlab start` runs `hlabd.exe` with administrator rights and without a window
    (`Start-Process -Verb RunAs -WindowStyle Hidden` in `powershell.exe`, which
    WSL2 reaches through interop): on Windows the one next to `hlab.exe`, in
    WSL2 the installed one in `C:\Program Files\HLab`. Then it asks the status
    every half second, reading the token file anew each time, until the lab
    runs (at most 3 minutes).
  - `hlab stop` sends `POST /_lab/shutdown` and waits the same way until the
    lab no longer answers (`--no-wait` does not).
  - `hlab ssh SERVER [COMMAND...]` gets the address and the lab's key from the
    lab control, writes the key to a temporary file of its own (ssh refuses a
    key that others may read, as the data folder's copy is) and runs `ssh`
    (OpenSSH, also Windows' own) without known hosts: a server's address
    comes back with a new host key after each create. Its exit code is
    ssh's.
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
  service), the hypervisor (Hyper-V, below), the host network and the NRPT
  rules (`hostnet.ps1`, Hyper-V, below) and the agent dialer (hv_sock).
  `internal/platform/linux` has only the Linux life cycle so far.
- A method joins an interface with the phase that first calls it. Still to come:
  freezing a running VM's disk for snapshots (phase 3a), counters for metrics
  (phase 4), the serial console (phase 6a).
- A NIC's port mode (plain, isolated, promiscuous, access VLAN, trunk) is set only through
  `NICSpec.Port`, when `CreateVM` or `AttachNIC` adds the NIC, and never changes;
  the Linux platform (phase 8) must set it there too. If a later phase needs to
  change a port, the method comes back through the recipe "A new platform method".
- `lab` reaches the platform only through `system`, which never sees the state.

## Hyper-V

The Windows hypervisor (`internal/platform/windows/hyperv.go`) runs the commands of
`hyperv.ps1` in long-running PowerShell processes (`pshost`). It needs administrator
rights, which `hlabd` and the live tests have, or the group Hyper-V Administrators.

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
- A VM's power state and heartbeat come from WMI (`Msvm_ComputerSystem`,
  `Msvm_HeartbeatComponent`), not from `Get-VM`: in a long-running process,
  `Get-VM` shows a VM as it was up to seconds ago when another process of
  the pool changed it, so the watchdog took a server that had just started
  for one that had shut down, and an appliance turned off for one running.
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
- `DeleteDisk` asks Hyper-V which VM uses the disk: an off VM does not hold its
  disks open, so the file alone would not tell.

**The host's side** (`hostnet.go`, `hostnet.ps1`) runs in a `pshost` of one
process each for `HostNet` and `HostDNS`; it needs administrator rights.

- `SetUp` creates the internal switch (Windows adds the host's NIC `vEthernet
  (<switch>)`) and the private ones, or takes them over, and makes the NIC's
  addresses and routes exactly those of the link: what is missing is added,
  what is too much goes, Windows' own link-local addresses stay. DHCP is off on
  that NIC.
- Addresses and routes go to the active store only: a restart of Windows clears
  them, and the next start of HLab sets them again. The switches stay until
  `TearDown`, which removes the switches of the last `SetUp`; with the internal
  one go its NIC, addresses and routes.
- `CheckRanges` lists every route and address of the active store, but those of
  NICs on the lab's own switches (`platform.SwitchPrefix`), and Go finds the
  overlaps; default routes do not count.
- The NRPT rules carry the comment `hlab`, so a start finds those of a crashed
  HLab. `Apply` adds and removes only what differs and then empties the DNS
  cache, which would answer from before the rule. A domain `example.com` becomes
  the namespace `.example.com`, which covers the domain and every name below.
- The agent dialer finds the VM's ID through `hyperv.ps1` and dials hv_sock with
  go-winio; the service ID is `VsockServiceID(port)`, which must be registered
  once (`bin/dev.ps1 dev-install`, later the installer). A connection to a VM
  whose agent does not listen yet hangs for seconds, so the appliance's start
  gives each try one second.

## Pictures and the agent

The data plane lives in the appliance, a small Debian 13 VM. hlabd never sends it a
change, only whole pictures; the agent inside makes the appliance match each one.

**Pictures** (`internal/picture`) are pure functions of a `picture.Lab`: plain
values the lab takes from its state and settings (the host link, the MAC set, the
servers with their Primary IPs). Each picture carries the Lab's generation; a
change of the servers or their addresses sends every picture anew
(`Lab.UpdatePictures`, one at a time, so that an older one never follows a
newer one). Their output is compared with golden files in
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
  Windows host or its LAN. So is the lab's own range `198.18.0.0/15`, which the
  uplink's routing table already makes unreachable (The appliance). IPv6 to the
  uplink is rejected (the Default Switch has none), and so is IPv4 from servers
  without Internet.
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
  dnsmasq (only `dnsmasq-base`: the agent runs it), knot, unbound with its trust
  anchor (`dns-root-data`), haproxy, pebble, openssh-server, rsync, borgbackup,
  btrfs-progs, qemu-utils. `--variant=apt` installs no Recommends: a package
  that the appliance needs from them is named.
- **Running:** `hlab-agent` before `sysinit.target`, after the modules (`hv_sock`,
  `vrf`, `8021q`) and the boot ruleset; it must not wait for the network, which it
  sets up itself. networkd only for the uplink (DHCP, matched by both MAC sets,
  `ManageForeignRoutes=no`). unbound as the lab's resolver on `198.18.0.2` and
  `2001:db8:0:ffff::2`. `hlab-dnsmasq` once the agent has its configuration.
- **The uplink's own routing table** (`uplink`, 200): networkd puts the DHCP
  default route there, not into the main table. Rules after the main table
  send only the servers' traffic (from `public`) and the appliance's own (from
  `lo`) to it, and `198.18.0.0/15` is unreachable in it: a lab address that
  nobody holds never leaves through the uplink, and nothing else reaches it.
- **Off until their phase:** knot, haproxy, sshd (its host keys are removed;
  each appliance makes its own in phase 5), and `systemd-networkd-wait-online`.
  pebble has no unit of its own. `serial-getty@ttyS0` is masked: the VM has no
  COM port until the console (6a).
- **The clock:** Hyper-V gives a VM the host's local time as its RTC, which
  Linux reads as UTC, so the appliance starts off by the time zone. Hyper-V's
  time sync (`hv_utils`) corrects it within a second of the boot; the journal
  shows the jump.
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
| `github.com/Microsoft/go-winio` | hlabd dials the agent over hv_sock on Windows, without cgo |

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
| Hyper-V; once as administrator `bin/dev.ps1 dev-install`; Smart App Control off (it blocks self-built programs unpredictably) | Windows 11 Pro | `bin/dev itest`, `bin/dev deploy` |

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
build only with the tag `live`, against the real platform: the contract tests of
the hypervisor, the host network and the NRPT rules
(`internal/platform/contract`), the appliance's boot with its watchdog, and
the servers (`TestServersBootAnswerAndOutliveTheLab`): the whole lab with
the Cloud API in the test's process creates a Debian and an Ubuntu server.

1. Both answer SSH and serve what their metadata service told them (their
   user data starts a small HTTP server on port 8000) over IPv4 and IPv6.
2. With the lab's key (`ssh.exe`), the guest sees the host as `198.18.0.1`
   and `2001:db8:0:ffff::1`, and as `198.18.0.1` from WSL2 (`wsl.exe`, left
   out without WSL or its ssh). It reaches 8080, 8081 and the Internet, not
   8090, and the private ranges behind the uplink answer EHOSTUNREACH
   (admin-prohibited). A ping from one server to the other has TTL 63: the
   isolated ports let them meet only through the appliance.
3. The lab stops (the host keeps no switch, address, route or NRPT rule of
   it) and starts: the same servers on the same addresses with their disks
   (a mark written before), booted again; the Ubuntu server's VM is removed
   meanwhile and comes back with its disk.
4. A crash: another process of the test binary (`TestLabForACrash`) runs the
   lab and is killed; the servers keep running, and the next start takes
   them over without a boot.
5. The appliance is turned off hard; the Debian server answers again within
   10 s.
6. A reset, the guest's shutdown, the deletes, and the host is clean.

The test keeps the system images in `C:\HLabDev\itest\image-cache`, so that
only its first run loads them.

- The host network and the NRPT need administrator rights, without UAC. So the
  tests run through the task "HLab itest" of the Task Scheduler (highest rights,
  S4U: no window, no stored password). `bin/dev itest` compiles them with
  `GOOS=windows go test -c -tags live` into `C:\HLabDev\itest`, copies the
  appliance's disk there if `bin/dev appliance` built a newer one, writes
  `run.cmd` with the test's flags, starts the task with `schtasks /run`, and
  prints `itest.out` as it grows until `itest.rc` holds the exit code.
  `bin/dev.ps1 itest` does the same on Windows with Go.
- Once, as administrator: `bin/dev.ps1 dev-install` creates `C:\HLabDev`,
  which only the developer, the administrators and SYSTEM may write (whatever
  lands there runs elevated; Hyper-V's VMs may read it), the tasks "HLab itest"
  and "HLab dev" (dev loop, below), the firewall rule "HLab dev" and the
  agent's hv_sock service (the registry key of `VsockServiceID(5000)` under
  `GuestCommunicationServices`). From WSL2: `powershell.exe -ExecutionPolicy
  Bypass -File \\wsl.localhost\<distribution>\<repo>\bin\dev.ps1 dev-install`
  in a PowerShell run as administrator. Running it again is safe.
- HLab must not run meanwhile: the tests take its switches, ranges and VM name.
- Disks go to a temporary folder on the Windows disk: Hyper-V cannot use files in
  WSL2's file system.
- The tests remove what they create. Their VMs and disks are named
  `hlab-contract-…` and their VMs carry the notes `hlab-contract`; after an aborted
  run, `Get-VM hlab-contract-* | Stop-VM -TurnOff -Force -Passthru | Remove-VM
  -Force` removes the VMs. The appliance test uses the lab's own names
  (`hlab-appliance`, `HLab-Link`, `HLab-Public`).
- The live tests stay out of CI: GitHub's Windows runners have no nested Hyper-V.
  All of them take about 4 minutes: the hypervisor's 30 s, the host network's 6 s,
  the appliance's 30 s, the servers' 170 s with the images cached (the
  appliance and the servers skip without the appliance's disk).

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

1. It starts `hlabd --platform fake` with a fresh data folder on free ports. Its
   catalog takes `debian-13` from a local HTTP server (python3), as a small
   qcow2 of the tests: the fake machine's servers need no real image.
2. It runs the hcloud CLI with the commands of the phases so far.
3. It runs OpenTofu (`apply`, `plan`, `destroy`) on every `examples/tofu-<phase>`.

The smoke tests pass when nothing fails and the `plan` after `apply` shows no
changes. Each phase adds its commands to `smoke` in `bin/dev` and its configuration
as `examples/tofu-<phase>`. The smoke tests need the provider from the OpenTofu
registry, so they stay out of CI.

**The dev loop.** `bin/dev deploy` runs a freshly built `hlabd` on Windows from
WSL2, without UAC:

1. It builds everything (`bin/dev build`).
2. If an `hlabd.exe` runs, it stops it with `hlab stop` (the servers shut
   down, as at every stop) or, with `--keep-vms`, ends the task with `schtasks
   /end` as a crash would end it: the servers and the appliance keep running,
   and the start takes them over. Then it waits until the process is gone.
3. It copies `hlabd.exe`, `hlab.exe` and a newer appliance's disk to
   `C:\HLabDev\bin`, starts the task "HLab dev" (`hlabd.exe --data
   C:\HLabDev\data`, highest rights, S4U as "HLab itest": no window, so its log
   is `C:\HLabDev\data\logs\hlabd.log`) and waits until `hlab status`
   answers.

It prints how long that took. Measured on a Ryzen AI Max+ 395: about 29 s
with the appliance made anew (stop 9 s, start 20 s), 9 s with `--keep-vms`.
The CLI in WSL2 finds the dev loop's lab by itself (instance file). The task's
firewall rule lets WSL2 reach 8080, 8081 and 8090 on `198.18.0.1`, the servers
8080 and 8081 through the appliance. An installed HLab must not run meanwhile.

**Installing.** `install/install.ps1` installs HLab for users (see its help and
the README), from the folder that `bin/dev package` fills (`build/package`:
`hlabd.exe`, `hlab.exe`, the Linux `hlab`, the appliance's disk, the script).
There is no Windows service and no autostart: HLab runs when the user starts
it (the start menu's link, whose "Run as administrator" flag the script sets
in the `.lnk`, or `hlab start`). `-Uninstall` first stops the installed
`hlabd` (`hlab stop`) and refuses while one from another folder runs. It
removes the VMs with the notes `hlab` (with `-KeepData` their disks stay, and
the next start gives each server a new VM with its disk), the switches `HLab-*` with the host's NIC, the NRPT rules
with the comment `hlab`, and the rest the install added; the Hyper-V feature
stays on. The firewall rule "HLab" is separate from the dev loop's, so both
can be there.

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
