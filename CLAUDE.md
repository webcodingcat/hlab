# Project rules (wcc)

wcc version: 0.36.0

These rules come from the wcc plugin; `/wcc:upgrade` updates them.

## Language and documents

- Everything in the repository is English, the planning documents included.
  The conversation follows the person working with Claude.
- The planning documents live in the hidden folder `.project/` (the Grep and
  Glob tools search it; a plain `rg` needs `--hidden`). IDEA.md is the only
  source for goals and non-goals; REQUIREMENTS, SPEC, ARCHITECTURE and
  ROADMAP follow from it. `.project/STATUS.md` says where the project stands.
- Work from this repository only. Other folders on the machine (an older
  copy, a sibling project) hold decisions nobody approved for this project;
  do not search, read or copy from them.
- User and developer documentation lives where "Documentation" in SPEC.md
  says, describes what is built, and is updated at the close of each
  phase.

## Lists for the user

- What the user is asked to confirm or decide (assumptions, a summary,
  findings, backlog items, options) is written for someone who knows the
  project but not its code: one plain sentence per line, saying what
  happens or what is chosen and what the user will notice. Explain a
  technical term the first time; use code names, file paths and commands
  only where the user acts on them.
- Choices only a developer would weigh (internal structure, interfaces
  between components, storage layout) follow in a short block of their
  own, marked as technical and safe to skip.

## During a phase

Nobody watches a `/wcc:phase` run after its start. In a normal
conversation, ask when something is unclear.

- Ask no questions. Decide with the simplest solution that fits the
  requirements and SPEC.md,
  record it in DECISIONS.md with its "Cost of change", and continue.
- A decision with high consequences (data schema, file formats, public
  URLs and API shapes, authentication, anything needing a migration) that
  REQUIREMENTS.md, SPEC.md, ARCHITECTURE.md and DECISIONS.md do not settle
  is the user's:
  the plan waits.
- Build what the requirements and the test map promise, no more. Repairing and
  adding is allowed (a bug fix, error handling, validation of outside
  input, extending an interface without breaking it); changing and
  replacing is not (renaming or removing in an interface, swapping a part
  of the stack, adding or dropping a component). When in doubt, it is not
  allowed. Removing what the plan's own change left without any caller in
  product and tests is cleanup, also in an internal interface; a public
  interface (API, file format, command line, configuration) keeps it.
- Never build a way into the product or the tests for debugging (a debug
  endpoint, port, account, key, shell access, hidden command or encoded
  payload), and never work around a protection the project builds.
- A plan stops, and is never forced, when it would need something
  destructive, an unsettled decision with high consequences, a forbidden
  change, a missing credential or elevated rights (never fake or obtain
  them), a package that cannot be installed (never try a similar name), or
  when its checks still fail after three repair attempts. The run finishes
  everything else first, then sets `STATUS: BLOCKED`.
- Only the main session writes the planning documents; subagents report.

## Git

- Stage files by name; never `git add -A` or `git add .`.
- Never `git stash`, `git reset --hard`, `git clean`, a forced push or a
  checkout that discards changes.
- A subagent commits only on the branch of its own plan.

## Secrets

- Secrets live in `.env`, never committed; `.env.example` lists the names.
  Credentials from a tool's own login stay with that tool; let the tool
  make the call. Read one out of the tool only where SPEC.md says so, for
  a program that cannot use the tool, inside that one command. Never
  print or write a secret anywhere.

## Tests

- Test what the requirements promise, once, at the cheapest level that proves it.
  Each phase plans its tests in `.project/phases/<id>/TESTS.md`
  first; the tests follow that map, no more.
- No tests for cases a requirement does not name unless they happen in normal
  operation or their failure destroys data or leaks a secret; none for glue
  code, data types or configuration without logic; none that read source
  files. Behaviour many resources share is tested with all its cases where
  it is implemented; across the resources only one case each, to show it
  is wired in.
  Shared helpers are used, not copied.
- End-to-end tests share the environment, expensive steps (a restart) and
  booted machines; one subtest per row of the map, named with its requirement
  ID only, so a filter can select it and still gets the shared setup.
  Independent waiting subtests run in parallel within the capacity of the
  environment; a test takes all it needs from a shared limit (slots,
  machines) in one step. The run logs each test as it starts and ends, with
  a time limit of about twice its last full run.
- No fixed waits; wait for a condition with a deadline. Every waiting step
  has a deadline of its own, well below the test's limit, including a
  framework's built-in wait for an element or a response. Time limits are
  tested with short values or a test clock.
- During development only the tests a change can break run; all end-to-end
  tests run at the end of a milestone.
- Change a test only if it contradicts a requirement; never weaken one to
  pass. A plan that removes or renames something adjusts the tests listed
  under `Adjusts:`: what they use, not what they check.

## Reviews

- Against the requirements, SPEC.md and the test map; decisions in
  ARCHITECTURE.md and DECISIONS.md stand. Blockers are fixed at once,
  including code that provably cannot be needed. What needs a change to
  REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md goes to "Open decisions" in STATUS.md. A
  backlog item needs a real consequence; most reviews have none.

## Build and test commands

All commands run from the repository root, without elevated rights.

- Milestone 2 is developed on Ubuntu 26.04 (Linux host). Until phase 31
  makes the Taskfile build per host OS, the commands below describe the
  Windows machine; phase 31 updates this section.
- Build: `task build` (frontend, `bin/hlabd.exe`, `bin/hlab.exe`,
  `bin/hlab-tray.exe`, Linux `bin/hlab-agent`).
- Windows check from the Linux machine (every phase of milestone 2):
  from phase 31 `task check:windows`; until then
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`, the same with
  `go test -run XXX_none -exec /bin/true ./...` (test binaries) and
  `golangci-lint run ./...`; a failure counts like a failing build.
- On Linux before phase 31, `task build` and `task test` still fail in
  the Windows-only packages (`cmd/hlab-tray`, `internal/platform/hyperv`,
  `test/docs`); phase 31 makes them build per host OS.
- Lint: `task lint` (golangci-lint on `./...` plus svelte-check). One
  package or directory: `task lint -- ./internal/store/...` (Go only);
  frontend only: `task lint:frontend`.
- Fast tests: `task test` (`go test ./...`). One package or directory:
  `task test -- ./internal/store/...`; by name:
  `task test -- ./internal/store/... -run <regexp>`.
- End-to-end tests (slow; real Hyper-V, dev service, hcloud, OpenTofu,
  Ansible in WSL2): `task e2e`; filter by test name with
  `task e2e -- -run <regexp>`, for example `task e2e -- -run 'TestE2E/SRV-01'`.
- Once per machine, elevated, by the user only: `scripts\dev-setup.ps1`
  registers the dev service `hlab-dev` running `bin\hlabd.exe`; on Linux
  (from phase 31) `sudo scripts/dev-setup.sh` installs `hlab-dev.service`
  running `bin/hlabd`, the polkit rule and group `hlab`.

## Repository and CI

- Phases do not push. The repository is
  `https://github.com/webcodingcat/hlab` (public); from milestone 2 on,
  phases run on `wcc/milestone-2` (and so on), and only what `/wcc:release` merges reaches
  `main` (see DECISIONS.md "2026-11-25 Releases through /wcc:release,
  phases do not push"). At the start of a phase, `gh run list --branch main
  --limit 1` shows the last CI run; a red run is fixed first. Once CI
  runs on `wcc/**` (phase 31), a red run on the milestone branch counts
  the same.
