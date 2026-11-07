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

## Concurrency

- A mutex in `lab` guards the state. Only code that holds it reads or changes records.
  It is never held across a call into `system`.
- Every change runs in `lab.Change(func(tx *state.Tx) error)`: if the function returns
  an error, the old state stays; otherwise the new one is saved.
- **Across a call into the system, code keeps IDs, not records**, and looks them up
  again afterwards (`lab.Current(kind, id)` fails when the record is gone).
- The slow part of an action runs as a goroutine with a `context.Context`, which
  shutting down and resetting a project cancel.
- `go test -race` runs on Linux, in `bin/dev test` and in CI.

## Dependencies

As few as possible, each with its reason here.

| Module | Why |
|---|---|
| `golang.org/x/tools` (only `go/packages`) | the layer test reads the imports of every package, for Linux and Windows |

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

**A command for later phases**: `bin/dev` and `bin/dev.ps1` name every command in
their usage text; a command that is not there yet says "not yet implemented".
