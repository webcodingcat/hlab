# HLab

HLab is a local stand-in for Hetzner Cloud, the Storage Box and "the Internet". A Go
service, `hlabd`, answers like Hetzner's APIs, and servers are real VMs: Hyper-V on
Windows 11 first, used from WSL2; Linux follows later. OpenTofu (hcloud provider), the
hcloud CLI and Ansible are meant to work against it without changes. HLab is generic:
nothing in it knows about the projects that use it.

The rebuild is under way; `docs/roadmap.md` shows the phases and where it stands. The
earlier Python implementation lives on the branch `legacy` (tag `v1-python`).

Everything in this repo is English. Read these before changing code:

- @DEVELOPING.md – how the code fits together, its rules, the tools.
- @STYLE.md – the standards, and the rules this project adds to them.

## Commands

From Linux or WSL2 (`bin/dev.ps1` takes the same commands on Windows):

| Command | What it does |
|---|---|
| `bin/dev lint` | gofmt, go vet and golangci-lint (for Linux and Windows), shfmt, shellcheck, PSScriptAnalyzer, yamllint |
| `bin/dev format` | apply what the formatters can fix |
| `bin/dev test` | the unit tests with the race detector (seconds) |
| `bin/dev test --windows` | the unit tests built for Windows, run through WSL interop |
| `bin/dev build` | `hlabd`, `hlab`, `hlab-agent` for Linux and `hlabd.exe`, `hlab.exe` into `build/` |

`bin/dev test --agent` and `bin/dev spec-diff` come with later phases and say so.
CI runs lint and the unit tests on Linux and Windows for every push and pull request.

## Rules that are easy to break

DEVELOPING.md explains each one in the section named in parentheses.

- A package imports only what its row in the layer table allows; a new package gets a
  row in the table and in `internal/layers_test.go` first (Layers).
- `api/*` reaches the system only through `lab`, never through `platform` (Layers).
- `system` never sees the state: it gets IDs, values and pictures (Layers).
- Code holds the lab's mutex only between calls to the system, and across such a call
  it keeps IDs, not records (Concurrency).
- No cgo in the binaries: everything builds in WSL2 for Windows (Tools).
- A new dependency needs a reason in the dependency table (Dependencies).
- Hetzner behaviour first: answers, status codes and error codes follow the real API.
