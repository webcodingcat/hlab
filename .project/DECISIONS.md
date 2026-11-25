# DECISIONS

Every decision Claude takes without asking, and every assumption, is recorded
here, as long as it holds. Newest entries first.

Format:

## <date> <short title>

- Type: decision | assumption | change | approval
- Phase: idea | spec | phase <id> | change | feedback | upgrade
- What: <one sentence>
- Why: <one sentence>
- Cost of change: low | medium | high
- Replaces: <date> <title of the earlier entry>   (only if it replaces one)

The cost is high when a later change needs a migration or breaks something
others already use: data schema, file and save formats, public URLs and
interfaces, authentication. Claude never takes such decisions alone. They are
taken with the approval of the spec or by an answer from the user.

An entry that revokes or changes an earlier one states in full what holds
now, names the earlier entry under `Replaces:`, and the earlier entry moves
unchanged to DECISIONS-DONE.md, so this file holds only what is valid.

Keep entries short and their titles telling: phases find the entries
they need by their titles and do not read this file in full. Details belong
in ARCHITECTURE.md or the plan.

## 2026-11-25 Releases through /wcc:release, phases do not push

- Type: change
- Phase: change
- What: phase 30 creates `webcodingcat/hlab` (Apache-2.0) private, pushes `main` with its history unchanged and builds the CI workflow for every push to `main` and pull request against `main` (build, Go tests, lint, frontend unit tests, installer build) and the release workflow for a tag `vX.Y.Z`, but publishes no release; the user makes the repository public; phases do not push, from milestone 2 on they run on `wcc/milestone-<n>`, only what `/wcc:release` merges reaches `main`, and `/wcc:release` publishes each version (first v0.1.0) as "Release" in SPEC.md describes; a phase still reads the last CI run of `main` at its start.
- Why: the user wants releases made by `/wcc:release` rather than inside a phase, and decides the publication of the repository themselves.
- Cost of change: low
- Replaces: 2026-11-25 Repository, CI and releases on GitHub

## 2026-11-25 Approval: Claude creates the private repository and releases with /wcc:release

- Type: approval
- Phase: change
- What: the user approved that Claude creates `webcodingcat/hlab` private with `gh` and pushes `main` in phase 30, and that `/wcc:release` pushes `main` and the version tag and has the release workflow publish the release; the user makes the repository public.
- Why: the user keeps the publication of the repository and lets releases go through `/wcc:release`.
- Cost of change: low
- Replaces: 2026-11-25 Approval: Claude creates the repository, pushes and releases

## 2026-11-25 Phase 16 is the last phase before the release phase

- Type: change
- Phase: change
- What: phase 16 is the only phase that stops for manual checks; it is the last phase of milestone 1 before phase 30 (repository, CI and release), with phases taken in from "Later" inserted before it, runs the elevated installer suite in a development test VM and every open manual check in a user test VM without development tools, both fresh Windows Hyper-V VMs with nested virtualization set up with the repository's scripts as `docs/development/release-checks.md` describes (DOC-08).
- Why: the release phase needs a checked milestone; the rest of the 2026-11-21 rule stands.
- Cost of change: low
- Replaces: 2026-11-21 Manual checks and the installer suite move to a last phase in two test VMs

## 2026-11-25 Deadlines for storage applies, jobs and agent commands

- Type: decision
- Phase: phase 16
- What: every command the storage agent runs ends after 10 minutes (the error names the command and its duration), hlabd gives up on a storage apply after 30 minutes and on a storage job after 25 minutes (the trim keeps its 5 minutes), and a killed command is waited for at most 5 seconds.
- Why: a hung `btrfs subvolume sync` held the storage lock for good and left every box action running; the longest legitimate commands (subvolume sync after deleting a large box, quota rescan, fstrim of a large disk) take a few minutes.
- Cost of change: low

## 2026-11-25 Box removal ends the box's sessions first

- Type: decision
- Phase: phase 16
- What: the storage agent's `removeBox` ends the sessions on the box's IP (as a rollback does) before it deletes the subvolume.
- Why: an smbd child running as root kept a deleted box's share in use while Windows held its connection, so `btrfs subvolume sync` waited without end.
- Cost of change: low

## 2026-11-25 Restore recreates the VM of a kept server

- Type: decision
- Phase: phase 16
- What: at a cloud start `servers.Module.Restore` reconciles a stored server whose VM is missing (VM recreated from its kept disk, seed and adapters, never a new disk), then powers it per its stored status; a failed reconcile is logged and the next server continues.
- Why: a KeepData uninstall removes the VMs but keeps the state and the disks, and INST-05 expects the servers back after a reinstall.
- Cost of change: low

## 2026-11-25 Upgrade ends the tray and stops the service before InstallValidate

- Type: decision
- Phase: phase 16
- What: on an upgrade (`WIX_UPGRADE_DETECTED AND NOT REMOVE`) the MSI runs, before InstallValidate, `taskkill /F /IM hlab-tray.exe` through WixQuietExec (failure ignored) and `hlabd stop-service` from the Binary table (failure stops the upgrade); `MSIRESTARTMANAGERCONTROL` stays `Disable`.
- Why: the files-in-use check runs in InstallValidate, before util:CloseApplication can end the tray, and the tray does not quit when its window closes.
- Cost of change: low

## 2026-11-25 Storage VM trims before a compaction

- Type: decision
- Phase: phase 16
- What: before hlabd shuts the storage VM down for a compaction it sends the storage job `trim` (`btrfs subvolume sync`, `btrfs filesystem sync`, `fstrim` on the data file system; 5-minute deadline, failure logged).
- Why: `discard=async` drops queued discards at unmount and a deleted box's subvolume is freed in the background, so without the trim `Optimize-VHD` reclaimed only part of the space.
- Cost of change: low

## 2026-11-25 Actions are followed in the project taken at submit

- Type: decision
- Phase: phase 16
- What: every create form and DNS tab follows its action through `runApi`, `runOn`, `runHetzner` or `runHetznerOn` with the project passed as a value and optional `onanswer`/`onerror` hooks.
- Why: a Svelte prop read later resolves to the current route, which has no or another project once the user has left the console.
- Cost of change: low

## 2026-11-25 Every named delete asks for the name

- Type: change
- Phase: feedback
- What: every delete of something with a name asks for that name to be typed (UI-SPEC.md "Confirm dialog"), not only projects, servers, volumes, networks, zones and storage boxes; unnamed entries and other destructive actions keep the plain confirm.
- Why: the user found the two kinds of delete dialog inconsistent during UI-09 and chose the typed name everywhere.
- Cost of change: low

## 2026-11-25 MSI without Restart Manager

- Type: decision
- Phase: phase 16
- What: `installer/Package.wxs` sets `MSIRESTARTMANAGERCONTROL=Disable`, so only the old product's `hlabd stop-service` stops the service during an upgrade and nothing starts it again.
- Why: in the release checks the service was running again after a quiet upgrade, most likely restarted by the Restart Manager; INST-06 expects it stopped.
- Cost of change: low

## 2026-11-25 Uninstall ends a service stuck in its start

- Type: decision
- Phase: phase 16
- What: stopping the service for an uninstall or upgrade waits up to 3 minutes for a START_PENDING service, stops it once it runs, and otherwise ends its process by the PID from its status before the usual 200-second stop wait.
- Why: Windows refuses a stop while a service starts, and the uninstall failed with 1603 and left HLab half removed.
- Cost of change: low

## 2026-11-25 Cloud start deadlines

- Type: decision
- Phase: phase 16
- What: the cloud start has an overall deadline of 1 hour, the first agent document push 2 minutes and the server restore 10 minutes; which step hung in the test VM was not reproduced (candidates: the host DNS query in the push, a Hyper-V call in the restore).
- Why: a start without deadlines kept the service in START_PENDING for good; 1 hour leaves room for a first start that downloads images.
- Cost of change: low

## 2026-11-25 Q28 WSL trust follows the interactive user

- Type: change
- Phase: feedback
- What: the CA trust switch, its removal at uninstall and the `.wslconfig` reading use the interactive user: the user at the console, else the user of the single active Remote Desktop session; with several such sessions and nobody at the console the switch stays pending as before.
- Why: the user chose it in /wcc:report after the WSL part of the trust switch never ran in vmconnect's enhanced session, which a developer working over Remote Desktop would hit too.
- Cost of change: low

## 2026-11-25 Setup bundle uses the hyperlink theme with an empty licence URL

- Type: decision
- Phase: phase 16
- What: `installer/Bundle.wxs` sets `Theme="hyperlinkLicense" LicenseUrl=""` with the own theme.xml, which drops its ProgressPassive page so passive runs show the Progress page.
- Why: with `Theme="none"` WiX 7 writes no wixstdba data and the bundle failed at start (0x80070490); theme.xml derives from the hyperlink theme, and an empty URL hides the licence link.
- Cost of change: low

## 2026-11-25 Test VM script waits for the Hyper-V service

- Type: decision
- Phase: phase 16
- What: `release-check-vm.ps1` treats setup as finished when the service `vmms` runs in the VM, read over PowerShell Direct, and shows sign-in errors in its progress line.
- Why: the account is a local administrator under UAC whose remote token is filtered, so `Get-WindowsOptionalFeature` could not be read.
- Cost of change: low

## 2026-11-25 Test VM script asks nothing before its checks

- Type: decision
- Phase: phase 16
- What: `release-check-vm.ps1` checks elevation, its arguments and that no VM of the name exists before it asks for the password, opens the console before starting the VM, and gives the user test VM 4 vCPU unless `-Cpu` says otherwise.
- Why: no password is typed for a refused run, and the short "boot from CD or DVD" prompt is not missed.
- Cost of change: low

## 2026-11-25 Test VM account created in the specialize pass

- Type: decision
- Phase: phase 16
- What: the answer file creates the local account with a script in the specialize pass that adds it to Administrators by SID S-1-5-32-544 and sets `UnattendCreatedUser`, so OOBE asks for no account; confirmed by the first real install of both test VMs on 2026-11-25.
- Why: `<Group>` needs the group's localized name, which depends on the ISO language.
- Cost of change: low

## 2026-11-25 Test VMs set up by scripts

- Type: change
- Phase: feedback
- What: `scripts/release-check-vm.ps1` (development machine, elevated; parameters for the VM name, the Windows ISO, its language and the VM folder) creates a test VM in one call, from the VM with its sizes up to the checkpoint `fresh`, and `scripts/release-check-dev-tools.ps1` (inside the development test VM, elevated) installs the tools of the installer suite and clones the repository bundle; the answer file they build runs the whole Windows setup (language from the ISO language parameter, disk layout for a Generation 2 VM, Windows 11 Pro without activation, licence terms, local account, privacy screens) and, through a one-time automatic sign-in and first-logon commands, enables Hyper-V and restarts; `docs/development/release-checks.md` describes the calls instead of copy-and-paste blocks.
- Why: the user asked for it in /wcc:report after a pasted block lost its password prompt to the pasted text and went on after an error; a script asks properly, stops at the first error and can be repeated.
- Cost of change: low
- Replaces: 2026-11-25 Windows in the test VMs installed fully by the answer file

## 2026-11-24 Test VM names and stop action

- Type: decision
- Phase: phase 16
- What: the test VMs are named `release-check-dev` and `release-check-user`, without automatic checkpoints and with `-AutomaticStopAction ShutDown`.
- Why: the names avoid HLab's `hlab-` prefix, and a host shutdown never saves a nested VM.
- Cost of change: low

## 2026-11-24 Test VMs use the Default Switch

- Type: decision
- Phase: phase 16
- What: both test VMs connect to Hyper-V's Default Switch, and the guide checks each VM's subnet against 198.19.0.0/16 and 172.31.1.1 before the first cloud start (on an overlap: restart the host, check again).
- Why: the user chose it, because an own NAT on the development machine would stop the dev cloud (one WinNAT per host).
- Cost of change: low

## 2026-11-24 Release checks guide written in two plans

- Type: decision
- Phase: phase 16
- What: 16-01 writes the VM setup part of `docs/development/release-checks.md`, the documentation plan 16-02 adds the reusable user-check steps and the links.
- Why: one plan would come to about 500 lines.
- Cost of change: low

## 2026-11-24 Order of the checks in the user test VM

- Type: decision
- Phase: phase 16
- What: INST-03 install, appliance name check, UI-09 with DOC-07, update check (INST-06), LIFE-05, COMPACT with the storage VM name check, INST-05 uninstall last.
- Why: each check leaves the state the next one needs, and the storage VM exists only after the first box.
- Cost of change: low

## 2026-11-24 Delayed seed swaps end with the cloud's run

- Type: decision
- Phase: phase B4
- What: delayed seed swaps run under a module-local context that `Halt` cancels, waiting for a swap already under way, which always finishes so slot 0 is never left empty.
- Why: No new dependency is needed, and a cancelled swap must not leave the drive empty.
- Cost of change: low

## 2026-11-24 A seed write during rescue clears seed_due

- Type: decision
- Phase: phase B4
- What: when rescue holds slot 0 or the server has no VM, `rewriteSeed` writes the seed file and clears `seed_due`.
- Why: Ending rescue or creating the VM inserts that current file anyway.
- Cost of change: low

## 2026-11-24 Due seed swaps run at the end of Restore

- Type: decision
- Phase: phase B4
- What: due seed swaps (`seed_due`) are done at the end of the servers module's `Restore`, through the normal swap path with its hold, not in `Recover`.
- Why: `Recover` runs before the VMs resume, and changing the DVD of a Saved VM is untested (E35 covers only Paused VMs).
- Cost of change: low

## 2026-11-24 An empty seed slot is found through seed_due

- Type: decision
- Phase: phase B4
- What: a slot 0 left empty by a crash between eject and insert is found through the `seed_due` note set before the eject; the platform gets no method to read a DVD drive.
- Why: The note already marks every server whose seed write did not finish.
- Cost of change: low

## 2026-11-24 A Saved VM started at cloud start is a resume

- Type: decision
- Phase: phase B4
- What: a Saved VM started by `Restore` gets no seed hold; only the power-on of an Off VM there counts as a boot.
- Why: Resume from saved state does not run cloud-init again (E48).
- Cost of change: low

## 2026-11-24 Guest hook keeps its own link list in a second file

- Type: decision
- Phase: phase B4
- What: the guest hook records the links it leaves alone or took over in a second file in `/run/hlab-guest`; `networks.json` keeps its format.
- Why: An unchanged `networks.json` keeps the hook's existing contract with hlabd.
- Cost of change: low

## 2026-11-24 Steady private traffic is 800 pings without loss

- Type: decision
- Phase: phase B4
- What: the restart e2e rows wait for one continuous `ping -c 800 -i 0.01 -W 1` (8 s) without loss, retried until a 2-minute deadline.
- Why: E47 saw up to about 3 s of good traffic between two drop windows.
- Cost of change: low

## 2026-11-24 Network drops after resume are a documented deviation (Q27)

- Type: decision
- Phase: phase B4
- What: hlab adds no workaround for the 11 to 25 s network drops of each server after a cloud start from saved state; docs/deviations.md describes them.
- Why: E47 found the cause in Hyper-V's restore and the guest's `hv_netvsc` driver, and no measured mitigation helped.
- Cost of change: low

## 2026-11-24 Appliance and storage VM name themselves through their user-data

- Type: decision
- Phase: phase 29
- What: the cloud-config user-data of the appliance and the storage VM sets `hostname` and `fqdn` to their hlab name (placeholder `@NAME@`); existing VMs apply it once at the next cloud start because their instance-id changes.
- Why: with `local-hostname` gone from meta-data (Q25) they would keep the image's default name.
- Cost of change: low

## 2026-11-24 Vendor-data sets fqdn next to hostname

- Type: decision
- Phase: phase 29
- What: the cloud-config part of vendor-data sets both `hostname` and `fqdn` to the server's name.
- Why: with `hostname` alone EL and Fedora take the reverse-DNS name as hostname (E46); Hetzner's `local-hostname` gives the same pair.
- Cost of change: low

## 2026-11-24 Empty user-data is `#cloud-config` with `{}`

- Type: decision
- Phase: phase 29
- What: cidata writes an empty user-data or vendor-data as `#cloud-config\n{}\n`, for servers without user_data and every other seed.
- Why: newer cloud-init reports an empty part as degraded and EL9's crashes in init-local on it (E46).
- Cost of change: low

## 2026-11-24 The hostname comes from vendor-data on every image (Q25)

- Type: change
- Phase: phase 29
- What: Q25 answered: the seed's `meta-data` carries no `local-hostname` on any image; the server's name reaches the guest as `hostname` in the cloud-config part of vendor-data, set in cloud-init's init stage.
- Why: the user chose one way for all images (option 2); on Fedora cloud-init sets a meta-data hostname in the local stage before D-Bus is up and `cloud-init status` then exits 2.
- Cost of change: low (seed content only; existing servers keep their hostname, all 16 images are re-checked by SRV-23)

## 2026-11-24 Image servers are created with --allow-deprecated-image

- Type: decision
- Phase: phase 29
- What: the e2e image fixture always passes `--allow-deprecated-image` to `hcloud server create`; the checks are unchanged.
- Why: hcloud refuses the deprecated fedora-43 without it.
- Cost of change: low

## 2026-11-24 Guest hooks get their interpreter by placeholder

- Type: decision
- Phase: phase 29
- What: vendor-data writes hooks, units and the udev rule with `@PYTHON@`; the shell part picks `/usr/bin/python3` or `/usr/libexec/platform-python` right after `set -e` and replaces the placeholder in all six files before reloading systemd and udev.
- Why: units and rules are loaded with the real interpreter of each image.
- Cost of change: low

## 2026-11-24 No download retry after cancellation

- Type: decision
- Phase: phase 29
- What: a failed image or package request is not retried when its context is already cancelled.
- Why: the retry could not succeed.
- Cost of change: low

## 2026-11-24 Image rows start their per-image subtests from goroutines

- Type: decision
- Phase: phase 29
- What: TestE2E/SRV-23 and SRV-24 share one server per image through `imageRow`, which starts the per-image subtests from goroutines without `t.Parallel`, capped by the three server slots, and counts a subtest the filter skips as released.
- Why: with `t.Parallel` a row could wait for a slot only the other row's not-yet-scheduled subtest frees, a deadlock.
- Cost of change: low

## 2026-11-24 One keyless rocky-8 server for the account checks

- Type: decision
- Phase: phase 29
- What: the SRV-24 default-user and user_data `users` checks and the SRV-23 create-time password check share one extra keyless rocky-8 server.
- Why: rocky-8 has the oldest cloud-init and Python 3.6, the strictest case, and a second extra server costs one more boot.
- Cost of change: low

## 2026-11-24 Vendor-data text changes proven end to end only

- Type: decision
- Phase: phase 29
- What: the root-only and Python 3.6 changes in vendor-data get no unit row; SRV-23 and SRV-24 prove them on every image.
- Why: they are configuration text without logic.
- Cost of change: low

## 2026-11-24 CAT-06 test no longer fixes the image count

- Type: decision
- Phase: phase 29
- What: TestImages/CAT-06 drops its "only these five images" and count checks and still checks the five images and their attributes.
- Why: the checks contradict CAT-09.
- Cost of change: low

## 2026-11-24 Deprecated fedora-43 checked in unit and e2e

- Type: decision
- Phase: phase 29
- What: the CAT-09 unit row lists with `include_deprecated=true` as hcloud sends it, and SRV-23 expects a date in fedora-43's deprecated column of `hcloud image list --type system`.
- Why: success criterion 1 names the hcloud CLI.
- Cost of change: low

## 2026-11-24 One on-link switch per seed

- Type: decision
- Phase: phase 29
- What: `cidata.Seed.NoOnLink` turns `on-link` off for the whole seed; the rescue seed keeps `on-link` because rescue always runs Debian 13.
- Why: all adapters of a guest share one renderer.
- Cost of change: low

## 2026-11-24 User-Agent and retry in the one download function

- Type: decision
- Phase: phase 29
- What: `User-Agent: hlab/<version>` and a single retry live in the one download function, so ISO and package downloads get them too; a 404 after the retry still falls back to the alternative source.
- Why: one place serves every download.
- Cost of change: low

## 2026-11-24 Upgrade to wcc 0.32.27

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.26 to 0.32.27: the DECISIONS.md header paragraph on replaced entries and the version line.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-24 Fault lists load target names best-effort

- Type: decision
- Phase: phase 28
- What: the project's Faults page and "Active faults" always show the faults of the management API and load the server, volume and storage box names per project best-effort through `faultTargetNames`.
- Why: an `api_error` fault on reads must not hide the faults that the user wants to end.
- Cost of change: low

## 2026-11-24 "End" in a fault row needs no confirmation

- Type: decision
- Phase: phase 28
- What: "End" in a fault row ends the fault at once with an action toast; only "End all" asks first.
- Why: ending one fault is harmless and quickly undone by creating it again.
- Cost of change: low

## 2026-11-24 Fault table texts the plan left open

- Type: decision
- Phase: phase 28
- What: a latency fault without jitter reads "50 ms", a cut "public, 100 % loss", a loss fault "private, 30 % loss"; the Percent field has no hint text.
- Why: the shortest texts that follow the UI-SPEC examples; a hint naming the range would hide the error check of UI-16.
- Cost of change: low

## 2026-11-24 A lost connection keeps the last fault count

- Type: decision
- Phase: phase 28
- What: the tray keeps the last fault count while the connection to hlabd is lost, like the problems, and shows 0 while the cloud is stopped.
- Why: the pill and the tray item do not flicker during short hiccups.
- Cost of change: low

## 2026-11-24 TrayModel carries only the fault count

- Type: decision
- Phase: phase 28
- What: `TrayModel` gets `Faults int`, and `cmd/hlab-tray` shows "End all faults" while it is above 0.
- Why: the count and `Cloud.EndAllFaults` are unit-tested in the bridge; showing the item is glue.
- Cost of change: low

## 2026-11-24 Field errors only on the fault create

- Type: decision
- Phase: phase 28
- What: the management API adds `details.fields` only to `invalid_input` of the fault create; other management errors stay unchanged.
- Why: the form shows errors under their fields, and the addition does not break existing clients.
- Cost of change: low

## 2026-11-24 Fault resource types are a fixed frontend list, error codes come from Go

- Type: decision
- Phase: phase 28
- What: the resource type select uses a fixed frontend list `faultResources`; the error code select uses `api.Codes()` from Go, general codes first.
- Why: hlabd builds the Go resource list only at cloud start from the router, while the error code table is static.
- Cost of change: low

## 2026-11-24 Faults routes plan carries the sidebar entry and first help texts

- Type: decision
- Phase: phase 28
- What: plan 28-03 adds the sidebar entry, `consoleNav.ts` and first help texts with the views; the documentation plan completes the help texts.
- Why: svelte-check and the UI-07 and DOC-05 tests need one help text per view in `views.json`.
- Cost of change: low

## 2026-11-24 Tray fault e2e rows reuse the fault fixture

- Type: decision
- Phase: phase 28
- What: UI-16 runs on the fault fixture's resources after FAULT-08/restart and before `releaseFaultFixture`, ending the freeze before the crash; UI-17 runs after UI-14 with two projects of its own and `api_error`/`rate_limit` faults.
- Why: no further VM boot is needed.
- Cost of change: low

## 2026-11-24 No new DOC rows for the fault views

- Type: decision
- Phase: phase 28
- What: phase 28 adds no DOC test rows.
- Why: DOC-04, DOC-05 and UI-07 pick up the new views through `views.json`, and DOC-07 is manual.
- Cost of change: low

## 2026-11-24 The restart rows wait for steady private traffic before measuring

- Type: decision
- Phase: phase 27
- What: FAULT-07/restart and FAULT-08/restart wait, with a deadline, until a short ping burst from B to A loses nothing before the zero-loss measurement.
- Why: after the VMs resume from saved state private pings drop for about 1 s in one block, also without the faults applier, and the rows check leftover faults, not that transient.
- Cost of change: low

## 2026-11-24 LRO stays off after a loss fault; tc deletes of missing qdiscs are ignored

- Type: decision
- Phase: phase 27
- What: when offloads go back on the applier turns on GRO, GSO and TSO but leaves LRO off; tc `del` failures saying "No such file" or "handle of zero" are ignored, and after any other tc failure the applier forgets its tc state and rebuilds on the next document.
- Why: LRO on broke TCP through the forwarding appliance in the e2e run, and the default root qdiscs (mq, noqueue) answer "handle of zero" on delete.
- Cost of change: low

## 2026-11-24 tc filters with explicit handles, netem changes with all parameters

- Type: decision
- Phase: phase 27
- What: flower filters get the handle `minor<<16 | index+1`, a `qdisc change` of a netem repeats all its parameters and passes `seed` only when loss or seed changed, a device leaving the plan only loses its root qdisc, and a minor in the previous state is not reused in the same run.
- Why: tc deletes one filter of a shared prio only by handle and resets what a change does not name (E29, E42).
- Cost of change: low

## 2026-11-24 A box fault with an unparsable IP fails the push

- Type: decision
- Phase: phase 27
- What: an unparsable stored box IP makes building the fault entries return an error, as the storage box module's document does.
- Why: every box record gets a valid IP when it is created, so the case is a broken store and is not hidden.
- Cost of change: low
- Replaces: 2026-11-24 A box fault with an unparsable IP is left out

## 2026-11-24 A locations latency lists only servers in the agent document

- Type: decision
- Phase: phase 27
- What: a locations fault lists only those of the project's stored servers that are also in `doc.Servers`, so a server being deleted drops out as for a server target.
- Why: the same rule for both target kinds needs no extra deletion hook.
- Cost of change: low

## 2026-11-24 A locations latency class carries the destination's own faults

- Type: decision
- Phase: phase 27
- What: a netem class of a locations latency adds the destination server's own latency (delay and jitter) to the half delay and takes that server's loss and seed on the device; without loss its seed is the locations fault's ID.
- Why: a packet passes one netem per device, so all faults on that destination must be merged into its class.
- Cost of change: low

## 2026-11-24 A locations latency covers a server's floating and alias IPs

- Type: decision
- Phase: phase 27
- What: for a `latency` fault between two locations, a server's floating IPs and alias IPs count as its addresses, as sources and as destinations, like for a `network` fault.
- Why: the same address set for all net faults is the simplest rule, and traffic to a floating IP reaches the same server.
- Cost of change: low

## 2026-11-24 A storage box fault blocks only the box's own address

- Type: decision
- Phase: phase 27
- What: `storage_box_unavailable` drops the box's IP on the appliance; its credentials still log in over SSH and FTP at another box's address (E17), and docs/deviations.md names this.
- Why: answer of the user to Q2; locking the accounts in the storage VM would change `agentapi.Storage` and the storage agent for a side path nobody uses by accident.
- Cost of change: low

## 2026-11-24 Fault cleanup on the appliance is proven by unit rows and behaviour

- Type: decision
- Phase: phase 27
- What: success criterion 4 of phase 27 is proven by unit tests of the agent's fault planner (an empty or smaller list removes every rule and qdisc, leftovers included) and e2e checks that ping time and loss return to normal; no status interface reports the appliance's fault rules.
- Why: answer of the user to Q1; a new read-only interface only for tests, or access into the appliance, is not worth it.
- Cost of change: low

## 2026-11-24 Agent fault logic as pure plan, pure tc diff and Linux applier

- Type: decision
- Phase: phase 27
- What: the agent's fault logic splits into `PlanFaults`, `TcCommands` with `TcState` (both pure) and a Linux applier, like firewall.go and firewall_linux.go.
- Why: the logic runs in `task test` on Windows, and the unit rows for success criterion 4 rest on it.
- Cost of change: low

## 2026-11-24 Netem classes keyed by destination and source group

- Type: decision
- Phase: phase 27
- What: netem classes are keyed by destination server plus source group (all sources, or one locations fault) with stable minors; an unchanged class is never touched, so a latency fault on the same server continues the loss sequence, but a locations latency splits that server's flows into a new class whose loss sequence starts anew.
- Why: E42 shows a sequence survives sibling changes and seed-less `change`; one class per flow group keeps loss from being drawn by other traffic.
- Cost of change: low

## 2026-11-24 Public fault rules pinned to the public device

- Type: decision
- Phase: phase 27
- What: cut and box drop rules for public addresses match `iifname`/`oifname` of the public device.
- Why: a project's private range may overlap the public range and must not be hit.
- Cost of change: low

## 2026-11-24 A netem without loss uses the latency fault's ID as seed

- Type: decision
- Phase: phase 27
- What: hlab passes the latency fault's ID as explicit `seed` to a netem that has no loss fault.
- Why: a seed-less `tc qdisc add` takes a random seed (E42), which would make later loss changes unrepeatable.
- Cost of change: low

## 2026-11-24 The agent's first document rebuilds fault state from scratch

- Type: decision
- Phase: phase 27
- What: on the first document after the agent starts, the applier deletes the root qdisc of the public device and of every `t<vlan>`, and any leftover `hlab_fault` table, then rebuilds.
- Why: the state after an abrupt end is unknown, and leftovers must not survive a cloud start.
- Cost of change: low

## 2026-11-24 Net fault e2e rows share the fault fixture and its restart

- Type: decision
- Phase: phase 27
- What: the net e2e rows reuse the fault fixture's server and add one more in fsn1, one in hel1, a private network and a storage box; they run after FAULT-05 and share the one cloud restart.
- Why: it stays within the three server slots and needs no second restart.
- Cost of change: low

## 2026-11-24 FAULT-02 gets agent unit rows for the empty plan

- Type: decision
- Phase: phase 27
- What: FAULT-02 gets two agent unit rows: an empty plan, and the tc commands that undo a tree.
- Why: follows "Cloud start cleanup is tested under FAULT-02".
- Cost of change: low

## 2026-11-24 One IOPS reset per server at cloud start

- Type: decision
- Phase: phase 26
- What: `SetDiskIOPS(vm, "", 0)` clears every drive of a VM in one call, and the servers module's cloud start cleanup uses it for the system disk and the volumes, so the volumes cleanup only reattaches disks.
- Why: E37 shows one read per VM is the cheapest cleanup, and the approved signature stays.
- Cost of change: low

## 2026-11-24 Cloud start cleanup is tested under FAULT-02

- Type: decision
- Phase: phase 26
- What: the unit rows for the `Recover` of the servers and volumes modules are named FAULT-02.
- Why: FAULT-02 holds the promise that no fault is active after a cloud start, at unit level.
- Cost of change: low

## 2026-11-24 Fault e2e rows share one server and one restart

- Type: decision
- Phase: phase 26
- What: success criterion 4 is checked by FAULT-06/restart, FAULT-09/restart and FAULT-10/restart sharing one lazy cloud stop and start, and all FAULT e2e rows run alone after the LIFE rows on one debian-13 server with one volume.
- Why: they restart the cloud and act on the same server, so they cannot run in parallel.
- Cost of change: low

## 2026-11-24 disk_slow on an unattached volume is refused in the target lookup

- Type: decision
- Phase: phase 26
- What: the cloud's fault target lookup refuses `disk_slow` on an unattached volume, next to the existing check for `volume_unavailable`.
- Why: both kinds need the drive, and one check keeps the error the same.
- Cost of change: low

## 2026-11-24 Crash block and freeze end run inside the action work

- Type: decision
- Phase: phase 26
- What: the power and rebuild actions check the crash block and end a freeze in their work, before any Hyper-V call, with the project name passed in from the request handler.
- Why: faults are kept by project name, and checking before Hyper-V leaves the server unchanged on refusal.
- Cost of change: low

## 2026-11-24 Freeze and slow disk e2e thresholds

- Type: decision
- Phase: phase 26
- What: the freeze check pings the server's IPv4 from the host, and the fio check accepts at most 220 IOPS at a limit of 200 and more than 1000 after the fault ends.
- Why: E39 measured 197 to 200 with ramp and 130k unlimited, so these bounds are stable.
- Cost of change: low

## 2026-11-24 Cloud holds the fault manager behind a stable layer

- Type: decision
- Phase: phase 25
- What: The cloud keeps the fault manager in an atomic pointer and gives `api.Deps.Faults` and the modules' `EndFaults` nil-safe functions that use the current manager or pass through.
- Why: the router and modules are built before the manager, which needs `Router.Resources()`, and actions may still run after a stop.
- Cost of change: low

## 2026-11-24 Where deletes end their faults

- Type: decision
- Phase: phase 25
- What: A server delete ends its faults at the start of the delete action, a volume after the checking transaction before its disk file goes, a storage box in the handler before its action; an `EndFaults` error is logged and the delete goes on; project deletion relies on `EndProject` in mgmt.
- Why: faults end before their target disappears, and cleanup errors must not block a delete.
- Cost of change: low

## 2026-11-24 MCP fault schemas generated, then extended

- Type: decision
- Phase: phase 25
- What: The `fault_create` input schema is generated with `jsonschema.For` and gets `maximum` 2^53-1 on `seed`; optional fields carry `omitempty`, and a missing `params` is sent as `{}`.
- Why: the same result as a hand-written schema without duplication, and MCP-06 needs calls that leave fields out.
- Cost of change: low

## 2026-11-24 hlab fault commands use their own types

- Type: decision
- Phase: phase 25
- What: The CLI uses its own fault request and object types instead of importing `internal/faults`, and empty flag values count as not given.
- Why: `hlab.exe` does not pull in the api package, and the MCP handlers can pass a flags map with blanks.
- Cost of change: low

## 2026-11-24 One bucket counts each token request

- Type: decision
- Phase: phase 25
- What: While a `rate_limit` fault is active only its bucket counts a token request; the project's default bucket stays untouched and is used again afterwards.
- Why: a request should be counted once, against the limit the client sees.
- Cost of change: low

