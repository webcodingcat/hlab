# Phase 30: Repository and CI

## Interfaces

No new Go types or functions. The phase delivers files that later work
(and `/wcc:release`) builds on; their names and shapes are fixed here:

- `.github/workflows/ci.yml`, workflow name `CI`, triggers `push` to `main`
  and `pull_request` against `main`; jobs `build`, `test`, `lint`,
  `frontend`, `installer` (the job IDs are what `gh run view` shows and
  what the close names on a red run).
- `.github/workflows/release.yml`, workflow name `Release`, trigger
  `push: tags: ['v[0-9]+.[0-9]+.[0-9]+']`; release `vX.Y.Z` with exactly
  the assets `hlab-X.Y.Z.msi`, `hlab-setup-X.Y.Z.exe`, `SHA256SUMS.txt`.
- `SHA256SUMS.txt`: one line per installer file,
  `<64 lowercase hex>␠␠<file name>`, LF line endings, UTF-8 without BOM,
  so `sha256sum -c SHA256SUMS.txt` passes in WSL2.
- `api/openapi.lock`, `api/hetzner-openapi.lock`: format unchanged
  (`{"url": ..., "sha256": ...}`), new hashes.
- `.gitleaksignore` (only if 30-02 finds test fixtures): one gitleaks
  fingerprint per line, each preceded by a `#` comment naming file and
  why the value is no secret.

## Plans

- [ ] 30-01 Renewed OpenAPI pins
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: REL-02 (Go unit tests green on a fresh runner) | Tests: `task test` in full (existing tests, run only); `test/docs/docs_test.go: TestDocs/DOC-03` (existing, run only)
  - Reads: CONTEXT.md (the pin message assumption, answer Q1); DECISIONS.md "2026-11-25 OpenAPI pins renewed for CI", "2026-11-18 OpenAPI copy not committed"; RESEARCH.md "Hetzner OpenAPI description"; `tools/openapi/main.go`; `api/openapi.lock`, `api/hetzner-openapi.lock`; `internal/apitest/openapi.go` (the response checks and their workarounds `upperRangeKeys`, `hetznerServer`, `lbAssignee`); `internal/resources/conventions_test.go` around `openapiDesc`; `docs/development/build-and-test.md` "Fast tests" (the `task openapi` paragraph)
  - Size: ~60 lines in 3 files, plus fixes (if the fixes come to more than about 300 lines, stop after the re-pin, commit nothing of the fixes and report the failing tests grouped by cause)
  - Adjusts: -
  - [ ] Copy the two cached descriptions in `%LocalAppData%\hlab\openapi\` to the scratchpad as the old versions. Download the published `cloud.spec.json` and `hetzner.spec.json` once, write their SHA-256 into `api/openapi.lock` and `api/hetzner-openapi.lock`, and run `task openapi` so that it reports both as cached. Change the mismatch error of `tools/openapi/main.go` so that one plain message names the file and says that Hetzner has changed the published description, that the pin in `api/<lock file>` no longer matches (published hash and pinned hash) and that a deliberate re-pin is needed (`task openapi` after updating the lock file, see `docs/development/releasing.md`).
  - [ ] Run `task test`. Compare the old and new descriptions for the paths and schemas hlab serves (added, removed or changed operations, required fields, enums, status codes) and write the differences that matter to hlab as a short list in the plan's summary. Fix what newly fails in product code; change a test only where it contradicts the new description, and never weaken a check. Where a new Hetzner endpoint or field changes what `api.Deviations()`, `docs/development/api-coverage.md` or `docs/deviations.md` must say, update them so that `TestDocs/DOC-03` holds. Run `task lint`.

- [ ] 30-02 Secret scan of the history
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: REL-01 (history pushed unchanged, no secret in it) | Tests: none (no requirement level applies; the check is the scan itself)
  - Reads: CONTEXT.md (the secret scan assumption); project CLAUDE.md "Secrets"; the files gitleaks names (only the lines it points to)
  - Size: ~30 lines in 1 file
  - Adjusts: -
  - [ ] Check that `gitleaks version` works; if not, stop: the user installs it (`winget install --id Gitleaks.Gitleaks -e`). Run `gitleaks git --redact --no-banner --report-format json --report-path <scratchpad>\gitleaks.json` on the repository (whole history of `main`), and never print or copy a reported value.
  - [ ] Classify each finding by file, commit and rule. A value that is provably not a secret goes into `.gitleaksignore` by its fingerprint, with a `#` comment line naming the file and the reason. That covers keys and certificates generated for tests (`_test.go`, `testdata/`, `ui/frontend/tests/`), documented example tokens in the docs and the planning documents, and hashes that look like keys. Rerun until the scan is clean. Any other finding (a real token, password or private key of a real account) stops the plan without a commit. The report names file, commit and rule only, because the history is pushed unchanged and only the user can decide.

