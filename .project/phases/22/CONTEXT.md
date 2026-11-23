# Phase 22 context

- The cause of error 67 was Git Bash collapsing the leading `\` of the UNC path; smb.conf (`internal/storageagent/smb.go`) and `internal/hostnet` stay unchanged.
- The phase consists of the e2e test SBOX-22, `docs/guide.md`, the correction in `docs/deviations.md`, one sentence in `docs/development/build-and-test.md` and the error-67 sentence in `.project/decisions/storage-box.md` (main session, at the close).
- `docs/development/build-and-test.md`: Windows commands with UNC paths run from PowerShell or cmd, not from Git Bash.
- The cause is inferred, not observed: if SBOX-22 fails with error 67 although the path reaches net.exe intact, the plan is `[!]` and the question goes to "Open decisions" in STATUS.md.
- `testSBOX22` calls `net.exe` directly via `exec.Command`, no shell; exit code 0 required.
- Before mapping, the test runs `net use \<server> /delete /y` per server name it uses (a leftover from an aborted run must not cause error 1219); the first mapping is retried with a deadline; `t.Cleanup` deletes the mapping also on failure.
- The box share is mapped as `\u<n>.storagebox.hetzner.internal\backup`, the subaccount share as `\u<n>-sub<m>.storagebox.hetzner.internal\u<n>-sub<m>` (Hetzner's form); the guide recommends one server name per account and does not state error 1219 as fact.
- The test writes over SMB and reads back over SFTP (`hostSum`), and reads over SMB a file written over SFTP; it creates its own box (reachable_externally, samba_enabled) and a subaccount with Samba.
- The generated test password appears in net.exe's argv, is never logged, and the test output does not print the command line.
- SMB from WSL2 and servers is checked by the existing e2e tests SBOX-14, SBOX-12, SBOX-04 unchanged; no new rows for them.
