# Phase 26 context

Confirmed by the user on 2026-11-24 without corrections. Experiments: E35 to E39.

## Assumptions

- A fault changes the server or volume directly; it creates no entry in the resource's action list.
- A crash on a server that is already off is accepted; the server stays off and cannot be started until the fault ends.
- A crash on a frozen server first ends the freeze fault; the fault list then shows only the crash and the server shows `off`.
- A freeze on a server whose machine is not running (off, crashed, starting) is refused as invalid input ("server is not running").
- The five power actions (poweron, poweroff, shutdown, reboot, reset) and rebuild end a freeze first and then run; all other actions run against the paused machine (E35).
- While a crash is active, a refused rebuild fails before it changes anything; the old disk stays.
- `disk_slow` on a volume needs the volume to be attached, otherwise invalid input; detaching ends the fault.
- `disk_slow` on a server keeps its system disk limit across power actions, rebuild, rescue and crash; only ending the fault, the cloud start cleanup or deleting the server removes it (E36).
- `volume_unavailable` on a volume whose server is off is accepted; the server starts without the disk until the fault ends.
- Every cloud start resumes paused machines, reattaches missing volume disks and removes every disk limit, about 20 ms per server with nothing to fix (E37).
- The e2e test waits for cloud-init, runs `apt-get update`, installs `fio` from the internet, about 2 minutes allowed (E38).
- The speed check reads the raw disk with fio as in E39; the slow disk passes within about 10 % of its limit, after the fault ends it must be more than 5 times faster.
- The restart check uses a normal stop and start of the dev service; cleanup after an abrupt end is tested with the simulated hypervisor in unit tests.
- `docs/guide.md` gets one example per new kind, explains what the guest sees with an unavailable volume (remount, `umount -l` first) and that the limit counts 8 KiB I/Os.

## Technical choices

- `platform`: new `PowerOp` values `PowerPause`/`PowerResume` (Suspend-VM/Resume-VM), both no-ops in the target state.
- `platform`: `SetDiskIOPS(vm, path, max)`, skips the set when the value is equal; the cleanup reads all drives of a VM in one call and sets only those above 0.
- `platformtest` fake records pause and IOPS.
- `faults.Manager`: additive `Active(project, kind, targetType, id) bool` and `EndKind(ctx, project, kind, targetType, id)`.
- Servers module `Deps`: hooks `FaultActive` and `EndFault`, wired nil-safe in `internal/cloud` like `EndFaults`.
- Effects live in `internal/faults` as constructors over `ServerInjector`/`VolumeInjector`, registered by the cloud in `Options.Effects`.
- Crash block in `bootPower` and at the top of `rebuildMachine`; error `server_error` "the server's host failed (injected fault)".
- Cloud start cleanup: servers module becomes a `resource.Recoverer` (resume, system disk IOPS 0); volumes `Recover` reattaches and sets IOPS 0; both before the API serves.
- fio finds the system disk through `findmnt`/`lsblk` and the volume by `/dev/disk/by-id/scsi-0HC_Volume_<id>`.
- `Snapshot`, NIC, DVD and disk calls need no change for paused VMs (E35).
- e2e: `test/e2e/fault_test.go` with one shared server and volume, subtests FAULT-05, 06, 09, 10 and the restart check using the restart helper of `life_test.go`.

## Questions

- none
