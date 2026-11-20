# Phase 01 manual checks

## DOC-07 README and guides are understandable for their audience

- Start: open `README.md`, `docs/` and `docs/development/` in the repository (GitHub view or an editor with Markdown preview).
- Setup: none; the dev cloud may be stopped. Claude can prepare a fresh project and token if you want to try the first steps.
- Steps:
  1. Read README.md as a developer who tests Hetzner setups and has never seen hlab: what it is, prerequisites, setup, first steps with hcloud, what does not work yet.
  2. Follow "First steps" once: `bin\hlab.exe cloud start`, create a project and token, set `HCLOUD_ENDPOINT` and `HCLOUD_TOKEN`, `hcloud server create --type cx23 --location fsn1 --image ubuntu-24.04 --ssh-key <key> --name test`, `ssh root@<ip>`, `hcloud server delete test`, `bin\hlab.exe cloud stop`.
  3. Skim docs/commands.md, configuration.md and deviations.md: is each command, key and deviation clear?
  4. Read docs/development/setup.md and build-and-test.md as an outside contributor: could you set up the machine and run `task build`, `task test`, `task e2e`?
  5. Open the help panel (`?`) of every tray view: each help text names only views and buttons that exist (DOC-05, Q15).
- Expected: every step works as written; nothing in the texts is unclear, missing or describes something that is not built.
- Result: does not fit, see plan phases/10/PLAN.md (README holds the developer setup; 2026-11-20)
