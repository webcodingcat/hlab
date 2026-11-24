# Phase 16 context

## Confirmed assumptions

- The guide `docs/development/release-checks.md` gives copy-and-paste PowerShell commands the user runs on the development machine to create each VM (Gen2, Secure Boot, vTPM, nested virtualization, static memory); no new script in the repository.
- The guide gives minimum sizes: development test VM 8 vCPU, 24 GB RAM, 200 GB disk; user test VM 4 to 8 vCPU, 16 GB RAM, 150 GB disk.
- Both VMs use Hyper-V's "Default Switch"; HLab builds its own network inside each VM as on a real machine.
- Before the first cloud start in each VM the guide shows the VM's IPv4 subnet (`Get-NetIPAddress`), which must contain neither 198.19.0.0/16 nor 172.31.1.1; on an overlap the guide says to restart the development machine (host) and check the subnet again.
- Windows comes from the official Windows 11 ISO, installed with a local account, unactivated; the guide says how to skip the Microsoft account sign-in.
- The repository reaches the development test VM as a `git bundle` of `main`, copied in from the development machine and cloned there.
- The development test VM gets only what the installer suite needs (Git, Go, Node.js, Task, .NET SDK with WiX 7.0.1 and extensions, Wails, WSL2 with Ubuntu), then runs `scripts\dev-setup.ps1` and starts the dev cloud once.
- The setup EXEs for the user test VM are built on the development machine (`task installer VERSION=0.1.0` and `VERSION=0.1.1`) and copied into the VM; the milestone version itself is decided when the milestone closes.
- The user test VM uses the account Windows creates at setup (administrator under UAC), no WSL2, no development tools; only the built-in SSH client and Explorer.
- Checks in the user test VM go through the tray console: LIFE-05 shuts down the VM with a running server and checks the uptime with `ssh`; COMPACT fills a storage box over SMB from Explorer; UI-09 creates and deletes the example environment by hand; INST-03 and INST-05 install and uninstall through the setup EXE and Settings > Apps.
- Name check: after the first cloud start in the user test VM, Hyper-V Manager shows `hlab-appliance` and its console shows `hlab-appliance login:`; the storage VM `hlab-storage` (VM name and `hlab-storage login:`) is checked after the first box, within the COMPACT check.
- Update check (INST-06): install 0.1.0, create a project with a server, install 0.1.1 over it; project, server and settings remain, tray and Apps show the new version, and after starting the cloud the server answers over SSH. The INST-05 uninstall comes last.
- DOC-07 is checked in the user test VM for the tray help texts; README and guides are read on the development machine.
- The guide recommends a checkpoint of each freshly set up VM while it is off.
- The two test VMs need not run at the same time: development test VM first, then the user test VM; each is shut down (not saved) after use, since nested virtualization rules out saved state.
- The reusable steps of each check run live in the guide; the phase's CHECKS.md links to them and records the results.
- Full end-to-end run: after the documentation plan the phase run executes `task e2e` in full once on the development machine against the dev service; failures are fixed in the run or set BLOCKED. Then `STATUS: MANUAL_CHECK`; the user runs `task e2e:elevated` in the development test VM and the user checks in the user test VM and reports with `/wcc:report`. Only when all pass the phase closes, `STATUS: DONE`, tag `wcc-milestone-1`. If a feedback phase changes code, `task e2e` runs in full again before the tag.

## Technical choices

- One documentation plan writes the guide and links it from `build-and-test.md` ("Elevated installer suite"), `setup.md` and the README link line; the existing DOC-04 and DOC-06 subtests cover it, no new test rows.
- No product code changes unless a check fails; a failure goes through `/wcc:report` as a feedback phase.

## Answers

- Q (test VM network): Hyper-V's Default Switch plus the subnet check in each VM; an own NAT on the development machine would stop the dev cloud (one WinNAT per host). On an overlap: restart the host, check again.
