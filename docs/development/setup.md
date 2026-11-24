# Developer setup

How to build HLab from source, register its development service and run
it from your working copy. Users install HLab with the setup program that
`task installer` builds (see [Build and test](build-and-test.md#installers)).
Before a release, the installers are checked in two Hyper-V test VMs; see
[Release checks](release-checks.md).

## Tools

On the Windows host:

- Windows 11 Pro, Enterprise or Education (or Windows Server 2022/2025 with
  Desktop Experience), x64, hardware virtualization on, Hyper-V and its
  PowerShell module enabled, your account in "Hyper-V Administrators".
- Go 1.27.4, Node.js 24 LTS or newer, Task v3.54.0, golangci-lint v2.15.0.
- WiX Toolset v7.0.1 (`dotnet tool install --global wix --version 7.0.1`)
  and Wails v3.1.0
  (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`).
- For the installers: WiX v7 needs its license (the Open Source
  Maintenance Fee EULA) accepted once per user, then its util and
  bootstrapper extensions in the global extension cache:

  ```powershell
  wix eula accept wix7
  wix extension add -g WixToolset.Util.wixext/7.0.1
  wix extension add -g WixToolset.BootstrapperApplications.wixext/7.0.1
  ```
- For the end-to-end tests: hcloud CLI v1.70.2 and OpenTofu 1.13.9 on the
  host, at least 16 GB RAM and 100 GB free disk.

In WSL2 (Ubuntu 24.04 or newer, NAT mode, `dnsTunneling` on): `uv`, then
`uv tool install ansible-core==2.22.0 --with hcloud==2.27.0` and
`ansible-galaxy collection install hetzner.hcloud:==7.2.0`. For the
storage box tests also `rsync`, `borgbackup`, `smbclient` and `lftp`
(`sudo apt install rsync borgbackup smbclient lftp`).

Internet access for image, ISO, package and OpenAPI downloads.

## Build

Clone the repository and build, in a normal terminal in its root:

```powershell
task build
```

The programs land in `bin\`; [Build and test](build-and-test.md#build)
says what the build produces.

## Register the development service

hlabd runs as a Windows service under LocalSystem, because it creates
virtual switches, host routes, NAT and DNS rules. Register it once per
machine, after the first build, in an administrator PowerShell in the
repository:

```powershell
.\scripts\dev-setup.ps1
```

The script runs `bin\hlabd.exe install --dev`, which

- registers the service `hlab-dev` (manual start, LocalSystem) running
  `bin\hlabd.exe` of this working copy,
- lets interactive users query, start and stop it, but not change it,
- creates `%ProgramData%\hlab-dev` with an empty `settings.toml` that
  interactive users may edit.

It also removes an older development service named `hlab` if that one
runs this working copy's `bin\hlabd.exe`.

From then on you build, start, stop and test without elevated rights.
`task build` replaces `bin\hlabd.exe`; stop the cloud before building so the
file is not in use.

**Security:** the service runs a program from a folder your account can
write to, with system rights. Any process of your account can therefore act
with system rights through it. Do this on a development machine only.

To remove it, run `bin\hlabd.exe uninstall-cleanup` in an administrator
terminal. It does for the dev profile what uninstalling the release does
for `hlab`: it stops and deletes the service `hlab-dev`, restores the IPv4
forwarding of the WSL adapter, removes the HLab CA from the trust stores,
removes the host routes, NAT, DNS and firewall rules tagged `hlab-dev`,
deletes the VMs `hlab-dev-appliance` and `hlab-dev-server-<n>` and the
switch `hlab-dev`, and deletes `%ProgramData%\hlab-dev`. `--keep-data`
keeps the data folder. It prints a `note:` line for what it skipped (for
example the CA in WSL with nobody logged on at the console) and exits
non-zero naming each object it could not remove.

## Profiles

Every binary is built for one profile, set at link time in
`internal/profile`. `task build` builds `dev`; a release build uses
`release`.

| | `release` | `dev` |
|---|---|---|
| Service | `hlab` | `hlab-dev` |
| Management pipe | `\\.\pipe\hlab` | `\\.\pipe\hlab-dev` |
| Data folder | `%ProgramData%\hlab` | `%ProgramData%\hlab-dev` |
| Catalogs | `%ProgramFiles%\hlab\catalog` | `catalog\` of the working copy |
| Switch, VM prefix, NAT, NRPT and firewall tag | `hlab` | `hlab-dev` |

Both profiles may be installed side by side, but they share the public
range and `api.hetzner.internal`, so only one cloud runs at a time: each
refuses to start while the other one runs.

## Optional: access to the real Hetzner API

`task catalog` reads the real API. Create an hcloud context named `HLab`
with a read-only token (`hcloud context create HLab`), or set
`HLAB_HCLOUD_CONTEXT` to the name of another context. Without hcloud or the
context, `task catalog` asks for a token and uses it for that run only.
The storage box types come from `https://api.hetzner.com/v1`; with the
context, hcloud fetches them itself
(`hcloud --context <name> --endpoint https://api.hetzner.com/v1 api /storage_box_types`),
so the context's own `hetzner-endpoint` setting does not matter.

Next: [Build and test](build-and-test.md).
