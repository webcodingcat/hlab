# GitHub Actions for phase 30 (research)

Checked 2026-11-25. Read-only; nothing was created or pushed on GitHub. Pages
that print dates without a year are marked "(year assumed)".

## 1. `windows-latest` image

- `windows-latest` = Windows Server 2025 (labels `windows-2025`, `windows-2025-vs2026`); `windows-2022` is the older label.
  https://github.com/actions/runner-images (README)
- Image 20261116.335.1: OS 10.0.26100 build 33828; Go 1.26.11 default (1.25.14 and **1.27.4** cached); Node 22.23.5 default (24.22.3 cached);
  .NET SDK 8.0.x, 9.0.x, 10.0.x and 11.0.100 (default not stated); Git 2.56.1; PowerShell 7.6.10; VS 2022 Enterprise 17.14; gcc 15.2.0;
  WSL listed (v1 enabled, v2 3.0.5.0); Chrome and Edge preinstalled.
  **WiX preinstalled is 3.14.1 (v3): not usable for this project, WiX 7.0.1 must be installed.** go-task and golangci-lint are not preinstalled.
  https://github.com/actions/runner-images/blob/main/images/windows/Windows2025-Readme.md
- Hardware (public repos): 4 vCPU, 16 GB RAM, 14 GB SSD. https://docs.github.com/en/actions/reference/runners/github-hosted-runners
- Nested virtualization / Hyper-V: not documented for Windows runners; expect none (the project's CI excludes e2e anyway).
  Not confirmed either way, which does not matter for the planned jobs.

## 2. Actions (latest at check time)

| Action | Latest | Notes |
|---|---|---|
| actions/checkout | v7 (7.0.1, July, year assumed) | `uses: actions/checkout@v7` |
| actions/setup-go | v7 (7.1.0, 17 Nov, year assumed) | caching on by default: modules and build cache; key is hash of `go.mod` by default, set `cache-dependency-path: go.sum` to key on go.sum; `go-version-file: go.mod` or `go-version: '1.27.4'` (1.27.4 is already in the image tool cache) |
| actions/setup-node | v7 (7.1.0, 8 Oct, year assumed) | `node-version: 24`, `cache: npm`, `cache-dependency-path: ui/frontend/package-lock.json` (lockfile is not in the root) |
| go-task/setup-task | v2 (2.2.0, 9 Aug, year assumed) | `with: version: 3.54.0` (exact or `3.x`), `repo-token: ${{ secrets.GITHUB_TOKEN }}` (default) avoids API rate limits |
| golangci/golangci-lint-action | v9 (needs node24 runtime) | `version: v2.15.0` (default `install-mode: binary`), `args:`, `working-directory:`, `skip-cache`; v8+ supports golangci-lint v2.1+ |

Sources: https://github.com/actions/checkout/releases/latest, https://github.com/actions/setup-go, https://github.com/actions/setup-node,
https://github.com/go-task/setup-task, https://github.com/golangci/golangci-lint-action

- **Lint decision.** `task lint` runs `golangci-lint run` plus svelte-check. The simplest way to keep `task lint` as the single entry point is to install
  the pinned binary with `golangci/golangci-lint-action@v9` (`version: v2.15.0`, `args: --help` is not needed; use the action only to install
  and its cache) or with `install-mode: none` and a manual install, then run `task lint`. Caveat: the action by default also *runs* golangci-lint;
  there is no documented "install only" flag besides `install-mode`. Unclear: whether the action can be used purely as an installer. Safe alternative:
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0` (module path unverified here; v2 needs `/v2`) or download the release zip.
  The action docs advise `.gitattributes` with `*.go text eol=lf` on Windows (gofmt/golines checks fail on CRLF checkouts otherwise).
- **Playwright browsers: not needed.** `task test:frontend` runs `playwright test --project unit` ("Playwright Test without a browser");
  `@playwright/test` is 1.65.1 in `ui/frontend/package.json`. Skip `npx playwright install`. If ever needed:
  `npx playwright install --with-deps chromium` (docs call `--with-deps` the CI form; Windows behaviour of `--with-deps` is not described;
  browsers go to `%USERPROFILE%\AppData\Local\ms-playwright`). https://playwright.dev/docs/browsers
- `npm ci` must run in `ui/frontend` (the Taskfile does this via `frontend:install`).

## 3. WiX 7.0.1 and its fee

- Latest WiX is v7.0.1 (26 Oct 2026), the pinned version is current. https://github.com/wixtoolset/wix/releases
- Install on a runner: `dotnet tool install --global wix --version 7.0.1` (.NET SDKs are preinstalled; `~/.dotnet/tools` is on PATH on GitHub Windows runners, to be verified in the first run), then
  `wix extension add -g WixToolset.Util.wixext/7.0.1` and `wix extension add -g WixToolset.BootstrapperApplications.wixext/7.0.1`.
  `tools/installer` checks `%USERPROFILE%\.wix\extensions\<id>\7.0.1`; that is where `-g` puts them.
- EULA: `tools/installer` already passes `wix build -acceptEula wix7`, the "direct acceptance ... designed for use in build scripts and CI/CD".
  `wix eula accept wix7` is "indirect acceptance, once per user and computer, intended for occasional manual use", so it is **not needed in CI**
  (unclear only whether `wix extension add` itself demands acceptance; if it fails, run `wix eula accept wix7` before it).
  https://docs.firegiant.com/wix/osmf/
- Fee: the OSMF EULA (v1.1) applies "only to Users that use the Software as part of revenue-generating activities"; "Users whose annual gross revenue is less than
  US$10,000 are exempt"; it covers only the pre-compiled Binary Release, "The Fee is not a license fee"; source stays OSI-licensed.
  https://raw.githubusercontent.com/wixtoolset/wix/main/OSMFEULA.txt. The docs page says organizations with more than $10,000 annual revenue
  must sponsor the `wixtoolset` GitHub organization. The EULA text does not address CI or open-source projects specially.
  Reading: a free Apache-2.0 project without revenue may accept the EULA in CI and owes no fee. This is a summary of the text, not legal advice;
  if HLab ever earns revenue (or the org `webcodingcat` does) the fee question reopens. The user, not the agent, should be aware that
  the build script accepts the EULA on behalf of the project.

## 4. Wails CLI

- `github.com/wailsapp/wails/v3 v3.1.0` is only a Go module in `go.mod`; `@wailsio/runtime` 3.1.0 is an npm dependency.
  Neither `Taskfile.yml` nor `tools/installer` calls `wails3`, and no `go:generate` of the CLI was found. **The CLI is not needed in CI.**
  (Only needed if bindings generation were added to a task; not the case.) Not verified by a trial build.

## 5. Release from a tag workflow

- Workflow trigger: `on: push: tags: ['v[0-9]+.[0-9]+.[0-9]+']`; job needs `permissions: contents: write` (GITHUB_TOKEN default is read-only in new orgs/repos,
  so set it explicitly at job level). https://docs.github.com/en/actions (permissions syntax)
- `gh release create "$GITHUB_REF_NAME" bin/hlab-X.msi bin/hlab-setup-X.exe SHA256SUMS.txt --title "$GITHUB_REF_NAME" --generate-notes --verify-tag`
  with `env: GH_TOKEN: ${{ github.token }}`. `gh` is preinstalled on the runners. Files after the tag are uploaded as assets; creation with assets makes a draft,
  uploads, then publishes (not atomic; `--draft` available). https://cli.github.com/manual/gh_release_create
- Alternative softprops/action-gh-release v3.0.4 (5 Nov, year assumed): maintenance release; also needs `contents: write`. No need for a third-party action; prefer `gh`.
  https://github.com/softprops/action-gh-release/releases/latest
- Checksums: `Get-FileHash -Algorithm SHA256` per file, written as `<hash>  <name>` lines (no `sha256sum` format helper on Windows; use PowerShell or `sha256sum` in Git Bash, which is on the runner).
- Version for `task installer VERSION=X.Y.Z`: strip `v` from `$GITHUB_REF_NAME` (`${GITHUB_REF_NAME#v}` in bash).

## 6. Pushing `.github/workflows/*` without the `workflow` scope

- GitHub rejects such pushes from an OAuth app or PAT lacking `workflow` ("refusing to allow an OAuth App to create or update workflow ... without `workflow` scope").
  Fix with `gh auth refresh -s workflow` (needs the user's interactive browser/device login; the agent must not do it silently).
  https://github.com/orgs/community/discussions/26254
- SSH: multiple independent reports say a push over an SSH remote with a write-capable key avoids the check. GitHub's official docs do not state this; I found no
  official statement. Treat as likely but unconfirmed until the first push; with `git_protocol ssh` the `origin` URL from `gh repo create` is an SSH URL.
  Requirements: the user's SSH key must be registered (gh scope `admin:public_key` suggests it is) and must be a user key, not a read-only deploy key.
  Fallback if refused: the user runs `gh auth refresh -s workflow` once.

## 7. `gh repo create webcodingcat/hlab --public --source . --remote origin`

- `OWNER/name` creates the repo in the org; `--source` takes the existing local repo, `--remote` names the remote, **`--push` is needed to push commits**
  (without it nothing is pushed; add `--push`, or `git push -u origin main` afterwards). https://cli.github.com/manual/gh_repo_create
- The org `webcodingcat` exists and has public repos (13), so it is an organization. https://github.com/webcodingcat
- Not confirmed: that the user may create public repos in the org (member permission, org OAuth-app restrictions on the gh app) - fails with a clear error otherwise.
  Remote URL protocol (ssh vs https) follows `gh config git_protocol`; official page does not say, expected ssh.
- Pushing all history includes branches/tags only if pushed explicitly: `--push` pushes the current branch; push `main` and tags with `git push origin main --tags`
  (branch `wcc/phase-16` and others stay local unless pushed).

## Findings that touch SPEC.md "Repository, CI and releases"

- No requirement is infeasible.
- Preinstalled WiX is v3.14: the workflow must install 7.0.1 itself (as the SPEC says "tool versions of setup.md").
- Frontend unit tests need no Playwright browser; no `wails3` install; no `wix eula accept` step (the installer tool passes `-acceptEula wix7`).
- Cache paths: npm cache needs `cache-dependency-path: ui/frontend/package-lock.json`; Go cache via setup-go (modules and build).
- Go 1.27.4 and Node 24 are already in the image tool cache (setup-go/setup-node will not download).
- The `gh repo create` command needs `--push` to push existing history; the SPEC wording "creates it with gh and pushes" fits.
- First release builds the installer: `fetchWebView2` downloads from `go.microsoft.com` into `%LocalAppData%`; needs internet on the runner (available).
- `windows-latest` label may move (windows-2022 ageing); pinning `windows-2025` is optional.
