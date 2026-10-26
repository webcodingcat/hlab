# HLab

A local stand-in for Hetzner Cloud, the Storage Box and "the Internet", in one privileged
Docker container: the FastAPI daemon `hlabd` answers like Hetzner's APIs, servers are real
QEMU/KVM VMs on routed taps. OpenTofu (hcloud provider), the hcloud CLI and Ansible run
against it unchanged. HLab is generic: nothing in it knows about the projects that use it;
they only use `hlab env`.

Everything in this repo is English. Read these before changing code:

- @DEVELOPING.md – how a request flows, the pieces, actions, where code runs, state and
  locking, recipes, the packet path, tests.
- @STYLE.md – the standards and the project's own rules on top of them.

## Commands

All of them run from the host through Docker (Linux, macOS, Git Bash; `bin/hlab.ps1` on
Windows PowerShell). Python, pytest and the linters are not needed on the host.

| Command | What it does |
|---|---|
| `bin/hlab lint` | ruff, mypy, shellcheck, shfmt, yamllint, `tofu fmt` in the dev image |
| `bin/hlab format` | apply what the formatters can fix |
| `bin/hlab test` | lint, then the unit tests in the dev image (seconds, no VMs) |
| `bin/hlab -c examples/hlab.yaml up` / `down` | start or stop the lab (state stays in the volume) |
| `bin/hlab -c examples/hlab.yaml itest` | live tests with VMs, resets the lab first (~15 min) |
| `tests/tofu-smoke/run.sh demo` | OpenTofu + hcloud provider against the running lab |

The unit tests need the Hetzner OpenAPI specs, which only the images have, and
`--cap-add NET_ADMIN` for the full nftables check; always run them through `bin/hlab test`.

## Rules that are easy to break

- **Action table keys are Hetzner's commands** (`add_subnet`, `poweroff`, ...) and the
  action names are Hetzner's (`stop_server`, ...). Never rename them with the functions.
- **`api/` decides, `fabric/` carries out.** `api/` never runs scripts; `fabric/` knows no
  HTTP and decides no state; only `scripts/*.sh` touch the system.
- **Every change to the state and every loop over it holds `state.lock`**; a saved change
  is `with state.change():`. Slow work (scripts, sleeps) stays outside the lock. Code on
  the event loop never saves: it hands the save to a thread with `await lab.blocking(...)`.
  See DEVELOPING.md, State and locking.
- **New actions go through `lab.act()`**; other code holds flags only through
  `with lab.holding() as held:`, never `lab.claim()`/`lab.release()`. See DEVELOPING.md,
  How flags flow.
- **Records are typed** (`records.py`); annotate `lab: Lab` and use the record types, so
  mypy catches a mistyped field.
- **Hetzner behaviour first:** answers, status codes and error codes as the real API (and
  its changelog) has them; the spec check in the unit tests is the safety net.
- `bin/hlab` and `bin/hlab.ps1` offer the same commands; change both together.

## Working on the repo

- Do not run `bin/hlab up`, `down`, `purge`, `itest` or `/_lab/reset` while someone else is
  using the lab, and never delete Docker volumes or images by hand.
- Old lab state may not fit a changed record shape: `hlab reset` (or `purge`) the lab and
  say so in the commit message.
