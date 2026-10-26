# HLab

A local stand-in for Hetzner Cloud, the Storage Box and "the Internet", in one privileged
Docker container: the FastAPI daemon `hlabd` answers like Hetzner's APIs, servers are real
QEMU/KVM VMs on routed taps. OpenTofu (hcloud provider), the hcloud CLI and Ansible run
against it unchanged. HLab is generic: nothing in it knows about the projects that use it;
they only use `hlab env`.

Everything in this repo is English. Read these before changing code:

- @DEVELOPING.md – how a request flows, the pieces, actions, where code runs, the
  state, recipes, the packet path, tests.
- @STYLE.md – the standards and the project's own rules on top of them.

## Commands

All of them run from the host through Docker (Linux, macOS; Git Bash or WSL on Windows).
Python, pytest and the linters are not needed on the host.

| Command | What it does |
|---|---|
| `bin/hlab lint` | ruff, mypy, shellcheck, shfmt, yamllint, `tofu fmt` in the dev image |
| `bin/hlab format` | apply what the formatters can fix |
| `bin/hlab test` | lint, then the unit tests in the dev image (seconds, no VMs) |
| `bin/hlab -c examples/hlab.yaml up` / `down` | start or stop the lab (state stays in the volume) |
| `bin/hlab -c examples/hlab.yaml itest` | live tests with VMs, resets the lab first (~15 min) |
| `tests/tofu-smoke/run.sh demo` | OpenTofu + hcloud provider against the running lab |

`bin/hlab test` is the reference: it runs every unit test, the full nftables check
(`--cap-add NET_ADMIN`) included. Outside Docker they run from a venv with the specs of
`tools/fetch_specs.py` (DEVELOPING.md, Tests); tests whose tools are missing skip.

## Rules that are easy to break

- **Action table keys are Hetzner's commands** (`add_subnet`, `poweroff`, ...) and the
  action names are Hetzner's (`stop_server`, ...). Never rename them with the functions.
- **`api/` decides, `fabric/` carries out.** `api/` never runs scripts; `fabric/` knows no
  HTTP and decides no state; only `scripts/*.sh` touch the system.
- **The event loop owns the state**: only code on the loop reads or changes it (every
  route is `async def`). Blocking work runs in a thread through
  `await lab.blocking(work, ...)` with plain data in and out, never the state. A saved
  change is `with state.change():`, undone if it raises; it never awaits.
  See DEVELOPING.md, State.
- **New actions go through `lab.act()`**; other code holds flags only through
  `with lab.holding() as held:`, never `lab.claim()`/`lab.release()`. See DEVELOPING.md,
  How flags flow.
- **Records are typed** (`records.py`); annotate `lab: Lab` and use the record types, so
  mypy catches a mistyped field.
- **Hetzner behaviour first:** answers, status codes and error codes as the real API (and
  its changelog) has them; the spec check in the unit tests is the safety net.
- A command `bin/hlab` dispatches is named in its usage comment, and back
  (`tests/test_wrappers.py`).

## Working on the repo

- Do not run `bin/hlab up`, `down`, `purge`, `itest` or `/_lab/reset` while someone else is
  using the lab, and never delete Docker volumes or images by hand.
- Old lab state may not fit a changed record shape: `hlab reset` (or `purge`) the lab and
  say so in the commit message.
