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

`task frontend:build` builds only the frontend (Vite, into
`ui\frontend\dist`); it runs `npm ci` first through
`task frontend:install`. `bin\hlab-tray.exe` embeds `dist`. A committed
placeholder file lets Go compile the tray without a frontend build, but
its window then stays empty.

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

```
task test:frontend
```

runs the frontend unit tests in `ui\frontend\tests\unit` with Playwright
Test, without a browser.

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
  the [tool list](setup.md#tools), ansible-core
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

The `UI-` rows test the tray program. The harness builds
`bin\hlab-tray-e2e.exe` with the build tag `e2e`, which opens a WebView2
remote debugging port on 127.0.0.1 and runs beside a tray you may have
open. Each row then runs the Playwright test of its ID from
`ui\frontend\tests\e2e`, which connects to the real tray window over CDP;
this needs the frontend dependencies (`task frontend:install`). `UI-01`
stops and starts the cloud. `UI-02` runs one spec per project section:
Servers and SSH Keys in a project of their own; Volumes, Load Balancers,
Networks, Firewalls, Floating IPs, Primary IPs, DNS, Certificates and
Placement Groups in one shared project `e2e-ui02-res-<time>`, made and
deleted through `hlab`, each creating a resource through its form,
labelling and deleting it; ISOs only checks the list. `UI-04` updates the server types with the
token of the same hcloud context as `CAT-03`, and without one checks
Hetzner's refusal of an invalid token. Release builds never carry the
`e2e` tag.

### Client matrix

```
task e2e -- -run '^TestE2E$/^CLI-0[1-4]$/'
task e2e -- -run 'TestE2E/CLI-02/volumes'
task e2e -- -run '^TestE2E$/^CLI-02$/^servers$'
task e2e -- -run '^TestE2E$/^CLI-0[1-3]$/^zones$'
```

The rows `CLI-01` (hcloud), `CLI-02` (OpenTofu) and `CLI-03` (Ansible in
WSL2) drive one client through every resource type, one subtest per
type: `servers`, `images`, `ssh_keys`, `placement_groups`, `networks`,
`primary_ips`, `floating_ips`, `volumes`, `firewalls`, `load_balancers`,
`certificates`, `zones`, `server_types`, `locations`,
`load_balancer_types` and `isos` (not for OpenTofu, which has no ISO data
source), plus `rdns` for OpenTofu and Ansible (hcloud sets reverse DNS in
each type's row) and `inventory` for Ansible. Each row changes only the
client's endpoint and token, checks every step through the API, deletes
what it made with the same client and checks that it is gone. `CLI-04`
has one row per client (`hcloud`, `tofu`, `ansible`); each builds the
example environment in a project of its own (network, firewall, two
servers, volume, floating IP, load balancer with a TCP service to SSH,
DNS zone), reaches both servers over SSH from Windows and WSL2 on their
public IPv4 and once through the load balancer, and deletes it again.

The rows of one client that need a running server share a fixture
server labelled `client=<client>` and run one after another; the other
rows run in parallel. At most three servers of a run exist at a time, so
a row waits until enough of them are free: the `servers` rows take one,
each `CLI-04` row two. All client rows together took about 14 minutes in
one run; `task e2e` gives the whole suite 120 minutes, about twice its last full run (58 minutes).

`go test` splits a `-run` pattern at the slashes and matches each part
against one level of subtest names. One pattern therefore selects one
group: a single row, all rows of a client, or one type across clients as
in the last example. Rows of different groups, such as `CLI-01/servers`
and `CLI-02/volumes`, need one run each. Anchor the parts (`^servers$`)
where a name is part of another one.

Not covered by the matrix: the datacenter commands, data sources and
module (the API answers 410 as at Hetzner), `hcloud server
request-console` (`unsupported_error`), vSwitch subnets and `hcloud
network expose-routes-to-vswitch`, storage boxes, and `hcloud all list`,
which needs storage boxes from the Hetzner endpoint. The Ansible
`servers` row attaches no ISO, because the `server` module of
hetzner.hcloud 7.2.0 has no ISO option, and the OpenTofu
`load_balancers` row does not read the services from the
`hcloud_load_balancer` data source, which provider 1.70.2 never fills.

The harness runs `task build`, starts the development cloud if it is
stopped (and stops it again at the end if it started it), creates one
project `e2e-<yyyymmdd-hhmmss>` with an SSH key per run, and deletes that
project with everything in it at the end, also after a failed row. At the
start it deletes projects of that exact name pattern that earlier,
interrupted runs left behind; other projects stay. A development cloud
that runs holds `bin\hlabd.exe`, so the harness stops it before the build,
starts it again afterwards and leaves it running at the end. A cloud that
is still starting is waited for (up to 15 minutes) and then handled by the
state it reaches; a cloud that is stopping is waited for until it has
stopped. It refuses to run while
the release cloud (service `hlab`) runs, when
`hlab-dev` points to another working copy, or in an elevated terminal.

## Installers

```
task installer VERSION=1.0.0
```

builds the release installers: the four programs for the `release`
profile with that version into `bin\release\`, the MSI
`bin\hlab-1.0.0.msi` from [installer/Package.wxs](../../installer/Package.wxs)
and the setup program `bin\hlab-setup-1.0.0.exe` from
[installer/Bundle.wxs](../../installer/Bundle.wxs), which embeds the MSI and
the WebView2 bootstrapper. It needs WiX and its two extensions (see
[Developer setup](setup.md#tools)) and names the missing one otherwise.
Its last two lines, `built <path>`, name the MSI and the setup program.

`VERSION` must have the form `x.y.z` with major and minor at most 255 and
patch at most 65535, the limits of an MSI version:

```
> task installer VERSION=1.2
...
task: [installer] go run ./tools/installer 1.2
installer: version "1.2" is not of the form x.y.z (for example task installer VERSION=1.2.3)
exit status 1
task: Failed to run task "installer": exit status 1
```

The WebView2 bootstrapper is downloaded into
`%LocalAppData%\hlab-build-cache` and downloaded again when it is older
than seven days; a failed download uses the cached copy and says so. The
installers are not signed.

Uninstalling a release deletes `%LocalAppData%\hlab` of the uninstalling
user, which holds the OpenAPI cache of the tests; run `task openapi` again
afterwards.

## Elevated installer suite

```
task e2e:elevated
task e2e:elevated -- -run 'TestInstall/INST-05'
```

installs, upgrades and uninstalls the release profile on this machine and
checks what is left in Hyper-V and on the host. Run it in an administrator
terminal; a full run can take several hours. It builds the installers 0.0.1 and 0.0.2
from the working copy (so it needs everything `task installer` needs and
the frontend dependencies), stops the development cloud if it runs and
starts it again at the end, and copies the dev profile's base images and
appliance packages into the release data folder so the release cloud
downloads nothing. The CA rows need a running WSL2 distribution, selected
as for the [end-to-end tests](#end-to-end-tests) (`HLAB_E2E_WSL_DISTRO`).

It refuses to run in a terminal without elevated rights, and while a
release HLab is installed (the service `hlab` or a registered HLab setup
program): uninstall that first. It checks that the VMs and the switch of
the dev profile survive uninstalling the release. When a run fails, its
logs stay in the temporary folder `hlab-e2einstall-*` it names.

## Catalogs

```
task catalog
```

regenerates `catalog\locations.json`, `server-types.json`,
`load-balancer-types.json` and `pricing.json` from the real Hetzner API
(see [Developer setup](setup.md#optional-access-to-the-real-hetzner-api)).
`catalog\images.json` is maintained by hand.

## Icons

```
task icons
```

draws the application icon, the four tray state icons, the icon
resources (`rsrc_windows_amd64.syso`) of `hlabd`, `hlab` and `hlab-tray`,
the frontend's `app-icon.svg` and the README banner `docs\images\banner.svg`
from one geometry in [tools/icons](../../tools/icons). The outputs are
committed; run it only after changing the icon, and commit what it writes.

## README screenshots

```
task docs:screenshots
```

renders the screenshots of the README into `docs\images`: the project
list and the "API tokens" section of a project console, each in light
and dark mode. It needs the development cloud running and the frontend
dependencies (it runs `task frontend:install`). It creates the sample
projects `demo-web`, `demo-staging` and `demo-ci` and a token in
`demo-web`, starts the e2e build of the tray program and runs the
Playwright project `screenshots` from `ui\frontend\tests\screenshots`
against its window. Afterwards it deletes the projects it created; a
token value never appears on a screenshot. Commit the new images.
