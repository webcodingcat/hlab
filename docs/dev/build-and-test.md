# Build and test

All commands run from the repository root without elevated rights.
`task --list` shows every task.

## Build

```
task build
```

builds the tray frontend and `bin\hlabd.exe`, `bin\hlab.exe`,
`bin\hlab-tray.exe` and the static Linux binary `bin\hlab-agent`, all for the
`dev` profile. `task build:agent` builds only `hlab-agent`. hlabd puts the
current `bin\hlab-agent` on the appliance at every cloud start, so a changed
agent needs a rebuild and a cloud restart, not a new appliance.

## Lint

```
task lint
task lint -- ./internal/store/...
task lint:frontend
```

`task lint` runs golangci-lint on `./...` (or the packages after `--`) and
svelte-check on the frontend; `task lint:frontend` runs only svelte-check.

## Fast tests

```
task openapi
task test
task test -- ./internal/resources/...
task test -- ./internal/resources/... -run 'TestResources/API-10'
```

`task test` runs `go test ./...`, or `go test` with the arguments after
`--`. Run `task openapi` once per machine first: it downloads Hetzner's
OpenAPI description, checks it against the SHA-256 pin in
`api/openapi.lock` and caches it in your user cache folder
(`%LocalAppData%\hlab\openapi\cloud.spec.json`). Every API test validates
the responses against it and fails with "run `task openapi`" when it is
missing.

Test names carry the requirement they prove (`TestResources/API-10`), so
`-run` selects one requirement.

## End-to-end tests

```
task e2e
task e2e -- -run 'TestE2E/SRV-01'
```

The end-to-end tests drive the development service, real Hyper-V and the
real clients: hcloud and OpenTofu on Windows, Ansible in WSL2. They take
several minutes. They need:

- the service `hlab-dev` registered from this working copy
  (see [Developer setup](setup.md#register-the-development-service)),
- hcloud on `PATH` and OpenSSH from Windows,
- OpenTofu 1.13.9 as `tofu` on `PATH`; `tofu init` downloads the provider
  hetznercloud/hcloud 1.70.2 into `%LocalAppData%\hlab-e2e\tofu-plugins`,
- one WSL2 distribution with Ubuntu 24.04 or newer, with `curl` and, as in
  the [README](../../README.md#prerequisites-and-setup), ansible-core
  2.22.0 with hcloud-python 2.27.0 and the collection hetzner.hcloud 7.2.0,
- a normal, not elevated, terminal.

The harness checks these versions and installs nothing. It uses the WSL2
distribution named in `HLAB_E2E_WSL_DISTRO`, or else the only registered
distribution whose name starts with `Ubuntu`; it never falls back to the
default distribution. With several Ubuntu distributions, set the variable:

```powershell
$env:HLAB_E2E_WSL_DISTRO = "Ubuntu-24.04"
task e2e
```

It copies the run's SSH key into a temporary folder inside that
distribution and deletes the folder at the end of the run.

The row `CAT-03` runs `hlab catalog update` with the token of the hcloud
context for the real API (`HLab`, or the name in `HLAB_HCLOUD_CONTEXT`; see
[Developer setup](setup.md#optional-access-to-the-real-hetzner-api)); it is
skipped when there is no such context.

The harness runs `task build`, starts the development cloud if it is
stopped (and stops it again at the end if it started it), creates one
project `e2e-<time>` with an SSH key per run, and deletes the run's servers
at the end; the project and its SSH key stay. A running development cloud
holds `bin\hlabd.exe`, so the harness stops it before the build, starts it
again afterwards and leaves it running at the end. It refuses to run while
the release cloud (service `hlab`) runs, when
`hlab-dev` points to another working copy, or in an elevated terminal.

## Catalogs

```
task catalog
```

regenerates `catalog\locations.json`, `server-types.json`,
`load-balancer-types.json` and `pricing.json` from the real Hetzner API
(see [Developer setup](setup.md#optional-access-to-the-real-hetzner-api)).
`catalog\images.json` is maintained by hand.
