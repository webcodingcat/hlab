# hlab

hlab is a local fake Hetzner Cloud on Hyper-V under Windows. Unmodified
Hetzner clients (hcloud CLI, OpenTofu provider hetznercloud/hcloud, Ansible
collection hetzner.hcloud) work against it.

The project is in development; there is no release yet.

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
   `.\scripts\dev-setup.ps1`. It registers the development service `hlab`
   that runs `bin\hlabd.exe` of your working copy as LocalSystem and lets
   interactive users start and stop it. Building, starting and testing then
   need no elevated rights. This setup is for development machines only:
   any process of your account can act with system rights through it.
   `.\scripts\dev-setup.ps1 unregister` removes it. The development service
   and an installed hlab cannot exist on the same machine.
3. Optional: create an hcloud context for the real Hetzner API, named
   `hetzner` (or set `HLAB_HCLOUD_CONTEXT` to its name), with a read-only
   token: `hcloud context create hetzner`. It is used by `task catalog` and
   by the live check of `hlab catalog update`; without it that check is
   skipped.

Build and test:

- `task build` builds all programs into `bin\`.
- `task lint` runs golangci-lint and svelte-check.
- `task test` runs the fast tests.
- `task e2e` runs the end-to-end tests against Hyper-V and the real clients.
