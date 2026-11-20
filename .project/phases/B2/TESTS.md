# Phase B2: test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| PROJ-04 | deleting a project deletes its networks only after all its servers and load balancers, so the network's VLAN is not free while the project's VMs still exist | unit | internal/mgmt/mgmt_test.go: TestMgmt/PROJ-04/networks_last | B2-01 |
| DOC-03 | every route hlabd registers is listed in docs/development/api-coverage.md, every entry of `api.Deviations()` (the `unsupported_error` included) names an endpoint on that page, and docs/deviations.md names no route | unit | test/docs/docs_test.go: TestDocs/DOC-03 | B2-02 |

## Tests changed without a new row

The harness fixes of B2-03 change no promise. These e2e tests keep their
name and what they check; only how they post their action changes:

| Requirement | Test before | Test after | Change |
|-------------|-------------|------------|--------|
| RDNS-02 | test/e2e: TestE2E/RDNS-02 | test/e2e: TestE2E/RDNS-02 | `change_dns_ptr` and its reset retry while the shared server is `locked` |
| SRV-10 | test/e2e: TestE2E/SRV-10 | test/e2e: TestE2E/SRV-10 | `reset_password` retries while the shared server is `locked` |

`stopRunningCloud` (test/e2e/main_test.go) is harness setup without its own
state; its proof is the measurement under B2-03 in PLAN.md.