- [x] 30-03 CI workflow
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: REL-02 | Tests: none (REL-02 is manual; the first CI run at the close proves it)
  - Reads: CONTEXT.md (all); RESEARCH.md "GitHub Actions (private repository, free organisation plan)"; `.project/research/github-actions.md` sections 1 to 4; SPEC.md "Repository, CI and releases", "Stack" (versions); `Taskfile.yml`; `tools/installer/main.go` (`checkExtensions`, `fetchWebView2`, the WiX PATH message); `docs/development/setup.md` "Tools"; `.golangci.yml`; `ui/frontend/package.json` (`test:unit`, `check`)
  - Size: ~170 lines in 1 file
  - Adjusts: -
  - [x] Write `.github/workflows/ci.yml` as "Interfaces" says. It runs on `windows-latest` with `permissions: contents: read` and `concurrency: group: ci-${{ github.ref }}`; `cancel-in-progress` holds only for `pull_request` events. Every job does checkout with `actions/checkout@v7`, `actions/setup-go@v7` (`go-version-file: go.mod`, `cache-dependency-path: go.sum`) and `go-task/setup-task@v2` (`version: 3.54.0`). Jobs that touch the frontend also use `actions/setup-node@v7` (`node-version: 24`, `cache: npm`, `cache-dependency-path: ui/frontend/package-lock.json`). Jobs: `build` runs `task build`. `test` resolves the absolute OpenAPI cache folder (`$env:LOCALAPPDATA\hlab\openapi`) in a step into an output, restores it with `actions/cache@v4` keyed on `hashFiles('api/openapi.lock', 'api/hetzner-openapi.lock')`, then runs `task openapi` and `task test`. `lint` installs golangci-lint v2.15.0 with `golangci/golangci-lint-action@v9` `install-only: true`, appends its folder to `$GITHUB_PATH` if `golangci-lint` is not found, falls back to `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0`, then runs `task lint`. `frontend` runs `task test:frontend`. `installer` runs `dotnet tool install --global wix --version 7.0.1`, appends `$env:USERPROFILE\.dotnet\tools` to `$GITHUB_PATH`, runs `wix extension add -g` for `WixToolset.Util.wixext/7.0.1` and `WixToolset.BootstrapperApplications.wixext/7.0.1`, then `task installer VERSION=0.0.0`. It uploads no artifact.
  - [x] Check that the file is valid YAML and that every `task` it calls exists in `Taskfile.yml`. Name in the plan's summary each point that only the first run on GitHub can confirm (PATH after the lint action, `wix extension add` without `wix eula accept`, tests that use PowerShell on the runner).

