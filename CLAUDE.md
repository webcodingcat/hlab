# HLab

HLab is a local stand-in for Hetzner Cloud, the Storage Box and "the Internet". It runs in
one privileged Docker container. The FastAPI daemon `hlabd` answers like Hetzner's APIs,
and servers are real QEMU/KVM VMs on routed taps. OpenTofu (hcloud provider), the hcloud
CLI and Ansible work against it without changes. HLab is generic: nothing in it knows
about the projects that use it; they only use `hlab env`.

Everything in this repo is English. Read these before changing code:

- @DEVELOPING.md – how the code fits together and how to make the usual changes.
- @STYLE.md – the standards, and the rules this project adds to them.

## Commands

All of them run from the host through Docker (Linux, macOS; Git Bash or WSL on Windows).
Python, pytest and the linters are not needed on the host.

| Command | What it does |
|---|---|
| `bin/hlab lint` | ruff, mypy, shellcheck, shfmt, yamllint in the dev image; `tofu fmt` in OpenTofu's image |
| `bin/hlab format` | apply what the formatters can fix |
| `bin/hlab test` | lint, then the unit tests in the dev image (seconds, no VMs) |
| `bin/hlab -c examples/hlab.yaml up` / `down` | start or stop the lab (state stays in the volume) |
| `bin/hlab -c examples/hlab.yaml itest` | live tests with VMs, resets the lab first (~15 min) |
| `tests/tofu-smoke/run.sh demo` | OpenTofu + hcloud provider against the running lab |

`bin/hlab test` is the reference: it runs every unit test, including the full nftables
check (`--cap-add NET_ADMIN`). Outside Docker the tests run from a venv with the specs
that `tools/fetch_specs.py` downloads (DEVELOPING.md, Tests); tests whose tools are
missing skip.

## Rules that are easy to break

- **Action table keys are Hetzner's commands** (`add_subnet`, `poweroff`, ...), and the
  action names are Hetzner's (`stop_server`, ...). Never rename them when you rename the
  functions.
- **`api/` decides, `fabric/` does the work.** `api/` never runs scripts. `fabric/`
  knows nothing about HTTP and never decides about the state. Only `scripts/*.sh`
  change the system.
- **The event loop owns the state.** Only code on the loop reads or changes it (every
  route is `async def`). Blocking work runs in a thread through
  `await lab.blocking(work, ...)`, with plain data in and out, never the state. A change
  that is saved is a `with state.change():` block. It is undone if it raises, and it
  never awaits. See DEVELOPING.md, State.
- **New actions go through `lab.act()`**: add an `ActionSpec(...)` row to the
  resource's table. Other code holds flags only through `with lab.holding() as held:`,
  never through `lab.claim()` or `lab.release()`. Flags are fields that only the lab
  uses (`_locked`, `_busy`, ...; see `FLAGS`). The API shows a server's `_locked` as
  `locked`. See DEVELOPING.md, How flags flow.
- **Records are typed** (`records.py`). Annotate `lab: Lab` and use the record types, so
  mypy catches a mistyped field. Code that works for every kind takes `Record` (or a
  type variable bound to it), not `Any`.
- **Hetzner behaviour first:** answers, status codes and error codes follow the real API
  and its changelog. The spec check in the unit tests is the safety net.
- Every command that `bin/hlab` dispatches is named in its usage comment, and the other
  way round (`tests/test_wrappers.py`).

## Working on the repo

- Do not run `bin/hlab up`, `down`, `purge`, `itest` or `/_lab/reset` while someone else
  is using the lab, and never delete Docker volumes or images by hand.
- Old lab state may not fit a changed record shape. Then `hlab reset` (or `purge`) the
  lab and say so in the commit message.