## 2026-11-24 Rate limit arithmetic and default reset clock

- Type: decision
- Phase: phase 25
- What: Refill is `capacity*elapsedSeconds/3600` and Reset `ceil((capacity-tokens)*3600/capacity)`; the router's default `RateLimit-Reset` on 401 and proxy answers uses the wall clock.
- Why: whole steps (360 s at 10 per hour) come out exact in floating point, and the router has no clock of its own.
- Cost of change: low

## 2026-11-24 Fault cleanup errors at project delete and fault delete

- Type: decision
- Phase: phase 25
- What: An `EndProject` error during `DELETE /projects/{name}` is logged as a warning and the deletion goes on; a fault `DELETE` with joined Undo errors answers `500 server_error` although the faults have ended.
- Why: project deletion must not depend on effect cleanup, and a failed undo should be visible to the caller.
- Cost of change: low

## 2026-11-24 Unknown fault parameters are refused

- Type: decision
- Phase: phase 25
- What: Unknown parameter keys of a fault are `invalid_input` on `params.<name>`; field names are `kind`, `target`, `target.type|id|from|to`, `params.<name>`, `seed` and `duration`.
- Why: unchecked outside input would be silently ignored.
- Cost of change: low

## 2026-11-24 Latency faults conflict in both directions

- Type: decision
- Phase: phase 25
- What: A `latency` fault from A to B conflicts with one from B to A.
- Why: both hit the same traffic.
- Cost of change: low

## 2026-11-24 Undo errors do not keep a fault alive

- Type: decision
- Phase: phase 25
- What: The End methods return the joined Undo errors and log them, and the faults end anyway.
- Why: a fault that cannot be undone must not stay listed forever.
- Cost of change: low

## 2026-11-24 Fault manager calls take a context

- Type: decision
- Phase: phase 25
- What: `faults.Manager` methods that run or undo effects (Create, End, EndProject, EndAll, EndTarget) take a `context.Context`.
- Why: the effects of phase 26 are Hyper-V calls that need a context.
- Cost of change: low

## 2026-11-24 Faults name projects, the cloud looks up targets

- Type: decision
- Phase: phase 25
- What: Faults identify projects by name, and target lookup goes through a `TargetLookup` function the cloud supplies.
- Why: the API context and management routes carry the name, and `faults` stays free of module logic.
- Cost of change: low

## 2026-11-24 Fault kinds become active by registered effects

- Type: decision
- Phase: phase 25
- What: `api_error` and `rate_limit` are always available; any other kind without an effect in `Options.Effects` is refused with `invalid_input` "not available yet".
- Why: phases 26 and 27 then only register effects.
- Cost of change: low

## 2026-11-24 Seed on a kind without randomness is refused

- Type: decision
- Phase: phase 25
- What: A seed given to a kind that takes none (or to `network` at 100 %) is `invalid_input` on `seed`.
- Why: a silently ignored seed would mislead.
- Cost of change: low

## 2026-11-24 Unknown location 404, unattached volume invalid

- Type: decision
- Phase: phase 25
- What: An unknown location in a `latency` target is `404 not_found`, and a volume that is not attached for `volume_unavailable` is `invalid_input`.
- Why: it follows the fault object table.
- Cost of change: low

## 2026-11-24 api_error resource checked against mounted modules

- Type: decision
- Phase: phase 25
- What: `api_error`'s `resource` is checked against the mounted modules' types (`api.Router.Resources()`).
- Why: a typo would otherwise create a fault that never hits.
- Cost of change: low

## 2026-11-24 Default RateLimit headers set before auth

- Type: decision
- Phase: phase 25
- What: The api layer always sets default `RateLimit-*` headers (3600/3600/now) before auth, and the fault middleware overwrites them for token requests.
- Why: 401 and unknown-path responses carry the headers without `api` importing `faults`.
- Cost of change: low

## 2026-11-24 RateLimit header rounding

- Type: decision
- Phase: phase 25
- What: `RateLimit-Remaining` is the floor of the bucket's tokens and `RateLimit-Reset` is now plus the time until full, rounded up to whole seconds.
- Why: Hetzner's documentation gives no rounding.
- Cost of change: low

## 2026-11-24 Status handler counts faults itself

- Type: decision
- Phase: phase 25
- What: The management status handler fills `faults` from `Deps.Faults.Count()` instead of the cloud's status function.
- Why: it can then be tested in `internal/mgmt`.
- Cost of change: low

## 2026-11-24 Small helpers for the fault wiring

- Type: decision
- Phase: phase 25
- What: New helpers `api.HasStatus` and `apitest.NewWith(Options{Faults})`, and modules get an optional `Deps.EndFaults` hook for target deletion.
- Why: they are the smallest additions that carry the interfaces.
- Cost of change: low

## 2026-11-24 Fault cloud wiring has no own test row

- Type: decision
- Phase: phase 25
- What: The cloud wiring (new manager per start, `EndAll` at cloud stop, `EndTarget` in deletes) has no own test row; the manager behaviour behind it has rows.
- Why: the wiring is glue.
- Cost of change: low

## 2026-11-24 More system images in milestone 1

- Type: change
- Phase: change
- What: Every current Hetzner x86 system image (rocky-8, rocky-9, rocky-10, alma-8, alma-9, alma-10, centos-stream-9, centos-stream-10, fedora-43, fedora-44 and opensuse-16) moves into milestone 1 as phase 29 with full server parity, after one boot experiment per image (E30-E34) that all passed; app images are dropped from the target vision.
- Why: The user wants their setups tested on the distributions Hetzner offers, but needs no app images.
- Cost of change: low

## 2026-11-24 Servers log in as root only

- Type: decision
- Phase: change
- What: vendor-data sets `users: []` on every image, so no cloud image creates its default user (`ubuntu`, `debian`, `rocky`, ...), while users from user_data still apply.
- Why: The user chose Hetzner's behaviour, where system images have root only.
- Cost of change: low

## 2026-11-24 EL8 hooks, checksum formats and no on-link outside netplan

- Type: decision
- Phase: change
- What: The guest hooks become Python 3.6 compatible with the interpreter chosen at install, the image pipeline reads GNU, BSD and PGP-signed BSD checksum files with its own User-Agent, and the network-config leaves out `on-link` for images that do not render with netplan.
- Why: The experiments E30-E34 showed these as the only changes the new images need, and the `on-link` key makes `cloud-init status` exit with 2 there.
- Cost of change: low

## 2026-11-24 WSL2 mirrored mode and MCP resource tools dropped

- Type: change
- Phase: change
- What: WSL2 in mirrored networking mode (HOST-05) and MCP tools that read and change Hetzner resources are removed from the target vision, "Possible later", "Later" and the architecture's extensions, without becoming non-goals; hlab keeps reporting mirrored mode as not supported.
- Why: The user does not want to pursue either of them.
- Cost of change: low

## 2026-11-24 Fault injection in milestone 1

- Type: change
- Phase: change
- What: Fault injection (server crash and freeze, network disruption, latency, API errors, rate limits, slow or unavailable disks, unavailable storage boxes) moves from the target vision into milestone 1 as phases 25 to 28 before phase 16, controlled in the tray console and through the management API, `hlab fault` and MCP tools (`decisions/fault-injection.md`).
- Why: The user wants to test failure handling of their setups with the first version, and set, see and end faults in the tray.
- Cost of change: medium

## 2026-11-24 Fault interfaces

- Type: decision
- Phase: change
- What: Faults are served at `/mgmt/v1/faults` and `/mgmt/v1/projects/{name}/faults` with the fault object of ARCHITECTURE.md (kinds `server_crash`, `server_freeze`, `network`, `latency`, `api_error`, `rate_limit`, `volume_unavailable`, `disk_slow`, `storage_box_unavailable` and their parameter names), as `hlab fault create|list|delete`, as the MCP tools `fault_create`, `fault_list` and `fault_delete`, and to the appliance as `agentapi.Config.Faults`; `api_error` draws one value per matching request in arrival order from a `math/rand/v2` PCG seeded with the fault's seed, and reads are GET and HEAD.
- Why: The user chose the kinds, parameters, seeds and ways of control in /wcc:change on 2026-11-24 (`decisions/fault-injection.md`); these shapes carry them, and scripts and tests will depend on them.
- Cost of change: high

## 2026-11-24 A network cut also cuts the appliance's services

- Type: decision
- Phase: change
- What: A `network` fault with 100 % cuts the server off from everything, including the appliance's DNS, metadata service and API proxy, and after it ends the server reaches all of them again without a restart.
- Why: The user wants a cut to behave like a real network failure, and a test must show the recovery.
- Cost of change: low

## 2026-11-24 A crashed server cannot be started while its fault is active

- Type: decision
- Phase: change
- What: While a `server_crash` fault is active, poweron, reboot and reset end with the action error `server_error`, and ending the fault leaves the server off.
- Why: A crash stands for a failed host, which at Hetzner also keeps the server down until the host is back; without the block the fault would be a plain power off.
- Cost of change: low

## 2026-11-24 One fault per kind and target, with fixed parameter ranges

- Type: decision
- Phase: change
- What: A second fault of the same kind on the same target is refused with `conflict`, and the parameters have the ranges of the fault object table in ARCHITECTURE.md (durations 1 s to 24 h, delay up to 10 s, IOPS from 10, at most 3600 requests per hour).
- Why: Two overlapping faults of one kind would have no clear combined effect, and the ranges keep every fault reversible and observable.
- Cost of change: low

## 2026-11-24 RateLimit headers on every API response

- Type: decision
- Phase: change
- What: Every response of the Cloud API and the Storage Box API carries Hetzner's `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset`, counted against Hetzner's default of 3600 per hour without ever refusing while no `rate_limit` fault is active.
- Why: The user chose Hetzner's behaviour with headers; clients that read them see the same values as at Hetzner.
- Cost of change: low

## 2026-11-23 request_console finishes its action in one step

- Type: decision
- Phase: phase 23
- What: `request_console` creates and finishes its action in one transaction without the server's lock check, and deleting a server also drops its unused console tokens.
- Why: The console must work while other actions run on the server, and a deleted server's URL must not connect.
- Cost of change: low

## 2026-11-23 Console input refused by the VM is dropped

- Type: decision
- Phase: phase 23
- What: Input to a VM without keyboard or mouse device, or a non-zero ReturnValue of an input method, is dropped without error, and the RFB session stays open.
- Why: An off or starting server must keep its console open (CONTEXT.md); only the e2e tests notice a wrong argument.
- Cost of change: low

## 2026-11-23 Console window shows the proxy's close reason

- Type: decision
- Phase: phase 23
- What: The console page opens the WebSocket itself and hands it to noVNC, so "Disconnected" shows the proxy's reason; "Console" is also in the server list's row menu, and the proxy relays in messages of at most 32 KiB.
- Why: noVNC's disconnect event carries no reason, the plan names the row menu, and default WebSocket peers refuse larger messages.
- Cost of change: low

## 2026-11-23 Console keyboard: physical keys and characters

- Type: decision
- Phase: phase 23
- What: Keys from a VNC client offering the QEMU Extended Key Event pass straight through as scan codes; other clients' keysyms go through a US set-1 table with Shift corrected (Q1 answered (c) by the user).
- Why: It behaves like Hetzner's console and is right for guests of any layout (E25).
- Cost of change: low

## 2026-11-23 Console screen source is platform.ConsoleConn

- Type: decision
- Phase: phase 23
- What: The screen source is `platform.ConsoleConn` (Screen with an ok flag, Scancodes, MouseMove, MouseButton, MouseScroll, Close), opened through an optional `platform.Console` extension, and `internal/console` translates RFB input into it.
- Why: E25 requires scan codes for every key, and pure translation in the console package is unit-testable without Hyper-V.
- Cost of change: low

## 2026-11-23 Console token is a UUID from crypto/rand

- Type: decision
- Phase: phase 23
- What: The console token is a UUID v4 built from crypto/rand, without a new direct dependency.
- Why: It is the simplest way to the UUID form the user confirmed.
- Cost of change: low

## 2026-11-23 Console certificate kept in memory

- Type: decision
- Phase: phase 23
- What: The console certificate is issued from the hlab CA at every cloud start and kept only in memory, not in kv.
- Why: SPEC.md issues it at every start, so nothing has to survive a restart.
- Cost of change: low

## 2026-11-23 request_console stays in the deviations with a new text

- Type: decision
- Phase: phase 23
- What: `api.Deviations` keeps the `request_console` endpoint with a new description (console.hetzner.internal, hlab CA certificate, Hyper-V screen), changed in plan 23-07.
- Why: DOC-03 checks that the endpoint appears in api-coverage.md and so stays green until the documentation plan.
- Cost of change: low

## 2026-11-23 Console forward is a plain TCP forward in the agent

- Type: decision
- Phase: phase 23
- What: The agent forwards `198.19.0.1:443` to hlabd's console listener with a plain TCP forward in `internal/agent/forward.go` and an A record for `console.hetzner.internal`; the host firewall rule opens 8781 next to 8780.
- Why: TLS has to reach hlabd unchanged (ARCHITECTURE.md).
- Cost of change: low

## 2026-11-23 Tray console proxy serves one connection per Open

- Type: decision
- Phase: phase 23
- What: The loopback proxy of `bridge.Console` serves exactly one connection per `Open` and closes its listener after a minute without one; Reconnect calls `Open` again.
- Why: It is the smallest proxy that fits one console page per window with a random path.
- Cost of change: low

## 2026-11-23 Console window is a route outside the views

- Type: decision
- Phase: phase 23
- What: The console window is the route `#/server-console/<project>/<id>`, rendered by App.svelte without the shell and outside views.json.
- Why: It is not a project view, and DOC-05 pairs only views with help texts.
- Cost of change: low

## 2026-11-23 Console accepts 32-bpp pixel formats only

- Type: decision
- Phase: phase 23
- What: The RFB layer accepts any 32-bpp true-colour pixel format with the client's shifts and endianness and closes the connection for other depths.
- Why: noVNC and common VNC clients use 32 bpp; smaller depths are not needed for the requirement.
- Cost of change: low

## 2026-11-23 CON-01 tested by unit rows and three e2e rows

- Type: decision
- Phase: phase 23
- What: CON-01 gets unit rows for input translation, RFB updates, resize, no picture and token expiry besides three e2e rows (screen, keyboard, mouse).
- Why: The e2e rows need real Hyper-V and take minutes, the logic behind them needs feedback in seconds.
- Cost of change: low

## 2026-11-23 A box gone at an action's turn ends it successfully

- Type: decision
- Phase: phase 24
- What: `run` in the storage box package treats a box missing at the change step, or the sentinel `errGone`, as success without `Apply`.
- Why: only queued deletes can meet a missing box, because deleting the box is locked while any action runs.
- Cost of change: low

## 2026-11-23 Subaccount and snapshot deletes wait instead of locking

- Type: decision
- Phase: phase 24
- What: deleting a subaccount or snapshot starts a queued action (`StartQueued` with the box reference) that waits its turn, and ends successfully when its item is already gone; every other action keeps answering 423 `locked`.
- Why: success criterion 1 of phase 24, so one `tofu destroy` of a box with a subaccount and a snapshot succeeds.
- Cost of change: low

## 2026-11-23 Phase 24 queueing tested by unit rows under CLI-06

- Type: decision
- Phase: phase 24
- What: the queueing logic is tested by four unit rows `CLI-06/...` in the storage box tests (wait, order, others still locked, item already gone) besides the e2e row `TestE2E/CLI-06/destroy`.
- Why: CLI-06 is the only requirement and is e2e; unit rows give fast feedback on the logic behind it.
- Cost of change: low

## 2026-11-23 Phase 24 deviation entries in the API plan

- Type: decision
- Phase: phase 24
- What: the new entries of `api.Deviations()` are written in plan 24-01, not in the documentation plan.
- Why: both DELETE routes are already in api-coverage.md, so DOC-03 stays green between the plans.
- Cost of change: low

## 2026-11-23 Storage box deletes of subaccounts and snapshots wait instead of answering locked

