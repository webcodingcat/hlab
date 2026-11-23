# Phase 22: SMB from the Windows host

## Interfaces

None. The phase adds no product code. The e2e test of SBOX-22 builds on the
existing helpers in `test/e2e` (`newTestBox`, `createSubaccount`,
`passwordLogin`, `hostSFTP`, `hostSum`, `seedHome`, `eventually`,
`storageBox.Server`). Its own `net use` helper stays private to its file.

## Plans

- [x] 22-01 SMB from the Windows host, end to end
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-22 | Tests: test/e2e/storagebox_hostsmb_test.go (SBOX-22)
  - Reads: REQUIREMENTS SBOX-22, SBOX-14, SBOX-04; CONTEXT.md; RESEARCH.md "SMB from the Windows host: system error 67" and "Windows `net use` semantics"; EXPERIMENTS.md E16; test/e2e/storagebox_test.go, storagebox_access_test.go (`createSubaccount`), storagebox_protocols_test.go (helpers `hostSum`, `seedHome`, `eventually`), e2e_test.go (registration)
  - Size: ~10 lines in 1 file (the test file itself, ~150 lines, comes from the test writer)
  - [x] Register the row in test/e2e/e2e_test.go as `t.Run("SBOX-22", parallel(testSBOX22))` next to the SBOX-14 rows, if the test writer has not done so. Then run `task e2e -- -run 'TestE2E/SBOX-22'` and, as the regression check of success criterion 2, `task e2e -- -run 'TestE2E/SBOX-14$|TestE2E/SBOX-12/smb$|TestE2E/SBOX-04/smb$'` (Go matches `-run` per name segment).
  - [x] Product code (smb.conf, internal/hostnet) stays unchanged (CONTEXT.md). If SBOX-22 fails with system error 67 although the logged UNC path reached net.exe intact (two leading backslashes), the cause inferred in RESEARCH.md does not hold: the plan stops `[!]` with the question for "Open decisions" in STATUS.md ("SMB from the Windows host still fails with error 67 with an intact path: 1. investigate the Windows client and Samba further in a new phase, 2. state in docs/deviations.md that the host cannot map shares; recommendation 1"). Do not change the Samba config or host settings to make it pass.

- [ ] 22-02 Documentation
  - Wave: 2 | Depends on: 22-01 | Review: alone
  - Requirements: SBOX-22 (documented), DOC-01 to DOC-06 (kept green), DOC-07 (manual) | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC; CONTEXT.md; RESEARCH.md "Confirmed fix" and "Windows `net use` semantics"; docs/guide.md "SMB"; docs/deviations.md "Storage boxes"; docs/development/build-and-test.md
  - Size: ~40 lines in 3 files
  - [ ] docs/guide.md, section SMB: a Windows example that maps `\\u<n>.storagebox.hetzner.internal\backup` and `\\u<n>-sub<m>.storagebox.hetzner.internal\u<n>-sub<m>` with `net use ... /user:<account> /persistent:no` from PowerShell or cmd, writes a file, and removes the mapping with `net use <path> /delete`. Recommend one server name per account (the subaccount under its own name), without stating error 1219 as fact. The box must be reachable externally. Run the example against the dev cloud as the doc-writer runs examples, without printing the password.
  - [ ] docs/deviations.md, storage boxes: the Windows host maps the shares; nothing says it cannot. The NTLM risk note stays.
  - [ ] docs/development/build-and-test.md: one sentence saying that Windows commands with UNC paths (`net use \\server\share`) run from PowerShell or cmd, not from Git Bash, which collapses the leading `\\`.
  - (Main session at the close, not this plan: the error-67 sentence in .project/decisions/storage-box.md.)
