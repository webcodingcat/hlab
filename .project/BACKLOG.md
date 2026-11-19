# BACKLOG

A parking lot: work worth a plan that no phase asked for. Nothing here
is scheduled by itself; `/wcc:change` takes chosen items into a backlog
phase. Every item states what goes wrong for whom and has a severity:
`security`, `data`, `bug`, `later` (a named later phase needs it) or
`minor` (a cosmetic finding the user wants to keep). Cleanups, rare cases
and taste do not go here; a finding that needs a change to REQUIREMENTS.md,
SPEC.md or ARCHITECTURE.md goes to "Open decisions" in STATUS.md. Checked items move to
BACKLOG-DONE.md when a backlog phase closes.

- [ ] minor: internal/resources/conventions_test.go: TestResources/API-05 and API-07 repeat over every endpoint the paging and label cases that internal/api/list_test.go and internal/labels/labels_test.go already cover in full; about 200 to 300 lines more to read and keep up, take in only when the tests get in the way, e.g. a rework of api.List (the per-endpoint sort rows of API-06 stay: each resource has its own sort table) (source: user after phase 02)
- [ ] later: servers: `Reconcile` (internal/resources/servers/reconcile.go) takes an existing VM as it is and never checks an existing disk file, so a VM left without disk, adapter, seed or boot order by an aborted create stays broken; phase 09 (LIFE-02/03/04) restores servers through `Reconcile` and needs it to do only the difference (source: 03 03-06 review)
- [ ] bug: test/e2e harness: `stopRunningCloud` (test/e2e/main_test.go) stops the dev cloud only in state `running`; a developer whose dev service is up in `starting` or `refused` still gets the `task build` error because the service holds `bin\hlabd.exe` (source: B1 verifier)
- [ ] security: networks (project deletion): `mgmt.purge` deletes kinds in sorted order, so `networks.Delete` frees the network's VLAN before the project's servers and VMs are deleted; a network another project creates in that window can get the VLAN, and its members share layer 2 with the old VMs without deny ACLs until those VMs are gone (source: 05 verifier)
- [ ] later: test/docs DOC-03: the test checks only the hand-kept list in `internal/api/deviations.go` (already missing the phase 05 and 06 endpoints), not the registered routes, so phases 07 and 08 can add endpoints without their deviations in docs/user/deviations.md and the test stays green (source: 06 verifier)
