## Hetzner OpenAPI description

Status: licence question UNCLEAR (no grant found); archive copy UNCONFIRMED (hash not checked).

### Licence and terms of use
- cloud.spec.json carries no `info.license`, `termsOfService` or `contact` (fetched 2026-11-25, first 100 kB of 3.5 MB read; info block is at the top). Source: https://docs.hetzner.cloud/cloud.spec.json
- The docs page https://docs.hetzner.cloud/ has no licence or terms text and does not link the spec files.
- I found no Hetzner GitHub repository that publishes either spec file under a licence. https://github.com/hetznercloud/apis is 404. Searches found none. The hcloud-go repo is MIT, but that covers the client code only. I could not see its `tools` directory contents, so a generator or spec there is unconfirmed. Source: https://github.com/hetznercloud/hcloud-go
- Hetzner's general terms reserve ownership of "specifications, instructions, documentation" for Hetzner and its licensors (US Special Terms 9(c)). They do not grant redistribution rights. Source: https://www.hetzner.com/legal/terms-and-conditions/
- Third parties redistribute derived specs: MaximilianKoestler/hcloud-openapi (MIT, unofficial, converted) and the APIs.guru directory. This is not a grant by Hetzner.
- Conclusion: no explicit permission to commit a copy into an Apache-2.0 repository. Treat it as unlicensed (all rights reserved by default). Safest options: keep the download-and-verify-by-hash flow (lock files only, no spec committed), or ask Hetzner in writing. If a copy is committed anyway, do not label it Apache-2.0, and note it as third-party material in NOTICE.

### Copy with the pinned SHA-256
- Not confirmed. This session has no shell, so I could not download or hash. web.archive.org is blocked for the fetch tool, so I could not list snapshots (CDX).
- The spec URL is a live, unversioned file. I found no versioned Hetzner URL. `info.version` is a fixed "1.0.0", so it does not identify a revision.
- To check by hand:
  - `https://web.archive.org/cdx/search/cdx?url=docs.hetzner.cloud/cloud.spec.json&fl=timestamp,digest`
  - download `https://web.archive.org/web/<ts>id_/https://docs.hetzner.cloud/cloud.spec.json` for timestamps around 2026-11-18, then compute sha256. Use `id_` so the archive does not rewrite the file. Do the same for hetzner.spec.json around 2026-11-21.
- If no snapshot matches, the pin can only be reproduced from a committed copy. That brings back the licence question above.

## Addendum (main session, 2026-11-25): archive check done

- Internet Archive CDX for `docs.hetzner.cloud/cloud.spec.json` lists captures 2026-09-30, 10-06, 10-13, 10-27 and 11-21. The 10-27 capture hashes to `bd14eed9...`, the 11-21 capture to `edbef406...`; neither matches the pin `3f90e2a0...`.
- `docs.hetzner.cloud/hetzner.spec.json` has no capture at all.
- Result: no archived copy with the pinned SHA-256 exists; option (b) of Q1 is not available.
## GitHub Actions (private repository, free organisation plan)

Checked 2026-11-25. Read-only. The Bash tool was not available in this run, so no `gh api` call was made.

### Minutes, cache, Windows rate
- Free plan, private repos: $12 of compute usage per month, expressed as money, not minutes. 500 MB artifact storage. Cache is separate: 10 GB per repository, entries unused for 7 days are evicted, the oldest are evicted first at the limit.
  https://docs.github.com/en/billing/concepts/product-billing/github-actions
- No "multiplier" any more; per-minute prices: Linux 2-core $0.006, Windows 2-core $0.010 (about 1.67x). My arithmetic: about 2,000 Linux minutes or about 1,200 Windows minutes per month.
- Not confirmed: that the organisation `webcodingcat` is on this free allowance, and its spending limit or budget. The docs name no default budget. They say that without a payment method on the account, usage is blocked once the allowance is used up (the same effect as a limit of 0). Check with `gh api orgs/webcodingcat --jq .plan` (plan name) and the org's Billing > Budgets page. The billing endpoints need the `admin:org` scope, so this may fail for the user.

### windows-latest hardware
- Private repo, standard Windows runner: 2 vCPU, 8 GB RAM, 14 GB SSD (public repos get 4 vCPU, 16 GB). This corrects the 4/16 figure in the earlier research, which was for public repos.
  https://docs.github.com/en/actions/reference/runners/github-hosted-runners
- With a 14 GB disk and a smaller CPU, builds are slower than on public runners. Do not budget Windows minutes on the 4-vCPU figures.

### golangci-lint-action v9
- Latest is v9.3.0 (29 Jun, year assumed). The `install-only` input exists (added in v9.0.0, PR #1305): `install-only: true` installs and does not run. Default is `'false'`. Relevant inputs: `version: v2.15.0`, `install-mode: binary` (default; accepts `v2.15.0`) or `goinstall` or `none`, `skip-cache`.
  https://raw.githubusercontent.com/golangci/golangci-lint-action/main/action.yml and https://github.com/golangci/golangci-lint-action/releases
- Example: `uses: golangci/golangci-lint-action@v9` with `version: v2.15.0` and `install-only: true`, then `run: task lint`.
- v2.15.0 exists (released 16 Oct, year assumed, marked Latest). The exact Windows asset name was not visible (81 assets); I did not confirm it.
- Fallbacks from the official install page: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0` (the docs warn that it is not guaranteed and is slower), or in Git Bash `curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.15.0`.
  https://golangci-lint.run/docs/welcome/install/local/
- Unclear: whether the action's `install-only` path adds the binary to PATH for later `run:` steps. Its purpose suggests it does, but I did not read the source. Verify in the first run.

### Cache path and dotnet tools
- actions/cache accepts absolute paths and paths relative to the workspace, plus globs. The docs show `~/` but do not say how `~` is expanded on Windows. Not confirmed that `~\AppData\Local\hlab\openapi` resolves.
  https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching
  Safe choice: use an absolute path such as `${{ runner.temp }}` or `${{ env.LOCALAPPDATA }}\hlab\openapi` (an expression context, resolved before the cache action runs). Save and restore must use the same string. The cache key is also tied to the path and OS.
- Not confirmed from docs that `~/.dotnet/tools` is on PATH after `dotnet tool install --global` on the runner. The .NET SDK adds it for the user on first run on Windows, but a later step may not see it. Fallback: `echo "$env:USERPROFILE\.dotnet\tools" >> $env:GITHUB_PATH` after the install (standard, documented GITHUB_PATH mechanism) or call `wix` by full path. Verify in the first run.

### Summary
- Minutes and cache figures: confirmed. Org spending limit of 0: unclear (no read access; docs imply blocking only without a payment method).
- Hardware: confirmed (2 vCPU, 8 GB, 14 GB).
- golangci-lint-action install-only: confirmed to exist; PATH behaviour unclear.
- `~` in cache path and dotnet tools on PATH: unclear; use absolute paths and GITHUB_PATH.
