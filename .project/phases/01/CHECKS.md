# Phase 01 manual checks

## DOC-07 README and guides are understandable for their audience

- Start: open the repository on GitHub (light and dark theme) or `README.md`, `docs/` and `docs/development/` in an editor with Markdown preview.
- Setup: none; the dev cloud may be stopped. Claude can prepare a fresh project and token if you want to try the first steps.
- Steps:
  1. README.md on GitHub in light and dark theme: the centred banner with the HLab icon, "HLab" and the subtitle is readable, the two badges and the link line "Guide · Commands · Configuration · Deviations · Development" work, and the two screenshots (project list, project console "API tokens") switch between light and dark.
  2. Read README.md as a developer who tests Hetzner setups and has never seen HLab: what it is, prerequisites for using it (no build tools), installation (no installer yet, link to docs/development/setup.md), first steps, the terminal example, tray program, WSL2, uninstallation. It is short and attractive.
  3. Follow "First steps" once: `bin\hlab.exe cloud start`, create a project and token, set the hcloud context, `hcloud server create ...`, `ssh root@<ip>`, delete the server, `bin\hlab.exe cloud stop`.
  4. Skim docs/guide.md (the per-resource sections moved from the README), docs/commands.md and docs/configuration.md: is each command and key clear?
  5. Read docs/deviations.md as a user: short prose by topic, no routes, at most about 150 lines, with a link to docs/development/api-coverage.md for the details.
  6. Read docs/development/setup.md and build-and-test.md as an outside contributor: tools, `task build`, `scripts\dev-setup.ps1`, `task test`, `task e2e`, `task icons`, `task docs:screenshots`.
  7. Open the help panel (`?`) of every tray view, "API tokens" included: each help text names only views and buttons that exist (Q15).
- Expected: every step works as written; nothing in the texts is unclear, missing or describes something that is not built.
- Result: open
- Note: postponed until the repository is on GitHub (planned at the end); the GitHub light/dark view of step 1 cannot be checked before (2026-11-20)
