# Phase 16: test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| INST-01 | the setup bundle built by `tools/installer` starts its bootstrapper application without elevation: `hlab-setup-<v>.exe /layout <temp dir> /quiet /log <file>` exits 0 and its log has no `e000` line (regression for the 2026-11-25 finding "setup bundle does not start") | e2e | test/e2e/installer_test.go: TestE2E/INST-01 | 16-06 |
| INST-05 | `downScript` run through `runPowerShell` succeeds for a tag whose switch and host adapter do not exist (no elevation needed for what it reads; skip with the reason if a cmdlet needs it), and a script that fails puts both its stdout and its stderr into the error | unit | internal/hostnet/windows/hostnet_test.go: TestHostNet/INST-05 | 16-08 |
| CERT-04 | the interactive user's session is the console session with a user; with nobody at the console it is the single active Remote Desktop session; there is none with two active Remote Desktop sessions or with only a disconnected one (fake session list) | unit | internal/hostnet/windows/trust_test.go: TestHostNet/CERT-04/interactive-user | 16-08 |
| INST-05 | stopping the service for uninstall waits for a START_PENDING service until it accepts the stop and then stops it, and ends the service's process when it stays pending past the deadline, so `uninstall-cleanup` can remove the service and its files (fake service manager, short deadlines) | unit | internal/sysint/windows/install_test.go: TestStopService/INST-05 | 16-09 |
| INST-06 | a cloud start whose step after the API has started does not finish within its deadline ends with an error and a teardown instead of staying in the service's START_PENDING (short deadline or test clock) | unit | internal/cloud/recover_test.go: TestCloud/INST-06/start-deadline | 16-09 |
| INST-05 | at a cloud start, a stored server whose VM does not exist (as after a KeepData uninstall) gets its VM recreated from its kept disk without a new disk, and a server stored as running is started; a reconcile error is logged and the next server is restored (fake platform) | unit | internal/resources/servers/lifecycle_test.go: TestServers/INST-05/restore-recreates-vm | 16-11 |
| UI-09 | the Create server form preselects the first location by ID; its type list (and Change type) offers every non-ARM type with an entry for the location regardless of `available`, a deprecated one labelled "(deprecated)" | unit | ui/frontend/tests/unit/servers.test.ts: UI-09 server type choice | 16-12 |
| UI-09 | the row menus of Volumes, Floating IPs and Primary IPs show one state-dependent entry: Attach/Assign (with the server select) while detached or unassigned, Detach/Unassign while attached or assigned | unit | ui/frontend/tests/unit/volumes.test.ts, floatingIps.test.ts, primaryIps.test.ts: UI-09 row menu attach/assign | 16-12 |
| UI-09 | every delete of something with a name asks for the typed name; unnamed entries keep the plain confirm (one case per kind of dialog where it is implemented, one resource each to show it is wired in) | unit | ui/frontend/tests/unit/actions.test.ts: UI-09 typed-name delete | 16-13 |
| UI-12 | creating a storage box from the window follows its action to success without a "project undefined" error | unit | ui/frontend/tests/unit/storageBoxes.test.ts: UI-12 create follows the action | 16-13 |
| INST-06 | an upgrade over a running cloud with the tray running and a passive UI (`/passive`) finishes without a files-in-use stop, closes the tray and leaves the service stopped | e2e (elevated) | test/install/install_test.go: TestInstall/INST-06 (extended) | 16-14 |
| SBOX-03 | after a box with data is deleted and the cloud stops with "Shut the VMs down", the storage VM data disk returns to roughly its size before the data was written | e2e | test/e2e: the storage box row that covers the compaction, or SBOX-03 (extended) | 16-15 |

Before the 2026-11-25 findings there were no rows: phase 16 has no `unit` or `e2e` requirement: DOC-08 and DOC-07 are
`manual` and are checked by the user (CHECKS.md). DOC-01 to DOC-06 already
have their tests in `test/docs/docs_test.go` (`TestDocs/DOC-01` to
`TestDocs/DOC-06`). DOC-04 (links) and DOC-06 (Taskfile commands in the
developer guide) cover the new `docs/development/release-checks.md`
without a change: they walk every Markdown file under `docs/` and
`docs/development/`. INST-01, INST-04, INST-05 and INST-06 run unchanged in
the existing elevated suite (`test/install`, `task e2e:elevated`) in the
development test VM.