- Type: change
- Phase: change
- What: Q24 answered: deleting a subaccount or a snapshot never answers 423 `locked` because of another action on the same box but queues behind it, while the storage VM still works on one box one job at a time; one `tofu destroy` of a box with subaccounts and snapshots succeeds in its first run (CLI-06, new phase 24), and the hint to run it again leaves docs/guide.md and docs/deviations.md.
- Why: real Hetzner allows parallel actions on a box (`locked` is documented only for parallel creates, provider issue #1297 and PRs #1294, #1307) and the provider never retries a delete, so a destroy against Hetzner is not known to stop, and OpenTofu users expect one destroy to clean up.
- Cost of change: low (an addition: one small phase; the box lock stays for every other action)
- Replaces: 2026-11-22 Every storage box action locks the box

## 2026-11-23 Server console moves into milestone 1

- Type: change
- Phase: change
- What: the server console moves from the target vision into milestone 1 as phase 23 after phase 21: request_console returns `wss://console.hetzner.internal/?server_id=<id>&token=<token>` with a VNC password and a certificate of the hlab CA, the tray opens it from the server's actions menu (with or without CA trust), graphical via Hyper-V if an experiment meets the limits (about 5 frames per second at 1024x768, input within about 0.5 s, at most one CPU core per console), otherwise the serial console; details in `decisions/server-console.md`, requirements SRV-17 (changed), CON-01, CON-02 and UI-15.
- Why: the user wants the server console working in the first version.
- Cost of change: medium (an addition: one phase with a new hlabd component, an appliance forward and a tray window; the existing `unsupported_error` answer and the disabled menu entry are replaced)
- Replaces: 2026-11-18 Server protection and console details; 2026-11-18 request_console and vswitch subnets

## 2026-11-23 Server protection details

- Type: decision
- Phase: change
- What: `change_protection` writes the flags in the transaction that starts its action; delete checks protection before the lock (protected and locked answers `protected`); resource action lists accept `id` and `status`.
- Why: the simplest order that the description allows (the console part of the 2026-11-18 entry went with the console change).
- Cost of change: low
- Replaces: 2026-11-18 Server protection and console details

## 2026-11-23 vswitch subnets

- Type: decision
- Phase: change
- What: vswitch subnets return `invalid_input` on field `type`.
- Why: vSwitches couple to dedicated servers that hlab has no equivalent for (the console part of the 2026-11-18 entry went with the console change).
- Cost of change: low
- Replaces: 2026-11-18 request_console and vswitch subnets

## 2026-11-23 Project deletion removes storage boxes through their module

- Type: decision
- Phase: phase 21
- What: the storage box module implements `resource.Deleter` like the API delete (mark deleting, apply storage, remove box and release its IP); a failed apply keeps the project for a retry.
- Why: deleting a project left box accounts and files in the storage VM and never released the boxes' IPs, which broke PROJ-04 (found by the phase 21 verifier).
- Cost of change: low

## 2026-11-23 Deleting a project deletes its actions

- Type: decision
- Phase: phase 21
- What: `store.DeleteProject` also deletes the project's actions, and opening the store deletes the actions of projects that no longer exist; no schema change.
- Why: SQLite reuses the highest deleted project ID, so a new project listed and could read a deleted project's actions (found by UI-13); AUTOINCREMENT IDs would need a migration.
- Cost of change: low

## 2026-11-23 Storage box SSH keys are trimmed, multi-line keys refused

- Type: decision
- Phase: phase 21
- What: creating a box trims the whitespace around each SSH key and refuses a key that still spans lines with 422 on `ssh_keys`; the storage document trims every stored key and leaves out any that still spans lines.
- Why: keys read from a file end in a newline, and one such key made the storage agent refuse the whole document, so every box create failed.
- Cost of change: low

## 2026-11-23 Own storage box type form keeps the "All locations" checkbox

- Type: decision
- Phase: phase 21
- What: the own-type form keeps UI-SPEC.md's "All locations" checkbox, checked by default; checked, the console sends every catalog location explicitly, unchecked at least one location is required.
- Why: UI-SPEC.md asks for it and it meets the user's "all preselected" correction while the file keeps an explicit location list.
- Cost of change: low

## 2026-11-23 Client storage box plans run one after another

- Type: decision
- Phase: phase 21
- What: the hcloud, OpenTofu and Ansible storage box plans run in sequence.
- Why: each may have to fix the same storage box module in hlab, and parallel plans must not touch the same files.
- Cost of change: low

## 2026-11-23 Subaccounts and Snapshots tabs built standalone

- Type: decision
- Phase: phase 21
- What: the two tabs are standalone components wired into the box detail page by plan 21-10; shared types `StorageBox` and `BoxType` live in `lib/resources/storageBoxes.ts`.
- Why: parallel wave-2 plans stay off the same files.
- Cost of change: low

## 2026-11-23 UI-12 and UI-13 share one storage box

- Type: decision
- Phase: phase 21
- What: both console rows take the shared box fixture, with a 20-minute limit each.
- Why: the storage VM boots once instead of being deleted and recreated between them.
- Cost of change: low

## 2026-11-23 Own storage box types: size in bytes, explicit locations

- Type: decision
- Phase: phase 21
- What: the console sends the own-type size in bytes (MiB/GiB/TiB as binary units), and the own-type spec stores an explicit non-empty location list.
- Why: matches hcloud's units, and the user's location multi-select replaces "no location means all".
- Cost of change: low

## 2026-11-23 UI-12 reaches the folder browser through a subaccount home

- Type: decision
- Phase: phase 21
- What: the folder browser test uses a subaccount with home directory `data/web` created through the API; the project-list count test is part of UI-12.
- Why: needs no file transfer; the count is new console behaviour of this phase.
- Cost of change: low

## 2026-11-23 hcloud all list checked through its debug file

- Type: decision
- Phase: phase 21
- What: the `all list` test reads hcloud's debug file to prove no request went to Hetzner; the file is written to the run's temp folder, deleted and never logged.
- Why: it is the only record of every request host, and it holds the token.
- Cost of change: low

## 2026-11-23 Deleting a storage box needs its name typed (Q23)

- Type: decision
- Phase: phase 21
- What: the console's confirm dialog for deleting a storage box asks for the box name to be typed, as for volumes; UI-SPEC.md adds storage boxes to that list.
- Why: The user chose it (Q23): deleting a box destroys its data and all its snapshots.
- Cost of change: low

## 2026-11-23 Upgrade to wcc 0.32.23

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.21 to 0.32.23: section "Lists for the user" added to CLAUDE.md, empty "If BLOCKED" section restored in STATUS.md.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-23 Error 67 came from Git Bash, not from SMB

- Type: assumption
- Phase: phase 22
- What: `net use` failed with system error 67 in phase 20 because Git Bash collapsed the leading `\` of the UNC path; smb.conf and `internal/hostnet` stay unchanged.
- Why: run from PowerShell or Go, `net use` mapped, wrote and read a box's and a subaccount's share without any change (phases/22/RESEARCH.md); SBOX-22 proves it, and a failure with an intact path becomes an open decision.
- Cost of change: low

## 2026-11-23 SBOX-22 is one e2e test in its own file

- Type: decision
- Phase: phase 22
- What: SBOX-22 is one e2e row covering the box share and the subaccount share, in the new file `test/e2e/storagebox_hostsmb_test.go`, with no unit row; SMB from WSL2 and servers is checked by the unchanged SBOX-14, SBOX-12 and SBOX-04 rows.
- Why: the phase has no product logic to test, and a separate file keeps the 610-line protocols test file from growing while the shared helpers are reused.
- Cost of change: low

## 2026-11-23 SMB from the Windows host (Q22)

- Type: change
- Phase: change
- What: The Windows host must map a box's and a subaccount's SMB share with `net use` (new requirement SBOX-22, e2e), built in the new phase 22 before phase 21.
- Why: The user chose it (Q22): mapping a share from Windows is the common way people use a Hetzner storage box over SMB, and phase 20 found `net use` failing with system error 67.
- Cost of change: low

## 2026-11-23 Samba ID map pinned for shared box UIDs

- Type: decision
- Phase: phase 20
- What: After setting the Samba accounts, each apply pins every account's SID-to-UID entry and maps each box UID to S-1-22-1-<uid> in Samba's cache (`net cache add`, 520 weeks).
- Why: A box and its subaccounts share one UID, and smbd takes a file's owner SID from a cached UID-to-SID mapping that each first login rewrites, so one subaccount login took the box's write access on `backup` (documentation run); it relies on Samba's internal cache keys, which SBOX-14 guards.
- Cost of change: low

## 2026-11-23 Mounts inside homes are handled by descriptor

- Type: decision
- Phase: phase 20
- What: `.zfs/snapshot` is attached read-only with open_tree, mount_setattr and move_mount onto a folder pinned through os.Root, and mounts inside a home are unmounted through an os.Root descriptor whose mount ID matches mountinfo (rollback, removeChroot, applyZFS).
- Why: A box account can swap a path for a symlink over SFTP, so a path-based remount or unmount could hit any mount of the storage VM (review of 20-07).
- Cost of change: low

## 2026-11-23 Storage agent jobs wait for a running apply

- Type: decision
- Phase: phase 20
- What: Every storage agent job sent by hlabd waits until a running storage apply has finished.
- Why: An apply reads the boxes long before it sends the document, so a snapshot created meanwhile was removed by the agent as unlisted (SBOX-05 e2e failure).
- Cost of change: low

## 2026-11-23 rollback_snapshot details

- Type: decision
- Phase: phase 20
- What: Snapshots "created after the target" are those with a higher ID; an empty `snapshot` answers 422 invalid_input; an unknown snapshot answers 404 before the box's active check.
- Why: IDs follow creation order even when two snapshots share a second.
- Cost of change: low

## 2026-11-23 Rollback on the storage VM

- Type: decision
- Phase: phase 20
- What: The agent keeps the last applied document for the rollback job; the fence is the nftables table `hlab_rollback` (input, TCP reset), cleared at the start of each rollback; it waits up to 30 s with `fuser -m` on the homes of the box and its subaccounts, unmounts without MNT_DETACH, keeps the old data at `<root>/.rollback-<box>` until deleted, and rebinds through `updateBox` also after a failure; rollback names are checked like snapshot names.
- Why: The job carries no IPs, subaccounts or ZFS setting, and a busy mount must stop the rollback before the swap so the data stays untouched.
- Cost of change: low

## 2026-11-23 Snapshots on the storage VM

- Type: decision
- Phase: phase 20
- What: Snapshots live in `<root>/.snapshots/<user>/<name>` (root:<box gid>, 0750); `.zfs/snapshot` is a read-only bind inside the chroot home that SFTP, SCP, SMB, FTP and WebDAV show (the documentation run listed it over WebDAV); snapshot jobs refuse names outside `^[A-Za-z0-9][A-Za-z0-9._-]*$` with 400.
- Why: A private bind cannot reach the box data or other homes; it follows E18.
- Cost of change: low

## 2026-11-23 vsftpd setup on the storage VM

- Type: decision
- Phase: phase 20
- What: pam_access also restricts a box account without reachable_externally; every account not skipped gets a vsftpd user file with write_enable by its read-only flag; vsftpd.conf replaces Debian's file (pam_service_name=vsftpd, local_root=/srv/chroot/$USER/home, allow_writeable_chroot=YES); changes reload through `systemctl reload vsftpd`; the TLS options are `ssl_tlsv11`/`ssl_tlsv12`/`ssl_tlsv13` and there is no `reverse_lookup_enable`, as Debian's vsftpd 3.0.5 binary knows them (RESEARCH.md section 4 names them wrongly).
- Why: sshd, Samba and WebDAV restrict the box account the same way, FTP has no access switch, and vsftpd's default PAM service is "ftp".
- Cost of change: low

## 2026-11-23 Apache setup on the storage VM

- Type: decision
- Phase: phase 20
- What: WebDAV password files are 0640 root and the box's group; without box IPs Apache is disabled; a box IP not listening on 443 15 s after a graceful reload gets one restart; a changed certificate reloads Apache; a failing configtest puts the old site back.
- Why: mpm-itk switches to the box user before authentication, Apache refuses to start without a Listen line, and a graceful reload picking up a new Listen is unconfirmed (RESEARCH.md section 3).
- Cost of change: low

## 2026-11-23 Snapshot plan bookkeeping

- Type: decision
- Phase: phase 20
- What: The plan job writes `_hlab.plan_checked` to every box with a plan on each run and clears it when the plan goes; a missing value or one older than the cloud start counts as now or the start; automatic snapshots take the run time as name and `created`; the stats refresh updates only the snapshots its result names.
- Why: A plan just enabled or a cloud just started never catches up past times, as `docs/deviations.md` says.
- Cost of change: low

## 2026-11-23 Plans with end-to-end rows finish in the main checkout

- Type: decision
- Phase: phase 20
- What: A plan with end-to-end rows is merged into the phase branch once its fast checks pass and runs its end-to-end rows and repairs in the main checkout, one such plan at a time.
- Why: The e2e harness refuses to run outside the checkout whose `bin\hlabd.exe` the dev service hlab-dev runs, and re-registering the service needs elevated rights.
- Cost of change: low

## 2026-11-23 Samba setup on the storage VM

- Type: decision
- Phase: phase 20
- What: NT hashes are imported from a 0600 smbpasswd file in /run with `pdbedit -i` and the account's SID is restored with `pdbedit -r -U`; smbd restarts when its interface list (the box IPs) changes and reloads otherwise; a box with Samba off gets an empty per-IP file and no tdbsam entry.
- Why: `pdbedit --set-nt-hash` takes the hash only in argv, and `reload-config` does not make smbd listen on new addresses (Samba source).
- Cost of change: low

## 2026-11-23 Snapshot API details

- Type: decision
- Phase: phase 20
- What: The snapshot description pattern is anchored, name and `created` come from `Deps.Clock`, `create_snapshot` and `delete_snapshot` call the agent job without an Apply (a failed create deletes the record and the agent removes the unlisted subvolume at its next apply), and delete runs on a box that is not active, like `delete_subaccount`.
- Why: The unanchored pattern of the description accepts any text, and the test clock must give matching names.
- Cost of change: low

## 2026-11-23 Storage certificate in kv, renewed daily

- Type: decision
- Phase: phase 20
- What: The storage VM's wildcard certificate is kept in kv (`storage.tls_cert`, `storage.tls_key`), reissued when missing, unparseable, not matching its key or within 30 days of expiry, and the daily cloud job `storage-tls` pushes the storage document again when that is due while boxes exist.
- Why: Without the job a certificate would expire on a storage VM whose boxes do not change for 60 days (review of 20-05).
- Cost of change: low

## 2026-11-23 Install groups run only for packages on the ISO

- Type: decision
- Phase: phase 20
- What: Each install group of `hlab-agent-install.sh`, xfs and HAProxy included, runs only when its main package is on the ISO, and the dynamic memory minimum is the constant `platform.DynamicMemoryMinMB` (512).
- Why: The storage ISO now carries libicu76, liblua5.4-0 and lsb-base, which would otherwise be installed again at every storage VM boot.
- Cost of change: low

## 2026-11-23 Storage box firewall chains pass the protocol ports

- Type: decision
- Phase: phase 20
- What: Each box IP gets its own nftables chain `box<i>`, jumped to from the forward chain, that drops external sources of boxes not reachable externally and ports other than TCP 21, 22, 23, 443, 445 and 50000-50099, and returns otherwise; the storage agent's 9100 stays unreachable from box IPs.
- Why: Allowed traffic must still pass the server's own outbound firewall (FW-03), and phase 20 serves FTP, WebDAV and SMB on the box IPs.
- Cost of change: low
- Replaces: 2026-11-23 Storage box firewall chains only drop or return; 2026-11-23 Only SSH ports reach box IPs in phase 19

## 2026-11-23 Storage agent services and snapshot contract

- Type: decision
- Phase: phase 20
- What: Samba, Apache and vsftpd are storage-agent services that register in `init`; `agentapi.Box.Snapshots` lists the stored snapshot names, and the agent removes only unlisted snapshot subvolumes; `StorageJob.Newer` carries the snapshots a rollback deletes, and `SnapshotFS` (referenced bytes) joins `SnapshotSizes` (exclusive bytes).
- Why: The three service plans then touch only their own files, and the agent can clean up after an interrupted create without ever losing a stored snapshot.
- Cost of change: medium

## 2026-11-23 Snapshot records and automatic snapshots

- Type: decision
- Phase: phase 20
- What: Snapshots are the store kind `storage_box_snapshots` with `_hlab.creating` for an unconfirmed create; a name taken in the same second moves to the next free second; automatic snapshots are visible `create_snapshot` actions started through `StartQueued`, named `snapshot-daily--<UTC>`, and plan times missed while the cloud is stopped are skipped (tracked in `_hlab.plan_checked`).
- Why: It reuses the store, action and scheduler patterns of the storage boxes module; Hetzner documents only the `snapshot-daily--` prefix.
- Cost of change: medium

## 2026-11-23 Snapshot errors

- Type: decision
- Phase: phase 20
- What: A full `snapshot_limit` answers 403 `resource_limit_exceeded` with `details.limits [{"name":"snapshot_limit"}]`, and rollback_snapshot with an unknown snapshot answers 404 `not_found`.
- Why: The new API's code is undocumented (the Robot API answers 409); this matches the subaccounts limit already served.
- Cost of change: low

## 2026-11-23 Storage VM setup for the protocols

- Type: decision
- Phase: phase 20
- What: `VMSpec.DynamicMemory` (minimum 512 MB, startup and maximum MemoryMB) switches existing storage VMs at their next start while off; the install script installs the Samba, Apache and vsftpd groups each while its tool is missing; the rollback fence is a reject rule set through `google/nftables`.
- Why: Storage VMs from phase 19 get the protocols at their next boot, and the generic image has no `nft` program.
- Cost of change: low

## 2026-11-23 Protocol test rows

- Type: decision
- Phase: phase 20
- What: TestE2E/SBOX-04/ftp is the pam_access proof, SMB and WebDAV get matching SBOX-04 and SBOX-12 rows, and the SMB refusal of a subaccount without reachable_externally is checked from WSL2 only.
- Why: Each protocol plan proves its own refusal and read-only access; curl on a server speaks only SMB1.
- Cost of change: low

## 2026-11-23 One wildcard certificate for WebDAV and FTPS

- Type: decision
- Phase: phase 20
- What: hlabd issues one certificate `*.storagebox.hetzner.internal` from the hlab CA for all Apache vhosts and vsftpd; Apache keeps one name-based vhost per account and selects it by SNI.
- Why: The user chose it (Q1): vsftpd cannot pick a certificate by name, and a per-account certificate would be issued again at every subaccount change.
- Cost of change: medium
- Replaces: 2026-11-21 WebDAV for subaccounts by name-based virtual hosts

## 2026-11-23 vsftpd refuses subaccounts by pam_access

- Type: decision
- Phase: phase 20
- What: vsftpd refuses subaccounts without reachable_externally from the host and WSL2 through `pam_access` in `/etc/pam.d/vsftpd`; an end-to-end row proves it on Debian 13.
- Why: The user accepted the evidence from vsftpd's source and access.conf(5) (Q2); sshd, Apache and Samba refuse such subaccounts in their own configuration the same way.
- Cost of change: low

## 2026-11-23 Snapshot plan days combine with AND

- Type: decision
- Phase: phase 20
- What: when both `day_of_week` and `day_of_month` of a snapshot plan are set, a day must match both.
- Why: The user decided it from the Ansible documentation of hetzner.hcloud.storage_box (RESEARCH.md of phase 20, section 5); Hetzner's API description does not say.
- Cost of change: low

## 2026-11-23 Storage VM with 2048 MB dynamic memory

- Type: decision
- Phase: phase 20
- What: the storage VM gets 2048 MB of dynamic memory.
- Why: The user set it because smbd, Apache with mpm-itk and vsftpd run side by side with sshd.
- Cost of change: low

## 2026-11-23 Storage box actions store their change before Apply

- Type: decision
- Phase: phase 19
- What: Box and subaccount actions store their change before Apply and put the whole old record back (or delete a new subaccount) when Apply fails, then push the appliance document again; create and delete keep activation and removal after a successful Apply; `storage_box_type` is accepted as name or ID.
- Why: The storage VM must get each change with its own action (found by the documentation run), and hcloud sends the type ID.
- Cost of change: low

## 2026-11-23 Storage VM in the cloud's life

- Type: decision
- Phase: phase 19
- What: Apply pushes the appliance document before it ensures the storage VM, cloud start also runs the storage apply when the `storage` folder exists without boxes, halt shuts the storage VM down after the servers and compacts its data disk (skipped and logged with `save`, at a Windows shutdown and with the uninstall override), and a failed box action pushes the appliance document again through `storageboxes.Deps.PushAgent`.
- Why: The appliance must route the storage address before hlabd can reach the agent, an orphaned VM must go, and the appliance must match the store after every outcome.
- Cost of change: low

## 2026-11-23 Boxes being deleted leave the appliance at once

- Type: decision
- Phase: phase 19
- What: `Contribute` skips boxes marked `Deleting`, so the push during a delete removes the box's route and names and, after the last box, `Config.Storage`.
- Why: Users and servers must not resolve a box that is being deleted (review of 19-11).
- Cost of change: low

## 2026-11-23 Storage agent puts box IPs on its interface

- Type: decision
- Phase: phase 19
- What: At each apply the storage agent adds every box IP as a /32 on the interface carrying `Addrs.Storage` and removes /32s of the public range no box holds; a box whose IP cannot be added fails like any other box step.
- Why: The appliance routes box IPs to the storage VM's MAC, so the VM must own them.
- Cost of change: low

## 2026-11-23 Storage VM boot and packages

- Type: decision
- Phase: phase 19
- What: The storage debs install from the shared `hlab-agent-install.sh` in one `dpkg -i` at every boot until btrfs exists; the storage user-data disables `ssh.socket`, enables `ssh.service` and keeps SSH host keys (`ssh_deletekeys: false`); a failed deb download fails the storage VM creation before any VM exists, and a create never deletes an existing data.vhdx.
- Why: A failed first boot retries, box clients keep trusting the host keys, and box data are never lost by a create.
- Cost of change: low

## 2026-11-23 Storage box usage, free space and stats

- Type: decision
- Phase: phase 19
- What: change_type asks the agent for usage only for a smaller type and checks free space only for a larger one; stats are refreshed every `StatsEvery` by a scheduled job and after each box action, a failing usage job is only logged; the job reads the store through the new `actions.Manager.Store()`.
- Why: Each check runs only where it can refuse, and stats must never fail an action.
- Cost of change: low

## 2026-11-23 Account chroots without /tmp

- Type: decision
- Phase: phase 19
- What: Account chroots have no `/tmp` (removed with its content at the next apply), and `storage-shell` sets `TMPDIR=/home` for accounts that may write.
- Why: Nothing may be written outside the box quota onto the storage VM's system disk (review of 19-07).
- Cost of change: low

## 2026-11-23 Storage agent owns sshd_config

- Type: decision
- Phase: phase 19
- What: The storage agent writes all of `/etc/ssh/sshd_config` (only listed accounts, no root) and reloads `ssh.service`; `storage-shell` and `storage-keys` run from `/usr/local/sbin/hlab-agent`.
- Why: Debian and cloud-init settings cannot reach past its Match blocks.
- Cost of change: low

## 2026-11-23 Port 23 and key filters are strict

- Type: decision
- Phase: phase 19
- What: Port 23 accepts only the flags OpenSSH scp and borg serve really send and refuses quotes (paths with spaces) and unknown users; authorized keys are passed on as `<type> <base64>` only, dropping options and comments.
- Why: No user input may become a command or an sshd key option.
- Cost of change: low

## 2026-11-23 Storage box not-active guard and Recover

- Type: decision
- Phase: phase 19
- What: Every box and subaccount action except the deletes answers 423 `locked` on a box that is not active (checked in `boxAction` and `subAction`); `Recover` at start deletes boxes still `initializing` and finishes deletes still marked `_hlab.deleting`.
- Why: No box stays stuck after a stop or crash, and a box whose data are gone never shows as active.
- Cost of change: low

## 2026-11-23 One failing box or subaccount does not stop the storage apply

- Type: decision
- Phase: phase 19
- What: A refused or failing subaccount is logged, loses its chroot and is left out of the passwords; a failing box is logged and skipped while the others are applied, and the PUT answers 500 with all box failures joined; a failed format stops the apply.
- Why: A user's files must not stop the storage of every other box (review of 19-04), and hlabd must not take a failed box as applied.
- Cost of change: low

## 2026-11-23 Box rules before established accept

- Type: decision
- Phase: phase 19
- What: The storage box rules run first in the forward chain of `hlab_fw`, before the established/related accept.
- Why: Switching off reachable_externally also cuts connections already open.
- Cost of change: low

## 2026-11-23 Storage agent sets hashes through chpasswd

- Type: decision
- Phase: phase 19
- What: The storage agent sets account hashes with `chpasswd -e` on stdin and writes only changed entries.
- Why: No hash appears in argv, ps or error messages.
- Cost of change: low

## 2026-11-23 Storage VM on-disk layout

- Type: decision
- Phase: phase 19
- What: Each box is the subvolume `<mount>/<user>`, chroots live under `/srv/chroot/<user>`, the passwd home is `/home` and the shell `/bin/sh`; `Plan` adds `StepUpdateBox` for every box in the document.
- Why: Chroot and data stay apart, and an update never touches data.
- Cost of change: medium

## 2026-11-23 Storage agent guards subaccount homes

- Type: decision
- Phase: phase 19
- What: The storage agent skips and logs a subaccount whose home is absolute, has a `..` element or leaves the box (checked through os.Root), and binds homes through a descriptor pinned by os.Root.
- Why: Defence in depth behind the API check, also against symlinks swapped in later.
- Cost of change: low

## 2026-11-23 hlab-agent role flag

- Type: decision
- Phase: phase 19
- What: `hlab-agent --role appliance|storage` (default appliance); the storage role listens on `<transit>:9100` and answers 503 to jobs before the first apply mounted the disk.
- Why: One binary serves both VMs.
- Cost of change: low

## 2026-11-23 Subaccount home directory without dot-dot

- Type: decision
- Phase: phase 19
- What: `checkHome` refuses any `..` element with 422 `invalid_input` (field `home_directory`) on create and change_home_directory, and stores the value cleaned by `path.Clean`.
- Why: A home directory must never lead a subaccount into another box's data (review of 19-06).
- Cost of change: low

## 2026-11-23 Storage box delete releases its IP

- Type: decision
- Phase: phase 19
- What: A successful `delete_storage_box` releases the box IP, and `storageboxes.Deps` gets `PublicRange`, `Job`, `FreeBytes` and `Clock`.
- Why: Create now assigns an IP from the public range, which must not leak.
- Cost of change: low

## 2026-11-23 Storage client readiness and generation

- Type: decision
- Phase: phase 19
- What: `StorageClient.WaitReady` takes any HTTP answer to `GET /v1/storage` as ready, and `Push` keeps its own generation counter in kv `storage.generation`, bounded only by the caller's ctx.
- Why: The storage agent has no status path, and pushes are bounded by each action's own deadline.
- Cost of change: low

## 2026-11-23 Exported transit.Broadcast

- Type: decision
- Phase: phase 19
- What: `transit.Broadcast(prefix)` is exported and replaces ipam's private broadcast helper.
- Why: `Addrs.Storage` and ipam need the same broadcast address.
- Cost of change: low

## 2026-11-23 Own storage box types follow own server types

- Type: decision
- Phase: phase 19
- What: Own storage box types need IDs from `MinOwnID`, a name, and no duplicate within the own file; the catalog exposes `StorageBoxTypes()`, `SetStorageBoxTypes` (tests), `StorageBoxTypeByRef`, `ReloadOwnStorageBoxTypes` and `OwnStorageBoxTypesWarning`.
- Why: The same rules as `server-types.custom.json` keep both files alike for the user, and the method guards the list for reload.
- Cost of change: low

## 2026-11-23 Storage box quota with full qgroups

- Type: decision
- Phase: phase 19
- What: Each box gets a subvolume with a level-1 qgroup limited to the type size (E18), not btrfs simple quotas.
- Why: Full qgroups are tested and their snapshot and rollback accounting is known; squota limits are unprobed (RESEARCH 6).
- Cost of change: medium

## 2026-11-23 Failed storage box create deletes the box

- Type: decision
- Phase: phase 19
- What: A failed or interrupted `create_storage_box` deletes the box and releases its IP, and the action carries the error.
- Why: No box gets stuck `initializing` and a new create just works (user answer).
- Cost of change: low

## 2026-11-23 Free-space reserve for storage boxes

- Type: decision
- Phase: phase 19
- What: Before `create_storage_box` and `change_type` to a larger type hlabd answers 412 `resource_unavailable` when the host drive of the data folder has less than 10 GiB free.
- Why: The dynamic data disk grows into the host drive, and a full drive would break every VM.
- Cost of change: low

## 2026-11-23 No image file per storage box

- Type: decision
- Phase: phase 19
- What: All boxes share one dynamic `storage\data.vhdx` with btrfs subvolumes; one image file per box was rejected.
- Why: Per-box files need host and VM to coordinate at every create and delete and a shrink at change_type.
- Cost of change: medium

## 2026-11-23 Storage agent package and subcommands

- Type: decision
- Phase: phase 19
- What: The storage agent is `internal/storageagent`, run as `hlab-agent --role storage`, with `storage-shell` (port 23 ForceCommand) and `storage-keys` (AuthorizedKeysCommand) as subcommands of the same binary.
- Why: No script runs through `sh -c`, and the filters stay testable Go.
- Cost of change: low

## 2026-11-23 Storage data disk at SCSI LUN 1

- Type: decision
- Phase: phase 19
- What: The data disk is attached at SCSI LUN 1 of the storage VM.
- Why: The agent finds it by a fixed path.
- Cost of change: low

## 2026-11-23 Read-only subaccounts get a read-only bind mount

- Type: decision
- Phase: phase 19
- What: Besides `internal-sftp -R` and `rrsync -ro`, a read-only subaccount's home is a read-only bind mount.
- Why: Borg serve has no read-only switch, so only the mount keeps it from writing.
- Cost of change: low

## 2026-11-23 Storage Apply matches the whole store

- Type: decision
- Phase: phase 19
- What: Apply makes the storage VM match all boxes of the store, creating the VM for the first box and deleting the VM and `storage\` without boxes.
- Why: The VM lifecycle lives in one place.
- Cost of change: low

## 2026-11-23 Storage agent logic as pure functions

- Type: decision
- Phase: phase 19
- What: `Plan`, `Port23` and `AuthorizedKeys` are OS-independent pure functions.
- Why: Data safety, the port 23 filter and key formats get unit tests on Windows.
- Cost of change: low

## 2026-11-23 Storage agent job error codes

- Type: decision
- Phase: phase 19
- What: Storage agent jobs fail with `not_found` or `invalid_path`, which hlabd maps to 404 and 422.
- Why: The folders endpoint needs both answers.
- Cost of change: low

## 2026-11-23 agentapi.Storage carries PublicRange and HostNet

- Type: decision
- Phase: phase 19
- What: `agentapi.Storage` gets `PublicRange` and `HostNet` (added fields).
- Why: The storage VM needs them for the sshd `Match Address` of subaccounts not reachable externally.
- Cost of change: low

## 2026-11-23 New storage box promises under phase 18 IDs

- Type: decision
- Phase: phase 19
- What: The unit rows for failed create, delete after confirmation and stats sit under SBOX-09, the 423 on boxes not active under SBOX-02.
- Why: They are new promises of those requirements from CONTEXT.md and the backlog.
- Cost of change: low

## 2026-11-23 No unit row for storage VM lifecycle in cloud

- Type: decision
- Phase: phase 19
- What: The compaction at stop and the VM lifecycle in `internal/cloud` have no unit row; CHECKS.md and the SBOX-03 e2e row cover them.
- Why: A fake of Hyper-V would test only glue.
- Cost of change: low

## 2026-11-23 Own storage box types file

- Type: change
- Phase: change
- What: `%ProgramData%\hlab\storage-box-types.custom.json` adds the user's own storage box types (Hetzner `storage_box_type` objects, IDs from 100000) like `server-types.custom.json` (SBOX-21, unit, phase 19); its tray view is UI-14 (e2e, phase 21); the SBOX-06 e2e test uses a 64 MiB and a 128 MiB own type, writes the file with the user's ACL and calls `POST /mgmt/v1/settings/reload`.
- Why: the smallest Hetzner type is 1 TiB, so quota and change_type could not be proven end to end with real data; the user wants own types as a feature, not a test aid.
- Cost of change: medium

## 2026-11-23 Q21 deviations.md up to about 250 lines

- Type: change
- Phase: change
- What: SPEC.md D4 allows `docs/deviations.md` about 250 lines.
- Why: phases 19 and 20 add storage box access, protocols and snapshots (Q21).
- Cost of change: low

## 2026-11-22 Upgrade to wcc 0.32.20

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.19 to 0.32.20: the secrets rule of the project CLAUDE.md now lets the tool make the call and allows reading a credential out only where SPEC.md says so, inside that one command; version line set.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-22 Subaccounts stored at create, listed without paging

- Type: decision
- Phase: phase 18
- What: a new subaccount is stored with its username in the create transaction and `create_subaccount` only runs Apply; the list uses `api.ListAllRequest` (no paging, no `meta`, as the description has none); the limit error carries `details.limits [{"name":"subaccounts_limit"}]`, and the subaccount `update_access_settings` action is named `update_subaccount_access_settings`.
- Why: the description shows no initializing state or paging for subaccounts, and the test map expects these names.
- Cost of change: low

## 2026-11-22 Storage box actions validate before the lock

- Type: decision
- Phase: phase 18
- What: change_type, reset_password and the snapshot plan check the body and the stored box before the box lock, so invalid input gets 422 even while another action runs, and a type without `automatic_snapshot_limit` puts no upper bound on max_snapshots.
- Why: the API validates first; a null limit is read as no limit.
- Cost of change: low

## 2026-11-22 Storage box sort and stored catalog objects

- Type: decision
- Phase: phase 18
- What: sort key `stats.size_filesystem` sorts by `stats.size`, and a box stores `storage_box_type` and `location` as catalog objects at create, so a later catalog update does not change existing boxes.
- Why: the description offers the sort key without such a stats field; Hetzner boxes keep their type data.
- Cost of change: low

## 2026-11-22 Storage box types only from the shipped catalog

- Type: decision
- Phase: phase 18
- What: `catalog.Load` reads `storage-box-types.json` only from the shipped catalog folder, never from the data folder, so `hlab catalog update` cannot touch it.
- Why: CAT-03 updates only the Cloud catalogs.
- Cost of change: low

## 2026-11-22 Storage VM interfaces and catalog through hcloud

- Type: change
- Phase: phase 18
- What: the storage VM has the fixed address `Addrs.Storage` (default `198.19.255.253`) with `PUT /v1/storage` and `POST /v1/jobs`, the appliance document gets `Storage` and `StorageBoxes`, the storage modules register under `/hetzner/v1` and the management API forwards `/projects/{name}/api/hetzner/v1/{rest}`; the storage VM's Debian packages are pinned in `catalog/storage-debs.json`; `task catalog` reads the storage box types with a usable context through `hcloud --context <name> --endpoint https://api.hetzner.com/v1 api /storage_box_types -v page=<n> -v per_page=50` over all pages, else with the token of the hidden prompt; SBOX-08 is a unit test of the uninstall cleanup, INST-04 in the elevated suite covers removal on a real machine.
- Why: the user decided that the token stays inside hcloud, as for the Cloud catalogs, after the permission system refused reading it out with `config get token`.
- Cost of change: low
- Replaces: 2026-11-21 Storage VM interfaces and catalog

## 2026-11-22 apitest needs both API descriptions

- Type: decision
- Phase: phase 18
- What: `apitest.RequireDescription` loads both `cloud.spec.json` and `hetzner.spec.json`, so every apitest-based test needs both cached by `task openapi`.
- Why: one entry check for all tests.
- Cost of change: low

## 2026-11-22 Bare API prefixes answer not_found

- Type: decision
- Phase: phase 18
- What: a request to exactly `/v1` or `/hetzner/v1` answers `not_found` before authentication.
- Why: the test map expects one behaviour for every unknown path under both prefixes.
- Cost of change: low

## 2026-11-22 Storage password error texts

- Type: decision
- Phase: phase 18
- What: `storagepass.Check` returns three texts (length, characters not allowed, missing character classes) used as the `details.fields` message.
- Why: the frozen description gives no wording.
- Cost of change: low

## 2026-11-22 Storage Box API on the same api.Router

- Type: change
- Phase: phase 18
- What: the Storage Box stays in milestone 1 as phases 18 to 21 (requirements SBOX-*, UI-12, UI-13, CLI-05 to CLI-07, details in `decisions/storage-box.md`), and its Hetzner API is served by the same api.Router and middleware under `/hetzner/v1`, a module choosing that prefix through the optional `resource.Prefixed`, with `APIRoutes` reporting full paths.
- Why: one middleware chain and one listener instead of a second router.
- Cost of change: low
- Replaces: 2026-11-21 Storage Boxes in milestone 1

## 2026-11-22 Storage box validation errors answer 422 at once

- Type: decision
- Phase: phase 18
- What: error codes follow Hetzner's general table, and every validation error answers 422 `invalid_input` at once where Hetzner sometimes fails the action (`action_failed`, e.g. home_directory), listed in `docs/deviations.md`.
- Why: an immediate, specific answer; the frozen description names no specific codes.
- Cost of change: low

## 2026-11-22 Storage box action list by wildcard route

- Type: decision
- Phase: phase 18
- What: `/storage_boxes/actions/{id}` is served through `GET /storage_boxes/{id}/{sub}` as zones do, with an alias in the docs test.
- Why: Go's ServeMux rejects that pattern beside `/storage_boxes/{id}/actions`.
- Cost of change: low

## 2026-11-22 Storage box stored layout

- Type: decision
- Phase: phase 18
- What: subaccounts are their own store kind `storage_box_subaccounts` with the box ID; the box keeps `_hlab.n`, `_hlab.next_sub`, `_hlab.ssh_keys` and `_hlab.hashes {crypt, nt}`, the subaccount `_hlab.hashes`, and kv `storage_boxes/next_user` starts at 100000.
- Why: global subaccount IDs and lists through ListRequest; phase 19 reads these fields.
- Cost of change: medium (stored layout read by later phases; no migration today)

## 2026-11-22 Storage box system and server names

- Type: decision
- Phase: phase 18
- What: `system` is `<LOCATION>-BX<box id>` and `server` is `u<n>.storagebox.hetzner.internal` (subaccounts `u<n>-sub<m>.…`), all null with `username` and `snapshot_plan` while `initializing`.
- Why: a stable number in the documented form, under hlab's own domain.
- Cost of change: low

## 2026-11-22 Storage passwords without x/crypto

- Type: decision
- Phase: phase 18
- What: the password policy (the description's exact character set, 12 to 128 runes) lives in `internal/storagepass` and the NT hash in `internal/nthash`, without golang.org/x/crypto; the breach check is not done.
- Why: as `internal/shacrypt` already is, no new dependency.
- Cost of change: low

## 2026-11-22 Storage box Apply hook

- Type: decision
- Phase: phase 18
- What: `storageboxes.Deps.Apply` runs for every box and subaccount action, nil in phase 18 and set by phase 19; tests gate it to keep actions running.
- Why: phase 19 plugs the storage VM in without changing the handlers.
- Cost of change: low

## 2026-11-22 Storage box types from a recorded response in tests

- Type: decision
- Phase: phase 18
- What: unit tests use `internal/apitest/testdata/storage-box-types.json` (bx11, bx21, bx31 with fsn1, nbg1, hel1 prices, bx11 without nbg1), and the main session runs `task catalog` with context HLab once for the shipped catalog.
- Why: tests stay stable whether the shipped catalog exists or not.
- Cost of change: low

## 2026-11-22 Storage box coverage rows in one plan

- Type: decision
- Phase: phase 18
- What: plan 18-05 writes all Storage Box rows of `docs/development/api-coverage.md` and all `api.Deviations()` entries, 18-08 the prose.
- Why: keeps DOC-03 green between plans; the wave-4 plans touch different files.
- Cost of change: low

## 2026-11-22 Subaccount create and PUT are synchronous

- Type: decision
- Phase: phase 18
- What: subaccount create returns the subaccount at once with its username, and box and subaccount `PUT` are synchronous.
- Why: the frozen description shows no initializing state for subaccounts and synchronous PUTs.
- Cost of change: low

## 2026-11-22 Shutdown retry only for the API action

- Type: decision
- Phase: phase B3
- What: a new `platform.PowerShutdownRetry` asks a still booting guest again for up to 2 minutes and is used only by the API `shutdown` action, while `PowerShutdown` stays a single ask for halt, appliance stop and rescue.
- Why: the B3-02 review found that the retry in `PowerShutdown` stretched cloud stop and uninstall past the 60 s halt timeout of phase 09.
- Cost of change: low
- Replaces: 2026-11-22 Shutdown retry as a testable function

## 2026-11-22 LabelsField sets message and validity together

- Type: decision
- Phase: phase B3
- What: a helper `fail(m)` in LabelsField.svelte sets the error line and the custom validity together and is also used when a chip is put back into the box.
- Why: a stale invalid mark would otherwise block a later valid submit.
- Cost of change: low

## 2026-11-22 Server delete reads load balancer targets before removal

- Type: decision
- Phase: phase B3
- What: the delete action reads the targeting load balancer IDs for the push warning just before `remove`, and a store error there fails the action like any other store error.
- Why: after the removal the targets are gone, and a store error means the delete cannot be done safely anyway.
- Cost of change: low

## 2026-11-22 Who set stopping decides whether it sticks

- Type: decision
- Phase: phase B3
- What: the status watch remembers in memory the servers it moved to `stopping` from a VM reading and returns only those to `running` when the VM runs, while a `stopping` set by the shutdown action (whose running lock is the shutdown marker) stays until the VM is off.
- Why: the shutdown action ends once the guest accepted the request, before the VM is off, and the B2 log shows the watch reading `stopping` during a guest's own reboot.
- Cost of change: low

## 2026-11-22 Push after every API server delete

- Type: decision
- Phase: phase B3
- What: the API delete action pushes the agent document after removing the server, a failed push logs a warning naming the server and its load balancers, and project deletion does not push per server.
- Why: the document also carries the deleted server's metadata and firewall state, and mgmt already pushes once at the end of a project deletion.
- Cost of change: low

## 2026-11-22 One shared createdText in local time

- Type: decision
- Phase: phase B3
- What: lib/format.ts `createdText` shows local time as "YYYY-MM-DD hh:mm", views/servers/servers.ts re-exports it, and a value that cannot be parsed is shown as given.
- Why: one place for the format, matching the local time of the CLI.
- Cost of change: low

## 2026-11-22 Labels e2e proves no request through the Wails bridge

- Type: decision
- Phase: phase B3
- What: "UI-02 invalid labels" is its own test that records the page's calls to `/wails/runtime` and checks the API afterwards to prove nothing was sent.
- Why: the console sends API calls only through the Wails bridge.
- Cost of change: low

## 2026-11-22 Test IDs of the B3 fixes

- Type: decision
- Phase: phase B3
- What: the reboot and shutdown fixes are unit rows under SRV-07, the delete push under LB-06 and the Created times under UI-02.
- Why: these are the requirements whose promise the bugs break; the SRV-07 e2e stays unchanged for the milestone close.
- Cost of change: low

## 2026-11-22 Backlog phase B3

- Type: change
- Phase: change
- What: the user took the five backlog bugs (console Created times without zone, reboot status stuck at stopping under load, invalid labels dropped silently, server delete without agent push for load balancer targets, shutdown of a booting guest without retry) into phase B3 before phase 18, and dropped the Reconcile and uninstall CA items as overtaken by phases 09 and 12.
- Why: the user's choice from the backlog.
- Cost of change: low

## 2026-11-22 MCP progress counts notifications

- Type: decision
- Phase: phase 17
- What: progress notifications carry a counter rising with each notification as `progress` and the current state or the action's percentage as `message`.
- Why: the action's percentage can stay the same between polls, and MCP requires progress to increase.
- Cost of change: low

## 2026-11-22 MCP tools listed by name

- Type: decision
- Phase: phase 17
- What: `cli.MCPTools()` returns the tool names sorted by name.
- Why: go-sdk lists tools sorted by name and MCP-04 compares both lists.
- Cost of change: low

## 2026-11-22 Default of logs lines applied by the handler

- Type: decision
- Phase: phase 17
- What: the `logs` input schema has no `default` keyword; the handler applies 200 and the description says so.
- Why: go-sdk v1.8.0 panics on a call without arguments when the schema has a default.
- Cost of change: low

## 2026-11-22 Project actions of project 0 include those without a project

- Type: decision
- Phase: phase 17
- What: `store.ProjectActions(tx, 0)` matches `project_id IS NULL`, so project 0 finds the `delete_project` action, as `Actions.Get(0, id)` already does.
- Why: without it the 409 of a running project deletion could not name its action (Q20).
- Cost of change: low

## 2026-11-22 APIError carries the action ID of a conflict

- Type: decision
- Phase: phase 17
- What: `mgmtclient.APIError` gets an additive field `ActionID` (`action_id`, omitempty) that carries Q20 to the cli.
- Why: the cli already decodes this error, and `Error()` and `hlab project delete` stay unchanged.
- Cost of change: low

## 2026-11-22 MCP waits in their own plan

- Type: decision
- Phase: phase 17
- What: 17-02 builds the server and all tools without waiting, 17-03 adds waits, progress and cancel in `internal/cli/mcp_wait.go` and the MCP-05 e2e test.
- Why: together they exceed the size of one reviewable plan.
- Cost of change: low

## 2026-11-22 Stale status.json checked by modification time

- Type: decision
- Phase: phase 17
- What: the cli treats `status.json` as newer than its request by `os.Stat` modification time; `cloudstatus` stays unchanged.
- Why: the simplest check that needs no format change.
- Cost of change: low

## 2026-11-22 SCM running counts as cloud running

- Type: decision
- Phase: phase 17
- What: the MCP cloud waits poll `Service.State` and read `status.json` only for `stopped`.
- Why: hlabd reports Running to the service manager only after its start function returned.
- Cost of change: low

## 2026-11-22 Q20 Conflict on project deletion names the running action

- Type: decision
- Phase: phase 17
- What: the `409 conflict` of `DELETE /projects/{name}` carries `action_id` of the running deletion in its error object, so `project_delete` of the MCP server waits for it.
- Why: the user chose it at the phase start; success and failure of the running deletion stay exact, and the field is additive.
- Cost of change: medium

## 2026-11-22 Upgrade to wcc 0.32.19

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.18 to 0.32.19: only the version line in CLAUDE.md; rules, migrations and planning document sections were already current.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-22 Q19 Load balancer state changes keep answering locked

- Type: decision
- Phase: change
- What: change_algorithm, change_type and the public interface actions of a load balancer keep answering 423 `locked` while a target or service action holds it; docs/deviations.md says that a following command after hcloud's `delete-service`, which does not wait, can get `locked`.
- Why: the user keeps the rule that state changes of a resource itself refuse, and only one hcloud command is affected.
- Cost of change: low

## 2026-11-22 Volume detach commits after its push; task e2e limit 120 minutes

- Type: decision
- Phase: phase 13
- What: a volume detach records the volume detached only after its agent push (an internal `_hlab.detaching` keeps it out of the agent document meanwhile, Recover completes an interrupted detach whose disk already left the VM), and `task e2e` gets `-timeout 120m`.
- Why: GET showed `server: null` while the detach still held the volume, so UI-02 deleted into 423; the full run at the close took 58 minutes.
- Cost of change: low

## 2026-11-22 The agent retries interrupted netlink dumps

- Type: decision
- Phase: phase 13
- What: every link, address and route list on the agent's apply path is made again on `netlink.ErrDumpInterrupted`, up to 5 tries, before the push fails.
- Why: parallel pushes since the action queue let links change during a list, which failed an LB delete with server_error, and the delete loops need a complete list.
- Cost of change: low

## 2026-11-22 Queued RRSet changes commit at success

- Type: decision
- Phase: phase 13
- What: an RRSet create, delete or action is checked against a copy at the request, applied again at its turn into the zone's optional `_hlab.pending` (served only to the agent while it publishes), committed to the current record at success and dropped on error or by Recover after a stop; zone-level actions still store at once and refuse while an action holds the zone.
- Why: GET and the published zone must show a queued change only at success; the zone keeps all RRSets in one record, so a pending copy is the smallest fit.
- Cost of change: low

## 2026-11-21 The OpenTofu LB row does not read services from the data source

- Type: decision
- Phase: phase 13
- What: CLI-02/load_balancers reads `data.hcloud_load_balancer` by its targets and algorithm, not by its `service` list, and the hcloud LB row waits for `delete-service`, which does not wait for its action.
- Why: provider 1.70.2 declares `service` in the data source schema but never sets it (internal/loadbalancer/resource.go:421-450), on Hetzner as on hlab; hcloud v1.70.2 delete-service never calls WaitForActions.
- Cost of change: low

## 2026-11-21 Rules of an unapplied firewall succeed at once

- Type: decision
- Phase: phase 13
- What: when a firewall is applied to nothing, the `set_firewall_rules` action of `POST /firewalls` and of `set_rules` ends `success` inside the request (`actions.Manager.Succeed`), without an agent push.
- Why: Ansible's `firewall` module does not wait for these actions, so its next call hit 423 `locked`; Hetzner also finishes them at once.
- Cost of change: low

## 2026-11-21 Network members list attached members with status ok

- Type: decision
- Phase: phase 13
- What: `GET /networks/{id}/members` (Hetzner's shape, filters type, subnet and status, sort, pagination) lists a server or load balancer once its attach has succeeded, always with status `ok`; the CLI-03 networks update keeps its ip_range because hlab refuses `change_ip_range` while servers are attached.
- Why: it matches the network's own `servers` list, and the range change is covered by CLI-01 before the attach.
- Cost of change: low

## 2026-11-21 Reboot of a booting guest retries for two minutes

- Type: decision
- Phase: phase 13
- What: when Hyper-V refuses both InitiateReboot and InitiateShutdown with 32768 (guest still booting), hlab asks again every 2 s for up to 2 minutes before it reports InvalidState.
- Why: hcloud reboots right after poweron; retrying keeps the reboot graceful instead of a hard reset.
- Cost of change: low

## 2026-11-21 hcloud all list moves to phase 21

- Type: decision
- Phase: phase 13
- What: the row CLI-01/all is dropped from phase 13 and `hcloud all list` is tested in the CLI-05 row of phase 21.
- Why: hcloud v1.70.2 always fetches storage boxes from the Hetzner endpoint, so with the Cloud API endpoint alone it fails and sends the hlab project token to api.hetzner.com.
- Cost of change: low

## 2026-11-21 Firewall apply commits at success and delete waits for actions

- Type: decision
- Phase: phase 13
- What: `apply_to_resources`/`remove_from_resources` start one action per target that keeps `_hlab.pending` and commits its own target to the current record at success (Recover clears leftovers), a server create still writes `applied_to` in its create transaction and queues its `apply_firewall` on the firewall, and DELETE /firewalls/{id} answers 423 `locked` while an action holds the firewall.
- Why: GET shows an apply only at success while FW-01/server_create expects the firewall applied in the create response, and without the lock a firewall could be deleted during its apply.
- Cost of change: low

## 2026-11-21 Floating IPs commit at success, primary IPs restore their earlier state

- Type: decision
- Phase: phase 13
- What: floating IP assign/unassign keep `_hlab.pending` and commit at success; primary IP assign/unassign store the change with the earlier state (`_hlab.before`, server `_hlab.public_before`) shown on GET until success, restore it on error and in Recover, which also rewrites the seed and fails cloud start if that fails; an assign of a floating IP that is on a server also queues on that server.
- Why: a floating IP change is only an agent push, while a primary IP change rebuilds the seed from the stored server record.
- Cost of change: medium

## 2026-11-21 Volume attach commits at success and delete waits for actions

- Type: decision
- Phase: phase 13
- What: a volume attach (also the one of a create with `server`, which only queues) is checked again at its turn, kept as `_hlab.pending` and applied to the current record at success; a failed push takes the disk off again, Recover drops a pending attach after a stop, and DELETE /volumes/{id} answers 423 `locked` while any action holds the volume.
- Why: GET shows the attach only at success, and without the lock a volume could be deleted during its attach.
- Cost of change: low

## 2026-11-21 Network actions keep the earlier state and restore it

- Type: decision
- Phase: phase 13
- What: network attach, detach, alias, subnet and route actions store their change at once, keep the earlier state in `_hlab.net_before` (servers) or `_hlab.shown` (networks), which GET shows while the action runs, and restore it on error and at cloud start (networks is a `resource.Recoverer`; a VM adapter an interrupted attach already added stays until that server's next network action).
- Why: adapter sync and agent push already read the stored change, so restoring covers the error and restart cases with a smaller change than committing at success.
- Cost of change: low

## 2026-11-21 Queued load balancer changes wait as pending state

- Type: decision
- Phase: phase 13
- What: a queued LB target or service action validates again against the state earlier actions left, keeps its result in the stored record's optional `_hlab.pending`, serves it to the agent while it runs and commits it to the visible fields only at `success` (dropped if the push fails).
- Why: GET must show the change only at `success`, while the agent document must already contain it when it is pushed.
- Cost of change: low

## 2026-11-21 Validation failures inside queued work keep their error code

- Type: decision
- Phase: phase 13
- What: a check that fails inside a queued action's work becomes the action's error with the code the request would have answered (e.g. `target_already_defined`), not `server_error`.
- Why: clients see the same code whether the conflict was found at the request or after the queue.
- Cost of change: low

## 2026-11-21 Private targets on an LB without network answer load_balancer_not_attached_to_network

- Type: decision
- Phase: phase 13
- What: `add_target` with `use_private_ip` on a load balancer without a committed network answers 422 `load_balancer_not_attached_to_network`, checked before `resolve_cloud_private_targets_error`.
- Why: the test map row asks for Hetzner's code for this case.
- Cost of change: low

## 2026-11-21 The Ansible servers row attaches no ISO

- Type: decision
- Phase: phase 13
- What: CLI-03/servers leaves out attaching an ISO, which hcloud and OpenTofu still cover.
- Why: the `hetzner.hcloud.server` module of collection 7.2.0 has no ISO option (checked with `ansible-doc` in WSL2).
- Cost of change: low

## 2026-11-21 Action queue in Run

- Type: decision
- Phase: phase 13
- What: `actions.StartQueued` refuses with `locked` only on the refuse refs, and every action's `Run` waits without taking a worker until all running actions with a lower ID that share a ref have finished; a queued action shows as `running`.
- Why: it realises the queuing the user asked for in one place without a new action status, and waiting before taking a worker keeps more queued actions than workers from deadlocking.
- Cost of change: medium

## 2026-11-21 Which actions queue instead of locked

- Type: decision
- Phase: phase 13
- What: network attach/detach and `change_alias_ips` queue on the network, subnet and route actions on the network, LB target and service actions on the LB and its target servers, volume attach/detach and floating/primary IP assign/unassign on the server, firewall apply/remove on the servers, a server create's firewall actions on the firewall, and RRSet create, delete and actions on the zone (zone-level actions keep refusing); all others refuse on every ref as before (table in phases/13/PLAN.md "Interfaces", deviation in deviations.md and `api.Deviations()`).
- Why: OpenTofu creates sibling resources in parallel and does not retry on `locked` (phases/13/RESEARCH.md §4), while Hetzner accepts these configurations.
- Cost of change: medium

## 2026-11-21 Client matrix rows register through init()

- Type: decision
- Phase: phase 13
- What: client rows register with `registerRow` from `init()` in one file per group (`test/e2e/clients_<group>[_<client>]_test.go`), and `testClients` runs each client's fixture rows one after another, the others in parallel.
- Why: plans of one wave never touch a shared dispatcher file, and a fixture server holds its slot only while its rows run.
- Cost of change: low

## 2026-11-21 One client matrix subtest per type and client

- Type: decision
- Phase: phase 13
- What: every type including the read-only catalog types has one row per client, snapshots are covered in the images row, and reverse DNS has own rows only for OpenTofu and Ansible because hcloud's `set-rdns` runs in each type's row.
- Why: each row stays filterable as `TestE2E/CLI-0n/<type>`.
- Cost of change: low

## 2026-11-21 The queue plan writes its own deviation entries

- Type: decision
- Phase: phase 13
- What: plan 13-02 adds the queue deviations to `api.Deviations()`, `api-coverage.md` and `deviations.md`; the documentation plan only checks them.
- Why: DOC-03 fails as soon as an `api.Deviations()` entry has no matching line in `api-coverage.md`.
- Cost of change: low

## 2026-11-21 Primary IP client rows use the fixture server

- Type: decision
- Phase: phase 13
- What: the primary IP rows assign an IPv6 primary IP to the powered-off fixture server after unassigning its own IPv6, then restore it.
- Why: Hetzner assigns primary IPs only to powered-off servers, and the fixture must stay reachable for the rows after it.
- Cost of change: low

## 2026-11-21 Bundle sets HLABUI from WixBundleUILevel

- Type: decision
- Phase: phase 12
- What: the bundle passes `HLABUI=1` to the MSI only when `WixBundleUILevel = 4` (full UI), so quiet and passive runs never show the prerequisite message box; the bundle UpgradeCode is 81FBE7E2-15D3-4E38-9FBA-57FC18DB72D5, named "HLab", Publisher webcodingcat.
- Why: a Burn built-in avoids the overridable fallback whose quiet install would wait on a message box; the value 4 is checked by the elevated suite.
- Cost of change: low

## 2026-11-21 MSI custom actions find hlabd.exe through INSTALLFOLDER

- Type: decision
- Phase: phase 12
- What: every custom action that runs while hlabd.exe is removed calls `"[INSTALLFOLDER]hlabd.exe"`; the shortcut component keys on the HKCU value `Software\webcodingcat\hlab\Shortcuts`; the MSI UpgradeCode is D98713BD-2D6B-4822-AB75-0F2943A2E802.
- Why: `[#file]` is blank when the component is Absent, and ICE43/ICE57 reject a file or HKLM key path for non-advertised shortcuts.
- Cost of change: low

## 2026-11-21 Uninstall runs only through internal/uninstall

- Type: decision
- Phase: phase 12
- What: `Uninstall(keepData)` is removed from `sysint.Installer`; `hlabd uninstall-cleanup` runs `uninstall.Run`, and a Hyper-V that cannot be loaded is reported as a failed object after all other steps.
- Why: nothing called the narrower path any more, and CONTEXT.md INST-04 requires every other step to run before an error is reported.
- Cost of change: low

## 2026-11-21 Prerequisite check refuses Server Core and finds the Hyper-V module in a version folder

- Type: decision
- Phase: phase 12
- What: `prereq.Facts.ServerCore` makes Server Core an unsupported edition, and the Hyper-V PowerShell module counts as present in `Modules\Hyper-V` or a version folder below it.
- Why: SPEC requires Desktop Experience, and Windows 11 keeps the module under `Hyper-V\2.0.0.0`.
- Cost of change: low

## 2026-11-21 Prerequisite check runs as an immediate MSI custom action from the Binary table

- Type: decision
- Phase: phase 12
- What: hlabd.exe is embedded as an MSI Binary and `check-prerequisites` runs as an immediate custom action before InstallValidate (`NOT REMOVE`), showing a message box when `HLABUI=1` and writing `%TEMP%\hlab-setup-prerequisites.log` otherwise.
- Why: INST-02 is unit-tested Go logic that must run before anything changes; `bal:Condition` cannot run Go and a failed ExePackage shows only an error code.
- Cost of change: low

## 2026-11-21 Prerequisite check needs no elevation

- Type: decision
- Phase: phase 12
- What: the check reads registry, files, the vmms service and Win32_ComputerSystem/Win32_Processor, never Win32_OptionalFeature or DISM.
- Why: an immediate custom action under Burn may run unelevated.
- Cost of change: low

## 2026-11-21 Service stop at upgrade and uninstall without ServiceControl

- Type: decision
- Phase: phase 12
- What: an upgrade runs a deferred `hlabd stop-service` in the old product, and a real uninstall lets `uninstall-cleanup` stop and remove the service; no ServiceControl element.
- Why: research could not confirm how ServiceControl handles a service that is already gone.
- Cost of change: low

## 2026-11-21 Uninstall stop mode is passed in the file uninstall-stop

- Type: decision
- Phase: phase 12
- What: the cleanup writes `<DataDir>\uninstall-stop` ("off" or "shutdown") before the SCM stop, and `cloud.halt` reads and deletes it, overriding stop_behaviour.
- Why: an SCM stop carries no parameter, and the `sysint.ServiceHost` interface stays as it is.
- Cost of change: low

## 2026-11-21 WSL trust removal at uninstall only when trust was used

- Type: decision
- Phase: phase 12
- What: uninstall removes the CA from WSL only when `trust_ca` is on or the root store held the CA; Hyper-V missing at uninstall counts as no VMs.
- Why: avoids running update-ca-certificates in every distro when trust was never on, and keeps uninstall possible without Hyper-V.
- Cost of change: low

## 2026-11-21 DeleteSwitch removes the host adapter on failure and retries

- Type: decision
- Phase: phase 12
- What: if `Remove-VMSwitch` fails, `DeleteSwitch` removes the management-OS adapter first and retries.
- Why: whether PVLAN settings block the removal is unverified; INST-04 settles it.
- Cost of change: low

## 2026-11-21 INST-01 does not test the WebView2 download path

- Type: decision
- Phase: phase 12
- What: INST-01 proves the install and that a present WebView2 is detected and the bootstrapper skipped, not its download.
- Why: Windows 11 always has WebView2, and removing it would damage the developer's machine (agreed by the user).
- Cost of change: low

## 2026-11-21 WebView2 bootstrapper cached outside the repository

- Type: decision
- Phase: phase 12
- What: `tools/installer` keeps the bootstrapper in `%LocalAppData%\hlab-build-cache`, refreshes it after 7 days, falls back to the cached copy on a failed download and embeds it in the bundle without committing it.
- Why: the redistribution terms allow embedding, and `%LocalAppData%\hlab` is deleted by uninstall.
- Cost of change: low

## 2026-11-21 Fixed UpgradeCodes for MSI and bundle

- Type: decision
- Phase: phase 12
- What: the MSI and the bundle each have one fixed UpgradeCode GUID in the repository that never changes.
- Why: the user set this for the MSI; the bundle's upgrades depend on its own code the same way.
- Cost of change: high

## 2026-11-21 Failed first install rolls back with keep-data cleanup

- Type: decision
- Phase: phase 12
- What: a failed first install rolls back by running `uninstall-cleanup --keep-data`.
- Why: removes host objects created so far without deleting data a user may have from an earlier install.
- Cost of change: low

## 2026-11-21 Tray per-user data deleted at uninstall

- Type: decision
- Phase: phase 12
- What: without KEEPDATA an impersonated deferred custom action deletes `%AppData%\hlab-tray.exe` (the Wails default WebView2 folder) and `%LocalAppData%\hlab` if present, for the uninstalling user only.
- Why: the tray uses the Wails default folder, and the user asked for the per-user data to go.
- Cost of change: low

## 2026-11-21 Tray e2e tests find project cards by their visible display name

- Type: decision
- Phase: phase 15
- What: the shared `projectCard` helper in tests/e2e/console.ts matches a card by its visible display name, and UI-05, `openConsole` and the screenshot spec use it; the direction arrow is named "Ascending"/"Descending" and the sort button "Sort: <key>".
- Why: the closed delete dialog keeps the name in a hidden element, so a hidden match made `toBeHidden` after a delete fail.
- Cost of change: low

## 2026-11-21 CLI project test matches the existing CREATED format

- Type: decision
- Phase: phase 15
- What: TestCLI/PROJ-06 matches CREATED as date and time with a space (`\S+ \S+`), the format `hlab project list` already prints.
- Why: the test wrote CREATED as one word, which contradicts the existing public output; the check stays the same.
- Cost of change: low

## 2026-11-21 Store counts resources per tab and takes display names

- Type: decision
- Phase: phase 15
- What: `store.CreateProject(tx, name, displayName)` replaces the old signature, and `CountResourcesByType` splits `images` by `json_extract(doc, '$.type')` in one GROUP BY.
- Why: internal interface; keeps mgmt free of image knowledge since the type is already stored.
- Cost of change: low

## 2026-11-21 Schema v1 migration adds display_name with a default

- Type: decision
- Phase: phase 15
- What: the v1→v2 migration runs `ADD COLUMN display_name TEXT NOT NULL DEFAULT ''` then sets it to `name`, so a migrated file keeps a DEFAULT a fresh v2 file lacks.
- Why: SQLite allows NOT NULL on ADD COLUMN only with a default, and a table rebuild is not worth it.
- Cost of change: low

## 2026-11-21 Tray project list sorting and stored view

- Type: decision
- Phase: phase 15
- What: display names sort with `localeCompare` (sensitivity "base"), ties by name in the same direction; sort and layout stay in localStorage `hlab.projectSort` and `hlab.projectLayout` (default `grid`, invalid values fall back); count labels follow the tab titles in sidebar order.
- Why: sorting must not depend on case, and the keys follow the existing `hlab.project`.
- Cost of change: low

## 2026-11-21 resources_by_type has one key per console tab

- Type: change
- Phase: phase 15
- What: `GET /projects` counts images as `snapshots` and `backups` instead of `images`, so every key of `resources_by_type` opens exactly one console tab.
- Why: the user's answer at the start of phase 15; "N resources" entries must each open one tab.
- Cost of change: medium

## 2026-11-21 hlab project list puts the display name last

- Type: decision
- Phase: phase 15
- What: `hlab project list` prints NAME, RESOURCES, CREATED, DISPLAY NAME; `--output json` adds `display_name` as a field.
- Why: the user's answer at the start of phase 15; existing columns keep their place for scripts and the display name with its spaces stands last.
- Cost of change: medium

## 2026-11-21 Project names transliterate only ä ö ü ß

- Type: decision
- Phase: phase 15
- What: letters without a canonical decomposition (ø, æ, œ, ł, đ, ...) become `-` like any other character; the derived name is cut to 255 before `-` is trimmed at both ends.
- Why: the user's answer at the start of phase 15; PROJ-06 names only those four, and a cut must not leave a trailing `-`.
- Cost of change: medium

## 2026-11-20 UI-09 is checked before the release, phase 14 closes

- Type: decision
- Phase: phase 14
- What: the manual check UI-09 no longer stops phase 14; it stays in "Open manual checks" and is done before the release of milestone 1.
- Why: the user deferred it to shortly before the release.
- Cost of change: low

## 2026-11-20 Section tabs sit above the section title row

- Type: decision
- Phase: phase 14
- What: the tab row of an entry is at the top of every page of that entry, above the open view's own h1 and help button; no separate entry title.
- Why: avoids a second heading and edits to every section.
- Cost of change: low

## 2026-11-20 Toast type comes from the request path

- Type: decision
- Phase: phase 14
- What: `resourceType(req.path)` in `lib/format.ts` derives the type and `runOn(project, name, action, req)` wraps `runApi`, for every action on a named resource, creates included.
- Why: one shared place without a new prop on every actions menu.
- Cost of change: low

## 2026-11-20 LabelsField keeps its props

- Type: decision
- Phase: phase 14
- What: the chip box keeps `rows` (bindable) and `error`; `parseLabel` and `checkLabel` in `labels.ts` follow `internal/labels` incl. optional prefix and reserved `hetzner.cloud/`.
- Why: its 25 users stay unchanged.
- Cost of change: low

## 2026-11-20 Catalog extended for own ISOs

- Type: decision
- Phase: phase 14
- What: `Catalog.ISOs` and `LoadAll` keep their meaning; added `OwnISOs`, `AllISOs`, `ReloadOwnISOs`, `DropOwnISO`, `OwnISOsWarnings`, `ISOSourceByCacheName`; `images.Source` gains `Path`, `SHA256`, `images.Options` an `ISOSource` lookup.
- Why: extending, not changing, the interfaces others use.
- Cost of change: low

## 2026-11-20 PickISOFile in a tray service Files

- Type: decision
- Phase: phase 14
- What: the file dialog lives in a Wails service `Files` in `cmd/hlab-tray`, not in `internal/bridge`.
- Why: the bridge package stays free of Wails.
- Cost of change: low

## 2026-11-20 Snapshots and Backups wrap one image list

- Type: decision
- Phase: phase 14
- What: `views/images/Section.svelte` becomes `ImageList.svelte` with a `type` prop, used by the Snapshots and Backups sections.
- Why: no copied view code.
- Cost of change: low

## 2026-11-20 Own local ISO served before its copy ends

- Type: decision
- Phase: phase 14
- What: a local file is served as soon as it can be opened and copied in the background; a failed copy drops that ISO.
- Why: start and reload do not wait for large copies.
- Cost of change: low

## 2026-11-20 Own ISOs have no name and attach by ID

- Type: decision
- Phase: phase 14
- What: own ISOs are served with `name` null and shown by their description (the form's required "Name"), attached and edited by ID only, cached as `own-<id>-<hash of the source>.iso`; an uncopyable local file drops only its ISO, and a cache file stays while a server has it attached.
- Why: Hetzner's schema gives private ISOs `name` null (answer of the user at the start of the phase 14 changes).
- Cost of change: medium

## 2026-11-20 Toast titles name type and resource first

- Type: decision
- Phase: phase 14
- What: action toast titles read `<Type> <name>: <action>`, e.g. "Network Test: Delete", built by one shared helper.
- Why: the user chose it at the start of the phase 14 changes; menu labels differ too much to insert the type.
- Cost of change: low

## 2026-11-20 Labels are entered as chips

- Type: change
- Phase: change
- What: the labels field of every create form and Labels tab is one box of chips (`key=value` or `key`, confirmed with Enter, Space or comma; click selects in the accent colour, Delete removes, double click edits) under the label rule text, replacing the key/value rows with "Add label" (UI-SPEC.md "Labels field").
- Why: the user wants labels entered and shown like in the Hetzner Console.
- Cost of change: low

## 2026-11-20 Volume create form like the Hetzner Console

- Type: change
- Phase: change
- What: the console's Create volume form has a size field with +/− stepper and a slider over 10 to 10240 GB, a required Server instead of a Location (volumes without a server only through API, hcloud and CLI), and always visible Mount options (segmented Automatic | Manual) and Filesystem (ext4 / xfs).
- Why: in the manual check of UI-09 the user compared the form with the Hetzner Console's volume dialog and did not find mount and filesystem behind the optional server.
- Cost of change: low

## 2026-11-20 Console sidebar in groups with section tabs, own ISOs in the Administration

- Type: change
- Phase: change
- What: the project console sidebar has the collapsible groups Cloud (Servers, Volumes, Firewalls) and Networking (Load Balancers, Networks, IPs, DNS) plus the entry Security, every entry shows its views as tabs (Servers: Servers, Snapshots, Backups, Placement Groups; IPs: Primary IPs, Floating IPs; DNS: DNS Zones; Security: SSH Keys, API tokens, Certificates), and ISOs move to an Administration view where own ISOs from a URL or a local file are kept in `isos.custom.json` and served as type `private` (CAT-10, UI-10).
- Why: in the manual check of UI-09 the user wanted the navigation of the current Hetzner Cloud Console and a way to add own ISOs.
- Cost of change: medium

## 2026-11-20 Project display names and Hetzner-style project list

- Type: change
- Phase: feedback
- What: projects get a fixed display name of 1-255 characters from which hlabd derives the name (slug, refused with 409 when taken, no length limit of 63), stored in schema version 2, and the tray's project list becomes Hetzner-like cards (grid/list, sort, resources per type, "New project" tile), built in phase 15.
- Why: the user wants names like "Web Coding Cat" as in the Hetzner Console, and the 63-character DNS-label limit has no technical reason in hlab.
- Cost of change: high

## 2026-11-20 Console menus offer only what the API accepts

- Type: decision
- Phase: phase 14
- What: menus and selects disable or hide what the API would refuse (Attach while attached, Detach while not, Assign/Unassign by state, primary IPs only to powered-off servers without such an IP, floating IPs only within the home network zone, secondary zones without Records, TTL and import, Convert to snapshot only for backups) and the reason shows as a hint.
- Why: a request the UI knows to fail only produces an error toast.
- Cost of change: low

## 2026-11-20 Console form defaults and editing behaviour

- Type: decision
- Phase: phase 14
- What: a new load balancer starts with one HTTP service on port 80, a new network with 10.0.0.0/16, a new firewall rule as TCP from `0.0.0.0/0, ::/0`; editing a service locks its listen port; the firewall rules editor and dialog inputs fill from the resource only on open and after a save, so the 5 s refresh never overwrites an edit; the ISO list loads once per opening.
- Why: the Hetzner console defaults; update_service finds a service by its port; ISOs do not change while the cloud runs.
- Cost of change: low

## 2026-11-20 Console shows API field errors and shared building blocks

- Type: decision
- Phase: phase 14
- What: zone file import and primary nameservers show the API's 422 field message under the field; `ResourceActionsMenu` has an `onselect` hook for entries a page handles itself; `ReverseDnsDialog` takes an IPv6 `network` to set a new address in it; every renamable resource uses the `rename(base)` builder and `lib/RenameInput.svelte`; `protocolText` lives in `lib/format.ts`.
- Why: one dialog per concern, and the first reverse DNS entry of an IPv6 IP must be settable.
- Cost of change: low

## 2026-11-20 UI-02 specs wait for running actions before Delete

- Type: decision
- Phase: phase 14
- What: the load balancer and DNS specs wait until the resource has no running action before Delete.
- Why: Delete right after create answers 423 locked under API-13, as at Hetzner; the wait weakens no check.
- Cost of change: low

## 2026-11-20 Console resources as pure action modules

- Type: decision
- Phase: phase 14
- What: a generic `lib/resourceActions.ts` and one pure module per resource under `lib/resources/` (create, menu, tabs, firewall rule conversion) with table-driven unit tests, rendered by one `ResourceActionsMenu.svelte` and one `ReverseDnsDialog.svelte`; `serverActions.ts` stays as it is and `labelText`/`createdText` move to `lib/format.ts`.
- Why: the phase 11 pattern for every resource keeps requests testable without a browser and dialog logic in one place.
- Cost of change: low

## 2026-11-20 Console sections found by glob, registered at once

- Type: decision
- Phase: phase 14
- What: sections live in `views/<view-id>/Section.svelte` and ProjectConsole shows a section once its file exists via `import.meta.glob`; plan 14-01 registers all eleven views in routes, help, views.json and sidebar with help stubs (one task touches 11 stub files).
- Why: section plans of one wave never touch the same file, and UI-07/DOC-05 need a help file for every view in views.json.
- Cost of change: low

## 2026-11-20 Phase 14 confirmations and zone file handling

- Type: decision
- Phase: phase 14
- What: deleting volumes, networks and zones needs the typed name, every other delete and detach, unassign, removal of sub-resources, zone file import, Change IP range, LB Change type and Disable public interface need a plain confirmation; zone file export shows in a read-only text area with copy, import takes pasted text.
- Why: UI-SPEC and the phase start; pasted text needs no new bridge function for file dialogs.
- Cost of change: low

## 2026-11-20 Phase 14 e2e scope

- Type: decision
- Phase: phase 14
- What: Images list only snapshots and backups without Create, ISOs are read-only; placement groups get no unit row; the certificates flow uses a managed certificate in a zone made through the API; the UI-02 time limit is set by plan 14-23 after one full run.
- Why: the API offers nothing more for images and ISOs, Node cannot easily make an uploadable certificate, and the limit is about twice the first full run.
- Cost of change: low

## 2026-11-20 Upgrade to wcc 0.32.15

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.13 to 0.32.15: waiting-step deadline rule in CLAUDE.md, `Replaces:` format and DECISIONS-DONE.md with three revoked entries, template key names in SPEC.md and UI-SPEC.md.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-20 Screenshot tool and tray launcher

- Type: decision
- Phase: phase 10
- What: `task docs:screenshots` (tools/screenshots + Playwright project `screenshots`) creates only missing `demo-*` projects and the "ci" token and removes only what it created; `test/traylaunch` offers `Build`, `Start(ctx, exe, webviewDir)` and a tray log; the viewport is reset with CDP `Emulation.clearDeviceMetricsOverride`.
- Why: never touch the user's projects or show a token value; Playwright cannot reset a viewport set over CDP.
- Cost of change: low

## 2026-11-20 Server Types dialogs

- Type: decision
- Phase: phase 10
- What: the shared `lib/Dialog.svelte` (open, title, onclose, children) holds the token and add forms with Cancel inside the content; `toast.ts` gets `showSuccess`; "All locations" keeps the other boxes' state while disabling them, and the browser checks "at least one location" before the call.
- Why: the update error stays under the field, so the existing `action()` toast helper does not fit.
- Cost of change: low

## 2026-11-20 Own server types share one check

- Type: decision
- Phase: phase 10 (10-38)
- What: `AddOwn` and `UpdateOwn` build and check the server type object through one internal helper `ownObject`; `UpdateOwn` treats an empty or non-own name like an unknown one (`fs.ErrNotExist`).
- Why: one place for the field checks; catalog types are never in the own types file.
- Cost of change: low

## 2026-11-20 Q17 own server types are editable

- Type: change
- Phase: feedback
- What: Q17 answered with option 1: own server types get "Edit" in the row menu, opening the add form filled in with the name read-only, saved through a new bridge method `UpdateServerType` into the own types file (UI-04, UI-SPEC "Views", ARCHITECTURE tray bridge); built as a findings plan of phase 10.
- Why: user's wish in the UI-08 check: changing an own type should not need removing and adding it again.
- Cost of change: low

## 2026-11-20 Q16 no second window; gear toggles Administration

- Type: change
- Phase: feedback
- What: Q16 answered with option 3, the tray program keeps one window; the gear becomes a toggle (a second click closes Administration and returns to the page shown before, the project list when there was none), and the logo keeps leading to the project list (UI-SPEC "Views"); the home icon recorded earlier the same day is dropped.
- Why: user's decision in the UI-08 check: no intuitive way back from Administration, and separate windows are not wanted.
- Cost of change: low

## 2026-11-20 Project console building blocks

- Type: decision
- Phase: phase 10
- What: the breadcrumb project menu is `lib/ProjectMenu.svelte` in the top bar; "Start the cloud to manage projects" is the shared `lib/CloudStopped.svelte`, shown inside Tokens so the h1 "API tokens" stays; ProjectDetail becomes `views/Tokens.svelte` with the table heading "Tokens".
- Why: the top bar is rendered by App, and the stopped state looks the same on the list and in the console.
- Cost of change: low

## 2026-11-20 Shell start route and last project

- Type: decision
- Phase: phase 10
- What: an empty hash shows the project list and switches to the last project's console once `GET /projects` lists it (the list stays when the cloud is stopped); unknown hashes fall back to `#/projects`; `lastProject` is set whenever a project route opens.
- Why: Q3 of the findings fixes (open the last project's console if it still exists) without a blank start page.
- Cost of change: low

## 2026-11-20 E2E rows of a plan run after its merge

- Type: decision
- Phase: phase 10
- What: plans implement in their own worktree, but their e2e rows run in the main checkout on the phase branch after the merge, one run at a time; the tray's e2e click acts only on the item's current label.
- Why: the dev service `hlab-dev` runs the main checkout's `bin\hlabd.exe`, and the harness refuses other working copies.
- Cost of change: low

## 2026-11-20 winres raises indirect modules

- Type: decision
- Phase: phase 10
- What: adding `github.com/tc-hib/winres` v0.3.1 brings the indirect modules `github.com/nfnt/resize` and `golang.org/x/image` v0.45.0; ICO files are encoded by `tools/icons` itself, winres only builds the `.syso`, and group 32512 shares the image data of group 3.
- Why: required by winres; no pinned module of SPEC.md changes.
- Cost of change: low

## 2026-11-20 Phase 10 findings: screenshot sample projects

- Type: decision
- Phase: phase 10
- What: the README screenshots use throwaway projects `demo-web`, `demo-staging`, `demo-ci`, filtered by the search `demo-`; only projects the tool created are deleted.
- Why: the list search is a name substring filter, so only a shared prefix shows exactly these.
- Cost of change: low

## 2026-11-20 Phase 10 findings: shared tray launcher

- Type: decision
- Phase: phase 10
- What: tray start and stop move from test/e2e/ui_test.go into package test/traylaunch, used by the UI e2e tests and tools/screenshots.
- Why: shared helpers are used, not copied.
- Cost of change: low

## 2026-11-20 Phase 10 findings: log search and older lines

- Type: decision
- Phase: phase 10
- What: search matches level, msg and key=value fields (raw text for non-JSON lines), the level filter runs in the service before the 1000 cap; "Load older lines" finds the oldest shown line by its text from the end each time, without a cursor, and older lines are outside the view's 5000-line cap.
- Why: survives rotation and the switch between stream and files with no state in the bridge.
- Cost of change: low

## 2026-11-20 Phase 10 findings: tray cloud item and pending state

- Type: decision
- Phase: phase 10
- What: `TrayModel` replaces `CanStart`/`CanStop` with `CloudItem`/`CloudAction`; a poll generation counter keeps a poll begun before Start/Stop from overwriting "starting"/"stopping".
- Why: one item, and window and tray agree without a gap.
- Cost of change: low

## 2026-11-20 Phase 10 findings: one icon geometry

- Type: decision
- Phase: phase 10
- What: `tools/icons` writes the app icon, the tray state icons, the `.syso` files, `ui/frontend/src/assets/app-icon.svg` and `docs/images/banner.svg`; its `debug/pe` check is a unit row under UI-01.
- Why: all artwork from one geometry; UI-08 is manual and cannot carry a row.
- Cost of change: low

## 2026-11-20 Phase 10 findings: product name in messages

- Type: decision
- Phase: phase 10
- What: new CAs get the CN "HLab CA" (dev profile "HLab-dev CA"; existing CA files are kept); the Windows service display names are "HLab" and "HLab (dev)"; `hlab version` text reads "HLab <version>"; the `hlab:` prefix, usage lines, `hlab <command>` mentions and `/hlab/v1/ca.crl` stay lowercase.
- Why: command, path and file names stay as they are.
- Cost of change: low
- Replaces: 2026-11-19 CA common name per profile

## 2026-11-20 Phase 10 findings: catalog locations

- Type: decision
- Phase: phase 10
- What: `catalog.Location` gets a `City` field from the shipped location objects; "Update from Hetzner" stays enabled only while the cloud runs.
- Why: the form labels "<name> (<city>)"; the update goes through hlabd.
- Cost of change: low

## 2026-11-20 Windows icon resources with winres

- Type: change
- Phase: phase 10
- What: the icon generator `tools/icons` writes the `.syso` icon resources (group ID 3 and 32512) with `github.com/tc-hib/winres` v0.3.1 (0BSD); SPEC "Stack" names it.
- Why: user's answer at the start of the phase 10 findings fixes; the standard library has no resource writer and Wails uses the same package.
- Cost of change: low

## 2026-11-20 Upgrade to wcc 0.32.13

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.12 to 0.32.13: `/wcc:feedback` renamed to `/wcc:report` in STATUS.md and ROADMAP.md.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-20 Q15 DOC-05 views from views.json

- Type: change
- Phase: change
- What: Q15 answered with option 2: the app takes its view list from views.json so the DOC-05 unit test covers the real views, and the clause that help texts name only existing views and buttons becomes a manual check under DOC-07.
- Why: the test cannot see buttons without reading Svelte source, and one source for the views closes the gap a test can close.
- Cost of change: low

## 2026-11-20 Backlog phase B2

- Type: change
- Phase: change
- What: the user took the VLAN release at project deletion (security), the two e2e harness bugs (RDNS-02 423 locked, cloud stop before build only when running) the DOC-03 route check and the load balancer https health check into phase B2 before phase 11.
- Why: the user's choice from the backlog.
- Cost of change: low

## 2026-11-20 Documentation folders

- Type: change
- Phase: feedback
- What: user documentation lives directly in `docs/` (commands.md, configuration.md, deviations.md) and the developer guide in `docs/development/`, replacing `docs/user/` and `docs/dev/`; `docs/deviations.md` becomes short prose for users without routes, the full route list moves to `docs/development/api-coverage.md`, which DOC-03 checks; README.md becomes compact with a banner, badges and generated light/dark screenshots, its per-resource sections move to `docs/guide.md`.
- Why: the user's wish during the manual check of DOC-07.
- Cost of change: low

## 2026-11-20 Hetzner token in a dialog

- Type: change
- Phase: feedback
- What: "Update from Hetzner" opens a dialog that asks for the Hetzner token instead of a token field on the Server Types page; the add form for own types chooses locations from a checkbox list of the catalog locations with "All locations" checked by default.
- Why: the user expected the token to be asked for only when the update is started.
- Cost of change: low

## 2026-11-20 Log search and older lines

- Type: change
- Phase: feedback
- What: the Logs view gets a search over hlabd.log and all rotated files (matches newest first, at most 1000, highlighted) and a "Load older lines" button that pages back 1000 lines at a time across rotated files.
- Why: in the manual check of UI-08 the user missed searching the log and seeing lines older than the last 1000.
- Cost of change: low

## 2026-11-20 Console shell like the Hetzner Cloud Console

- Type: change
- Phase: feedback
- What: the window has a top bar (HLab, breadcrumb, status, Start/Stop, gear), a project list as start page, a project console per project with a sidebar of its sections (phase 10: API tokens; phase 11 adds the resource sections) and a breadcrumb project switcher, and an Administration area behind the gear with Server Types, Settings and Logs; the sidebar project switcher goes.
- Why: in the manual check of UI-08 one sidebar mixed the project switcher, Projects and the administration views, which the user found confusing.
- Cost of change: low

## 2026-11-19 One Start/Stop item in the tray menu

- Type: change
- Phase: feedback
- What: the tray menu has one cloud item whose label and action follow the cloud state ("Start cloud", "Stop cloud", disabled "Starting cloud…", "Stopping cloud…", "Checking cloud…") instead of two items; the tray icon is the application icon with a status dot in the lower-right corner in the UI-SPEC role colours.
- Why: the user's manual check of UI-08 found two items of which one is always greyed out.
- Cost of change: low

## 2026-11-19 Product name HLab and an app icon

- Type: change
- Phase: feedback
- What: the product is named "HLab" in every user-facing text (window, tray, UI, help, API and CLI messages, docs prose) while program, command, service, file and module names stay lowercase; the tray program gets an application icon for window, taskbar and EXE.
- Why: the user's manual check of UI-08 found the lowercase name and the missing window icon.
- Cost of change: low

## 2026-11-18 Server public_net carries primary IP IDs

- Type: decision
- Phase: phase 02
- What: new servers get `public_net.ipv4.id` and `public_net.ipv6.id` from the `primary_ips` ID sequence (`omitempty`, so older stored servers stay valid).
- Why: hcloud-python reads these IDs and the Ansible `server_info` module failed without them; phase 05 can turn them into real primary IP records.
- Cost of change: low

## 2026-11-18 Project deletion waits for running actions

- Type: decision
- Phase: phase 02
- What: the project-deletion action first waits (at most 20 minutes, woken when an action ends) until the project has no running action, else it ends in error and the project stays listed.
- Why: a server create still running could otherwise leave an orphaned VM after the delete (02-14 review).
- Cost of change: low

## 2026-11-18 Project deletion details

- Type: decision
- Phase: phase 02
- What: a running deletion locks the project (action resource type `project`), so a second delete gets 409 `conflict`; `hlab project delete` polls every 200 ms for at most 30 minutes; a failed agent push after the deletion only logs a warning; a server being deleted is `deleting` and goes back to `off` if the VM cannot be deleted.
- Why: reuses the existing lock check, and the next push rebuilds the whole agent document.
- Cost of change: low

## 2026-11-18 CAT-03 live check leaves the dev catalog updated

- Type: decision
- Phase: phase 02
- What: the CAT-03 e2e live check does not restore the dev profile's `catalog\server-types.json`; after it, the dev cloud serves Hetzner's current server types.
- Why: the non-elevated test user may only read the protected config root, and a restore endpoint or extra write permission would weaken the product for a test.
- Cost of change: low

## 2026-11-18 Catalog update CLI details

- Type: decision
- Phase: phase 02
- What: the hidden prompt uses `golang.org/x/term` `ReadPassword` in cmd/hlab; `UpdateOptions.DataDir` gets the config root; a usage error raised while a command runs (missing token) also exits with 2.
- Why: x/term already sets the console mode through x/sys/windows, and ARCHITECTURE puts the updated catalog in the config root.
- Cost of change: low
- Replaces: 2026-11-18 Hidden token prompt through x/sys/windows

## 2026-11-18 Placement group membership stored on the group

- Type: decision
- Phase: phase 02
- What: only the group's `servers` list records membership and a server's `placement_group` is derived when read, so deleting a group clears it; an unknown group answers `invalid_input` on `placement_group`; a server in any group gets `already_in_placement_group`; removing a server from no group succeeds.
- Why: one source of truth, and Hetzner allows one group per server.
- Cost of change: low

## 2026-11-18 Catalog lists through ListRequest

- Type: decision
- Phase: phase 02
- What: `/server_types`, `/locations`, `/images` and `/actions` are sorted by ID and paged by `ListRequest`; `/actions` ignores its old `sort`; `name` takes one exact value.
- Why: the clients follow `meta.pagination`, and the frozen description gives these lists only these parameters.
- Cost of change: low

## 2026-11-18 Catalog loading and pricing source

- Type: decision
- Phase: phase 02
- What: an unreadable updated `server-types.json` stops loading with an error (a missing one falls back to the shipped file); `/pricing` takes server type prices from each type's own `prices`; catalog problems show in `/status` as `custom_server_types`.
- Why: updated types are then priced correctly, and a broken file is not silently replaced by older data.
- Cost of change: low

## 2026-11-18 Placement group membership errors

- Type: decision
- Phase: phase 02
- What: adding a server already in the group answers 422 `already_in_placement_group`; a full group answers 422 `placement_error` without `details.fields`; group names are unique per project.
- Why: keeps the member list free of duplicates; the full-group error concerns no single field.
- Cost of change: low

## 2026-11-18 List details: sort without keys, last_page, repeats

- Type: decision
- Phase: phase 02
- What: endpoints without sort keys ignore `sort`; `last_page` is always a number (at least 1); a repeated sort field is ignored; bad `sort` or `label_selector` answers `invalid_input` on that field.
- Why: the parameter does not exist on those endpoints, the description allows a number, and Hetzner ignores contrary orders.
- Cost of change: low

## 2026-11-18 Reading .wslconfig: missing user, unreadable file, defaults

- Type: decision
- Phase: phase 02
- What: no console user token means no WSL problem; an unreadable profile or `.wslconfig` counts as an unknown mode and gives no problem (hostnet has no logger); unknown `networkingMode` counts as NAT and only `dnsTunneling=false` as off.
- Why: WSL falls back to its defaults for unknown values, and `wsl_mode_unsupported` is reserved for bridged and none.
- Cost of change: low

## 2026-11-18 Status of codes outside the frozen error table

- Type: decision
- Phase: phase 02
- What: the status table also lists `token_readonly` 403 and the operation codes `placement_error`, `server_not_stopped`, `already_in_placement_group` 422.
- Why: API-03 requires 403 `token_readonly`, the description answers the operation codes with 422, and an unknown code would answer 500.
- Cost of change: low

## 2026-11-18 Negative selectors match absent keys

- Type: decision
- Phase: phase 02
- What: `k!=v` and `k notin (...)` also match resources without the key (Kubernetes semantics); selector keys and values are checked only for allowed characters, label prefixes must be lowercase RFC 1123 subdomains.
- Why: Hetzner's text does not settle these cases and its selectors follow Kubernetes.
- Cost of change: low

## 2026-11-18 Hyper-V VM group in the data folder DACL

- Type: decision
- Phase: phase 02
- What: the protected data folder DACL grants "NT VIRTUAL MACHINE\Virtual Machines" (S-1-5-83-0) read and execute on folders and read and write on files, inherited.
- Why: hlabd creates disks and ISOs itself, so they get only inherited entries, and the appliance could not open cidata.iso.
- Cost of change: low

## 2026-11-18 Primary IP prices only for ipv4

- Type: decision
- Phase: phase 02
- What: `/pricing` returns the primary IP prices of the shipped catalog, which has only `ipv4`; no `ipv6` entry is invented.
- Why: the catalog is generated from the real Hetzner API, which lists only ipv4 under `primary_ips`.
- Cost of change: low

## 2026-11-18 HTTP status of error codes from the OpenAPI error table

- Type: decision
- Phase: phase 02
- What: `api.Status(code)` is the one table of HTTP statuses, copied from the error table of the frozen OpenAPI description (`invalid_input` 422, `protected` 423), and `api.Error` takes no status.
- Why: the user asked for one sourced table; a read-only call against the real API answered `invalid_input` with 422.
- Cost of change: low

## 2026-11-18 Eleventh server in a spread group gives placement_error

- Type: decision
- Phase: phase 02
- What: adding an eleventh server to a spread placement group answers 422 `placement_error`.
- Why: it is the only related code Hetzner documents; the real one could not be confirmed.
- Cost of change: low

## 2026-11-18 resource.Deleter embedded in Reconciler

- Type: decision
- Phase: phase 02
- What: new interface `resource.Deleter{Delete}`, embedded in `Reconciler`; servers implement only `Deleter` until phase 03 adds `Reconcile`.
- Why: project deletion needs `Delete` now, and a fake `Reconcile` would lie.
- Cost of change: low

## 2026-11-18 WSL mode from the console user's .wslconfig

- Type: decision
- Phase: phase 02
- What: hostnet reads `networkingMode` and `dnsTunneling` from the console user's `.wslconfig` (injectable for tests), maps consomme/virtioproxy to `wsl_virtioproxy` and bridged/none to the new code `wsl_mode_unsupported`; no console user means no WSL problem.
- Why: the user took this answer; a LocalSystem service cannot see the user's distros.
- Cost of change: low

## 2026-11-18 CAT-03 live check as an e2e row

- Type: decision
- Phase: phase 02
- What: besides its unit test, CAT-03 gets `TestE2E/CAT-03` using the hcloud context, skipped with a reason without one.
- Why: SPEC.md "Accounts and secrets" prescribes the live check and success criterion 3 needs it.
- Cost of change: low

## 2026-11-18 Success criteria 1 and 4 tested as CLI-02 and CLI-03 rows

- Type: decision
- Phase: phase 02
- What: the tofu tests for ssh_keys and placement_groups and the Ansible `server_info` test are rows of CLI-02 and CLI-03; both requirements stay open until phase 13.
- Why: the user took this answer at the start of the phase.
- Cost of change: low

## 2026-11-18 PUT changes only the fields present

- Type: decision
- Phase: phase 02
- What: every PUT changes only the fields in the body; `labels`, when present, replaces the whole set.
- Why: the OpenTofu provider sends name and labels in separate PUTs.
- Cost of change: low

## 2026-11-18 Placement group create returns no action

- Type: decision
- Phase: phase 02
- What: `POST /placement_groups` answers `action: null`.
- Why: the description allows it and the clients then have nothing to poll.
- Cost of change: low

## 2026-11-18 Server types behind an accessor

- Type: decision
- Phase: phase 02
- What: `Catalog.ServerTypes` becomes unexported behind `Types()` and `ReplaceServerTypes`.
- Why: `hlab catalog update` swaps the types while requests run.
- Cost of change: low

## 2026-11-18 WSL distro of the e2e harness

- Type: decision
- Phase: phase 02
- What: the harness uses `HLAB_E2E_WSL_DISTRO`, else the single registered distro whose name starts with `Ubuntu`, always through `wsl.exe -d`, and fails with none or several; the test key goes to a temporary directory deleted at the end.
- Why: the user asked for the SPEC.md Ubuntu distro instead of the default distro.
- Cost of change: low

## 2026-11-18 Page beyond the last is empty

- Type: decision
- Phase: phase 02
- What: a page beyond the last returns an empty list with `next_page` null.
- Why: the description does not say, and the clients' paging loops expect it.
- Cost of change: low

## 2026-11-18 Pricing built in the catalog plan

- Type: decision
- Phase: phase 02
- What: `/pricing` is built in plan 02-03 from the merged server types.
- Why: it derives from them and needs only one registration line.
- Cost of change: low

## 2026-11-18 Project deletion waits through a management action endpoint

- Type: decision
- Phase: phase 02
- What: `GET /mgmt/v1/actions/{id}` is added and `hlab project delete` waits for it; a second deletion while one runs answers 409 `conflict`.
- Why: the user took this answer; the CLI must report success or failure.
- Cost of change: medium

## 2026-11-18 Q1: real resources reconciled by their module

- Type: change
- Phase: change
- What: modules owning real resources implement `resource.Reconciler` with platform, image store, agent push and settings in their `Deps`, replacing `servers.Deps.Machines` and `internal/cloud/servers.go` as the first plan of phase 03 (decisions/resource-reconciler.md).
- Why: a new resource type stays one package plus one line before phases 03, 04, 05 and 08 add more such modules, and phase 09 gets one way to restore every resource.
- Cost of change: medium

## 2026-11-18 Q2: guest-visible identifiers approved

- Type: approval
- Phase: change
- What: the user approves server MAC, formal IPv6, root login via `disable_root: false` and the volume identifier byte order exactly as recorded in phase 01 (decisions/guest-identifiers.md).
- Why: the values follow the spec's identifiers and the documentation range, and the MAC wrap-around after 2^20 servers is out of reach for a local lab.
- Cost of change: high

## 2026-11-18 Server MACs, IPv6 and root login

- Type: decision
- Phase: 01 (01-16)
- What: a server's public MAC is `02:48:4c` followed by `0x1` and the low 20 bits of its ID (the appliance has `02:48:4c:00:00:01`), kept in `_hlab`; its formal IPv6 is `2001:db8:<ID as 32 bits>::/64`; server vendor-data sets `disable_root: false` so key logins as root work like at Hetzner.
- Why: unique MACs without colliding with the appliance; Ubuntu cloud images block root logins by default.
- Cost of change: high

## 2026-11-18 Store timestamps and access scoping

- Type: decision
- Phase: 01 (01-02)
- What: timestamps are stored as RFC3339Nano text in UTC; resource access functions take the project ID (0 = catalog, `project_id IS ?`), so one project cannot address another's rows.
- Why: sortable text that SQLite and Go both read; isolation enforced in the one place every module uses.
- Cost of change: medium

## 2026-11-18 Volume identifier byte order

- Type: decision
- Phase: 01 (01-10)
- What: `CreateDisk` writes `page83` in GUID text byte order, so `484C4142-0000-4000-8000-<id>` reaches the disk with the volume ID in the last 6 bytes.
- Why: those bytes survive in both designators the guest sees (E6).
- Cost of change: high

## 2026-11-18 Management API details in phase 01

- Type: decision
- Phase: 01 (01-14)
- What: token names are 1-63 characters; `/status` carries state, version and problems until later phases fill `shadowing_zones` and `appliance_generation`; deleting a project with resources answers 409 until PROJ-04 (phase 02).
- Why: ARCHITECTURE.md sets no rule for token names; the other fields have no data yet.
- Cost of change: low

## 2026-11-18 Agent document without JSON tags

- Type: decision
- Phase: 01 (01-01)
- What: `agentapi` types carry no JSON tags and use Go's field names on the wire, with `const Schema = 1`; `SegmentFor` returns the public PVLAN for network 0 and leaves private VLANs to its caller until phase 05.
- Why: hlabd and hlab-agent are built from the same package and the agent is replaced at every cloud start, so the two sides never differ in version.
- Cost of change: low

## 2026-11-18 Release and dev profile side by side

- Type: change
- Phase: 01 (discuss)
- What: Binaries carry a link-time profile `release` or `dev` that sets service, pipe, data folder, catalog folder, switch, VM prefix and host-object tags (`hlab` / `hlab-dev`); both may be installed, only one cloud runs (`other_instance_running`, LIFE-10); the e2e harness refuses to run while the release cloud runs.
- Why: the user develops and uses hlab on the same host; a separate running dev cloud would need a second range, API name and a shared WinNAT, which Windows limits to one.
- Cost of change: high

## 2026-11-18 Public IPv4 allocation continues after the last address

- Type: decision
- Phase: 01 (discuss)
- What: Public IPv4s are handed out after the last one given (counter in `kv`), wrapping to `.4` only at the end of the range and skipping used ones.
- Why: reusing a freed address at once causes SSH host key conflicts when servers are deleted and recreated (user's choice).
- Cost of change: low

## 2026-11-18 Appliance DNS forwards to the host's DNS servers

- Type: decision
- Phase: 01 (discuss)
- What: hlabd collects the host's DNS servers at cloud start and passes them as `Upstream` in the agent document; without any, the agent forwards to 1.1.1.1 and 9.9.9.9.
- Why: Windows has no resolver on port 53 the appliance could forward to; the host's servers keep LAN and VPN names working (user's choice).
- Cost of change: low

## 2026-11-18 Firewall rule for the API port

- Type: decision
- Phase: 01 (discuss)
- What: hostnet adds one inbound Windows Firewall rule (group per profile): TCP 8780, all firewall profiles, interface `vEthernet (hlab)`, local 198.19.0.2, remote 198.19.0.1, program `hlabd.exe`; `Up` replaces an existing rule of the group, `Down` and the uninstaller remove it; hlabd listens on 198.19.0.2:8780 only.
- Why: the appliance's API proxy must reach hlabd, and the host must expose nothing else.
- Cost of change: low

## 2026-11-18 Host settings restored after a crash

- Type: decision
- Phase: 01 (discuss)
- What: hostnet records previous values of settings it does not own (IPv4 forwarding on the WSL and hlab adapters) in `hostnet-restore.json` before changing them, and restores them at the next `Up`, `Down` or uninstall-cleanup.
- Why: a crash of hlabd must not leave the WSL adapter forwarding (user's wish).
- Cost of change: low

## 2026-11-18 Unit tests fail without the cached OpenAPI description

- Type: decision
- Phase: 01 (discuss)
- What: when the frozen OpenAPI description is missing from the cache, the API tests fail with "run `task openapi`" instead of skipping the validation.
- Why: a silently skipped API-10 check would pass without proving anything (user's choice).
- Cost of change: low

## 2026-11-18 Hetzner token: hcloud context in development, prompt in operation

- Type: change
- Phase: change
- What: Development and release reach the real API through a named hcloud context (default name in "Default hcloud context name HLab", `HLAB_HCLOUD_CONTEXT`; `task catalog` via `hcloud api`, the CAT-03 live check gets the token through hcloud and skips with a reason without a valid context); in operation `hlab catalog update` and the tray ask for the token (`--hetzner-token-env`, hidden prompt, password field), use it once and never store it; `HLAB_HETZNER_TOKEN` and `.env.example` are gone.
- Why: the user's hcloud login already holds the token for development, while the product must not depend on hcloud and must not keep a real Hetzner secret.
- Cost of change: low

## 2026-11-18 Default hcloud context name HLab, token prompt without hcloud

- Type: change
- Phase: change
- What: The hcloud context for the real API is named `HLab` unless `HLAB_HCLOUD_CONTEXT` says otherwise; without hcloud or a usable context `task catalog` asks for the token with a hidden prompt, and the unattended live check is skipped.
- Why: `HLab` is the user's existing context; development must not be tied to hcloud.
- Cost of change: low

## 2026-11-18 Module path and repository in SPEC.md

- Type: change
- Phase: change
- What: SPEC.md "Stack" names the Go module path `github.com/webcodingcat/hlab` and the repository `https://github.com/webcodingcat/hlab`, confirmed by the user.
- Why: the module path was only recorded in DECISIONS.md.
- Cost of change: low

## 2026-11-18 Upgrade to wcc 0.31.2

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.31.0 to 0.31.2: project CLAUDE.md gains the rule to work from this repository only and the rule that tool login credentials stay with their tool.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-18 Go module path confirmed

- Type: approval
- Phase: upgrade
- What: The user confirmed the Go module path `github.com/webcodingcat/hlab`, which /wcc:prepare had taken from a sibling folder before the rule "work from this repository only" existed.
- Why: user's choice.
- Cost of change: low

## 2026-11-18 Go module path and repository layout

- Type: decision
- Phase: prepare
- What: Go module `github.com/webcodingcat/hlab` (confirmed by the user, see "Go module path confirmed"); programs in `cmd/<name>`, tray frontend in `ui/frontend` (Vite, svelte-check as its linter, Playwright as dev dependency), tasks `build`, `lint`, `test`, `e2e` in `Taskfile.yml`.
- Why: SPEC.md names no module path; this matches the owner's GitHub account.
- Cost of change: low

## 2026-11-18 Frontend audit finding accepted

- Type: assumption
- Phase: prepare
- What: `npm audit` reports a ReDoS advisory in picomatch (via vite, svelte-check); it is accepted because it affects only the local build tools and the fix would downgrade vite below SPEC.md.
- Why: no untrusted glob patterns reach the build.
- Cost of change: low

## 2026-11-18 Spec approved

- Type: approval
- Phase: spec
- What: The user approved REQUIREMENTS, SPEC, ARCHITECTURE, UI-SPEC and ROADMAP with all decisions of cost high listed in the summary, the test levels and the design changes from E1 (private VLAN) and E9 (CDP port in the e2e build).
- Why: approval step of /wcc:spec.
- Cost of change: low

## 2026-11-18 Guest-visible identifiers

- Type: decision
- Phase: spec
- What: Volume disks carry the identifier `484C4142-0000-4000-8000-<volume ID, 12 hex digits>`; the rescue disk sits at SCSI LUN 63, server disk at LUN 0, volumes from LUN 1, DVDs on SCSI controller 1; public PVLAN 4094/4093, private-network VLANs 2..4092; metadata paths `/hlab/v1/password-reset`; MACs `02:48:4c:...`.
- Why: guests and their scripts see these values; changing them later breaks existing servers' udev rules and hooks (E6, E7).
- Cost of change: high
## 2026-11-18 Tray UI tests through a CDP port in an e2e-only build

- Type: approval
- Phase: spec
- What: The user decided that the tray UI is tested in the real tray window: a build with tag `e2e` sets Wails' `AdditionalBrowserArgs` to `--remote-debugging-port=<port>` (bound to 127.0.0.1) and a per-run `WebviewUserDataPath`, both taken from test-harness environment variables; Playwright Test connects over CDP. Release builds never use the tag; `task release` fails if the tag is set, and a unit test checks that the default build has no remote-debugging argument.
- Why: Wails v3.1.0 clears `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` (E9); the user preferred testing the real window over a Wails server build. This is a user-approved exception to the project rule against debug ports, limited to the `e2e` build.
- Cost of change: low

## 2026-11-18 Service name hlab

- Type: approval
- Phase: spec
- What: The Windows service keeps the name `hlab`; the user removes the old prototype service of the same name on the development machine.
- Why: user's choice.
- Cost of change: high
## 2026-11-18 Port isolation by private VLAN

- Type: change
- Phase: spec
- What: The public segment is isolated with a private VLAN (appliance promiscuous, host adapter and guest adapters isolated, VLANs 4094/4093) instead of MAC port ACLs; private-network adapters get MAC deny-lists of the other members.
- Why: allow-appliance-deny-ANY MAC ACLs dropped legitimate traffic intermittently (E1); PVLAN kept all legitimate paths and blocked direct ones, including host and WSL2 traffic (E1, E3).
- Cost of change: medium

## 2026-11-18 VM settings and Windows shutdown

- Type: change
- Phase: spec
- What: VMs get `AutomaticStartAction Nothing` and `AutomaticStopAction` matching `stop_behaviour`; shutdown and reboot use the Shutdown integration service; disks copied from a base get a new disk identifier.
- Why: vmms runs before hlabd at Windows shutdown, the default start action would start VMs with Windows (LIFE-06), Hyper-V has no ACPI button, and copies inherit the parent's identifier (E7, E11).
- Cost of change: low
## 2026-11-18 Stack

- Type: decision
- Phase: spec
- What: Go 1.27.4; Wails v3.1.0 with Svelte 5, Vite 8, TypeScript 5.9; SQLite via modernc.org/sqlite; Hyper-V through a persistent Windows PowerShell 5.1 process; appliance Debian 13 with nftables, HAProxy 3.0 and a Go agent (miekg/dns); WiX v7 MSI plus Burn bundle; Task; Go testing and Playwright Test; versions in SPEC.md.
- Why: Go and Wails are fixed by IDEA.md. Svelte needs the least frontend code for a Go maintainer (React rejected: more boilerplate). PowerShell is what minikube and Packer use for Hyper-V; WMI via github.com/microsoft/wmi rejected: no releases, Msvm method calls need embedded CIM. modernc SQLite needs no cgo; bbolt rejected: no queries, hand-written indexes. CoreDNS rejected: a second daemon to configure, while the agent already holds the zone data. WiX is the maintained free MSI/Burn toolset; Inno Setup rejected: no MSI. TypeScript 7 postponed until svelte-check supports it. Sources: go.dev/dl, github.com/wailsapp/wails/releases (v3.0.0 stable 2026-10-26), minikube Hyper-V driver docs, Packer Hyper-V driver source, proxy.golang.org, docs.firegiant.com/wix/osmf, registry.npmjs.org.
- Cost of change: high

## 2026-11-18 Data schema: SQLite with JSON documents per resource

- Type: decision
- Phase: spec
- What: State in `%ProgramData%\hlab\state.db` with tables projects, tokens (SHA-256 only), resources (kind, id, project, name, labels, doc JSON in Hetzner's shape plus `_hlab`), actions, ids, kv; schema version in `PRAGMA user_version` (ARCHITECTURE.md "Data schema").
- Why: one generic table keeps every resource module alike and makes new fields no migration; IDs global per kind like Hetzner.
- Cost of change: high

## 2026-11-18 Files and formats

- Type: decision
- Phase: spec
- What: settings in `%ProgramData%\hlab\settings.toml` (TOML, keys in ARCHITECTURE.md); catalogs as JSON in Hetzner's API shape; own server types in `server-types.custom.json` with IDs from 100000; logs as JSON lines with rotation.
- Why: TOML allows comments for a file users edit; Hetzner's own JSON shape lets `hlab catalog update` store the API response unchanged.
- Cost of change: high

## 2026-11-18 Network addressing

- Type: decision
- Phase: spec
- What: Default public range `198.19.0.0/16` (inside the benchmark range 198.18.0.0/15, not the commonly used 198.18.0.0/16 of proxy fake-IP tools, not CGNAT used by Tailscale); transit /30 at its start (appliance .1, host .2); servers from .4; guests see gateway 172.31.1.1 and resolvers 185.12.64.1/.2 like at Hetzner; private networks as VLAN plus VRF on the appliance so ranges of different projects may overlap.
- Why: a range that home LANs, VPNs and Docker/WSL (172.16/12) do not use; addresses inside guests match Hetzner, so scripts run unchanged.
- Cost of change: high

## 2026-11-18 API reached through the appliance

- Type: decision
- Phase: spec
- What: `api.hetzner.internal` resolves to the appliance (198.19.0.1); the agent proxies port 80 to hlabd on 198.19.0.2:8780.
- Why: hlabd never binds port 80 on the host, where http.sys (IIS and others) claims it on all addresses by default; servers, host and WSL2 use the same path.
- Cost of change: high

## 2026-11-18 Start and stop for Interactive Users instead of a group

- Type: decision
- Phase: spec
- What: The service security descriptor, the management pipe and the settings file grant Interactive Users (`IU`) query/start/stop, connect and modify; no `hlab-users` group.
- Why: group membership needs a sign-out after installation, and security for multi-user use is a non-goal; a well-known SID needs nothing created or removed.
- Cost of change: medium

## 2026-11-18 Management interface

- Type: decision
- Phase: spec
- What: `hlab` command set and the management API over `\\.\pipe\hlab` as in ARCHITECTURE.md; the tray uses the same API, Hetzner resources through `/mgmt/v1/projects/{name}/api/v1/...`.
- Why: one interface for CLI, tray and scripts; the tray tests the same handlers as the clients.
- Cost of change: high

## 2026-11-18 Appliance as declarative network document

- Type: decision
- Phase: spec
- What: hlabd pushes the complete network configuration as one versioned JSON document (`agentapi.Config`, schema 1) to hlab-agent, which applies it idempotently; firewalls in nftables, one HAProxy per load balancer in its own namespace, DNS and metadata in the agent.
- Why: answers "where LBs, firewalls and floating IPs live" with one place that sees all traffic; a full document makes restarts and stop/start trivial to reconcile.
- Cost of change: high

## 2026-11-18 Guests configured by a NoCloud seed ISO

- Type: decision
- Phase: spec
- What: Each server gets a cidata ISO (meta-data, user-data, vendor-data, network-config v2 with /32 and on-link gateway); vendor-data installs a udev rule for `scsi-0HC_Volume_<id>` and a timer that applies root password resets from the metadata service.
- Why: cloud-init's Hetzner datasource only runs when DMI says "Hetzner", which Hyper-V cannot fake; Hetzner's password reset needs a guest agent, which official images lack on Hyper-V.
- Cost of change: medium

## 2026-11-18 Rescue system from a separate rescue disk

- Type: decision
- Phase: spec
- What: Rescue boots a differencing disk of a prepared Debian 13 rescue base (new UUIDs and labels) at SCSI LUN 63; the server disk stays at LUN 0 (`/dev/sda`); the rescue system runs from its own disk, not from RAM.
- Why: live ISOs (GRML, Debian live) cannot take SSH keys without remastering; a cloud image takes keys and password through cidata. Running from RAM is not observable for scripts, the disk name is.
- Cost of change: low

## 2026-11-18 Datacenters follow Hetzner's current API

- Type: decision
- Phase: spec
- What: hlab serves datacenter fields and endpoints exactly as the frozen OpenAPI description does; `/datacenters` answers 410 like Hetzner since 2026-10-01.
- Why: IDEA.md lists datacenters, but Hetzner removed the endpoint; goal 1 (unmodified clients) is served by matching the real API.
- Cost of change: low

## 2026-11-18 OpenAPI copy not committed

- Type: decision
- Phase: spec
- What: The frozen Hetzner OpenAPI description is downloaded by `task openapi` from docs.hetzner.cloud, checked against the SHA-256 in `api/openapi.lock` and cached outside the repository; changing the pin is a deliberate commit.
- Why: the file carries no licence, so committing it to an Apache-2.0 repository is unclear; the pin keeps it frozen.
- Cost of change: low

## 2026-11-18 Phases run without elevated rights

- Type: decision
- Phase: spec
- What: A one-time elevated `scripts/dev-setup.ps1` registers the dev service with the installer's descriptor; phases then build, start and test as a normal user; only the installer suite needs an elevated terminal.
- Why: a Claude Code run with elevated rights gives every agent those rights.
- Cost of change: low
## 2026-11-18 IPv6 assigned formally, not routed

- Type: assumption
- Phase: idea
- What: The API assigns IPv6 addresses to servers and primary IPs as clients expect, but they carry no traffic; listed as a known deviation.
- Why: v1 is IPv4 only, while clients create servers with IPv6 enabled by default.
- Cost of change: medium

## 2026-11-18 Public IPv4 range configurable

- Type: assumption
- Phase: idea
- What: Public IPv4 addresses come from a configurable range whose default avoids common networks (VPNs, Tailscale, home LANs); hlab checks for collisions at start.
- Why: a fixed range could collide with networks on the user's machine.
- Cost of change: medium

## 2026-11-18 Stop behaviour is a setting

- Type: assumption
- Phase: idea
- What: A setting chooses whether stopping the cloud saves VM state or shuts servers down cleanly (default: save state); a Windows shutdown with a running cloud follows the same setting and the cloud is stopped after reboot.
- Why: the user wants to choose per installation, not at every stop.
- Cost of change: low

## 2026-11-18 Automatic daily backups

- Type: assumption
- Phase: idea
- What: Servers with backups enabled get a backup daily while the cloud runs, keeping 7 like Hetzner, plus manual backups.
- Why: matches Hetzner's behaviour.
- Cost of change: low

## 2026-11-18 Rescue system is a real Linux

- Type: assumption
- Phase: idea
- What: Enabling rescue boots a Linux rescue system with root password and SSH keys.
- Why: "everything really works".
- Cost of change: low

## 2026-11-18 ISO catalog from public sources

- Type: assumption
- Phase: idea
- What: ISOs are a catalog of publicly downloadable ISOs (e.g. Ubuntu and Debian installers), downloaded on demand; Hetzner's private ISOs are not available.
- Why: Hetzner's ISO files cannot be downloaded.
- Cost of change: low

## 2026-11-18 Server type catalog update needs optional real token

- Type: assumption
- Phase: idea
- What: Every release ships the server type catalog; updating it from the real Hetzner API optionally uses a real Hetzner token provided by the user.
- Why: the real API needs authentication; hlab itself needs no Hetzner account.
- Cost of change: low

## 2026-11-18 Resource limits: RAM and disk hard, vCPUs overcommitted

- Type: assumption
- Phase: idea
- What: RAM and disk are hard limits including the Windows reserve; total vCPUs may exceed host cores, but a single VM gets at most the host's logical cores, otherwise resource_unavailable; disks are thin-provisioned in a configurable location.
- Why: RAM is fixed per VM, CPU is time-shared like Hetzner's shared vCPUs.
- Cost of change: low

## 2026-11-18 Tokens read and read/write, projects isolated

- Type: assumption
- Phase: idea
- What: Tokens have read or read/write permission like Hetzner; several projects run at the same time, isolated from each other.
- Why: matches Hetzner's behaviour.
- Cost of change: medium

## 2026-11-18 UI structure like Hetzner Console, own design

- Type: assumption
- Phase: idea
- What: The tray window follows the structure and workflows of the Hetzner Cloud Console with its own visual design, without Hetzner logos or branding.
- Why: familiar workflows without copying a third party's brand.
- Cost of change: low

## 2026-11-18 Reverse DNS resolves, metadata at 169.254.169.254

- Type: assumption
- Phase: idea
- What: Reverse DNS entries really resolve (PTR) via the appliance; the metadata service is reachable at 169.254.169.254 like at Hetzner.
- Why: "everything really works".
- Cost of change: low

## 2026-11-18 Clients installed by the user, no auto-update, no telemetry

- Type: assumption
- Phase: idea
- What: The user installs hcloud, OpenTofu and Ansible (in WSL2) themselves; updates come via a new installer without automatic update check; hlab sends no telemetry.
- Why: keeps v1 small; hlab must not depend on outside services.
- Cost of change: low

## 2026-11-18 Upgrade to wcc 0.32.2

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.0 to 0.32.2: CLAUDE.md test rule on shared behaviour takes the template wording; version line updated
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-18 Q3 SRV-18 counts live RAM and remaining disk growth

- Type: decision
- Phase: 03
- What: A server fits when its type RAM is at most the host's free physical RAM minus `reserve_ram_mb`, and the remaining growth of all thin disks plus the new disk is at most the free data-folder space minus `reserve_disk_gb`.
- Why: RAM matches success criterion 4 literally, and disk stays a hard limit although disks are thin.
- Cost of change: low

## 2026-11-18 reset_password on a stopped server answers 409 conflict

- Type: decision
- Phase: 03
- What: `reset_password` on a server that is not running answers 409 `conflict`.
- Why: the frozen description lists no code for this case.
- Cost of change: low

## 2026-11-18 platform.Sample only gains counters

- Type: decision
- Phase: 03
- What: `platform.Sample` gains disk-op and packet counters; `CPUPercent` stays, computed in hyperv from two reads, and `disk.0` is the sum of all disks of a VM.
- Why: extends the interface without removing anything.
- Cost of change: low

## 2026-11-18 Minimal scheduler for status watch and metrics

- Type: decision
- Phase: 03
- What: a minimal `internal/scheduler` (Clock, Job, Run) and an optional `resource.Scheduled` interface run the status watch (5 s) and metrics sampling (10 s).
- Why: the scheduler is already in ARCHITECTURE.md and phase 04 extends it for backups.
- Cost of change: low

## 2026-11-18 Fake platform replaces NoopMachines in tests

- Type: decision
- Phase: 03
- What: `internal/platform/platformtest` and `apitest.ServersDeps` replace NoopMachines/BlockingMachines; five test files change only their constructor.
- Why: `servers.Machines` goes away with the Reconciler refactor.
- Cost of change: low

## 2026-11-18 Root password without expiry, root SSH login by drop-in

- Type: decision
- Phase: 03
- What: the root password is set with `expire: false`, and a `00-hlab-root.conf` sshd drop-in with `PermitRootLogin yes` plus `ssh_pwauth: true` allows root password login; documented as a deviation.
- Why: SRV-02's SSH login must work without a forced change; cloud-init alone does not allow root login.
- Cost of change: low

## 2026-11-18 Guest password hook in python3

- Type: decision
- Phase: 03
- What: the guest password hook is a python3 stdlib script run by a 5 s systemd timer.
- Why: cloud-init guarantees python3, curl is not certain on the Debian images.
- Cost of change: low

## 2026-11-18 e2e password login through SSH_ASKPASS

- Type: decision
- Phase: 03
- What: the e2e harness logs in with a password through OpenSSH `SSH_ASKPASS_REQUIRE=force` and a temporary askpass script; if that fails on Windows OpenSSH, plan 03-10 stops instead of adding `x/crypto/ssh`.
- Why: no new Go module for a test-only need.
- Cost of change: low

## 2026-11-18 Metadata service binds IP and MAC on its input path

- Type: decision
- Phase: 03
- What: the agent's metadata service on 169.254.169.254:80 applies the IP/MAC binding to its input path, proven by the e2e row META-01/spoof.
- Why: the metadata carries user_data and password hashes; a spoofing guest must not read them.
- Cost of change: low

## 2026-11-18 Rebuild from system images only in phase 03

- Type: decision
- Phase: 03
- What: rebuild accepts only system images; snapshot and backup images follow in phase 04.
- Why: images other than system images arrive in phase 04.
- Cost of change: low

## 2026-11-18 Availability zone in metadata

- Type: decision
- Phase: 03
- What: the metadata availability zone is `<location>-dc<n>` from the catalog.
- Why: matches Hetzner's datacenter naming.
- Cost of change: low

## 2026-11-18 Vendor-data as multipart MIME

- Type: decision
- Phase: 03
- What: vendor-data is multipart MIME: a minimal cloud-config part and a shell-script part that writes and enables the guest hooks and the root sshd drop-in.
- Why: user cloud-config lists (`runcmd`, `write_files`) replace vendor lists and would silently drop the hooks.
- Cost of change: low

## 2026-11-18 COM1 pipe served by hlabd with a protected ACL

- Type: decision
- Phase: 03
- What: every VM gets COM1 on a named pipe that hlabd creates before each start with a SYSTEM/Administrators/VMs-only SD and drains; starts fail if the pipe cannot be served.
- Why: Debian 12 panics with `console=ttyS0` when no serial port exists, and a pipe Hyper-V creates is readable by Everyone (console carries user_data output).
- Cost of change: low

## 2026-11-18 Upgrade to wcc 0.32.3

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.2 to 0.32.3: version line updated; CLAUDE.md rules and planning documents already matched the templates
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-18 Q4 Automount on later attach via a metadata path

- Type: decision
- Phase: 04
- What: the agent document and metadata serve a per-server list of automount volumes at `GET /hlab/v1/volumes`; a udev-triggered unit reads it and mounts at `/mnt/HC_Volume_<id>` with an fstab entry.
- Why: a volume attached after server creation with `automount: true` must be mounted like at Hetzner, and only then.
- Cost of change: medium (guest-visible path baked into vendor-data of existing servers)

## 2026-11-18 Q5 SRV-15 proved by SSH into the live ISO

- Type: decision
- Phase: 04
- What: the SRV-15 e2e test boots an Ubuntu live-server ISO that reads the cidata in DVD slot 0 and checks over SSH that `/` is the live system; the indirect proof (installed system silent, DVD first) is the fallback if research refutes it.
- Why: a direct login shows the boot source; the indirect check cannot tell a hung boot from an ISO boot.
- Cost of change: low

## 2026-11-18 xfsprogs from cached .debs on the appliance seed

- Type: decision
- Phase: 04
- What: `catalog/appliance-debs.json` lists xfsprogs, libicu76, libinih1 (pool URL, snapshot.debian.org URL, sha256); hlabd caches them in `<data>\cache\debs\`, puts them on the appliance cidata, and the script runs `dpkg -i` only when `mkfs.xfs` is missing.
- Why: The user ruled out installing at every boot; building into the image needs a Linux builder.
- Cost of change: low

## 2026-11-18 Rescue counts as booted when hlab starts the server

- Type: decision
- Phase: 04
- What: a poweron, reset or reboot hlab drives while rescue is enabled ends rescue (`rescue_enabled` false, LUN 0 first, own seed back); a guest-initiated reboot is ended only by the 60-minute timeout (deviation).
- Why: Hyper-V gives no signal for a boot the guest starts itself.
- Cost of change: low

## 2026-11-18 Rescue seed reuses the server's instance-id and network-config

- Type: decision
- Phase: 04
- What: the rescue seed carries the server's instance-id and network-config, the rescue keys and password hash, and no user_data.
- Why: The installed system sees no new instance if it boots before the seed is restored, and rescue never runs user_data.
- Cost of change: medium (guest-visible)

## 2026-11-18 Rescue disk is a differencing disk via Platform.CreateChildDisk

- Type: decision
- Phase: 04
- What: `Platform` gains `CreateChildDisk`; the rescue disk is a differencing disk at LUN 63.
- Why: ARCHITECTURE names a differencing disk; adding a method extends the interface.
- Cost of change: low

## 2026-11-18 Checkpoint type Standard on every VM

- Type: decision
- Phase: 04
- What: VMs get CheckpointType Standard; a running server's snapshot takes a Standard checkpoint, copies the parent, removes the checkpoint and waits for the merge; leftover hlab checkpoints are removed first.
- Why: The guest images have no VSS daemon, so Production falls back to Standard anyway.
- Cost of change: low

## 2026-11-18 Error codes the frozen description leaves open

- Type: decision
- Phase: 04
- What: a volume attached elsewhere gives 422 `volume_already_attached`; another location, a backup without backups enabled, an image larger than the type's disk, deleting an attached volume and a 17th volume give 422 `invalid_input`.
- Why: The description names no code for these; hcloud-go knows `volume_already_attached`.
- Cost of change: low

## 2026-11-18 Appliance jobs run one at a time at LUN 1

- Type: decision
- Phase: 04
- What: appliance jobs are serialized and the job disk always sits at the appliance's LUN 1.
- Why: One job at a time needs no LUN bookkeeping.
- Cost of change: low

## 2026-11-18 Volume LUNs 1 to 16

- Type: decision
- Phase: 04
- What: a volume gets the lowest free LUN from 1 to 16; LUN 63 stays reserved for rescue.
- Why: Hetzner allows 16 volumes per server.
- Cost of change: low

## 2026-11-18 backup_window fixed and daily backups as create_image

- Type: decision
- Phase: 04
- What: `backup_window` is "22-02" while backups are enabled, null otherwise; a daily backup is a `create_image` action and a locked server waits for the next hourly run.
- Why: Hetzner's field shape, and locking without a second mechanism.
- Cost of change: low

## 2026-11-18 Images module keeps its old constructor

- Type: decision
- Phase: 04
- What: `imagesres.New(cat)` stays; `NewModule(Deps)` serves catalog plus project images.
- Why: Existing conventions and OpenAPI tests stay unchanged.
- Cost of change: low

## 2026-11-18 Volume disks join the disk rule through the cloud

- Type: decision
- Phase: 04
- What: `servers.Deps.ExtraDisks` gets volume disks from `volumes.Disks` (wired in internal/cloud); volumes call the servers module's `CheckDisk`.
- Why: servers cannot import volumes; wiring stays in the cloud.
- Cost of change: low

## 2026-11-18 VOL-04 tested without a guest rescan

- Type: decision
- Phase: 04
- What: the e2e test waits for the new size without writing to the rescan file.
- Why: The test proves the promise unaided; the product's hlab-guest timer does the rescan (see "Grown volumes picked up by the guest timer"), because an idle disk learns of a new size only on its next SCSI command.
- Cost of change: low

## 2026-11-18 SRV-15 uses the ubuntu-22.04 live-server ISO

- Type: decision
- Phase: 04
- What: the test boots that ISO; the indirect proof in the row is the fallback.
- Why: Smallest Ubuntu ISO; SSH into the live session is unconfirmed.
- Cost of change: low

## 2026-11-18 Snapshot merge deadline 10 minutes

- Type: decision
- Phase: 04 (04-01)
- What: Snapshot waits at most 10 minutes (`mergeTimeout`) for the checkpoint merge, polling every second.
- Why: No figure is documented; a short snapshot merges quickly.
- Cost of change: low

## 2026-11-18 Only hlab checkpoints are cleaned up

- Type: decision
- Phase: 04 (04-01)
- What: At the start of Snapshot only checkpoints named `hlab-snapshot-*` are removed, in every VM state; the merge wait runs only if one was removed.
- Why: A leftover checkpoint makes a direct copy miss data; foreign checkpoints are not touched.
- Cost of change: low

## 2026-11-18 Direct copy only for Off and Saved

- Type: decision
- Phase: 04 (04-01)
- What: Off and Saved (with Critical and FastSaved variants) are copied directly; every other state, Paused included, goes through a checkpoint.
- Why: The plan names off and saved as the direct cases.
- Cost of change: low

## 2026-11-18 Agent job responses

- Type: decision
- Phase: 04 (04-02)
- What: A malformed body, unknown kind, unsupported FS or negative LUN answer 400; a failing job answers 200 with `{ok:false, message}`; `Client.Job` turns both into an error.
- Why: Input errors stay apart from job failures.
- Cost of change: low

## 2026-11-18 Agent jobs bounded only by the caller's context

- Type: decision
- Phase: 04 (04-02)
- What: `Client.Job` uses its own http.Client without timeout.
- Why: mkfs of a large volume can exceed the 30 s of config and status requests.
- Cost of change: low

## 2026-11-18 Agent finds job disks by LUN in sysfs

- Type: decision
- Phase: 04 (04-02)
- What: `findDisk` matches L of H:C:T:L with SCSI type 0, ignores the host number, fails on two matches and waits up to 30 s for a hot-added disk.
- Why: E7: the host number is not stable; hot-added disks appear late.
- Cost of change: low

## 2026-11-18 Fixed labels for the rescue base

- Type: decision
- Phase: 04 (04-02)
- What: Labels present in the image become "hlab-rescue" (ext4) and "HLABRESCUE" (vfat); the ESP gets a new random volume ID; absent labels stay absent.
- Why: They differ from every image label; all rescue disks come from the one prepared base.
- Cost of change: low

## 2026-11-18 Only label references are rewritten

- Type: decision
- Phase: 04 (04-02)
- What: `LABEL=<old>` and `--label <old>` are rewritten, not the bare label text.
- Why: A short bare label would match unrelated text in grub.cfg.
- Cost of change: low

## 2026-11-18 ISO IDs 130001-130005 named by file name

- Type: decision
- Phase: 04 (04-04)
- What: Catalog ISOs get IDs 130001 to 130005; `name` is the ISO file name, also the cache file name.
- Why: Names follow the file names; the description defines no IDs.
- Cost of change: low

## 2026-11-18 Download fallback only on 404

- Type: decision
- Phase: 04 (04-04)
- What: The fallback URL pair is tried only when the primary URL or checksum URL answers 404, for system images and ISOs alike.
- Why: One shared download path; without a fallback nothing changes.
- Cost of change: low

## 2026-11-18 Image Deleter removes only the disk file

- Type: decision
- Phase: 04 (04-06)
- What: `Module.Delete` removes `<data>\images\image-<id>.vhdx`; project deletion removes the record.
- Why: `Deps` has no store; project deletion owns the rows.
- Cost of change: low

## 2026-11-18 Image PUT converts only to snapshot

- Type: decision
- Phase: 04 (04-06)
- What: A `type` other than `snapshot` answers 422 `invalid_input` on `type`.
- Why: The description allows only the conversion to snapshot.
- Cost of change: low

## 2026-11-18 Image write routes only with NewModule

- Type: decision
- Phase: 04 (04-06)
- What: PUT, DELETE, change_protection and actions are registered only by `NewModule`; `New(cat)` stays read-only.
- Why: Existing users of `New` stay unchanged.
- Cost of change: low

## 2026-11-18 Disk-size refusal field

- Type: decision
- Phase: 04 (04-07)
- What: An image larger than the type's disk gives 422 `invalid_input` on `server_type` at create and on `image` at rebuild.
- Why: At create the type is the wrong choice, at rebuild the image.
- Cost of change: low

## 2026-11-18 Default image description

- Type: decision
- Phase: 04 (04-07)
- What: `create_image` without description uses "<server name> <created RFC3339>".
- Why: Hetzner generates one without saying how.
- Cost of change: low

## 2026-11-18 Deleting a creating image is locked

- Type: decision
- Phase: 04 (04-07)
- What: `DELETE /images/{id}` answers 423 `locked` while the image is `creating`.
- Why: DELETE starts no action, so a ref lock would not be checked.
- Cost of change: low

## 2026-11-18 image_size unrounded

- Type: decision
- Phase: 04 (04-07)
- What: `image_size` is the snapshot file size in GB, not rounded.
- Why: Simplest.
- Cost of change: low

## 2026-11-18 Failed volume create is dropped

- Type: decision
- Phase: 04 (04-12)
- What: When create fails (CreateDisk or mkfs) the volume record and file are removed and the action ends in error.
- Why: Same pattern as create_image; nothing half-made stays.
- Cost of change: low

## 2026-11-18 Resize stores the new size at the start

- Type: decision
- Phase: 04 (04-12)
- What: The new size is written with the action and restored on failure; resize needs an integer 10–10240 above the current size.
- Why: The disk rule counts the growth at once.
- Cost of change: low

## 2026-11-18 Volume delete refusals

- Type: decision
- Phase: 04 (04-12)
- What: Delete protection gives 423 `protected`, an attached volume 422 `invalid_input`, a `creating` volume 423 `locked`.
- Why: Follows the images module and the test map.
- Cost of change: low

## 2026-11-18 Volume names non-empty and unique per project

- Type: decision
- Phase: 04 (04-12)
- What: Names are checked like server names; Hetzner's character pattern is not.
- Why: Same rule as servers.
- Cost of change: low

## 2026-11-18 Missing appliance debs do not stop the cloud

- Type: decision
- Phase: 04 (04-05)
- What: If `appliance-debs.json` cannot be read or a download fails, hlabd logs a warning and writes the cidata without debs; a failing `dpkg -i` only logs.
- Why: ext4 and everything else work without xfsprogs; the xfs job fails with a clear message.
- Cost of change: low

## 2026-11-18 Appliance debs as `<name>.deb` at the cidata root

- Type: decision
- Phase: 04 (04-05)
- What: Debs are `xfsprogs.deb`, `libicu76.deb`, `libinih1.deb` on the ISO root and in `<data>\cache\debs\`; JSON `{"debs":[{"name","urls","sha256"}]}`.
- Why: cidata takes root files only; plain names avoid ISO9660 problems.
- Cost of change: low

## 2026-11-18 Rescue base prepared via a partial file

- Type: decision
- Phase: 04 (04-05)
- What: The base is copied to `<dst>.partial.vhdx`, prepared, then renamed; a failure or leftover partial file is removed.
- Why: A crash never leaves a half-prepared rescue.vhdx.
- Cost of change: low

## 2026-11-18 Appliance instance-id carries a user-data hash

- Type: decision
- Phase: 04 (04-05)
- What: The appliance instance-id is `applianceName()-<12 hex of sha256(user-data)>`; a changed script makes cloud-init rerun write_files and runcmd once.
- Why: An existing appliance picks up the new install script without recreating its disk.
- Cost of change: low

## 2026-11-18 Volume created with a server: locks

- Type: decision
- Phase: 04 (04-13)
- What: POST /volumes with a server starts `create_volume` on the volume and `attach_volume` on the server; the volume is recorded attached at the end of create, then the disk is added in the attach action.
- Why: `actions.Start` refuses a second action on the same volume in one transaction.
- Cost of change: low

## 2026-11-18 Attach state in the volume record

- Type: decision
- Phase: 04 (04-13)
- What: `_hlab` gets `lun` and `automount`; attach records before adding the disk and undoes on failure, detach records only after `DetachDisk` succeeds.
- Why: Concurrent attaches get distinct LUNs; a LUN is freed only when the disk is gone.
- Cost of change: low

## 2026-11-18 Detach from a missing VM succeeds

- Type: decision
- Phase: 04 (04-13)
- What: Detach and `DetachAll` treat a missing VM as success; detaching an unattached volume is a successful no-op action.
- Why: A half-created server must still be deletable.
- Cost of change: low

## 2026-11-18 Agent push only with automount

- Type: decision
- Phase: 04 (04-13)
- What: After attach or detach the agent document is pushed only when automount is involved.
- Why: Metadata.Volumes changes only then.
- Cost of change: low

## 2026-11-18 Router hands the action manager to modules

- Type: decision
- Phase: 04 (04-08)
- What: New optional `resource.ActionUser { UseActions(*actions.Manager) }`, called by `api.Router.Mount` before Routes.
- Why: The backup job must start real actions on the router's manager so locks and WaitIdle see them.
- Cost of change: low

## 2026-11-18 Daily backup done when an automatic backup exists for the UTC day

- Type: decision
- Phase: 04 (04-08)
- What: The job skips a server only if an image with `_hlab.automatic` was created that day; manual backups do not count.
- Why: A failed backup leaves no record, so the next hourly run retries.
- Cost of change: low

## 2026-11-18 Backup slots trimmed inside create_image

- Type: decision
- Phase: 04 (04-08)
- What: Right after a backup becomes available the action deletes the oldest records beyond 7 and their files.
- Why: Slots stay exact as soon as the action ends.
- Cost of change: low

## 2026-11-18 Volume hooks in the guest

- Type: decision
- Phase: 04 (04-11)
- What: A udev rule calls `hlab-volume-id` (last 6 bytes of the `vpd_pg83` designator holding `42414c480000`), links `scsi-0HC_Volume_<id>` and starts `hlab-volume@<id>.service`, which polls `GET /hlab/v1/volumes` every 2 s for up to 60 s and mounts only a volume with a filesystem, fstab `discard,nofail,defaults`.
- Why: The volume list is pushed after the hot-add, so the hook must wait for it.
- Cost of change: medium (guest script in vendor-data of existing servers)

## 2026-11-18 Volumes present at first boot are triggered

- Type: decision
- Phase: 04 (04-11)
- What: After writing the hooks the vendor script runs `udevadm control --reload`, `trigger --action=change` and `settle`.
- Why: Volumes attached at create exist before the rule.
- Cost of change: low

## 2026-11-18 Automount at create via runcmd

- Type: decision
- Phase: 04 (04-11)
- What: Seed user-data is multipart: the user's user_data unchanged plus a cloud-config with `merge_how` append and one `runcmd` per volume; runcmd and the hook share `/run/hlab-volume.lock`.
- Why: cloud-init's `mounts` runs before the vendor script creates the link; append keeps the user's runcmd.
- Cost of change: low

## 2026-11-18 Grown volumes picked up by the guest timer

- Type: decision
- Phase: 04 (04-11)
- What: The hlab-guest timer writes `1` to `/sys/block/<disk>/device/rescan` for each `scsi-0HC_Volume_*` disk every 5 s.
- Why: An idle disk learns of a new size only on its next SCSI command; the test itself still does not rescan.
- Cost of change: low

## 2026-11-18 Rescue seed held 90 s after the boot into rescue

- Type: decision
- Phase: 04 (04-09)
- What: After a poweron, reset or reboot into rescue the action waits `Deps.RescueSeedHold` (90 s) before restoring LUN 0 first and the server's seed.
- Why: The rescue system's cloud-init must read the rescue seed, not the server's.
- Cost of change: low

## 2026-11-18 One fixed rescue disk path per server

- Type: decision
- Phase: 04 (04-09)
- What: The rescue disk is `disks\server-<id>-rescue.vhdx`, the seed `server-<id>-rescue-cidata.iso`; enable_rescue first ends a previous rescue and deletes its disk.
- Why: LUN 63 holds one disk, and every rescue needs a fresh child disk.
- Cost of change: low

## 2026-11-18 Rescue disk detach errors after a boot are logged

- Type: decision
- Phase: 04 (04-09)
- What: A failed detach of a leftover rescue disk after a boot without rescue logs a warning; the status watch removes the disk once the VM is off.
- Why: A running or rebooting VM can refuse the hot-remove.
- Cost of change: low

## 2026-11-18 One boot-order helper for rescue and ISO

- Type: decision
- Phase: 04 (04-10)
- What: `bootOrder(rescue, iso)` puts LUN 63 first during rescue, then DVD slot 1 with an ISO, then LUN 0; ending rescue keeps an attached ISO in front of LUN 0.
- Why: Rescue keeps LUN 63 first and must not drop an attached ISO.
- Cost of change: low

## 2026-11-18 A failed ISO download leaves the server unchanged

- Type: decision
- Phase: 04 (04-10)
- What: `EnsureISO` runs before any VM change; on failure the action ends in error and a previously attached ISO stays.
- Why: Simplest form, matching the test row.
- Cost of change: low

## 2026-11-18 Reconcile restores an attached ISO

- Type: decision
- Phase: 04 (04-10)
- What: When reconcile recreates a VM with `_hlab.iso`, it re-inserts the ISO into slot 1 with the matching boot order.
- Why: The server's state stays consistent.
- Cost of change: low

## 2026-11-18 rescue-prepare waits for the partitions

- Type: decision
- Phase: 04 (04-09)
- What: The agent checks every 500 ms up to 30 s for the ext4 root and vfat ESP of the hot-added disk and runs `blockdev --rereadpt` while they are missing.
- Why: The kernel creates the block device before it reads the partition table.
- Cost of change: low

## 2026-11-18 Reboot out of a booted rescue via shutdown, detach, start

- Type: decision
- Phase: 04 (04-09)
- What: After a booted rescue ends, an hlab reboot shuts the guest down, waits up to 2 min for Off, removes the rescue disk and starts the VM.
- Why: Detaching the rescue disk while the rescue system runs from it hangs the guest.
- Cost of change: low

## 2026-11-19 Disk order fixed in the rescue initramfs

- Type: decision
- Phase: 04 (04-09)
- What: rescue-prepare installs an initramfs local-top script that, if LUN 0 is not sda, unbinds the sd disks and binds LUN 0 first, and rebuilds the initramfs in a chroot.
- Why: Debian probes sd disks in parallel; no kernel parameter makes the order stable.
- Cost of change: low

## 2026-11-19 Versioned rescue base file

- Type: decision
- Phase: 04 (04-09)
- What: The cached base is `<data>\images\rescue-v2.vhdx`; the name is bumped whenever the preparation changes.
- Why: Existing installs prepare the base again instead of keeping a stale one.
- Cost of change: low

## 2026-11-19 Guest flushes after cloud-init's final stage

- Type: decision
- Phase: 04 (04-10)
- What: The vendor script installs `hlab-sync.service` (After=cloud-final.service, runs sync once) and queues it with --no-block, replacing its own final `sync` (updates "Vendor script syncs before first boot ends").
- Why: user_data runcmd and scripts run after scripts_vendor; only a flush after cloud-final makes their files survive a hard reset soon after first boot.
- Cost of change: low
- Replaces: 2026-11-18 Vendor script syncs before first boot ends

## 2026-11-19 Backlog phase B1 before phase 05

- Type: change
- What: The seven open `bug` items of BACKLOG.md become phase B1, inserted before phase 05; the `later` item for phase 09 and the `minor` test item stay in the backlog.
- Why: They cause stuck resources after a restart, wrong passwords and ignored user_data on normal paths, and a broken e2e build, which later phases would build on.

## 2026-11-19 Q6 Cloud start removes images and volumes left creating

- Type: decision
- Phase: B1
- What: an optional module interface `resource.Recoverer` runs at cloud start, before the API serves and before the first backup job run, and removes images and volumes left `creating` by an interrupted action, with their partial files; Reconcile is not called at cloud start (ARCHITECTURE corrected).
- Why: matches the `error` result of the interrupted action and the existing "failed create is dropped" handling, lets a client retry with the same name, and keeps the server path unchanged.
- Cost of change: low

## 2026-11-19 Stale password reset filtered by its action's status

- Type: decision
- Phase: B1
- What: `Contribute` serves a pending reset to the agent only while the reset_password action with the reset's ID is `running`; rebuild clears `Hlab.PasswordReset`.
- Why: `failInterrupted` marks the action `error` before the first agent push, so no start-up hook is needed and no path serves a stale reset.
- Cost of change: low

## 2026-11-19 NoCloud instance-id after a rebuild

- Type: decision
- Phase: B1
- What: Optional `Internal.InstanceID` in the server record (JSON `instance_id`, empty = server ID); every rebuild sets `<server id>-<rebuild action id>`, used by the server and rescue seed; metadata `/instance-id` stays the server ID.
- Why: A disk restored from the same server must count as a new instance so cloud-init reruns chpasswd and user_data.
- Cost of change: medium (guest-visible)

## 2026-11-19 Strict base64 decoding of user_data for the seed

- Type: decision
- Phase: B1
- What: user_data that is valid standard base64 without whitespace is decoded for the NoCloud seed, with and without volumes; anything else stays raw; metadata serves raw user_data.
- Why: Mirrors `maybe_b64decode(validate=True)` of the Hetzner datasource, which NoCloud lacks.
- Cost of change: low

## 2026-11-19 User MIME parts become leaf parts of hlab's multipart

- Type: decision
- Phase: B1
- What: With automount volumes, gzip is unpacked and each leaf part of a user MIME document is copied unchanged and in order before hlab's cloud-config part (boundary chosen to not collide); non-MIME content stays one base64 text/plain part.
- Why: cloud-init walks nested multiparts but does not unpack MIME or gzip inside text/plain, and `merge_how` needs hlab's part last.
- Cost of change: low

## 2026-11-19 `Platform.MetricsAll` with VM mapping in Go

- Type: decision
- Phase: B1
- What: A new interface method reads the three counter classes and VM disks once per call; Go maps rows to VMs by exact name (adapters: longest matching VM name); `Metrics(vm)` stays on top of it.
- Why: Extends the interface without breaking it and keeps the mapping unit-testable without Hyper-V.
- Cost of change: low

## 2026-11-19 Images module gets the store in its Deps

- Type: decision
- Phase: B1
- What: `imagesres.Deps.Store`, used only by `Recover`.
- Why: `Recover` runs outside a request and cannot take the store from the request context.
- Cost of change: low

## 2026-11-19 Start-up recovery runs before mounting

- Type: decision
- Phase: B1
- What: `recoverModules` (internal/cloud/recover.go) runs right after `c.modules()`, before mounting, the API and the scheduled jobs; an error fails cloud start.
- Why: Q6 requires it before the API serves and before the backup job.
- Cost of change: low

## 2026-11-19 E2E harness restores the cloud state it found

- Type: decision
- Phase: B1
- What: If the dev cloud runs, `setup()` stops it with the existing binary, builds, and starts it again without setting `startedCloud`, so it stays running.
- Why: A developer gets a test run instead of a build error and the cloud is left as found.
- Cost of change: low

## 2026-11-19 SRV-08 e2e row shares SRV-12's server and snapshot

- Type: decision
- Phase: B1
- What: A once-fixture provides the server and its snapshot to SRV-12 and SRV-08/own_snapshot.
- Why: No extra boot, as confirmed at the phase start.
- Cost of change: low

## 2026-11-19 A reset without its action is not served

- Type: decision
- Phase: B1
- What: a pending password reset whose action ID is no longer in the actions table counts as not running and is not served to the agent.
- Why: an action that no longer exists can never finish the reset, so serving it could only apply a password nobody was told about.
- Cost of change: low

## 2026-11-19 Seed multipart written with mime/multipart

- Type: decision
- Phase: B1
- What: With automount volumes the seed user-data is written by Go's `multipart.Writer` (CRLF, sorted part headers) instead of a hand-built LF document.
- Why: Standard MIME that cloud-init's email parser accepts, and copied user parts keep their exact bytes.
- Cost of change: low

## 2026-11-19 Base64 check for seed user_data

- Type: decision
- Phase: B1
- What: user_data is decoded only if the whole value matches `^[A-Za-z0-9+/]*={0,2}$` and decodes with correct padding; extra bits in the last character are accepted as in Python's validate=True.
- Why: Mirrors cloud-init's `maybe_b64decode(validate=True)`.
- Cost of change: low

## 2026-11-19 Seed boundary collision and unparsable MIME

- Type: decision
- Phase: B1
- What: If hlab's boundary occurs in copied content a number is appended until it does not; a MIME document that cannot be parsed falls back to one base64 text/plain part with the content after gunzip.
- Why: Keeps the outer document valid and never drops user content.
- Cost of change: low

## 2026-11-19 E2E harness on a fresh checkout

- Type: decision
- Phase: B1
- What: the harness skips the running check and the stop when bin\hlab.exe does not exist yet.
- Why: a fresh checkout has no binary to ask and no cloud of this working copy can run.
- Cost of change: low

## 2026-11-19 MetricsAll leaves out VMs not on the host

- Type: decision
- Phase: B1
- What: A VM counts as existing when `Get-VM` lists it; a requested VM missing there is left out of the map and `Metrics(vm)` returns NotFound as before.
- Why: Keeps the behaviour of `Metrics` and lets the sampler skip servers whose VM is gone.
- Cost of change: low

## 2026-11-19 Adapters map to the longest VM name on the host

- Type: decision
- Phase: B1
- What: Network adapter rows go to the longest matching VM name among all VMs on the host, not only the requested ones.
- Why: A VM not sampled must not have its adapters counted for a VM whose name is a prefix of its own.
- Cost of change: low

## 2026-11-19 Failed metrics tick stores nothing

- Type: decision
- Phase: B1
- What: If the MetricsAll call fails the tick stores nothing and logs a warning (not when the context was cancelled); with no running servers no platform call is made.
- Why: One failed read must not write zero samples for all servers.
- Cost of change: low

## 2026-11-19 Upgrade to wcc 0.32.4

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.3 to 0.32.4: CLAUDE.md rule on removing code the plan's own change left without callers
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-19 Q7: drop the unused per-VM Metrics call

- Type: change
- Phase: change
- What: `Metrics(ctx, vm)` is removed from `platform.Platform`, the hyperv implementation, the fake and ARCHITECTURE.md; `MetricsAll` stays, and phase 05 does the code removal (Q7).
- Why: since B1-03 nothing calls it, and every new platform would otherwise implement and maintain a method nothing uses.
- Cost of change: low

## 2026-11-19 Guest picks up private network changes live

- Type: decision
- Phase: 05
- What: The `hlab-guest` timer configures attached and detached private adapters (`02:48:4c:2x` only, primary private IP and the route to the network range, no alias IPs) from the metadata service via networkd files in `/run` or an `ip` fallback; the seed is rebuilt too, and a cloud-init drop-in `99-hlab-network.cfg` re-renders network config on every boot.
- Why: The user wants changes to act without a reboot, as at Hetzner (hc-utils with DHCP); hlab has no DHCP server and vendor-data `updates` acts only from the second boot.
- Cost of change: low

## 2026-11-19 Primary IP records for existing servers

- Type: decision
- Phase: 05
- What: The primary IP module's `Recover` creates missing primary IP records of existing servers at cloud start, idempotently, assigned and `auto_delete: true`; a primary IPv6 created with a server keeps `IPv6Net(serverID)`, standalone ones use `0x80000000|id`, floating ones `0xC0000000|id`.
- Why: The user keeps the dev cloud's servers; existing formal addresses stay.
- Cost of change: low

## 2026-11-19 Private adapter MACs

- Type: decision
- Phase: 05
- What: Private adapters get `02:48:4c` plus nibble `0x2` plus a 20-bit per-attachment counter; the appliance trunk adapter is `02:48:4c:00:00:02`; deviations.md notes Hetzner's `86:00:00:xx:xx:xx`.
- Why: Stays within the approved prefix and cannot collide with public MACs or the appliance.
- Cost of change: low

## 2026-11-19 One gateway per network

- Type: decision
- Phase: 05
- What: Each network's gateway is the first host address of its `ip_range`, shown as every subnet's `gateway` and reserved with each subnet's network and broadcast address and `172.31.1.1`; private IPs are the lowest free host address of the subnet.
- Why: Hetzner's FAQ and the spec examples say so (the spec text contradicts itself), and it fits one VRF per network.
- Cost of change: medium

## 2026-11-19 Network and IP refusals

- Type: decision
- Phase: 05
- What: `change_ip_range` keeps the start address and only grows (422 `service_error` with attached servers, 409 `conflict` during a change); deleting an assigned primary or floating IP gives 422 `must_be_unassigned`; delete protection on an `auto_delete` primary IP, more than 5 alias IPs and a create without IPv4, IPv6 and network give 422 `invalid_input`; a fourth network gives 403 `resource_limit_exceeded`; `delete_subnet` with attached servers gives 422 `service_error`.
- Why: The frozen spec where it says so; Hetzner documents no code for the rest.
- Cost of change: low

## 2026-11-19 Floating IP zone rule

- Type: decision
- Phase: 05
- What: A floating IP is assigned only to a server in the network zone of its home location (else 422 `invalid_input`); assigning to another server moves it, to the same server gives 422 `floating_ip_assigned`.
- Why: The stricter docs rule; FIP-02 needs a move without unassigning first.
- Cost of change: low

## 2026-11-19 Appliance private network layout

- Type: decision
- Phase: 05
- What: Per network a subinterface `t<vlan>` of the trunk adapter (found by MAC), VRF `vrf<vlan>`, table 1000+vlan, sysctls `proxy_arp`, `proxy_arp_pvlan`, no redirects, `rp_filter=0`; one conntrack zone per VLAN; stateless isolation that admits a gateway member's route destinations; one metadata listener per VRF with `SO_BINDTODEVICE`.
- Why: Members share one subinterface, ranges may overlap across VRFs, and research prefers per-VRF listeners.
- Cost of change: low

## 2026-11-19 Network membership on the server record

- Type: decision
- Phase: 05
- What: Membership lives in the server record (`private_net` plus `_hlab.networks`); the network's `servers` list is derived; servers own adapters, deny lists and seed (`SyncAdapters`), reached through the hooks `SetPublicIPs`, `SetNetworks`, `OnDelete`; a running VM's seed is swapped by ejecting and reinserting DVD slot 0.
- Why: Seed and adapters are built from the server record; hooks avoid import cycles as with volumes.
- Cost of change: medium

## 2026-11-19 Trunk adapter guards and deny-list sync

- Type: decision
- Phase: 05
- What: Trunk adapters have router guard and DHCP guard off like the appliance's promiscuous port; `SetNICDeny` keeps wanted deny entries present in both directions, removes partial or unwanted ones, adds missing ones, and leaves IP and non-deny ACLs alone.
- Why: The appliance routes on the trunk; Hyper-V refuses duplicate ACLs and lists `Both` as two entries.
- Cost of change: low

## 2026-11-19 Network validation details

- Type: decision
- Phase: 05
- What: Field errors name nested create fields `subnets[i].<field>`/`routes[i].<field>`; a `0.0.0.0/0` route skips the overlap checks with subnets and 172.31.1.1 but not with other routes; `change_ip_range` to the same size is a no-op; no free VLAN gives 403 `resource_limit_exceeded`; no free /24 and unknown subnet or route in delete actions give 422 `invalid_input`.
- Why: Hetzner documents none of these; the simplest answers that fit the frozen spec.
- Cost of change: low

## 2026-11-19 Server create and delete without public IPv4

- Type: decision
- Phase: 05
- What: cidata renders an interface without addresses as `optional: true`, so a server without IPv4 keeps eth0 and does not block wait-online; with the PublicIPs hook set, server delete leaves releasing the IPv4 to the primary IP's `ServerDeleted`; without hooks create follows `enable_ipv4/6`, ignores primary IP IDs and refuses `networks` with 422 `invalid_input`.
- Why: Keeps interface names stable and gives one owner for each IPv4.
- Cost of change: low

## 2026-11-19 VRF details on the appliance

- Type: decision
- Phase: 05
- What: Each VRF table holds an `unreachable default` route (kernel metric 4278198272); the gateway sits on `t<vlan>` as a `/32` without a connected route for the range; the trunk link is looked up only when the document has networks; stale `vrf<N>`/`t<N>` links and agent routes in tables 1002..5092 are always cleaned; the VRF applier runs after the Network applier.
- Why: Misses must not fall through to the main table (NET-02), only members, aliases and routes are reachable, and appliances without a trunk keep applying.
- Cost of change: low

## 2026-11-19 Private adapters of a server

- Type: decision
- Phase: 05
- What: The server record keeps the private MACs its VM has in `Internal.Adapters`; while rescue is on `SyncAdapters` writes the seed but leaves DVD slot 0; NotFound from `SetNICDeny`/`DetachNIC` counts as done; private adapters get `Segment{VLAN}` from the server record, `SegmentFor` decides only the public segment (ARCHITECTURE.md updated).
- Why: The platform cannot list adapters, a detach clears `private_net` before the sync, and the VLAN is server data.
- Cost of change: low

## 2026-11-19 Isolation on private networks

- Type: decision
- Phase: 05
- What: The metadata input rule on private networks matches the ingress subinterface with `meta sdifname` (key 34, defined locally; kernel 5.1+), since `iifname` is the VRF device there; a route accept rule exists only when the route's gateway is a member IP or alias IP; conntrack zones are set in prerouting only.
- Why: Keeps "Metadata service binds IP and MAC on its input path" on every segment; no rule uses `ct state`, and Hetzner firewalls do not filter private networks.
- Cost of change: low

## 2026-11-19 Primary IP details

- Type: decision
- Phase: 05
- What: Primary IPs have no `datacenter` field (the frozen schema has none), the location lives in `_hlab.location`; create with `assignee_id` returns an `assign_primary_ip` action, with `location` none; automatic and recovered ones are named `primary_ip-<id>`; given `public_net` IDs win over `enable_ipv4/6: false`; DELETE checks protection before assignment; assign and unassign rebuild the server's seed through `SyncAdapters`.
- Why: The frozen spec forbids extra properties; the seed must carry the new address for the next boot (PIP-02).
- Cost of change: low

## 2026-11-19 Guest hook for private adapters

- Type: decision
- Phase: 05
- What: `hlab-guest` takes over only private links that do not yet carry their private IP (hot-added after boot), records what it applied in `/run/hlab-guest/networks.json` and acts only on changes; without networkd it sets the route before the address; a failed network sync does not stop the password reset.
- Why: The seed's netplan config must stay in charge of links it set up, so user alias IPs and NAT routes survive (NET-03).
- Cost of change: low

## 2026-11-19 Floating IP details

- Type: decision
- Phase: 05
- What: A floating IP without name is named `floating_ip-<id>`; create with `server` returns an `assign_floating_ip` action, with `home_location` none; the list sorts by id and created and filters by name, label_selector and ip; the FIP-01 test reads the server's top-level `location` as the frozen schema has it.
- Why: The frozen spec says so; the old test path read a field the server shape does not have.
- Cost of change: low

## 2026-11-19 Metadata per VRF

- Type: decision
- Phase: 05
- What: The VRF applier gets the metadata service through the field `VRF.Metadata`, set by main.go before the first Apply; each listener passes its VLAN to the shared request router through the request context.
- Why: Keeps `NewVRF(trunkMAC)` as planned and registers every metadata path once.
- Cost of change: low

## 2026-11-19 Private metadata entries

- Type: decision
- Phase: 05
- What: `servers.Module.Metadata` builds a server's metadata entry without VLAN and IP and networks uses it for each member's private entry; `InterfaceNum` is the 1-based index in `private_net`; a member's `Subnet` is the subnet holding its IP.
- Why: A server without public IPv4 has no public entry to copy; the index matches the seed's interface order.
- Cost of change: low

## 2026-11-19 Attach and detach rules

- Type: decision
- Phase: 05
- What: `attach_to_network` checks network existence, `server_already_attached`, `networks_overlap`, then the 3-network limit; no free IP or no zone subnet gives `no_subnet_available`, a used or reserved `ip` or alias IP `ip_not_available`, an `ip`/`ip_range`/alias outside the subnets 422 `invalid_input`; alias IPs may come from any subnet of the network; `DELETE /networks` syncs the detached members' adapters after the commit before answering 204, logging failures.
- Why: Hetzner documents no order or code for these cases.
- Cost of change: low

## 2026-11-19 Cloud wiring of the network modules

- Type: decision
- Phase: 05
- What: Module order is servers, networks, primary_ips, floating_ips, volumes; cloud start detects a missing appliance trunk adapter by `SetNICDeny(appliance, 02:48:4c:00:00:02, nil)` returning NotFound and then attaches it; hyperv `SetDVD` with "" returns only once the drive is empty (at most 60 s).
- Why: volumes fill every metadata entry of a server only after networks made them; no new platform method is needed; Hyper-V finishes an eject about 5 s after returning, which broke every seed rewrite on running VMs.
- Cost of change: low

## 2026-11-19 Upgrade to wcc 0.32.6

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.4 to 0.32.6: the test rule on parallel end-to-end tests now takes shared limits in one step and logs each test's start and end with a time limit
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-19 Firewall create with unknown server answers 422

- Type: decision
- Phase: phase 06
- What: `POST /firewalls` with an `apply_to` server that does not exist answers `422 firewall_resource_not_found`; `apply_to_resources` and `remove_from_resources` answer 404 with that code.
- Why: the Hetzner OpenAPI documents 422 for create (RESEARCH.md), which overrides the 404 assumed at the phase start.
- Cost of change: low

## 2026-11-19 Firewall rule limits

- Type: decision
- Phase: phase 06
- What: a firewall takes at most 50 rules (validated); Hetzner's limit of 500 effective rules per firewall is not enforced and listed in `deviations.md`.
- Why: research shows the 500 limit is per firewall in effective rules, not per server as assumed at the phase start.
- Cost of change: low

## 2026-11-19 Outbound firewall rules never filter appliance services

- Type: decision
- Phase: phase 06
- What: resolvers, metadata service and API proxy on the appliance are never filtered by firewall rules; the firewall matches only forwarded traffic on the public interface.
- Why: Hetzner never filters resolver and metadata traffic (RESEARCH.md), so the user's answer to Q1 falls back to "never filter"; `hlab-guest` needs the metadata service.
- Cost of change: low

## 2026-11-19 Firewall rule input format

- Type: decision
- Phase: phase 06
- What: a CIDR with host bits set or without a mask gives 422 `invalid_input`; ports are `any`, `N` or `A-B` (1 <= A <= B <= 65535, digits only) echoed as sent; rules and IPs keep the order sent, IPv6 comes back in RFC 5952 form.
- Why: the spec wording suggests rejection, clients always send masked CIDRs, and the provider and Ansible compare rules order-sensitively.
- Cost of change: low

## 2026-11-19 Firewall apply and remove error cases

- Type: decision
- Phase: phase 06
- What: removing a server or selector that is not applied gives 404 `firewall_resource_not_found`; applying an applied selector gives 422 `firewall_already_applied`; a sixth direct firewall on a server gives 403 `resource_limit_exceeded`, selector matches beyond five are skipped; DELETE of a firewall applied only by selector gives 422 `resource_in_use` with message `firewall <id> is still in use`; server create with an unknown firewall gives 404 `not_found`, with firewalls and no public IPv4 422 `private_net_only_server`; `incompatible_network_type` is never returned.
- Why: `firewall_already_removed` is deprecated and the pinned clients tolerate the 404; Ansible retries on "is still in use"; Hetzner documents the rest only loosely.
- Cost of change: low

## 2026-11-19 Firewall actions

- Type: decision
- Phase: phase 06
- What: `apply_firewall`/`remove_firewall` for a server name server and firewall, for a selector one action naming only the firewall; `set_rules` returns `set_firewall_rules` plus one `apply_firewall` per directly applied server, create with rules and `apply_to` the same; the record changes in the request's transaction before the actions run; new `actions.Manager.StartAll(tx, projectID, lock, specs)` checks locks once on `lock` and never locks actions of one batch against each other.
- Why: follows the spec examples; GET and DELETE right after a remove see the new state (provider delete retry); `Start` would lock the apply actions against the running `create_server` and against each other.
- Cost of change: low

## 2026-11-19 Firewall traffic semantics

- Type: decision
- Phase: phase 06
- What: with several firewalls, outbound is restricted once any has an `out` rule and the union of `out` rules is allowed; traffic to and from a floating IP is filtered by the firewalls of its server (listed in `deviations.md`); not enforced and listed in `deviations.md`: 500 effective rules per firewall, 50 firewalls per project.
- Why: natural reading of "combined"; Hetzner does not document floating IP behaviour.
- Cost of change: low

## 2026-11-19 Firewall nftables table on the appliance

- Type: decision
- Phase: phase 06
- What: the firewall lives in its own table `inet hlab_fw`, forward chain at filter priority + 10, matching only the public interface and stateful through `ct state established,related`; table `inet hlab` stays unchanged.
- Why: keeps isolation untouched, gives FW-04 by construction, and an explicit priority avoids undefined order between base chains.
- Cost of change: medium

## 2026-11-19 Firewalls in server create

- Type: decision
- Phase: phase 06
- What: a firewall ID listed twice in a server create is applied once; a sixth firewall answers 403 `resource_limit_exceeded`; a selector's `applied_to_resources` also lists servers applied directly; each firewall action runs its own agent push.
- Why: Hetzner documents none of these cases; the simplest behaviour that keeps every client working.
- Cost of change: low

## 2026-11-19 Upgrade to wcc 0.32.8

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.6 to 0.32.8: SPEC.md section "References" with the Hetzner OpenAPI description, version line in CLAUDE.md
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-19 Dev cloud access for agents through hcloud context hlab-docs

- Type: change
- Phase: change
- What: agents run documentation examples against the dev cloud through a user-created hcloud context `hlab-docs` (endpoint `http://api.hetzner.internal/v1` set per context, token of project `docs`) and pass its token to OpenTofu and Ansible only inside the one command that needs it, never reading it.
- Why: the doc-writer has to check that the README examples work, and hcloud 1.70.2 stores the endpoint per context, so no environment variable or task is needed.
- Cost of change: low

## 2026-11-19 Zones under private-use suffixes

- Type: change
- Phase: phase 07
- What: besides a registrable domain of the Public Suffix List, a zone name may lie under `.internal`, `.test` or `.home.arpa` (except `hetzner.internal` and below, used by hlab, and never `.local`, used by mDNS); such zones never shadow a public name, so DNS-04 skips them; DNS-01 changed and the deviation goes to docs/user/deviations.md.
- Why: a local lab needs zone names that cannot collide with real domains.
- Cost of change: low

## 2026-11-19 Q8: secondary zones are stored and forwarded

- Type: change
- Phase: phase 07
- What: zones with `mode: secondary` are accepted and stored with their `primary_nameservers`, have no records in hlab, and the appliance forwards their queries upstream; they get their NRPT rule (DNS-03) but never count as `shadowing_zones` (DNS-04); listed in docs/user/deviations.md.
- Why: clients and OpenTofu configurations with secondary zones keep working without a zone transfer component.
- Cost of change: low

## 2026-11-19 Zone modes and synchronous updates

- Type: decision
- Phase: phase 07
- What: on a secondary zone the RRSet calls, `change_ttl` and `import_zonefile` answer 422 `incorrect_zone_mode`, as does `change_primary_nameservers` on a primary zone; `PUT /zones/{id}` and `PUT .../rrsets/{name}/{type}` answer 200 synchronously without an action, all other writes run as actions; where the phase 07 research finds Hetzner behaving differently, the research wins.
- Why: the user's answer at the start of phase 07, matching Hetzner's documented behaviour as far as known.
- Cost of change: low

## 2026-11-19 Zone name and record validation

- Type: decision
- Phase: phase 07
- What: zone names count only the ICANN section of the Public Suffix List (everything under `arpa` except `home.arpa` refused) and private-use names are exactly one label below `internal`, `test` or `home.arpa`, all refusals `invalid_input` on `name`; RP values only in DNS mailbox form; a CNAME conflict (beside another type, twice, at the apex) is 422 `invalid_input` on `type` with `(<name>, CNAME) conflicts with (<name>, <type>)`; an existing RRSet on create is 409 `uniqueness_error`, a write on a protected RRSet `protected`; the apex SOA is read-only except its TTL, apex NS editable.
- Why: the research could not confirm these details; the closest of Hetzner's documented rules and the project's existing codes.
- Cost of change: low

## 2026-11-19 Zone files, counts and actions

- Type: decision
- Phase: phase 07
- What: `record_count` counts SOA and NS (4 for a new zone); a bad zone file fails synchronously with 422 `invalid_input` on `zonefile` naming the line; the export writes `$ORIGIN`, `$TTL`, SOA, NS, then the rest sorted by name and type, a TTL only where the RRSet has its own; zone and RRSet actions are readable under `/zones/actions/{id}`, the per-zone action path and `/actions/{id}`; zone `status` is always `ok`.
- Why: follows the hcloud-go and CLI fixtures; the simplest behaviour where Hetzner is unconfirmed.
- Cost of change: low

## 2026-11-19 Reverse DNS defaults and zone serving

- Type: decision
- Phase: phase 07
- What: fresh primary and floating IPv4 addresses get the server default `static.<d>.<c>.<b>.<a>.clients.your-server.de`, IPv6 starts empty; a server's `change_dns_ptr` for an IP it does not hold is `invalid_input` on `ip`; `servers.IPv6.DNSPtr` becomes `[]rdns.Entry` with compatible JSON; the agent follows CNAME chains at most 8 steps and wildcards per RFC 4592; secondary zones stay out of the agent document but get their NRPT rule; secondary `primary_nameservers` are checked for syntax only; a zone name used in another project is 409 `uniqueness_error` without naming it.
- Why: consistent behaviour across resources without a migration or an `agentapi` change, and no leak across projects.
- Cost of change: low

## 2026-11-19 Reading NRPT rules in the DNS-03 test

- Type: decision
- Phase: phase 07
- What: the DNS-03 e2e test reads NRPT rules with `Get-DnsClientNrptRule` filtered by the profile tag and falls back to the `DnsPolicyConfig` registry key if that needs elevation; it never changes rules.
- Why: whether reading needs elevation is unconfirmed.
- Cost of change: low

## 2026-11-19 Reverse DNS entry rules

- Type: decision
- Phase: phase 07
- What: a new IPv6 entry beyond 100 per /64 is `invalid_input` on `ip`; a `dns_ptr` needs at least two labels of letters, digits, `-` or `_`, no trailing dot, at most 253 characters; entries are keyed by the canonical IP form.
- Why: Hetzner's exact rules are unconfirmed; the simplest check that refuses obvious mistakes.
- Cost of change: low

## 2026-11-19 Default zone sets and record value checks

- Type: decision
- Phase: phase 07
- What: the default SOA (`hydrogen.ns.hetzner.com. dns.hetzner.com. <serial> 86400 10800 3600000 3600`) and NS sets carry no TTL of their own and use the zone TTL; record values with a line break or a trailing `;` comment are refused; owner names may contain `_` and a `*` label.
- Why: follows Hetzner's defaults where known and keeps zone file export unambiguous.
- Cost of change: low

## 2026-11-19 change_dns_ptr body and side effects

- Type: decision
- Phase: phase 07
- What: `change_dns_ptr` requires the `dns_ptr` key (missing is `invalid_input` on `dns_ptr`, `null` resets); a primary IP's action names only the primary IP, pushes only the agent document and copies the PTR into the assigned server's `public_net` without rebuilding its seed.
- Why: a PTR is not part of the seed, and an explicit key avoids resetting a PTR by an empty body.
- Cost of change: low

## 2026-11-19 Zone file parsing details

- Type: decision
- Phase: phase 07
- What: an RRSet has its own TTL only when its line names one; `$TTL` and record TTLs accept BIND units (`1h30m`); `$INCLUDE` and `$GENERATE` are refused with the line; owner names are lower-cased on import; the apex NS must contain the three Hetzner nameservers and may hold more.
- Why: ordinary BIND zone files import unchanged and export and import round-trip exactly.
- Cost of change: low

## 2026-11-19 Agent zone answers

- Type: decision
- Phase: phase 07
- What: empty non-terminals count as existing (wildcards answer below them, a direct query gets NODATA); an RRSet with TTL 0 uses the zone TTL; unparsable values are skipped with a warning and a zone without apex SOA is not served; for an outside CNAME target an upstream NXDOMAIN is passed on with the chain, any other upstream failure or the 8-step limit returns the chain so far.
- Why: RFC 4592 behaviour and an agent that keeps answering when one record is bad.
- Cost of change: low

## 2026-11-19 Zone create and publish details

- Type: decision
- Phase: phase 07
- What: `rrsets` errors on zone create name `rrsets[<i>].<field>`; a given SOA set is refused, a given apex NS set replaces the default; a secondary zone needs `primary_nameservers`; every zone action publishes (agent push, then `SetZones`) under one module lock, `change_ttl` raises the SOA serial; `GET /zones/actions/{id}` is served through the `/zones/{id}/{sub}` route because `http.ServeMux` refuses both patterns side by side.
- Why: concurrent zone actions must never leave a zone without its NRPT rule (DNS-03).
- Cost of change: low

## 2026-11-19 Public lookup for shadowing zones

- Type: decision
- Phase: phase 07
- What: `PublicLookup` treats SERVFAIL or REFUSED as no answer and tries the next server; a later `Check` or `Forget` discards the result of an older lookup still running; `hlab cloud status` prints `warning: zone <zone> (project <project>) shadows a public domain`.
- Why: the simplest reading of "error when no server answers" and "a later call replaces the result".
- Cost of change: low

## 2026-11-19 RRSet list and record actions

- Type: decision
- Phase: phase 07
- What: `sort=created` on RRSets sorts by creation order in the zone document; `remove_records` and `update_records` refuse a value not in the RRSet on `records[<i>].value`; `add_records` with a `ttl` other than the RRSet's TTL is `invalid_input` on `ttl`; RRSet list and get on a secondary zone also answer `incorrect_zone_mode`.
- Why: RRSets have no `created` field, and the OpenAPI text and the user's answer at the phase start set the rest.
- Cost of change: low

## 2026-11-19 Zone file import rules

- Type: decision
- Phase: phase 07
- What: an import is refused with `protected` when any RRSet of the zone is change-protected; the imported SOA keeps its values except the serial, which hlab continues; a `$TTL` below 60 is a zone file error on its line; imported RRSets have no labels and no protection; `POST /zones` with both `zonefile` and `rrsets` is `invalid_input` on `zonefile`; create and import both run the shadow check.
- Why: the simplest reading of D4, D5 and DNS-04.
- Cost of change: low

## 2026-11-19 Zones at cloud start and e2e runs from worktrees

- Type: decision
- Phase: phase 07
- What: the zones module is built after `hostnet.Up`, so `SetZones(Names)` and the shadow check of every primary public zone run right after the modules recover; end-to-end runs happen from the main checkout after a plan's merge, because the dev service runs the main checkout's `bin\hlabd.exe` and the harness refuses any other working copy.
- Why: NRPT rules need the host network up; the harness guard protects against testing a stale binary.
- Cost of change: low

## 2026-11-19 Zones deleted with their project

- Type: decision
- Phase: phase 07
- What: the zones module is a `resource.Deleter`; project deletion removes each zone, forgets its shadow result and publishes (agent push, `SetZones`) like the zone DELETE action, and a failed publish stops the project deletion.
- Why: the verifier found NRPT rules and shadow warnings left behind for zones of a deleted project (DNS-03).
- Cost of change: low

## 2026-11-19 Upgrade to wcc 0.32.9

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.8 to 0.32.9: allow rule `Bash(hcloud --context hlab-docs *)` in .claude/settings.json, version line in CLAUDE.md
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-19 Q9: hlabd installs the CA into WSL2 distros

- Type: decision
- Phase: phase 08
- What: `hostnet.TrustCA` runs `wsl.exe` with the console user's token (WTSQueryUserToken, CreateProcessAsUser) for every WSL2 distro, only while trust is on, and removes the CA from them the same way when it is switched off; with nobody logged on the WSL part waits for the next logon, cloud start or switch; distros without `update-ca-certificates` are skipped and logged.
- Why: the user's answer to Q1; it keeps TrustCA in hostnet as ARCHITECTURE.md places it. If the first CERT-04 run shows wsl.exe fails under the user token, a scheduled task in the user's context is the fallback.
- Cost of change: medium

## 2026-11-19 Q10: `hlab ca trust on|off` in the CLI

- Type: decision
- Phase: phase 08
- What: the CLI gets `hlab ca trust on|off` (with `--output json`) over `PUT /mgmt/v1/trust`.
- Why: the user's answer to Q2; scripts and the CERT-04 e2e use it before the tray switch of phase 10.
- Cost of change: low

## 2026-11-19 Q11: formal IPv6 of load balancers

- Type: decision
- Phase: phase 08
- What: a load balancer without an IPv6 primary IP shows `2001:db8:<0x80000000 | LB ID as 32 bits>::1`; with an IPv6 primary IP it shows `<its /64>::1`.
- Why: the user's answer to Q3; one documentation prefix, no collision with server IDs below 2^31, and the hcloud CLI prints the IPv6 without a nil check.
- Cost of change: high
- Superseded: the prefix `0x80000000` by Q14 (`0xA0000000`).

## 2026-11-19 Q12: empty CRL for leaf certificates

- Type: decision
- Phase: phase 08
- What: every leaf certificate names `http://api.hetzner.internal/hlab/v1/ca.crl` as its CRL distribution point; hlabd serves an empty CRL there, signed by the hlab CA, renewed at every cloud start and weekly, `nextUpdate` 8 days ahead; the root CA has no CDP; the URL goes into the documentation and deviations.md.
- Why: the user's answer to Q4; curl.exe (Schannel) checks revocation by default and success criterion 2 asks for a plain `curl https://<name>`.
- Cost of change: high

## 2026-11-19 Q13: PROXY protocol v2

- Type: decision
- Phase: phase 08
- What: `proxyprotocol: true` makes HAProxy send PROXY protocol v2 (`send-proxy-v2`); deviations.md names the version as not verified against Hetzner.
- Why: the user's answer to Q5; no source says which version Hetzner sends, and receivers usually accept both.
- Cost of change: low

## 2026-11-19 Load balancer public IPs and primary IPs

- Type: decision
- Phase: phase 08
- What: `POST /load_balancers` takes `public_net.ipv4`/`ipv6` as primary IP IDs only while the public interface is on (422 `primary_ip_assigned`, 422 `primary_ip_blocked`, 422 `primary_ip_location_mismatch`, 412 `primary_ip_version_mismatch`); without them the LB gets a system-managed IPv4 from the public pool that is no primary IP and is deleted with it; a primary IP held by an LB shows `assignee_type` "load_balancer" with the LB ID, `assign` on it gives 422 `primary_ip_already_assigned`, `unassign` 422 `invalid_input`, and LB deletion frees or deletes it by `auto_delete`; listed in deviations.md.
- Why: the user's corrections from the frozen OpenAPI description; the description's enum knows only `server`, and neither it nor the research says what an LB-held IP shows.
- Cost of change: medium

## 2026-11-19 Load balancer networks, targets and services

- Type: decision
- Phase: phase 08
- What: an LB attaches to at most one network (422 `load_balancer_already_attached`, `ip_not_available`, `no_subnet_available`); `disable_public_interface` needs a network (422 `load_balancer_not_attached_to_network`) and no targets without `use_private_ip` (422 `targets_without_use_private_ip`), and while it is off a public target gives 422 `load_balancer_public_interface_disabled`; add_target gives 422 `resolve_cloud_private_targets_error`, `resolve_cloud_public_targets_error`, `target_already_defined`; IP targets take any unicast IPv4 except the transit /30 and the LB's own IP over the public path (deviations.md); `source_port_already_used` is 412 on add_service and 422 on update_service, and `redirect_http` takes port 80; change_type over the new type's limits gives 422 `invalid_load_balancer_type`, add_service/add_target over a limit 403 `resource_limit_exceeded`, a target in another network zone 422 `invalid_input`, a duplicate name 409 `uniqueness_error`; an omitted health check is tcp/15/10/3 and health protocol `https` is accepted.
- Why: the user's corrections from the frozen OpenAPI description; where the research differed (update_service status 422) the research wins, as the user asked.
- Cost of change: low

## 2026-11-19 Certificate issuance and status

- Type: decision
- Phase: phase 08
- What: a managed certificate starts `issuance pending, renewal unavailable`, ends `completed`/`scheduled`, or `failed` with `dns_zone_not_found` (no matching zone in the project) or `dns_zone_is_secondary_zone`, the same code on the failed action; renewal goes `scheduled` → `pending` → `scheduled` or `failed`; retry when not failed and on uploaded certificates gives 422 `invalid_input`; deleting a certificate a service uses gives 422 `resource_in_use`; uploaded certificates are not checked for expiry or chain order, and a bad PEM or a key that does not match the leaf gives 422 `invalid_input`.
- Why: the user's corrections; the renewal values scheduled/pending/failed/unavailable come from the research, which wins over the user's list as the user asked.
- Cost of change: low

## 2026-11-19 HAProxy on the appliance and Windows trust store

- Type: decision
- Phase: phase 08
- What: HAProxy comes from five cached Debian 13 .debs (hashes recomputed before commit), one master-worker instance per LB in its own netns, reloaded through the master CLI after `haproxy -c`, health and counters from `show stat`; the network veth gets the conntrack-zone rule of its VLAN; hlabd adds and removes the CA in LocalMachine\Root through the Go Cert* calls, found by thumbprint.
- Why: RESEARCH.md of phase 08; the simplest way with the existing appliance and no PowerShell round trip.
- Cost of change: medium

## 2026-11-19 Load balancer veth addressing

- Type: decision
- Phase: phase 08
- What: every LB veth's root end carries 169.254.0.1/32 with onlink routes, so no transit addressing per LB.
- Why: same pattern as 172.31.1.1; simplest.
- Cost of change: low

## 2026-11-19 HAProxy object names

- Type: decision
- Phase: phase 08
- What: frontends `fe<port>`, backends `be<port>`, server name = target key, redirect frontend `fe80redirect`.
- Why: `ParseStat` needs no mapping table.
- Cost of change: low

## 2026-11-19 WSL trust without a logged-on user

- Type: decision
- Phase: phase 08
- What: TrustCA changes the Windows store and keeps WSL pending; while a change is pending, hostnet tries again every 30 s until a user is logged on; cloud start and the next switch apply it too (WatchWSL is a stub and is not used for this).
- Why: the user's answer to Q1 promised the next logon; the 08-12 review found WatchWSL is never called.
- Cost of change: low

## 2026-11-19 settings.Save rewrites the file

- Type: decision
- Phase: phase 08
- What: `settings.Save` writes the whole settings.toml, dropping comments; the tray writes it the same way.
- Why: simplest writer for `[certificates] trust_ca`.
- Cost of change: low

## 2026-11-19 Load balancer package split

- Type: decision
- Phase: phase 08
- What: the loadbalancers package is split per feature file; 08-13 creates stubs so 08-14 to 08-18 run in parallel on separate files.
- Why: parallel plans without merge conflicts.
- Cost of change: low

## 2026-11-19 CRL served by hlabd

- Type: decision
- Phase: phase 08
- What: the CRL is served by hlabd on the API listener outside the token-checked `/v1` tree, reached through the agent's port-80 proxy, and stored as `ca.crl` beside `ca.pem`.
- Why: hlabd holds the CA key; no new listener.
- Cost of change: low

## 2026-11-19 Validator patch for LB-held primary IPs

- Type: decision
- Phase: phase 08
- What: the test response validator adds `load_balancer` to `primary_ip.assignee_type` in the loaded description, the only patched shape.
- Why: the frozen enum lacks the value hlab returns (see LB public IPs decision).
- Cost of change: low

## 2026-11-19 Per-call error status

- Type: decision
- Phase: phase 08
- What: `FieldError.Status` overrides the status only for `source_port_already_used` on update_service (422).
- Why: extends `api.Error` without changing it.
- Cost of change: low

## 2026-11-19 Load balancer metrics storage

- Type: decision
- Phase: phase 08
- What: LB metrics keep their own samples in the module with the step, point and 24 h retention rules of the server metrics.
- Why: `internal/metrics` is bound to the server samples.
- Cost of change: low

## 2026-11-19 health_status without detail

- Type: decision
- Phase: phase 08
- What: `health_status` shows only `listen_port` and `status`.
- Why: `detail` is optional in the description.
- Cost of change: low

## 2026-11-19 HTTPS services without an issued certificate

- Type: decision
- Phase: phase 08
- What: a managed certificate not yet issued or failed is left out of an HTTPS service's TLS; a service with no usable certificate is not rendered until renewal or retry issues it.
- Why: HAProxy refuses a bind without a certificate.
- Cost of change: low

## 2026-11-19 Load balancer e2e fixture ports

- Type: decision
- Phase: phase 08
- What: the e2e http service listens on 8000 because `redirect_http` takes port 80; LB-05 uses its own tcp service 8081 to a second web server on both servers.
- Why: stopping it leaves LB-02/03/04 alone.
- Cost of change: low

## 2026-11-19 hlab CA files and validity

- Type: decision
- Phase: phase 08
- What: a `ca.crl` that exists but cannot be parsed makes `ca.Open` fail and stops the cloud start; the CA and leaf certificates start one hour in the past.
- Why: simplest validation; allows for clock skew between host and guests.
- Cost of change: low

## 2026-11-19 HAProxy configuration rendering

- Type: decision
- Phase: phase 08
- What: every API value drops control characters before it reaches haproxy.cfg, the cookie name keeps only RFC 6265 token characters, path, domain and response are double-quoted with escaping; an https service without a certificate is left out; health checks carry `port <n>`, `check-ssl verify none` for tls or https checks and `check-send-proxy` with proxyprotocol; sticky cookie values come from the target key; defaults timeouts 5s/50s/50s with a server-state file.
- Why: the 08-02 review found config injection through unquoted API values; the rest is the simplest valid HAProxy config for the services.
- Cost of change: low

## 2026-11-19 Validator patch and load balancer type order

- Type: decision
- Phase: phase 08
- What: the D7 validator patch finds the response schemas of `assignee_type` by regex and fails loading when it finds none; `GET /load_balancer_types` returns the catalog order without sorting; a PEM without a CERTIFICATE block makes `TrustCA` fail before the store is touched.
- Why: a silent no-op patch would hide a changed description; the catalog is already in ID order.
- Cost of change: low

## 2026-11-19 Trust switch details

- Type: decision
- Phase: phase 08
- What: `settings.Save` writes a `.tmp` file beside settings.toml and renames it; `hlab ca export` and `hlab ca trust` got short entries in docs/user/commands.md during 08-06 because DOC-01 fails for an undocumented command; the documentation plan extends them.
- Why: a failed write keeps the old settings; the docs test guards every command.
- Cost of change: low

## 2026-11-19 Appliance package install

- Type: decision
- Phase: phase 08
- What: the install script names the xfs packages instead of `*.deb`, installs haproxy with its four libraries only while `haproxy` is missing, skips a package file missing from the ISO, ignores a failed `systemctl disable` and creates `/run/hlab`.
- Why: the glob would mix the xfs and HAProxy installs; missing debs must not stop the cloud (earlier decision).
- Cost of change: low

## 2026-11-19 Certificate and network module details

- Type: decision
- Phase: phase 08
- What: `POST /certificates` without `type` means `uploaded`; a missing `certificate` or `private_key` is `invalid_input` on that field; certificate names follow the firewall rules (128 characters, unique per project); networks pick server and LB IPs through one shared `pickIP`, and `NetworkDeleted` runs for every network removal, project deletion included.
- Why: the OpenAPI default; one place for the IP rules; LBs must hear of every deleted network.
- Cost of change: low

## 2026-11-19 Primary IPs held by load balancers, details

- Type: decision
- Phase: phase 08
- What: `ServerDeleted` and `change_dns_ptr` mirroring skip IPs held by a load balancer; `AssignLoadBalancer` checks version, assigned, blocked, location in that order; an unknown or foreign ID is `invalid_input` on `public_net.ipv4`/`ipv6`; an IP freed from a deleted LB gets `assignee_type` "server" back.
- Why: LB and server IDs can be equal, so an IP would otherwise be freed with or mirrored onto the wrong server.
- Cost of change: low

## 2026-11-19 Managed certificate details

- Type: decision
- Phase: phase 08
- What: a domain is checked against the most specific zone of its project that equals it or lies above it, and a secondary zone there gives `dns_zone_is_secondary_zone`; a failed renewal is tried again every hour while less than 30 days remain; the agent document is pushed after every successful issuance; a managed create without a CA answers 422 `invalid_input` on `type`.
- Why: the simplest reading of CERT-02/03 and decisions/managed-certificates.md; a retried certificate an LB references must reach the agent.
- Cost of change: low

## 2026-11-19 Load balancer applier

- Type: decision
- Phase: phase 08
- What: a HAProxy failure for one LB (not installed, refused by `haproxy -c`, failed start or reload) is logged and the other LBs and appliers continue, while netns, veth and route failures fail Apply; LB routes use route protocol 201; the applier runs after `vrf` and before `isolation` and `firewall`; only the first `Private` entry is used.
- Why: missing appliance debs must not stop the cloud (earlier decision); the network and VRF appliers delete foreign protocol-200 routes; an LB has at most one network.
- Cost of change: low

## 2026-11-19 WSL trust runner

- Type: decision
- Phase: phase 08
- What: WSL failures (list, no distros, failed distros) are logged and never fail `TrustCA` after the Windows store changed; the user runner calls CreateProcessAsUser directly with desktop winsta0\default and adds stderr to the output on a non-zero exit; the pending-logon trigger is a 30 s polling loop that runs only while a change is pending.
- Why: a missing WSL must not undo or fail the Windows part; os/exec cannot set the desktop; a session-change hook would need a new `sysint.ServiceHost` interface and a WTS window loop.
- Cost of change: low

## 2026-11-19 Load balancer package layout

- Type: decision
- Phase: phase 08
- What: 08-13 adds a stub `contribute.go` (filled by 08-19); stored service types live in services.go, target types in targets.go, the status state in status.go, so wave 3 plans extend them without touching loadbalancers.go; the views already show `public_net.ipv4/ipv6.dns_ptr` from `Record.DNSPtr`.
- Why: PLAN D5, parallel plans on separate files; the test helper needs `Contribute` to compile.
- Cost of change: low

## 2026-11-19 Load balancer network details

- Type: decision
- Phase: phase 08
- What: network.go reads `use_private_ip` from each stored target's JSON; `detach_from_network` while targets use private IPs answers 422 `invalid_input` on `network`; `NetworkDeleted` clears only `private_net` and the VLAN; attach and detach list the network as an action resource.
- Why: no dependency on the Go fields of 08-16; no documented code for the detach refusal.
- Cost of change: low

## 2026-11-19 Load balancer service details

- Type: decision
- Phase: phase 08
- What: a health check with protocol `https` is stored and shown as `http` with `tls: true`; an https service without certificates is `invalid_input`; add_service defaults listen port 80 (http) or 443 (https), destination port 80 and 15/10/3; update/delete of an unknown listen port is `invalid_input` on `listen_port`; `timeout_idle` 0 keeps the stored value; an https service with `redirect_http` collides with a service on 80 (`source_port_already_used`).
- Why: the OpenAPI response enum allows only tcp and http; the agent cannot pass TLS through (D12); defaults from the Terraform docs.
- Cost of change: low

## 2026-11-19 Load balancer metrics rules

- Type: decision
- Phase: phase 08
- What: the step and point rules of the server metrics are copied into the loadbalancers package instead of exported from internal/metrics; the `lb-status` job runs every 10 s.
- Why: keeps 08-17 out of internal/metrics (PLAN D9).
- Cost of change: low

## 2026-11-19 Load balancer target details

- Type: decision
- Phase: phase 08
- What: remove_target of an unknown target is 404 `not_found`; while the public interface is off every target without `use_private_ip` (IP targets included) is 422 `load_balancer_public_interface_disabled`; label selectors match the project's servers in the LB's network zone that are not being deleted, each server once; `use_private_ip` on an LB without a network is `resolve_cloud_private_targets_error`.
- Why: Terraform treats not found on remove as harmless; one rule with disable_public_interface.
- Cost of change: low

## 2026-11-19 Load balancer agent document details

- Type: decision
- Phase: phase 08
- What: a certificate that is not issued yet is left out of the service's TLS, a missing certificate fails the push; the PTR of an LB's IPv4 is pushed while its public interface is off; reverse DNS display keeps the view code of 08-13.
- Why: D12; the address stays assigned; project deletion removes LBs through their Deleter before the store drops certificates, so a missing certificate is a bug.
- Cost of change: low

## 2026-11-19 Cloud wiring for load balancers and certificates

- Type: decision
- Phase: phase 08
- What: a failed `TrustCA` at cloud start is a /status problem `trust_ca` with a log warning and the start continues; the loadbalancers module is built before certificates so `certificates.Deps.UsedBy` is `lb.UsedBy`; a cloud job `crl` renews the CRL every 7 days; the server view's `load_balancers` comes from `lb.ForServer`.
- Why: trust is a convenience and must not stop the cloud; no setter needed.
- Cost of change: low

## 2026-11-19 WSL test scripts without set -e

- Type: decision
- Phase: phase 08
- What: the LB-04 WSL script runs without `set -e` and exits on a failed `mktemp` explicitly; netns/VRF, the HAProxy config and wsl.exe under the user token passed their first e2e run unchanged, so the scheduled-task fallback of Q9 is not used.
- Why: under `bash -l` with `set -e` the shell exits through `~/.bash_logout`, whose `clear_console` fails without a console, and returns 1 after curl succeeded.
- Cost of change: low

## 2026-11-19 Close fixes of phase 08

- Type: decision
- Phase: phase 08
- What: `ca.key` gets a protected DACL with SYSTEM only (plus the running user when hlabd does not run as SYSTEM, for tests and manual starts) and `ca.Open` repairs an existing key; `settings.Save` copies the replaced file's DACL onto the temp file before the rename; the agent's LB teardown treats ENODEV on a link delete as done.
- Why: the doc-writer found ca.key readable by INTERACTIVE and Virtual Machines; the verifier found the Interactive Users modify grant lost after `hlab ca trust`; the close e2e run failed deleting a veth that the netns deletion had already removed.
- Cost of change: low

## 2026-11-19 Upgrade to wcc 0.32.10

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.9 to 0.32.10: version line only; the autoMode environment entry for the hcloud context `hlab-docs` is shown to the user for their own settings
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-19 Q14: separate IPv6 prefix for load balancers

- Type: change
- Phase: change after phase 08
- What: a load balancer without an IPv6 primary IP shows `2001:db8:<0xA0000000 | LB ID as 32 bits>::1` instead of `0x80000000` (Q11); IDs stay below 2^29; a `dns_ptr` entry stored on the old formal IPv6 moves to the new one; built in phase 09.
- Why: standalone IPv6 primary IPs already use `0x80000000 | ID`, so an LB and a primary IP with the same ID shared a /64 and their PTR entries could overlap; `0xA0000000` keeps one documentation prefix and separates servers, primary IPs, load balancers and floating IPs.
- Cost of change: low

## 2026-11-19 Server status kept until restore at cloud start

- Type: decision
- Phase: phase 09
- What: at cloud start nothing reconciles a server's stored status with its VM state before `Restore` has run, and actions still running at stop end with `error` at start.
- Why: the user's answer at the start of phase 09; after "shutdown" the VMs are off and the stored status is the only record of which servers ran.
- Cost of change: low

## 2026-11-19 Stop behaviour at a Windows shutdown

- Type: decision
- Phase: phase 09
- What: a Windows shutdown applies the `AutomaticStopAction` set at the last cloud start; a changed `stop_behaviour` is applied to running VMs at once only if Hyper-V accepts it on running VMs, otherwise documented as taking effect after the next cloud start.
- Why: the user's answer at the start of phase 09; RESEARCH.md leaves open whether `Set-VM -AutomaticStopAction` works on running VMs.
- Cost of change: low

## 2026-11-19 public_range excludes private and shared ranges

- Type: decision
- Phase: phase 09
- What: `network.public_range` is refused when it overlaps 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 or 100.64.0.0/10, besides the reserved ranges and Hetzner's special addresses.
- Why: the user's answer at the start of phase 09; users' private networks live in these ranges and Hetzner requires RFC 1918 for them.
- Cost of change: low

## 2026-11-19 Changed public_range refuses the start

- Type: decision
- Phase: phase 09
- What: if the state holds assigned IPs outside a changed `public_range`, hlabd refuses to start with problem `public_range_changed`, naming the old range and `hlab cloud start --discard-state`.
- Why: the user's answer to the planner's question at the start of phase 09; no data is lost and the API never shows addresses that do not work.
- Cost of change: medium

## 2026-11-19 Discarding the state cleans host objects and names downgrades

- Type: decision
- Phase: phase 09
- What: `--discard-state` also removes the old state's zone NRPT rules and the tagged host objects `hostnet` cleans up, and a state with a higher `user_version` is reported as coming from a newer hlab version.
- Why: the user's answer at the start of phase 09; a discard leaves nothing stale on the host and a downgrade does not look like a broken file.
- Cost of change: low

## 2026-11-19 Changed stop_behaviour reaches VMs at the next cloud start

- Type: decision
- Phase: phase 09
- What: `SetStopAction` runs at cloud start only (on Running VMs too if the 09-01 probe allows it); no settings reload is built in phase 09, so a changed `stop_behaviour` applies to a Windows shutdown after the next cloud start, as documented.
- Why: hlabd learns of a settings change only through `POST /mgmt/v1/settings/reload`, which belongs with the tray settings of phase 10; building it now goes beyond the phase's requirements.
- Cost of change: low

## 2026-11-19 Stop action on running VMs refused by Hyper-V

- Type: decision
- Phase: phase 09
- What: the 09-01 probe on a real server VM showed Hyper-V refuses a changed `AutomaticStopAction` on a Running VM (InvalidState) and accepts it on a Saved VM, so `SetStopAction` runs at cloud start on Off and Saved VMs only and the documentation says a changed `stop_behaviour` applies to a Windows shutdown after the next cloud start.
- Why: this settles the open part of the user's answer on the stop behaviour at a Windows shutdown.
- Cost of change: low

## 2026-11-19 Recorded public range in ipam

- Type: decision
- Phase: phase 09
- What: `ipam.Outside` reads the `ipam.public.used.` keys and the range of the last successful start is kept in kv `ipam.public.range`; a state without that record names the first address outside instead of the old range.
- Why: every public IPv4 goes through ipam, so one place answers whether a range change strands addresses.
- Cost of change: low

## 2026-11-19 Agent reads its transit address from the seed

- Type: decision
- Phase: phase 09
- What: the agent reads its listen address from a new `transit` file on the appliance seed, copied by `hlab-agent.service` like the secret; the appliance instance-id hashes user-data and network-config so a changed range applies once.
- Why: transit addresses now follow `public_range` instead of fixed 198.19.0.x values.
- Cost of change: low

## 2026-11-19 Package internal/discard

- Type: decision
- Phase: phase 09
- What: the discard decision (`Requested`) and cleanup (`Run`, after `hostnet.Down`) live in a new package `internal/discard`; discarded files get the suffix `.discarded-<UTC 20060102T150405Z>`, missing `-wal`/`-shm` are skipped.
- Why: a wrong discard decision destroys data, so it is unit-tested with the fake platform.
- Cost of change: low

## 2026-11-19 Small phase 09 implementation choices

- Type: decision
- Phase: phase 09
- What: `logfile.Tail` follows by polling with a test-shortenable interval and never splits a rotated line; the 60 s halt wait is `servers.Deps.HaltTimeout`; the `range_collision` logic moves into exported `RangeCollisions` in hostnet/windows; e2e LIFE rows share one stop/start cycle per stop behaviour; at a Windows shutdown the jobs are cancelled first and `Halt` changes no VM or status.
- Why: each keeps the behaviour testable at the cheapest level without new components.
- Cost of change: low

## 2026-11-19 Server Types view shows the catalog while the cloud is stopped

- Type: decision
- Phase: phase 10
- What: the Server Types view lists catalog and own types, merged and checked by the same Go package hlabd uses, also while the cloud is stopped; UI-SPEC.md changed from "own types only".
- Why: the view must match what clients see, and the merge needs no running service.
- Cost of change: low

## 2026-11-19 Deleting a project needs its name typed

- Type: decision
- Phase: phase 10
- What: the confirm dialog for deleting a project requires typing the project name, like servers, volumes, networks and zones; UI-SPEC.md lists projects there.
- Why: deleting a project deletes all its resources.
- Cost of change: low

## 2026-11-19 Existing servers outlive a removed own server type

- Type: decision
- Phase: phase 10
- What: power on, rebuild and the reconcile at cloud start take cores, memory and disk from the server's stored `server_type` copy when the type is no longer in the catalog; create and change_type still need the catalog.
- Why: an own type may be removed while servers use it, as Hetzner keeps deprecated types valid for existing servers.
- Cost of change: low

## 2026-11-19 Tray writes server-types.custom.json directly

- Type: decision
- Phase: phase 10
- What: hlabd creates `server-types.custom.json` (`[]`) at start when missing, with the settings.toml ACL (Interactive Users modify); the tray validates with the hlabd package, writes it directly and calls `POST /mgmt/v1/settings/reload` while the cloud runs.
- Why: keeps ARCHITECTURE's "writer: tray" and works with the cloud stopped, without broader folder rights.
- Cost of change: medium

## 2026-11-19 Tray settings writes

- Type: decision
- Phase: phase 10
- What: the tray validates settings with the `settings` package and writes settings.toml in one write in place; before saving it rereads the file and keeps `trust_ca` from the file, which `PUT /trust` owns.
- Why: Interactive Users cannot create files in the config folder, and the form must not overwrite the trust switch.
- Cost of change: low

## 2026-11-19 Tray quit and tray state in e2e

- Type: decision
- Phase: phase 10
- What: "Quit" ends only the tray, without a question; the e2e build (tag `e2e`) exposes the tray state (icon key, tooltip, menu items with enabled state) through a bridge method, and UI-01 drives start/stop through the menu handlers of the same controller.
- Why: the cloud is a Windows service; Playwright cannot reach the notification area, the real icon is judged in UI-08, and no debug access exists in release builds.
- Cost of change: low

## 2026-11-19 Tray connection banner and log stream reconnect

- Type: decision
- Phase: phase 10
- What: "connection to the service lost" shows only after three failed `/mgmt/v1/status` polls (about 6 s) while the SCM reports running and clears by itself; a broken log follow reconnects at the poll interval and continues after the last shown line, switching to the files while the cloud is stopped.
- Why: single hiccups must not alarm, and the log view must not duplicate or lose lines.
- Cost of change: low

## 2026-11-19 Tray build and bridge shape

- Type: decision
- Phase: phase 10
- What: package `ui` embeds `frontend/dist` with a committed placeholder file so Go builds without a frontend build; the bridge is three Wails services without a Wails import, `Call` takes and returns strings in a `Response` struct, the frontend uses `Call.ByName` without generated bindings, views switch by `location.hash`, log lines go out in 100 ms batches.
- Why: Wails sends `[]byte` as base64 and tuples as arrays, has no history routing and blocks on each event; fast bridge tests need no Wails (phase 10 research).
- Cost of change: low

## 2026-11-19 Tray settings reload and log followers

- Type: decision
- Phase: phase 10
- What: `POST /mgmt/v1/settings/reload` applies `logging.level` and the own server types; stop_behaviour is re-read at stop, public_range, data_folder and the reserves apply at the next start and the form says so; log followers end through `mgmt.Deps.Done`, closed before the management server shuts down.
- Why: the reserves go to the servers module only at start; followers must not delay a stop.
- Cost of change: low

## 2026-11-19 Tray tests

- Type: decision
- Phase: phase 10
- What: frontend unit tests run with Playwright Test without a browser through `task test:frontend`, the help module loaded through Vite `runnerImport`; the e2e tray has its own single-instance UniqueID and a `TrayTest` service under tag `e2e`; UI e2e rows share one tray process and run `npx playwright test --grep <ID>` after the LIFE rows; the Hetzner token check of CAT-03 becomes a shared helper; settings structs get json tags equal to their toml keys.
- Why: `@playwright/experimental-ct-svelte` no longer exists, and a test tray must not collide with the user's.
- Cost of change: low

## 2026-11-19 Wails raises an indirect module

- Type: decision
- Phase: phase 10
- What: adding Wails v3.1.0 raises the indirect `klauspost/compress` from v1.18.5 to v1.18.7.
- Why: required by Wails; no pinned module of SPEC.md changes.
- Cost of change: low

## 2026-11-20 Load balancer health check https stays http with tls

- Type: decision
- Phase: phase B2
- What: the backlog item to return health-check protocol `https` is dropped; `https` is still stored as `http` with `tls: true`.
- Why: the real Hetzner API rejects `health_check.protocol: https` ("Not a valid choice", probed with hcloud 1.70.2 on 2026-11-20) and its OpenAPI description knows only tcp and http.
- Cost of change: low

## 2026-11-20 Project deletion deletes networks last

- Type: decision
- Phase: phase B2
- What: `mgmt.purge` keeps the sorted kind order but moves the kind `networks` to the end through a string constant, without importing the networks package.
- Why: the network's VLAN must stay taken until no VM or load balancer of the project is left.
- Cost of change: low

## 2026-11-20 Registered routes for DOC-03

- Type: decision
- Phase: phase B2
- What: `cloud.APIRoutes()` builds the modules through `Cloud.modules` with zero values and records every registered route; the DOC-03 test requires each in docs/development/api-coverage.md, the wildcard `GET /zones/{id}/{sub}` through an explicit alias.
- Why: a hand-kept list let new endpoints pass without their documentation.
- Cost of change: low

## 2026-11-20 e2e harness stops the dev service by its state

- Type: decision
- Phase: phase B2
- What: `stopRunningCloud` stops `running`, waits through `starting` and `stopping`, and does nothing for `stopped` and `refused`.
- Why: in `refused` the service process has already exited (`handler.Execute` returns before Running, `hlab cloud status` shows `refused` only for a stopped service), so `bin\hlabd.exe` is free; the user's wish to stop it there was checked against the code.
- Cost of change: low

## 2026-11-20 e2e actions on the shared server retry on locked

- Type: decision
- Phase: phase B2
- What: RDNS-02 and SRV-10, the two parallel rows that post actions on the shared server, retry on `locked` through `postAction` and a new `postActionInto`.
- Why: the real API also answers `423 locked` while another action runs; clients retry.
- Cost of change: low

## 2026-11-20 APIRoutes builds modules with an empty catalog

- Type: decision
- Phase: phase B2
- What: `cloud.APIRoutes()` calls `Cloud.modules()` with an empty catalog, so test/docs builds only on Windows; api-coverage.md names RRSet routes `{rr_name}/{rr_type}` like the registered patterns.
- Why: the image and catalog constructors iterate the catalog; routes do not depend on its content, and the project builds and tests on Windows only.
- Cost of change: medium (moving `APIRoutes` into an untagged package)

## 2026-11-20 e2e waits for stopped accept refused

- Type: decision
- Phase: phase B2
- What: `waitCloudState("stopped")` in the e2e harness also accepts `refused`.
- Why: hlab reports `refused` only once the service manager reports the service stopped.
- Cost of change: low

## 2026-11-20 Phase 11 split into frame and remaining resources

- Type: change
- What: phase 11 builds the shared console frame with Servers and SSH Keys and stops for a manual check; the new phase 14 builds the remaining resource sections and UI-09, UI-02 is done only after both, and phase 13 depends on 14 instead of 11.
- Why: the user wants to check the look and operation of the frame before the other sections are built on the same pattern.

## 2026-11-20 Unit-tested UI modules stay free of the Wails runtime

- Type: decision
- Phase: phase 11
- What: the modules the unit tests load (actions, formErrors, labels, routes, serverActions) import neither bridge.ts nor the Wails runtime and recognise API errors by their shape.
- Why: the Playwright `unit` project runs without a browser, and the Wails runtime needs a window.
- Cost of change: low

## 2026-11-20 Console routes in a pure module, project console starts on Servers

- Type: decision
- Phase: phase 11
- What: routing moves into `lib/routes.ts` with hashes `#/projects/<p>/<view>`, `.../new` and `.../<id>/<tab>`; an unknown tab falls back to `overview` and a project console starts on Servers.
- Why: a project console opens on Servers as at Hetzner, and pure parsing can be unit-tested.
- Cost of change: low

## 2026-11-20 Server list row menu without dialogs

- Type: decision
- Phase: phase 11
- What: the server list's row menu offers only the entries without input (power on, shutdown, reboot, reset, power off, delete); the full menu with dialogs is on the detail page.
- Why: the dialogs live once, in ServerActions.svelte.
- Cost of change: low

## 2026-11-20 Restore from backup or snapshot is rebuild

- Type: decision
- Phase: phase 11
- What: restoring a server from a backup or snapshot calls `rebuild` with the image ID and shows the returned root password.
- Why: the API has no separate restore action, and rebuild returns `root_password`.
- Cost of change: low

## 2026-11-20 Server detail tabs built one plan after another

- Type: decision
- Phase: phase 11
- What: the server tabs are added in plans 11-09 to 11-11 in successive waves; tabs not built yet stay out of the tab list.
- Why: plans of one wave must not touch the same file, and stub tabs would be placeholders.
- Cost of change: low

## 2026-11-20 Own SVG line chart for server graphs

- Type: decision
- Phase: phase 11
- What: the Graphs tab uses its own `lib/LineChart.svelte` with ranges 1 h, 24 h and 7 d, reloaded every 30 s while visible.
- Why: SPEC.md names no chart library and UI-SPEC wants its own components.
- Cost of change: low

## 2026-11-20 Firewalls and volumes from the server tabs through their own actions

- Type: decision
- Phase: phase 11
- What: the Firewalls and Volumes tabs apply, remove, attach and detach through `/firewalls/{id}/actions/apply_to_resources`, `remove_from_resources` and `/volumes/{id}/actions/attach`, `detach`.
- Why: the API has no server action for them, and the tabs would otherwise only list.
- Cost of change: low

## 2026-11-20 UI-02 e2e depth for server actions

- Type: decision
- Phase: phase 11
- What: the e2e flows create, change one field, run one power action and delete (servers) or create, label and delete (SSH keys); every menu and tab request is proven by serverActions.test.ts, the rest of the operation by the manual check.
- Why: the user's answer at the phase start; each action against a real VM costs minutes.
- Cost of change: low

## 2026-11-20 Detaching from a server tab asks plainly

- Type: decision
- Phase: phase 11
- What: detaching a network, removing a firewall and detaching a volume in the server tabs ask for a plain confirmation; `ServerAction.body` gets the server ID as a second parameter.
- Why: they cut a running server off a resource; the firewall and volume bodies need the server ID.
- Cost of change: low (one flag per entry)

## 2026-11-20 One actions menu for tables and detail pages

- Type: decision
- Phase: phase 11
- What: Table's row menu uses the new `lib/ActionsMenu.svelte`; disabled items use `aria-disabled` and stay focusable, their hint sits beside the item.
- Why: one menu implementation, and a disabled entry such as Console stays reachable by keyboard with its hint readable.
- Cost of change: low

## 2026-11-20 Server create shows the password before the first boot ends

- Type: decision
- Phase: phase 11
- What: the server create form opens the root password dialog as soon as `POST /servers` answers while the toast follows the create and start actions; "Backups" sends `enable_backup` after they succeed; only x86 images and types are offered.
- Why: a first boot may download an image for minutes, `POST /servers` has no backups field, and ARM types are left out.
- Cost of change: low

## 2026-11-20 Server dialogs close before the action ends

- Type: decision
- Phase: phase 11
- What: confirmation and input dialogs of server actions close at once and the action runs on under its progress toast.
- Why: the toast already shows progress and the outcome, and a long action would otherwise block the page.
- Cost of change: low

## 2026-11-20 Firewalls applied by label selector cannot be removed from the server tab

- Type: decision
- Phase: phase 11
- What: the Firewalls tab lists a firewall applied only through a label selector as "Through a label selector" without Remove; `runAction` also waits for an `actions` array.
- Why: `remove_from_resources` for the server answers `firewall_resource_not_found` there, and `apply_to_resources` answers with `actions`.
- Cost of change: low

## 2026-11-20 Disabling backups asks first

- Type: decision
- Phase: phase 11
- What: "Disable backups" asks for a plain confirmation; the Backups and Snapshots tabs share `ImageTable.svelte`, snapshots are found by `created_from.id`, deprecated ISOs are not offered, and `runAction` accepts an empty 204 answer.
- Why: disabling deletes every backup, which the user wants confirmed like deleting one; `bound_to` is set only on backups and HLab refuses deprecated ISOs.
- Cost of change: low

## 2026-11-20 Graphs tab offers 7 days over 24 hours of samples

- Type: decision
- Phase: phase 11
- What: the Graphs tab loads all metric types in one request with the server's step and offers 1 h, 24 h and 7 d, with a muted note that hlabd keeps samples for 24 hours.
- Why: UI-SPEC names the three ranges, and hlabd's retention is 24 hours.
- Cost of change: low

## 2026-11-20 e2e actions have a 60 s deadline

- Type: decision
- Phase: phase 11
- What: the Playwright `e2e` project sets `actionTimeout: 60_000`, and the servers spec waits for a form control to be visible before choosing select or radio group.
- Why: an unbounded click held a UI-02 run for 40 minutes; the spec counted controls before the form had rendered.
- Cost of change: low

## 2026-11-21 Upgrade to wcc 0.32.17

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.16 to 0.32.17: version line only; all migrations and templates already matched.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-21 The MCP server runs as `hlab mcp` over stdio

- Type: decision
- Phase: change (Q18)
- What: the MCP server taken from "Later" into milestone 1 is a subcommand `hlab mcp` that the MCP client starts and that speaks MCP over stdio and talks to hlabd through the management pipe; it offers neither `cloud start --discard-state` nor `catalog update`; its scope is in clarification (Q18, `decisions/mcp-server.md`).
- Why: no new port, listener or authentication, the same access as `hlab`, and no destructive state discard or real Hetzner token in an agent's hands.
- Cost of change: low

## 2026-11-21 MCP server in milestone 1 with the functions of `hlab`

- Type: change
- Phase: change (Q18)
- What: the MCP server moves from "Later" into milestone 1 as phase 17 before phase 16, with goal 5 in IDEA.md, requirements MCP-01 to MCP-05, DOC-01 extended to its tools, and the tools of `decisions/mcp-server.md` (the functions of `hlab` plus the resource overview per project); tools for Hetzner resources stay for later.
- Why: the user wants agents to control hlab in the first version, and a thin layer over the management client adds no domain logic.
- Cost of change: low

## 2026-11-21 MCP library: official Go SDK

- Type: decision
- Phase: change (Q18)
- What: `hlab mcp` uses `github.com/modelcontextprotocol/go-sdk` v1.8.0 (latest stable; MCP 2026-07-28 and older protocol versions, needs Go 1.25), with `mcp.AddTool` on typed structs, `StdioTransport`, and `NewInMemoryTransports` for unit tests; handler errors become tool errors (`isError`).
- Why: the official SDK infers and validates schemas from structs and has an in-memory transport for tests; `mark3labs/mcp-go` needs manual argument handling and offers nothing more here.
- Cost of change: low

## 2026-11-21 Storage Box details

- Type: decision
- Phase: change
- What: usernames `u<n>` from 100000, one public IPv4 per box routed to the storage VM, `reachable_externally` means reachable from host and WSL2, btrfs subvolumes with quotas and snapshots, WebDAV and FTPS certificates from the hlab CA, types and prices from `task catalog`, the Hetzner API description frozen in `api/hetzner-openapi.lock`.
- Why: the simplest layout that gives every protocol its Hetzner port and behaviour without changing the appliance's role.
- Cost of change: medium

## 2026-11-21 Upgrade to wcc 0.32.18

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.17 to 0.32.18: version line only; all migrations and templates already matched.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-21 Storage Box requirements split, feasibility checked

- Type: change
- Phase: change
- What: the bundled Storage Box requirements become one capability each (SBOX-01 to SBOX-20, UI-12 and UI-13, CLI-05 to CLI-07 per client), the phases become 18 API, 19 storage VM and SSH, 20 SMB, WebDAV, FTP and snapshots, 21 console and clients, and the feasibility check E12 to E19 is worked into ARCHITECTURE.md, SPEC.md and `decisions/storage-box.md` (shared action store, `HETZNER_ENDPOINT` for OpenTofu, mpm-itk, subaccounts with the box's UID, crypt and NT hashes, level-1 qgroups, passive FTP ports).
- Why: one requirement per checkable capability lets each phase test and verify it, and the riskiest assumptions of the storage VM are proven before it is built.
- Cost of change: low (planning only)

## 2026-11-21 SMB from WSL2 to a storage box works like SSH

- Type: assumption
- Phase: change
- What: SMB (TCP 445) from WSL2 in NAT mode reaches a storage box over the same forwarding path as SSH (E4, HOST-03); the check needs elevated rights and was skipped by the user (E16).
- Why: SMB is one more TCP port on a confirmed path; the end-to-end tests of SBOX-04 and SBOX-14 prove it.
- Cost of change: high

## 2026-11-24 Upgrade to wcc 0.32.26

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.25 to 0.32.26: version line only.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-25 Upgrade to wcc 0.33.0

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.27 to 0.33.0: header paragraph of DECISIONS.md on short entries and telling titles, version line.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-25 Upgrade to wcc 0.34.0

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.33.0 to 0.34.0: version line only; the "Release" section that SPEC.md now needs follows with `/wcc:change`.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low
