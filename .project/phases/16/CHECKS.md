# Phase 16 manual checks

Phase 16 stops for a manual check: the release checks of milestone 1 run in
the two test VMs described in `docs/development/release-checks.md`. The
reusable steps live in that guide; each entry here links to its section and
records the result. The development test VM comes first, then the user test
VM; the two need not run at the same time. The full `task e2e` ran green on
the development machine at this close (see PLAN.md "Close").

## DOC-08 The guide describes both test VMs, and they are set up by it

- Start: `docs/development/release-checks.md`, sections "What runs where" and "Create a test VM", on the development machine in an elevated PowerShell.
- Setup: the official Windows 11 ISO; Hyper-V on the development machine. Claude can check the commands against the machine on request, but runs neither script and creates neither VM.
- Steps: 1. Run `scripts\release-check-vm.ps1` for `release-check-dev` and for `release-check-user` as "Create a test VM" says, pressing a key for "boot from CD or DVD" when asked. 2. Wait until each run ends with the checkpoint `fresh`.
- Expected: each run ends without an error; both VMs boot Windows 11 Pro with nested Hyper-V enabled, the subnet check inside passes, no answer ISO is left attached, and each VM is off with a checkpoint `fresh`; any step that differed is reported with `/wcc:report` so the guide or the script is corrected.
- Result: fits (2026-11-25: both VMs set up by `release-check-vm.ps1`; the development test VM also has its tools and the bundle from `release-check-dev-tools.ps1`)

## INST-01 to INST-06 The elevated installer suite passes in the development test VM

- Start: `docs/development/release-checks.md`, section "Development test VM" ("Install the tools", "Bring the repository in", "Register the service and start the dev cloud once", "Run the elevated installer suite").
- Setup: `release-check-dev` from DOC-08; a `git bundle` of `wcc/phase-16` copied in (the setup bundle fix of 16-06 is not on `main` until phase 16 closes).
- Steps: 1. Run `release-check-dev-tools.ps1` in the VM (the tools, then after the restart the clone of the bundle), run `task build` and `scripts\dev-setup.ps1` elevated, start the dev cloud once. 2. In an elevated terminal run `task e2e:elevated`.
- Expected: the suite ends with `ok` for every subtest (INST-01, INST-04, INST-05, INST-06); on a failure the log folder named in the guide stays for the report.
- Result: fits (2026-11-25, sixth run on wcc/phase-16 after 16-14 with the extended INST-06, reported by the user; the fifth run on 24fe51c fitted too)

## INST-03 The setup creates shortcuts that open only the tray program, and "Launch" starts it without admin rights

- Start: `docs/development/release-checks.md`, "User test VM" > "Copy the setup programs in", then "Install and launch (INST-03)".
- Setup: `release-check-user` from DOC-08; `hlab-setup-0.1.0.exe` and `hlab-setup-0.1.1.exe` built on the development machine and copied in.
- Steps: as in the guide section.
- Expected: as in the guide section: "Launch" opens the tray without a UAC prompt, one Start menu and one desktop shortcut, HLab not among the startup apps.
- Result: fits (2026-11-25, release-check-user, hlab-setup-0.1.0.exe)

## NAMES The appliance carries its name after the first cloud start

- Start: `docs/development/release-checks.md`, "Names of the first cloud start".
- Setup: HLab 0.1.0 installed by INST-03, the cloud started once.
- Steps: as in the guide section.
- Expected: Hyper-V Manager in the VM lists `hlab-appliance`, and its console shows `hlab-appliance login:`. The storage VM's name is checked within COMPACT.
- Result: fits (2026-11-25, release-check-user)

## UI-09 The example environment can be created and deleted by hand in the console (with the DOC-07 help texts)

- Start: `docs/development/release-checks.md`, "Example environment in the window (UI-09)".
- Setup: as in the guide section (project `demo` with an SSH key).
- Steps: as in the guide section, opening "?" in the views it passes.
- Expected: every resource of the example environment can be created and deleted in the console, and each help text names only views and buttons that exist.
- Result: fits (2026-11-25, release-check-user from the checkpoint fresh with the rebuilt 0.1.0: default location, server types, state-dependent row entries, typed-name deletes, storage box create toast and help texts checked; the full walk-through fitted earlier the same day)

## INST-06 An update over an older version keeps settings and state

- Start: `docs/development/release-checks.md`, "Update over an older version (INST-06)".
- Setup: HLab 0.1.0 with a project and a running server.
- Steps: as in the guide section: install 0.1.1 over it, start the cloud, reach the server with `ssh`.
- Expected: project, server and settings remain, Settings > Apps and `hlab cloud status` show 0.1.1, and the server answers over SSH after the cloud start.
- Result: fits (2026-11-25, release-check-user, rebuilt 0.1.0 to 0.1.1 with the tray open: no Files In Use dialog, settings, project and server kept, version 0.1.1, ssh answers)

## LIFE-05 A Windows shutdown or restart with the cloud running applies the stop behaviour

- Start: `docs/development/release-checks.md`, "Windows shutdown with a running cloud (LIFE-05)".
- Setup: as in the guide section (server `upd1` running, stop behaviour **Save the VMs' state**).
- Steps: as in the guide section, optionally repeated with **Shut the VMs down**.
- Expected: with **Save the VMs' state** the server keeps its boot ID; with **Shut the VMs down** it gets a new one; Logs show no error about restoring servers.
- Result: fits (2026-11-25, release-check-user, Save the VMs' state: same boot ID after the Windows shutdown, cloud stopped until started, no restore error; the optional Shut down run was not done)

## COMPACT A deleted box gives its space on the host back after a cloud stop

- Start: `docs/development/release-checks.md`, "Storage box space after a delete (COMPACT)".
- Setup: as in the guide section (stop behaviour **Shut the VMs down**, two boxes, a 2 GB random file).
- Steps: as in the guide section.
- Expected: Hyper-V Manager lists `hlab-storage` and its console shows `hlab-storage login:`; `data.vhdx` grows by about 2 GB after the copy and shrinks again after deleting box A and stopping the cloud.
- Result: fits (2026-11-25, release-check-user with the rebuilt 0.1.1: data.vhdx 2.21 GB after a 2 GB copy over SMB, 0.27 GB after deleting the box and stopping the cloud; hlab-storage named correctly, checked earlier the same day)

## INST-05 Uninstalling asks whether to keep the data; with the default answer no file of hlab remains

- Start: `docs/development/release-checks.md`, "Uninstall (INST-05)".
- Setup: HLab installed in `release-check-user`, after all checks above.
- Steps: as in the guide section.
- Expected: the uninstall asks about the data, and with the default answer no HLab file, folder or shortcut remains.
- Result: fits (2026-11-25, release-check-user, 0.1.1 with the default answer: checkbox shown unchecked, tray closed, HLab gone from Apps, no file, folder, shortcut, VM or switch left)

## DOC-07 README and guides are understandable for their audience, help texts name only existing views and buttons

- Start: `docs/development/release-checks.md`, "Reading the documentation", on the development machine; the help texts within UI-09.
- Setup: none.
- Steps: as in the guide section.
- Expected: README, user guides and developer guides read clearly for their audience; the help texts checked during UI-09 name only existing views and buttons.
- Result: fits (2026-11-25: README, user documents and developer guides read on the development machine; the help texts were checked within UI-09)
