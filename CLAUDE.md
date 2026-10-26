# HLab

HLab is a local stand-in for Hetzner Cloud, the Storage Box and "the Internet". It runs in
one privileged Docker container. The FastAPI daemon `hlabd` answers like Hetzner's APIs,
and servers are real QEMU/KVM VMs on routed taps. OpenTofu (hcloud provider), the hcloud
CLI and Ansible work against it without changes. HLab is generic: nothing in it knows
about the projects that use it; they only use `hlab env`.

Everything in this repo is English. Read these before changing code:

- @DEVELOPING.md – how the code fits together, its rules, and how to make the usual changes.
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

DEVELOPING.md explains each one in the section named in parentheses.

- Action table keys are Hetzner's commands, `ActionSpec` names Hetzner's action names;
  never rename them with the functions (Actions and flags).
- `api/` decides and never runs scripts; `fabric/` does the work and never sees the
  state; only `scripts/*.sh` change the system (The pieces, Layers).
- Only code on the event loop reads or changes the state; blocking work goes through
  `await lab.blocking(...)` with plain data in and out (Where code runs).
- Every state change is inside a `with state.change():` block, which never awaits
  (The state).
- New actions are `ActionSpec` rows run by `lab.act()`; other code holds flags only
  through `with lab.holding() as held:` (How flags flow).
- A new flag goes into `Flag` and `FLAGS` in `lab.py`, or a restart keeps it (Recipes).
- Records are typed: `lab: Lab`, the types of `records.py`, `Record` instead of `Any`
  (STYLE.md).
- Hetzner behaviour first: answers, status codes and error codes follow the real API;
  the spec check in the unit tests is the safety net.
- Every command that `bin/hlab` dispatches is named in its usage comment, and the other
  way round (`tests/test_wrappers.py`).

## Working on the repo

- Do not run `bin/hlab up`, `down`, `purge`, `itest` or `/_lab/reset` while someone else
  is using the lab, and never delete Docker volumes or images by hand.
- Old lab state may not fit a changed record shape. Then `hlab reset` (or `purge`) the
  lab and say so in the commit message.
