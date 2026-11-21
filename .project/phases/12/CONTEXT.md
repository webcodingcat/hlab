# Phase 12 context

Confirmed assumptions and the user's corrections at the start of the phase.

## Build and package

- WiX sources in `installer/` (`Package.wxs` MSI, `Bundle.wxs` WixStdBA bundle); `task installer VERSION=x.y.z` builds the release binaries, caches the WebView2 Evergreen bootstrapper (not committed), embeds it and writes `bin\hlab-<version>.msi` and `bin\hlab-setup-<version>.exe`.
- `task installer` rejects a VERSION not of the form x.y.z and one beyond the MSI limits (major and minor at most 255, patch at most 65535).
- The UpgradeCode is one fixed GUID in the repository that never changes; upgrade and downgrade depend on it.
- Per-machine MSI to `%ProgramFiles%\hlab` without folder choice (hlabd.exe, hlab.exe, hlab-tray.exe, hlab-agent, `catalog\*.json`); adds the folder to the system PATH and removes it on uninstall; hidden from "Apps" (ARPSYSTEMCOMPONENT), only the bundle is listed; Manufacturer "webcodingcat"; unsigned.

## INST-01 WebView2

- The bundle installs the bootstrapper silently only when `pv` (HKLM or HKCU) is missing or `0.0.0.0`.
- The e2e test proves install and detection, not the download path (WebView2 is always present on Windows 11); recorded in DECISIONS.md with this reason.

## INST-02 prerequisites

- `hlabd check-prerequisites` holds the unit-tested logic: supported edition/build on x64, virtualization or running hypervisor, Hyper-V platform, vmms, Hyper-V PowerShell module; WSL2 optional, not checked.
- It runs before anything is installed: from the bundle before the MSI chain, or as an immediate custom action from the MSI Binary table, never from an installed hlabd.exe. The planner's choice goes to DECISIONS.md. On failure the user sees the list and setup ends without any change.

## INST-03 shortcuts

- Start menu and desktop shortcuts "HLab" to hlab-tray.exe; never autostart.
- "Launch" on the success page uses WixStdBA's LaunchTarget, which runs in the user's process without elevation; never an MSI custom action (tray and WebView2 would run elevated).

## INST-04 uninstall

- `hlabd uninstall-cleanup` order: stop and remove the service -> restore IPv4 forwarding from `hostnet-restore.json` and then delete that file (also with KEEPDATA) -> remove CA trust -> `hostnet.Down` (NRPT, NAT, firewall group, routes, host IP) -> delete VMs -> delete the switch -> delete files.
- Stopping at uninstall, regardless of the configured stop_behaviour: without KEEPDATA the server VMs are turned off without saved state; with KEEPDATA the guests shut down as with stop_behaviour "shutdown" and 60 seconds, so the kept disks are consistent.
- Matching by exact name only: VMs `<tag>-server-<digits>` (as `internal/discard` does with VMPrefix `<tag>-server-`) and the appliance VM by its exact name; switch, NAT, firewall group and NRPT comment compared for exact equality with the tag, never as a prefix. Test row: a VM `hlab-dev-server-1` and the dev switch survive uninstalling the release.
- `platform.Platform` gets `DeleteSwitch(ctx, name)` (interface extension).
- Errors: something already missing is not an error. Only an existing object that cannot be removed fails the uninstall; cleanup continues with everything else, then exits non-zero naming that object; the MSI rolls back and the product stays installed.
- CA: the thumbprint is read from the CA certificate in the data folder before any file is deleted; no certificate means nothing to remove; never a search by subject. WSL covers only the distros of the logged-on console user, as the trust switch does. A WSL failure or no logged-on user does not fail the uninstall; the message names it and README says so. This closes the BACKLOG item about the CA.
- Data folder outside ProgramData: only the subfolders hlabd creates ({disks,volumes,images,isos,cache,appliance,vms}) and the folder itself if then empty.
- A running hlab-tray is closed before files are replaced or removed.

## INST-05 keep data

- Bundle uninstall page checkbox "Keep projects, servers and settings", unchecked by default, passed as MSI property `KEEPDATA` to `uninstall-cleanup --keep-data`; a quiet uninstall without `KEEPDATA=1` deletes everything.
- With KEEPDATA, VMs, switch, host objects and trust entries are still removed; only the data folder stays. A reinstall recreates VMs through `Reconcile` at the next cloud start (servers boot fresh); trust returns at the next start when the switch in settings.toml is on.
- Without KEEPDATA, the per-user folders of the uninstalling user are deleted by a deferred custom action with Impersonate running as that user; other accounts' folders stay, README says so.
- Research found that the tray uses the Wails v3 default WebView2 user data folder `%AppData%\hlab-tray.exe` (no product code writes `%LocalAppData%\hlab`). The custom action therefore deletes `%AppData%\hlab-tray.exe` and, if present, `%LocalAppData%\hlab`; the tray's data folder is not moved in this phase.

## INST-06 upgrade

- MajorUpgrade; cleanup conditioned on `REMOVE="ALL" AND NOT UPGRADINGPRODUCTCODE`. A running cloud is stopped through the service before files are replaced and not started again; `hlabd install` re-registers in place; a downgrade is refused with a message.

## Tests and documentation

- Elevated suite `test/install` with its own build tag, `task e2e:elevated` (named in SPEC): refuses when a release hlab is installed, stops and restores a running dev cloud, builds installers 0.0.1 and 0.0.2 from the working copy, seeds the release caches from the dev cache, creates project, token, server and trust, uninstalls and checks VMs, switch, routes, NRPT, NAT, firewall group, forwarding, service, Root entry, WSL CA file and folders.
- Documentation: README (installation, uninstallation, keep-data, other accounts' folders, WSL trust note, SmartScreen), `docs/development/build-and-test.md` (`task installer`, `task e2e:elevated`), `setup.md` (WiX extensions).
- The phase stops for the manual check and the user's elevated run.
