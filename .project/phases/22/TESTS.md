# Phase 22 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| SBOX-22 | from the Windows host without elevation, `net.exe` called directly (no shell) maps `\\<box>.storagebox.hetzner.internal\backup` as the box and `\\<sub>.storagebox.hetzner.internal\<sub>` as a subaccount with Samba on (both reachable_externally) with exit code 0. A file written to each share through the UNC path reads back over SFTP with the same SHA-256 (the subaccount's under its home), and a file written over SFTP reads back through each UNC path. Leftover connections to both server names are deleted first, the first mapping is retried until a deadline, and the mappings are deleted in cleanup. The password never appears in the test output. | e2e | test/e2e/storagebox_hostsmb_test.go: TestE2E/SBOX-22 | 22-01 |

SMB from WSL2 and servers (success criterion 2) keeps its existing rows
TestE2E/SBOX-14, TestE2E/SBOX-12/smb and TestE2E/SBOX-04/smb, unchanged.
DOC-01 to DOC-06 keep their existing tests in `test/docs/docs_test.go`.
DOC-07 and DOC-08 are manual.

## Writers

1. End to end: `test/e2e/storagebox_hostsmb_test.go` (`testSBOX22` and its private `net use` and delete helpers through `exec.Command("net", ...)`, using the shared helpers of `storagebox_test.go` and `storagebox_protocols_test.go`), and the `parallel` registration in `e2e_test.go`; about 150 lines.
