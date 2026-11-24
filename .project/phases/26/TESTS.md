# Phase 26 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| FAULT-05 | `Active(p, server_crash, server, 7)` is true while the fault is listed and false after it ends, and false for another server, kind or project | unit | internal/faults/manager_test.go: TestFaults/FAULT-05/active | 26-02 |
| FAULT-06 | `EndKind(p, server_freeze, server, 7)` ends that freeze with `Undo` and leaves a `disk_slow` on server 7 and a freeze on server 8 listed; with none listed it returns nil | unit | internal/faults/manager_test.go: TestFaults/FAULT-06/end_kind | 26-02 |
| FAULT-10 | `Effects` `disk_slow` (table): Apply on a volume target calls `SetIOPS(iops)`, on a server target `SetSystemDiskIOPS(iops)`; Undo calls the same with 0 | unit | internal/faults/effects_test.go: TestFaults/FAULT-10/effects | 26-02 |
| FAULT-05 | `Crash` on a running server turns its VM off hard and the API shows `off` at once without a new action; on an off server it is accepted and the server stays off; on a frozen server `EndFault(server_freeze)` runs before the stop | unit | internal/resources/servers/faults_test.go: TestServers/FAULT-05/crash | 26-03 |
| FAULT-05 | while `FaultActive(server_crash)` is true, poweron, reboot, reset and rebuild (table) end with action error `server_error`, the VM is not started and a rebuild leaves the disk file; poweroff succeeds; once false, poweron starts the VM and the server shows `running` | unit | internal/resources/servers/faults_test.go: TestServers/FAULT-05/blocked | 26-03 |
| FAULT-06 | `Freeze` pauses a running server's VM and the API keeps `running` after a status watch run; on an off or starting server it is an `*faults.InputError` and nothing is paused; `Thaw` resumes a Paused VM and leaves an Off or Saved one untouched | unit | internal/resources/servers/faults_test.go: TestServers/FAULT-06/freeze | 26-03 |
| FAULT-06 | the five power actions and rebuild (table, one case each) call `EndFault(server_freeze)` for the server before their power operation; attaching an ISO does not | unit | internal/resources/servers/faults_test.go: TestServers/FAULT-06/power_action_ends | 26-03 |
| FAULT-10 | `SetSystemDiskIOPS(200)` limits the server's system disk path on its VM and only that; 0 removes the limit | unit | internal/resources/servers/faults_test.go: TestServers/FAULT-10/system_disk | 26-03 |
| FAULT-02 | servers `Recover` at cloud start resumes a Paused server VM, leaves Off and Saved VMs in their state, and removes every disk limit of every server VM | unit | internal/resources/servers/faults_test.go: TestServers/FAULT-02/start_cleanup | 26-04 |
| FAULT-09 | `Remove` detaches the volume's disk from its server's VM while `GET /volumes/{id}` still shows the server; `Restore` attaches the same path at the record's LUN; both work on an off server | unit | internal/resources/volumes/faults_test.go: TestVolumes/FAULT-09/remove_restore | 26-05 |
| FAULT-09 | detach and resize of a volume (table) end its faults through `EndFaults` before their platform work | unit | internal/resources/volumes/faults_test.go: TestVolumes/FAULT-09/ends_first | 26-05 |
| FAULT-10 | `SetIOPS(300)` limits the volume's path on its server's VM; 0 removes the limit | unit | internal/resources/volumes/faults_test.go: TestVolumes/FAULT-10/set_iops | 26-05 |
| FAULT-02 | volumes `Recover` at cloud start attaches every attached volume's disk at its LUN on its server's VM, and none of an unattached volume | unit | internal/resources/volumes/faults_test.go: TestVolumes/FAULT-02/start_cleanup | 26-05 |
| FAULT-10 | target lookup: `disk_slow` on an unattached volume is an `*faults.InputError`, on an attached volume and on a server it passes | unit | internal/cloud/faults_test.go: TestCloud/FAULT-10/unattached_volume | 26-06 |
| FAULT-05 | `hlab fault create server_crash`: the server shows `off`; `hcloud server poweron` fails with `server_error`; after `hlab fault delete` it is still `off`, and poweron brings it to `running` with SSH | e2e | test/e2e/fault_test.go: TestE2E/FAULT-05 | 26-06 |
| FAULT-06 | `server_freeze`: the server shows `running` and the host's ping to its IPv4 gets no answer; `hcloud server reset` ends the fault (gone from `hlab fault list`) and ping answers; a second freeze ended by `hlab fault delete` gives ping answers again with the same `boot_id` | e2e | test/e2e/fault_test.go: TestE2E/FAULT-06 | 26-06 |
| FAULT-09 | `volume_unavailable` on the mounted volume: `hcloud volume describe` still shows the server, reading a file written before on the volume fails in the guest (I/O error or mount gone); after `hlab fault delete`, `/dev/disk/by-id/scsi-0HC_Volume_<id>` is back and after `umount -l` and `mount` the file reads the same | e2e | test/e2e/fault_test.go: TestE2E/FAULT-09 | 26-06 |
| FAULT-10 | `disk_slow` with `iops` 200 (table: the volume, the server's system disk): fio as in E39 measures at most 220 IOPS; after the fault ends more than 1000 | e2e | test/e2e/fault_test.go: TestE2E/FAULT-10 | 26-06 |
| FAULT-06 | after one cloud stop and start shared with the two rows below, with a `server_freeze`, a `volume_unavailable` and a `disk_slow` on the system disk active before: `hlab fault list` is empty, no server VM is Paused and the server answers SSH | e2e | test/e2e/fault_test.go: TestE2E/FAULT-06/restart | 26-06 |
| FAULT-09 | after the shared restart, the volume's VHDX is on the VM at its LUN (`Get-VMHardDiskDrive`) and the guest sees `/dev/disk/by-id/scsi-0HC_Volume_<id>` | e2e | test/e2e/fault_test.go: TestE2E/FAULT-09/restart | 26-06 |
| FAULT-10 | after the shared restart, every drive of the server's VM has `MaximumIOPS` 0 | e2e | test/e2e/fault_test.go: TestE2E/FAULT-10/restart | 26-06 |

The platform calls of 26-01 (`Suspend-VM`, `Resume-VM`,
`Set-VMHardDiskDrive -MaximumIOPS`) are proven by the e2e rows; the fake
has no row. Wiring `Effects`, `FaultActive` and `EndFault` in the cloud
is glue. DOC-01, DOC-03 and DOC-04 keep their tests in
`test/docs/docs_test.go`; DOC-07 is manual.

The FAULT rows of `TestE2E` stop and start the cloud, so they run alone
in `test/e2e/e2e_test.go` after `releaseLifeFixture` and before the UI
rows, in the order FAULT-10, FAULT-09, FAULT-06, FAULT-05, then the three
restart rows (the first of them runs the shared stop and start lazily),
then `releaseFaultFixture`. They share one debian-13 server with one
mounted volume; fio is installed once (cloud-init wait, `apt-get update`,
about 2 minutes, E38).

## Writers

1. Faults and cloud: `internal/faults/manager_test.go` (rows FAULT-05
   active, FAULT-06 end_kind, with the existing helpers),
   `internal/faults/effects_test.go` (row FAULT-10 effects, fake
   injectors) and `internal/cloud/faults_test.go` (row FAULT-10
   unattached_volume, registered in `TestCloud`), ~220 lines.
2. Servers: `internal/resources/servers/faults_test.go`, registered in
   `TestServers` (rows FAULT-05 crash and blocked, FAULT-06 freeze and
   power_action_ends, FAULT-10 system_disk, FAULT-02 start_cleanup), on
   `platformtest.Fake` with fake `FaultActive`/`EndFault` hooks, ~380
   lines.
3. Volumes: `internal/resources/volumes/faults_test.go`, registered in
   `TestVolumes` (rows FAULT-09 remove_restore and ends_first, FAULT-10
   set_iops, FAULT-02 start_cleanup), ~220 lines.
4. End to end: `test/e2e/fault_test.go` (fixture with server, volume and
   fio, the shared restart, rows FAULT-05, FAULT-06, FAULT-09, FAULT-10
   and the three restart rows) and its registration in
   `test/e2e/e2e_test.go`, ~480 lines.
