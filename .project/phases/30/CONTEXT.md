# Phase 30 context

## Confirmed assumptions

- `LICENSE` is the unchanged Apache-2.0 text; no NOTICE file, no licence headers; README gets a short "Licence" section and a licence badge.
- README gets a CI status badge (visible only to members while the repository is private).
- README "Installation": download `hlab-setup-X.Y.Z.exe` from GitHub Releases and check it against `SHA256SUMS.txt` with `Get-FileHash`; building the installer yourself moves to the developer guide.
- README "Installation" has one sentence on the SmartScreen warning for the unsigned EXE.
- `gh repo create webcodingcat/hlab --private` with a one-line description, all other settings at GitHub's default, no branch protection.
- Only `main` is pushed, at the close after the phase branch is merged; no other branch and no tag (also not `wcc-milestone-1`).
- Before every push, gitleaks scans the whole history of `main` with redacted output; a finding stops the push and sets BLOCKED with file, commit and kind of finding, never the value.
- After every push the close waits up to 45 minutes for the CI run of that commit; on red it repairs, scans and pushes again, at most three repair rounds; only a green run closes the phase, otherwise BLOCKED with the failing job named.
- CI builds the installer with version `0.0.0` and keeps no download.
- The release workflow runs only for tags `vX.Y.Z`, stops before building on a version invalid for an MSI, runs no tests, publishes a normal release with generated notes.
- `SHA256SUMS.txt`: one line `<lowercase hash>  <file name>` (two spaces) per file, so `sha256sum -c` works.
- CI uses the free allowance (12 USD a month, about 1,200 Windows minutes); superseded pull request runs are cancelled.
- If `task openapi` downloads a description whose SHA-256 differs from its pin, it stops with one plain message naming the file, saying Hetzner changed the published description, the pin in `api/<lock>` no longer matches and a deliberate re-pin is needed.
- If the runner lacks something the developer machine has, the phase repairs it without changing what any test checks.

## Technical choices

- `ci.yml`: parallel jobs on `windows-latest` (build, Go tests with `task openapi` first, lint, frontend tests, installer with WiX 7.0.1 dotnet tool and util/bal extensions); setup-go (`go-version-file: go.mod`), setup-node 24, go-task/setup-task 3.54.0, with caches.
- golangci-lint v2.15.0 via `golangci/golangci-lint-action@v9` with `install-only: true`, its folder added to `$GITHUB_PATH`; fallback `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0`; `task lint` stays the one command.
- OpenAPI cache with `actions/cache` on an absolute `%LOCALAPPDATA%\hlab\openapi` path resolved in a step, keyed on both lock files; `%USERPROFILE%\.dotnet\tools` appended to `$GITHUB_PATH`.
- `release.yml`: `push: tags: ['v[0-9]+.[0-9]+.[0-9]+']`, `permissions: contents: write`, `task installer VERSION=<tag without v>`, checksums with `Get-FileHash`, `gh release create --verify-tag --generate-notes` with `GH_TOKEN: ${{ github.token }}`.
- `concurrency` groups per ref; only pull request runs are cancelled.
- Secret scan runs locally as `gitleaks git --redact --no-banner` before each push; none in CI.

## Answers

- Q1 (OpenAPI descriptions in CI): re-pin both lock files now to the currently published descriptions, review the differences and fix what newly fails; CI caches them keyed on the pins. When Hetzner changes them again and the cache is gone, CI turns red with the clear pin message and needs a deliberate re-pin.