- [ ] 30-04 Release workflow
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: REL-03 | Tests: none (REL-03 is manual; checked at `/wcc:release`)
  - Reads: CONTEXT.md (release workflow and SHA256SUMS assumptions); RESEARCH.md "GitHub Actions (private repository, free organisation plan)"; `.project/research/github-actions.md` sections 3 and 5; SPEC.md "Repository, CI and releases", "Release"; `tools/installer/main.go` and `tools/installer/version.go` (output names, version limits); the `installer` job of 30-03's interface above (same tool setup)
  - Size: ~90 lines in 1 file
  - Adjusts: -
  - [ ] Write `.github/workflows/release.yml` as "Interfaces" says: `windows-latest`, job-level `permissions: contents: write`, no concurrency cancel. The steps are checkout, setup-go, setup-node and setup-task as in CI, and WiX 7.0.1 with both extensions as in the CI `installer` job. A step sets `VERSION` to `$GITHUB_REF_NAME` without its `v`, and the job stops with a plain error before building when it is not x.y.z within the MSI limits (major and minor at most 255, patch at most 65535). Then `task installer VERSION=$VERSION`, then `SHA256SUMS.txt` in `bin\` written with PowerShell (`Get-FileHash -Algorithm SHA256`, lowercase hash, two spaces, LF, UTF-8 without BOM, through `[IO.File]::WriteAllText`). Last comes `gh release create "$GITHUB_REF_NAME" bin/hlab-$VERSION.msi bin/hlab-setup-$VERSION.exe bin/SHA256SUMS.txt --title "$GITHUB_REF_NAME" --verify-tag --generate-notes` with `GH_TOKEN: ${{ github.token }}`, neither draft nor prerelease. It runs no tests.
  - [ ] Check that the file is valid YAML. In WSL2 or Git Bash, check that the checksum step's PowerShell block produces a file `sha256sum -c` accepts, run against two dummy files in the scratchpad.

- [ ] 30-05 Documentation
  - Wave: 2 | Depends on: 30-01, 30-02, 30-03, 30-04 | Review: alone
  - Requirements: REL-01, REL-03 (README and releasing.md parts), DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: `test/docs/docs_test.go: TestDocs/DOC-01` to `TestDocs/DOC-06` (existing, run only)
  - Reads: CONTEXT.md (all); SPEC.md "Documentation" (D1, D6), "Repository, CI and releases", "Release"; `.github/workflows/ci.yml` and `release.yml` from 30-03 and 30-04; `tools/openapi/main.go` message from 30-01; `.gitleaksignore` from 30-02 if present; README.md (banner, badges, link line, "Installation", "More documentation"); `docs/development/build-and-test.md` "Fast tests" and "Installers"; `docs/development/setup.md` (start and "Tools")
  - Size: ~300 lines in 5 files (plus the licence text)
  - Adjusts: -
  - [ ] Add `LICENSE` with the unchanged Apache License 2.0 text from `https://www.apache.org/licenses/LICENSE-2.0.txt`. Update README.md: add a CI status badge (`https://github.com/webcodingcat/hlab/actions/workflows/ci.yml/badge.svg`) and an Apache-2.0 licence badge linking to `LICENSE`. Rewrite "Installation" so that it says to download `hlab-setup-X.Y.Z.exe` from `https://github.com/webcodingcat/hlab/releases`, check it with `Get-FileHash` against `SHA256SUMS.txt`, and expect a SmartScreen warning because the EXE is unsigned (one sentence, with **More info** and **Run anyway**). Building the installer yourself becomes a link to the developer guide. Add a short "Licence" section at the end linking to `LICENSE`, and link `releasing.md` from "More documentation".
  - [ ] Write `docs/development/releasing.md`. It covers what CI runs, on which events and in which jobs, that the e2e tests and the elevated suite stay local, and how to read a run (`gh run list --branch main --limit 1`, `gh run view`). It covers the free Actions allowance and cancelled pull request runs. It covers the OpenAPI pins: what the pin message means, and re-pinning as a deliberate commit (update the lock file, `task openapi`, `task test`, review the differences). It covers making a release: CI green on the `main` commit, then pushing a tag `vX.Y.Z` (as `/wcc:release` does), what the `Release` workflow builds and publishes, checking with `gh release view vX.Y.Z`, verifying `SHA256SUMS.txt`, and that installers are unsigned. It covers the secret scan with gitleaks before a push and what `.gitleaksignore` holds. Link it from `docs/development/setup.md` and `build-and-test.md` ("Installers": building one yourself stays there). Update the `task openapi` paragraph in "Fast tests" if the message changed. Run `task test -- ./test/docs/...`.

## Close

Done by the main session after 30-05 is reviewed and `wcc/phase-30` is
merged into `main`. Every command runs from the repository root without
elevated rights; no secret is printed.

1. Preconditions: `gh auth status` shows the scopes `repo` and `workflow`,
   `ssh -T git@github.com` greets the user's account, `gitleaks version`
   works, `git status` is clean on `main`, and
   `gh repo view webcodingcat/hlab` reports that the repository does not
   exist yet. If one fails, set BLOCKED with the reason.
2. Secret scan: `gitleaks git --redact --no-banner` over `main`. Any
   finding means nothing is pushed: set BLOCKED with file, commit and rule.
3. Create and push:
   `gh repo create webcodingcat/hlab --private --description "Local Hetzner-compatible cloud on Hyper-V for Windows" --source . --remote origin`,
   then `git push -u origin main`. Push only `main`: no other branch and
   no tag (also not `wcc-milestone-1`).
4. Wait for CI: find the run of the pushed commit with
   `gh run list --commit <sha> --workflow CI --json databaseId,status,conclusion`.
   Wait for it to appear (deadline 5 minutes), then wait for the result
   with `gh run watch <id> --exit-status` (deadline 45 minutes).
5. On red: read the failing jobs with `gh run view <id> --log-failed`,
   repair on `main` as the phase rules allow (environment differences of
   the runner are repaired without changing what a test checks), run the
   affected checks locally, commit, scan again (step 2), push `main` and go
   back to step 4. Do at most three repair rounds; after that set BLOCKED
   and name the failing job and step.
6. Green: record the run URL in the phase summary. REL-01 to REL-03 go to
   the user as manual checks: the repository with LICENSE and README, a
   green CI run including `installer`, and `release.yml` present.
   v0.1.0 follows with `/wcc:release`.
