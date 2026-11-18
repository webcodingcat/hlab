# BACKLOG

A parking lot: work worth a plan that no phase asked for. Nothing here
is scheduled by itself; `/wcc:change` takes chosen items into a backlog
phase. Every item states what goes wrong for whom and has a severity:
`security`, `data`, `bug`, `later` (a named later phase needs it) or
`minor` (a cosmetic finding the user wants to keep). Cleanups, rare cases
and taste do not go here; a finding that needs a change to REQUIREMENTS.md,
SPEC.md or ARCHITECTURE.md goes to "Open decisions" in STATUS.md. Checked items move to
BACKLOG-DONE.md when a backlog phase closes.

- [ ] later: platform/hyperv: Reboot uses Msvm_ShutdownComponent.InitiateReboot, which answered "not supported" on a VM without guest OS; if it also fails with Linux guests, every server reboot fails (phase 03, SRV-07) and Reboot needs guest shutdown plus start (source: 01 01-09 implementer)
- [ ] bug: test/e2e harness: `task e2e` fails in `task build` when the dev cloud is already running, because the service holds `bin\hlabd.exe`; a developer who left the cloud running gets a build error instead of a test run (source: 01 verifier)
- [ ] minor: internal/resources/conventions_test.go: TestResources/API-05 and API-07 repeat over every endpoint the paging and label cases that internal/api/list_test.go and internal/labels/labels_test.go already cover in full; about 200 to 300 lines more to read and keep up, take in only when the tests get in the way, e.g. a rework of api.List (the per-endpoint sort rows of API-06 stay: each resource has its own sort table) (source: user after phase 02)
