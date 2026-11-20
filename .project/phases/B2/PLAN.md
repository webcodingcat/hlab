# Phase B2: Backlog: VLAN release at project deletion, docs and e2e test fixes

Taken in by the user with /wcc:change on 2026-11-20.

## Items

- [ ] bug: test/e2e harness: `stopRunningCloud` (test/e2e/main_test.go) stops the dev cloud only in state `running`; a developer whose dev service is up in `starting` or `refused` still gets the `task build` error because the service holds `bin\hlabd.exe` (source: B1 verifier)
- [ ] security: networks (project deletion): `mgmt.purge` deletes kinds in sorted order, so `networks.Delete` frees the network's VLAN before the project's servers and VMs are deleted; a network another project creates in that window can get the VLAN, and its members share layer 2 with the old VMs without deny ACLs until those VMs are gone (source: 05 verifier)
- [ ] later: test/docs DOC-03: the test checks only the hand-kept list in `internal/api/deviations.go` (already missing the phase 05 and 06 endpoints), not the registered routes, so phases 07 and 08 can add endpoints without their deviations in docs/deviations.md and the test stays green (source: 06 verifier)
- [ ] bug: test/e2e: RDNS-02 posts `change_dns_ptr` on the shared server while parallel rows run actions on it, so a full `task e2e` can fail with `423 locked` (seen at the phase 10 close; passed 3 of 3 alone), and a developer gets a red run without a product fault (source: 10 close)

Note: the DOC-03 item applies to `docs/development/api-coverage.md`, which the phase 10 findings create from `docs/deviations.md`; the test should compare the registered routes, not only the hand-kept list in `internal/api/deviations.go`.
