# hlab

hlab is a local fake Hetzner Cloud on Hyper-V under Windows. Unmodified
Hetzner clients (hcloud CLI, OpenTofu provider hetznercloud/hcloud, Ansible
collection hetzner.hcloud) talk to it as if it were the real API, and the
servers they create are real Hyper-V VMs you can reach with SSH from
Windows and from WSL2.

The project is in development; there is no release and no installer yet.
You run hlab from a working copy as described below. The current version
covers projects, tokens, SSH keys and servers; see
[What does not work yet](#what-does-not-work-yet).

## Prerequisites and setup

For developing hlab you need:

- Windows 11 Pro, Enterprise or Education (or Windows Server 2022/2025 with
  Desktop Experience), x64, with hardware virtualization on.
- Hyper-V and its PowerShell module enabled, and your account in the group
  "Hyper-V Administrators". For the end-to-end tests at least 16 GB RAM and
  100 GB free disk.
- On the host: Go 1.27.4, Node.js 24 LTS or newer, Task v3.54.0,
  golangci-lint v2.15.0, WiX Toolset v7.0.1 (`dotnet tool install --global wix`),
  Wails v3.1.0 (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`),
  hcloud CLI v1.70.2 and OpenTofu 1.13.9.
- WSL2 with an Ubuntu 24.04 or newer distribution in NAT mode with
  `dnsTunneling` on (the defaults), and in it:
  - `uv`
  - ansible-core 2.22.0 with hcloud-python 2.27.0:
    `uv tool install ansible-core==2.22.0 --with hcloud==2.27.0`
  - the collection: `ansible-galaxy collection install hetzner.hcloud:==7.2.0`
- Internet access for image, ISO, package and OpenAPI downloads.

Setup:

1. Clone the repository and run `task build`.
2. Once per machine, in an administrator PowerShell in the repository:
   `.\scripts\dev-setup.ps1`. It registers the development service
   `hlab-dev` that runs `bin\hlabd.exe` of your working copy as LocalSystem
   and lets interactive users start and stop it, and it creates the data
   folder `%ProgramData%\hlab-dev`. Building, starting and testing then
   need no elevated rights. This setup is for development machines only:
   any process of your account can act with system rights through it.
3. Optional: create an hcloud context for the real Hetzner API, named
   `HLab` (or set `HLAB_HCLOUD_CONTEXT` to its name), with a read-only
   token: `hcloud context create HLab`. `task catalog` uses it, and asks for
   a token instead when hcloud or the context is missing.

The developer guide in [docs/dev/](docs/dev/setup.md) explains the build,
the tests and the code.

## First steps

Start the cloud. The first start creates the virtual switch and the
appliance VM and takes a few minutes; wait until the status says
`running`:

```
> bin\hlab.exe cloud start
the cloud is starting
> bin\hlab.exe cloud status
state: running
version: dev
```

Create a project and a token for it. The token is printed once and cannot
be shown again:

```
> bin\hlab.exe project create demo
project demo created
> bin\hlab.exe token create --project demo --name ci
<64-character token>
```

Point hcloud at hlab with two environment variables (PowerShell):

```powershell
$env:HCLOUD_ENDPOINT = "http://api.hetzner.internal/v1"
$env:HCLOUD_TOKEN = "<the token>"
```

Then use hcloud as with the real cloud:

```
> hcloud ssh-key create --name demo --public-key-from-file $HOME\.ssh\id_ed25519.pub
SSH Key 2 created
> hcloud server create --name web1 --type cx23 --location fsn1 --image ubuntu-24.04 --ssh-key demo
Waiting for create_server (server: 4) ...
Waiting for create_server (server: 4) ... done
Server 4 created
IPv4: 198.19.0.7
IPv6: 2001:db8:0:4::1
IPv6 Network: 2001:db8:0:4::/64
> ssh root@198.19.0.7
```

The server gets the CPUs, memory and disk of its server type. The first
server with an image downloads that image from its official source, so it
takes longer; later servers reuse the cached copy. SSH answers a few
seconds after `server create` returns, once the guest has booted.

Clean up with `hcloud server delete web1`, `hcloud ssh-key delete demo`,
`bin\hlab.exe project delete demo` and `bin\hlab.exe cloud stop`.

All `hlab` commands are in the [command reference](docs/user/commands.md),
the settings in the [configuration reference](docs/user/configuration.md),
and the differences from the real API in
[Known deviations](docs/user/deviations.md).

## WSL2

From a WSL2 distribution in NAT mode with `dnsTunneling` on (the WSL
defaults), `api.hetzner.internal` and the server addresses work as on
Windows, so hcloud, OpenTofu and Ansible in WSL2 can use the same endpoint
and token. hlab enables IPv4 forwarding for the WSL adapter when the cloud
starts and restores the previous setting when it stops. It does not yet
notice when WSL recreates its adapter (after `wsl --shutdown`); stop and
start the cloud again in that case.

## The release and the development service

Every build belongs to a profile. `task build` builds the `dev` profile:
service `hlab-dev`, data in `%ProgramData%\hlab-dev`, Hyper-V objects named
`hlab-dev...`. A future installed hlab uses the `release` profile with the
name `hlab`. Both may be installed on one machine, but only one cloud runs
at a time: a cloud refuses to start while the other one runs, and
`hlab cloud status` names the reason.

## Removing the development service

In an administrator terminal in the repository:

```
bin\hlabd.exe uninstall-cleanup
```

It stops and removes the service `hlab-dev`, removes the host network
settings it made (route, NAT, DNS rules, firewall rule) and deletes
`%ProgramData%\hlab-dev`. With `--keep-data` the data folder stays. The
Hyper-V VMs and the virtual switch `hlab-dev` stay; remove them in Hyper-V
Manager if you want them gone.

## What does not work yet

- The API offers actions, locations, server types, images, SSH keys and
  servers (create, list, get, delete). Everything else, for example server
  power actions, networks, firewalls, volumes, floating IPs and load
  balancers, answers `not_found`.
- Lists have no pagination and only the filters listed in
  [Known deviations](docs/user/deviations.md).
- A project can be deleted only when it has no resources left.
- `hlab cloud stop` stops the appliance and the host networking, but does
  not save or shut down the server VMs; they stay as they are in Hyper-V.
- There is no tray program and no installer; OpenTofu and Ansible are not
  covered by the tests yet.
