# HLab

HLab is a local stand-in for Hetzner Cloud, the Storage Box and "the Internet". A Go
service, `hlabd`, answers like Hetzner's APIs, and servers are real VMs: Hyper-V on
Windows 11 first, used from WSL2; Linux follows later. OpenTofu (hcloud provider), the
hcloud CLI and Ansible are meant to work against it without changes. HLab is generic:
nothing in it knows about the projects that use it.

The rebuild is under way; `docs/roadmap.md` shows the phases and where it stands. The
earlier Python implementation lives on the branch `legacy` (tag `v1-python`).

Everything in this repo is English. Read these before changing code:

- @DEVELOPING.md – how the code fits together, its rules and recipes; it links
  the files under `docs/` that explain each part in detail.
- @STYLE.md – the standards, and the rules this project adds to them.

## Commands

From Linux or WSL2 (`bin/dev.ps1` takes the same commands on Windows):

| Command | What it does |
|---|---|
| `bin/dev lint` | gofmt, go vet and golangci-lint (for Linux and Windows), shfmt, shellcheck, PSScriptAnalyzer, yamllint |
| `bin/dev format` | apply what the formatters can fix |
| `bin/dev test` | the unit tests with the race detector (seconds) |
| `bin/dev test --windows` | the unit tests built for Windows, run through WSL interop |
| `bin/dev test --agent [--host]` | the agent's tests in a network namespace: in Docker, or with `--host` through sudo (CI) |
| `bin/dev build` | `hlabd`, `hlab`, `hlab-agent` for Linux and `hlabd.exe`, `hlab.exe` into `build/` |
| `bin/dev spec-diff` | Hetzner's current specs against `compat/specs` (`--update` takes them over) |
| `bin/dev itest [--run <pattern>]` | the live tests against Hyper-V: built for Windows, run with administrator rights by the task "HLab itest" |
| `bin/dev deploy [--keep-vms]` | the dev loop: build `hlabd`, restart it on Windows through the task "HLab dev" (data `C:\HLabDev\data`), wait until it answers; `--keep-vms` ends it as a crash would |
| `bin/dev.ps1 dev-install` | once, as administrator: `C:\HLabDev`, the tasks "HLab itest" and "HLab dev", the firewall rule "HLab dev", the agent's hv_sock service |
| `bin/dev smoke` | the smoke tests: hcloud CLI and OpenTofu against `hlabd --platform fake` (WSL2 or Linux, end of a phase) |
| `bin/dev appliance` | the appliance's disk, `build/appliance/hlab-appliance.vhdx` (WSL2 or Linux, without root) |
| `bin/dev package` | the folder `install/install.ps1` installs from, `build/package` (after `bin/dev appliance`) |

CI runs lint, the unit tests on Linux and Windows and the agent's tests for every
push and pull request, and `spec-diff` weekly; the live tests stay out of CI.

## Rules that are easy to break

DEVELOPING.md explains each one in the section named in parentheses, or the
file named there.

- A package imports only what its row in the layer table allows; a new package gets a
  row in the table first, which `internal/layers_test.go` reads (Layers).
- `api/*` reaches the system only through `lab`, never through `platform` (Layers).
- `system` never sees the state: it gets IDs, values and pictures (Layers).
- The agent applies whole pictures and decides nothing; a picture changes only
  through `internal/picture` and its golden files (docs/appliance.md).
- The appliance holds only Debian packages and the agent (docs/appliance.md).
- Code holds the lab's mutex only between calls to the system, and across such a call
  it keeps IDs, not records (Concurrency).
- No cgo in the binaries: everything builds in WSL2 for Windows (docs/tools.md).
- A new dependency needs a reason in the dependency table (Dependencies).
- Hetzner behaviour first: answers, status codes and error codes follow the real API;
  where the spec differs, record a deviation in `compat/` (Spec check and compat/).
- Every API test sends its requests through a `spectest` client, so that every
  answer is checked against the spec (Spec check and compat/).
