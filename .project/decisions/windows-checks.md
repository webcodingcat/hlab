# Decision: checking Windows while milestone 2 adds Linux

Status: decided (2026-11-26)

## Question

Milestone 2 adds Linux (KVM/libvirt, systemd, .deb) as a full host next to
Windows. Its phases run on an Ubuntu 26.04 machine on branch
`wcc/milestone-2` and refactor code Windows shares (`internal/cloud` becomes
platform-neutral; `internal/platform`, `internal/hostnet`, `internal/sysint`
get Linux implementations). How is Windows checked meanwhile so the Hyper-V
path does not regress? (Q29)

## Findings

- `.github/workflows/ci.yml` runs only on pushes to `main` and pull requests
  against `main`. Phases do not push, so without a change CI would not see
  milestone 2's Windows code before `/wcc:release`.
- Cross-compiling for Windows works on the Linux machine (checked
  2026-11-26, Go 1.27.4): `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build
  ./...` builds every package including `cmd/hlab-tray` (about 30 s), and the
  Go test binaries compile for Windows (about 5 s). They cannot run there,
  and the WiX installer cannot be built on Linux.

## Decision

1. Every phase of milestone 2 checks Windows locally without pushing: it
   builds all packages with `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`,
   compiles the Go test binaries for Windows (including the `e2e` and
   `e2einstall` build tags), and runs golangci-lint with `GOOS=windows`.
   A failure counts like a failing build of the phase.
2. CI on GitHub's `windows-latest` (build, Go unit tests, linter, frontend
   unit tests, installer build) also runs on pushes to `wcc/**` branches;
   `ci.yml` gets that trigger. The user pushes `wcc/milestone-2` when they
   choose to; phases still do not push. A red CI run on the milestone branch
   is fixed first, like a red run on `main`.
3. At the end of milestone 2 the full Windows end-to-end suite (`task e2e`)
   and the elevated installer suite (`task e2e:elevated`, in the
   development test VM as `docs/development/release-checks.md` describes)
   run once as a manual check the user starts, on the user's existing
   Windows development machine with the `hlab-dev` service. The user pushes
   `wcc/milestone-2` and pulls it there.

No intermediate Windows end-to-end run (for example after the refactor of
`internal/cloud`) is planned.

## Reasons

- Compile and lint errors on the Windows side, the most likely breakage of a
  refactor, show up in the phase that causes them, at no cost to the user.
- CI covers what Linux cannot: Windows unit tests and the installer build,
  whenever the user pushes.
- One real Hyper-V run at the milestone end checks runtime behaviour before
  the release, with the machine and setup that already exist.

## Rejected alternatives

- CI only, no Windows end-to-end run in milestone 2: Hyper-V regressions
  would first surface after the release; "full parity" would leave the
  Windows side unchecked.
- An additional Windows end-to-end run after the refactor of
  `internal/cloud`: a second long manual run that pauses the phase until
  the user reports back; the local cross-compile check and CI catch most of
  what such a refactor breaks.
- A Windows 11 VM with nested Hyper-V under libvirt on the Linux machine, in
  which phases run the Windows suite themselves: much setup, Hyper-V nested
  in KVM is slow and fragile, the WSL2 tests need WSL inside the VM, and the
  suite needs 16 GB RAM in the VM.
- Getting the branch to Windows by `git bundle`: the user pushes instead,
  which also triggers CI.

## Consequences for the plans

- `ci.yml` gets a push trigger for `wcc/**`.
- The phase checks (CLAUDE.md "Build and test commands" or a Task target)
  name the Windows cross-compile and lint step.
- Milestone 2's last phase holds the manual Windows end-to-end check.
