# Phase 26: Server and disk faults

## Interfaces

Additive to `platform.Platform` (26-01), as in ARCHITECTURE.md "platform":

```go
package platform

const (
	// appended after PowerShutdownRetry
	PowerPause  // Suspend-VM; no error on a Paused VM
	PowerResume // Resume-VM; no-op (no error) unless the VM is Paused
)

type Platform interface {
	// ... existing methods ...

	// SetDiskIOPS sets Hyper-V MaximumIOPS (I/Os of 8 KiB per second) of
	// the drive holding the VHDX path on vm, running or not; 0 removes the
	// limit. A drive already at max is left as it is. path "" means every
	// hard disk drive of vm, read in one call (cloud start cleanup, E37).
	// NotFound when vm does not exist or path is not attached to it.
	SetDiskIOPS(ctx context.Context, vm, path string, max int) error
}
```

`platformtest.Fake` (26-01): `Power` handles `PowerPause` (Running or
Paused to Paused, otherwise InvalidState) and `PowerResume` (Paused to
Running, any other state unchanged); `SetDiskIOPS` records the limit per
VM and path (`""` clears all of the VM's) and the fake answers
`IOPS(vm, path string) int`.

Additive to `internal/faults` (26-02):

```go
package faults

// Active reports whether a fault of kind on the target of targetType and
// id is listed (applied) in project.
func (m *Manager) Active(project, kind, targetType string, id int64) bool

// EndKind ends only the faults of kind on that target (Undo as End does);
// none listed is no error.
func (m *Manager) EndKind(ctx context.Context, project, kind, targetType string, id int64) error

// ServerInjector is implemented by the servers module.
type ServerInjector interface {
	Crash(ctx context.Context, project string, id int64) error
	Freeze(ctx context.Context, project string, id int64) error  // *InputError "server is not running" unless the VM is Running
	Thaw(ctx context.Context, project string, id int64) error    // resumes only a Paused VM
	SetSystemDiskIOPS(ctx context.Context, project string, id int64, max int) error // 0 removes
}

// VolumeInjector is implemented by the volumes module.
type VolumeInjector interface {
	Remove(ctx context.Context, project string, id int64) error
	Restore(ctx context.Context, project string, id int64) error
	SetIOPS(ctx context.Context, project string, id int64, max int) error // 0 removes
}

// Effects returns the effects of server_crash (Apply Crash, Undo nothing:
// the block ends with the listing), server_freeze (Freeze/Thaw),
// volume_unavailable (Remove/Restore) and disk_slow (by target type:
// server SetSystemDiskIOPS, volume SetIOPS; Undo with 0), for
// Options.Effects.
func Effects(s ServerInjector, v VolumeInjector) map[string]Effect
```

Additive to `servers.Deps` (26-03), set by the cloud nil-safe like
`EndFaults` (26-06):

```go
	// FaultActive reports whether a fault of kind is active on server id
	// of project (faults.Manager.Active); nil = never.
	FaultActive func(project, kind, targetType string, id int64) bool
	// EndFault ends the faults of kind on server id of project
	// (faults.Manager.EndKind); nil = none.
	EndFault func(ctx context.Context, project, kind, targetType string, id int64) error
```

The injected crash's action error is `server_error` with message
"the server's host failed (injected fault)".

## Plans

- [x] 26-01 Platform: pause, resume and disk IOPS
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FAULT-06, FAULT-10 | Tests: - (the Hyper-V calls are proven by test/e2e/fault_test.go, the fake by the module tests)
  - Reads: ARCHITECTURE "platform (interface) and platform/hyperv"; EXPERIMENTS E26, E27, E35, E36, E37; phases/26/CONTEXT.md "Technical choices"
  - Size: ~170 lines in 4 files
  - Adjusts: -
  - [x] `internal/platform/platform.go`: `PowerPause`, `PowerResume` (with `String`), `SetDiskIOPS` in the interface.
  - [x] `internal/platform/hyperv/hyperv.go`, `internal/platform/hyperv/disks.go`: `Suspend-VM` / `Resume-VM` (resume only when the state is Paused), `SetDiskIOPS` via `Get-VMHardDiskDrive` filtered by full path (or all drives for ""), `Set-VMHardDiskDrive -MaximumIOPS` only where the value differs; errors as `platform.Error`.
  - [x] `internal/platform/platformtest/fake.go`: pause, resume, `SetDiskIOPS` and `IOPS` as in "Interfaces".

- [x] 26-02 Fault manager: Active, EndKind and effects
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FAULT-05, FAULT-06, FAULT-10 | Tests: internal/faults/manager_test.go, internal/faults/effects_test.go
  - Reads: ARCHITECTURE "faults (phase 25)"; DECISIONS "2026-11-24 Fault kinds become active by registered effects", "2026-11-24 Undo errors do not keep a fault alive"; phases/26/CONTEXT.md
  - Size: ~130 lines in 3 files
  - Adjusts: -
  - [x] `internal/faults/manager.go`: `Active` (applied entries only) and `EndKind` over the existing `end`.
  - [x] `internal/faults/effects.go` (new): `ServerInjector`, `VolumeInjector`, `Effects`.

- [x] 26-03 Servers: crash, freeze and system disk limit
  - Wave: 2 | Depends on: 26-01, 26-02 | Review: alone
  - Requirements: FAULT-05, FAULT-06, FAULT-10 | Tests: internal/resources/servers/faults_test.go
  - Reads: REQUIREMENTS FAULT-05, FAULT-06, FAULT-10; ARCHITECTURE "faults (phase 25)" the server paragraph, "resource"; DECISIONS "2026-11-24 A crashed server cannot be started while its fault is active", "2026-11-24 Where deletes end their faults"; EXPERIMENTS E26, E35, E36; phases/26/CONTEXT.md; `internal/resources/servers/power.go`, `rescue.go` `bootPower`, `rebuild.go`, `watch.go` `statusOf`
  - Size: ~280 lines in 5 files
  - Adjusts: -
  - [x] `internal/resources/servers/faults.go` (new): `Crash` (ends a freeze through `Deps.EndFault` first, `PowerStop`, status `off` through `setStatus`, no action), `Freeze` (`*faults.InputError` "server is not running" unless the VM is Running; `PowerPause`; the status stays), `Thaw` (`PowerResume` only on a Paused VM), `SetSystemDiskIOPS` (`SetDiskIOPS` on the system disk path); unknown server `faults.ErrNotFound`.
  - [x] `internal/resources/servers/servers.go`, `power.go`, `rebuild.go`, `rescue.go`: `Deps.FaultActive` and `Deps.EndFault`; in the work of the five power actions and rebuild, before any platform call, `EndFault(server_freeze)`; for poweron, reboot, reset and rebuild, while `FaultActive(server_crash)` the action ends with `server_error` "the server's host failed (injected fault)" before anything changes (rebuild keeps its disk); the project name is taken in the handler and passed into the work.

- [ ] 26-04 Servers: cleanup at cloud start
  - Wave: 2 | Depends on: 26-01 | Review: alone
  - Requirements: FAULT-06, FAULT-10 (FAULT-02 at cloud start) | Tests: internal/resources/servers/faults_test.go
  - Reads: ARCHITECTURE "resource" (`Recoverer`), "faults (phase 25)" the cloud start paragraph, "Cloud start and stop"; EXPERIMENTS E37; phases/26/CONTEXT.md
  - Size: ~70 lines in 1 file
  - Adjusts: -
  - [ ] `internal/resources/servers/recover.go` (new): `Recover` implementing `resource.Recoverer`: for every server whose VM exists, `PowerResume` when it is Paused and `SetDiskIOPS(vm, "", 0)` (all drives, volumes included); errors logged per server, never abort the start.

- [x] 26-05 Volumes: unavailable, slow and cleanup at cloud start
  - Wave: 2 | Depends on: 26-01, 26-02 | Review: alone
  - Requirements: FAULT-09, FAULT-10 (FAULT-02 at cloud start) | Tests: internal/resources/volumes/faults_test.go
  - Reads: REQUIREMENTS FAULT-09, FAULT-10; ARCHITECTURE "faults (phase 25)" the volume paragraph, "resource"; DECISIONS "2026-11-24 Where deletes end their faults"; EXPERIMENTS E28, E36; phases/26/CONTEXT.md; `internal/resources/volumes/attach.go`, `volumes.go` `resize`, `disk.go` `Recover`
  - Size: ~200 lines in 4 files
  - Adjusts: -
  - [x] `internal/resources/volumes/faults.go` (new): `Remove` (`DetachDisk` from the server's VM, record unchanged), `Restore` (`AttachDisk` at the record's LUN), `SetIOPS` (`SetDiskIOPS` on the volume's path at its server's VM); unknown volume `faults.ErrNotFound`, an unattached one `*faults.InputError`.
  - [x] `internal/resources/volumes/attach.go`, `volumes.go`: detach and resize call `Deps.EndFaults` for the volume before their platform work (errors logged, the action goes on).
  - [x] `internal/resources/volumes/disk.go`: `Recover` also calls `AttachDisk` (idempotent) for every attached volume at its LUN; a failure is logged.

- [ ] 26-06 Server and disk faults in the cloud
  - Wave: 3 | Depends on: 26-03, 26-04, 26-05 | Review: alone
  - Requirements: FAULT-05, FAULT-06, FAULT-09, FAULT-10 | Tests: internal/cloud/faults_test.go, test/e2e/fault_test.go
  - Reads: ARCHITECTURE "faults (phase 25)", "Cloud start and stop"; phases/26/CONTEXT.md; `internal/cloud/faults.go`, `internal/cloud/cloud.go` `modules`, `start`
  - Size: ~90 lines in 2 files
  - Adjusts: -
  - [ ] `internal/cloud/faults.go`: `Options.Effects = faults.Effects(servers module, volumes module)`; nil-safe `faultActive` and `endFault` over the run's manager; `lookupTarget` refuses `disk_slow` on an unattached volume with an `*faults.InputError` like `volume_unavailable`.
  - [ ] `internal/cloud/cloud.go`: the servers module's `Deps.FaultActive` and `Deps.EndFault`; the modules kept so `newFaults` can pass them.

- [ ] 26-07 Documentation
  - Wave: 4 | Depends on: 26-01, 26-02, 26-03, 26-04, 26-05, 26-06 | Review: alone
  - Requirements: DOC-01, DOC-03, DOC-04, DOC-07 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation" (D1, D4, D6) and "Accounts and secrets" (hlab-docs context); REQUIREMENTS FAULT-05, FAULT-06, FAULT-09, FAULT-10, DOC-01 to DOC-07; decisions/fault-injection.md; EXPERIMENTS E28, E27; phases/26/CONTEXT.md
  - Size: ~150 lines in 3 files
  - Adjusts: -
  - [ ] `docs/guide.md`: in "Fault injection", one example each for `server_crash`, `server_freeze`, `volume_unavailable` and `disk_slow` (volume and system disk) with `hlab fault create`; what the guest sees with an unavailable volume and how to mount it again (`umount -l` first when it was busy); that the limit counts I/Os of 8 KiB; which actions are refused during a crash and which end a freeze; the kinds still to come named.
  - [ ] `docs/deviations.md` and `docs/development/components.md`: a crashed or frozen server creates no action; the platform's pause and IOPS calls and the cloud start cleanup.
