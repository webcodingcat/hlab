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
| `tests/tofu-smoke/run.sh` | OpenTofu + hcloud provider against the running lab (`demo`) |

`bin/hlab test` is the reference: it runs every unit test, including the full nftables
check (`--cap-add NET_ADMIN`); CI runs it on every push and pull request. Outside
Docker the tests run from a venv with the specs that `tools/fetch_specs.py` downloads
(DEVELOPING.md, Tests); tests whose tools are missing skip.

## Rules that are easy to break

DEVELOPING.md explains each one in the section named in parentheses.

- Action table keys are Hetzner's commands, `ActionSpec` names Hetzner's action names;
  never rename them with the functions (Actions and flags).
- `api/` decides and reaches the system only through `lab.fabric`, always in a thread
  (`await lab.blocking(...)`); `fabric/` does the work and never sees the state; while
  the lab runs, only `fabric/` changes the system: through `scripts/*.sh`, plus the
  lab's own files under `/hlab` (The pieces, Layers).
- Only code on the event loop reads or changes the state; blocking work goes through
  `await lab.blocking(...)` with plain data in and out, never the state (Where code
  runs).
- Every state change is inside a `with state.change():` block, which never awaits
  (The state).
- Across an await, code keeps a record's ID, not the record: steps look it up again
  with `lab.current(KIND, id)` (The state, Awaits).
- The state is indexed by `Kind` constants (`lab.state[SERVERS]`), and the lab-only
  records are its typed properties (`lab.state.faults`), never strings (The state,
  Kinds).
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
- A changed record shape bumps the state's version with a migration and its test,
  so old lab state keeps loading (DEVELOPING.md, Recipes: changing a record's shape).
