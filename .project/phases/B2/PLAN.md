# Phase B2: Backlog: VLAN release at project deletion, LB health check, docs and e2e test fixes

Taken in by the user with /wcc:change on 2026-11-20.

## Items

- [ ] bug: test/e2e harness: `stopRunningCloud` (test/e2e/main_test.go) stops the dev cloud only in state `running`; a developer whose dev service is up in `starting` or `refused` still gets the `task build` error because the service holds `bin\hlabd.exe` (source: B1 verifier)
- [x] security: networks (project deletion): `mgmt.purge` deletes kinds in sorted order, so `networks.Delete` frees the network's VLAN before the project's servers and VMs are deleted; a network another project creates in that window can get the VLAN, and its members share layer 2 with the old VMs without deny ACLs until those VMs are gone (source: 05 verifier)
- [ ] later: test/docs DOC-03: the test checks only the hand-kept list in `internal/api/deviations.go` (already missing the phase 05 and 06 endpoints), not the registered routes, so phases 07 and 08 can add endpoints without their deviations in docs/deviations.md and the test stays green (source: 06 verifier)
- [ ] bug: test/e2e: RDNS-02 posts `change_dns_ptr` on the shared server while parallel rows run actions on it, so a full `task e2e` can fail with `423 locked` (seen at the phase 10 close; passed 3 of 3 alone), and a developer gets a red run without a product fault (source: 10 close)
- [x] bug: loadbalancers/services: `applyHealthCheck` (internal/resources/loadbalancers/services.go) stores health-check protocol `https` as `http` with `tls: true`, so Terraform and Ansible users who set `https` read back `http` and every plan or run shows a change it never applies; the OpenAPI response enum knows only tcp and http, so returning `https` needs the validator patch too (source: 08 08-15 review)
  - Dropped without a code change: the real Hetzner API rejects `health_check.protocol: https` ("Not a valid choice"; probed with hcloud 1.70.2 against a throwaway lb11 on 2026-11-20), and its OpenAPI description knows only tcp and http in requests and responses. `http` with `tls: true` stays; no client can rely on `https` against the real API (CONTEXT.md).

Note: the DOC-03 item applies to `docs/development/api-coverage.md`, which the phase 10 findings create from `docs/deviations.md`; the test should compare the registered routes, not only the hand-kept list in `internal/api/deviations.go`.

## Interfaces

```go
// internal/cloud/routes.go (package cloud, //go:build windows)

// APIRoutes returns the http.ServeMux pattern of every endpoint the Hetzner
// API of hlabd serves, relative to /v1 (for example
// "POST /servers/{id}/actions/poweron"), sorted and without duplicates.
// It builds the same module list as cloud start (Cloud.modules) without
// opening the store, the platform or the agent, and records what each
// module's Routes registers.
func APIRoutes() []string
```

```go
// test/e2e/network_test.go (package e2e, build tag e2e): extended, not
// renamed. postAction keeps its signature and calls postActionInto with
// out == nil.

// postActionInto posts an action, trying again while the API answers
// locked or conflict, decodes the 201 body into out as well (when out is
// not nil) and waits for the action's success.
func (e *env) postActionInto(path string, body, out any, timeout time.Duration) error
```

## Plans

- [x] B2-01 Project deletion deletes networks last
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: PROJ-04 | Tests: internal/mgmt/mgmt_test.go
  - Reads: REQUIREMENTS PROJ-04, NET-02, HOST-01; ARCHITECTURE "Data schema" and "resource (one module per resource type)"; DECISIONS "Port isolation by private VLAN"; internal/mgmt/mgmt.go `purge`; internal/resources/networks/networks.go `remove`, `Delete`, `allocVLAN`
  - Size: ~80 lines in 2 files
  - Adjusts: -
  - [x] internal/mgmt/mgmt.go: `purge` collects the kinds in sorted order but moves the kind `networks` (string constant in mgmt, no import of the networks package) to the end, so the VLAN kv key is released only after every server (VM) and load balancer of the project is deleted; doc comment says why (a released VLAN can go to another project's network at once)
  - [x] internal/mgmt/mgmt_test.go: `TestMgmt/PROJ-04/networks_last`: a project with a server and a network row, fake deleters for `servers`, `load_balancers` and `networks` recording into one shared call log; after the deletion `networks` was called last

- [ ] B2-02 DOC-03 checks the registered routes
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: DOC-03 | Tests: test/docs/docs_test.go
  - Reads: REQUIREMENTS DOC-03; SPEC "Documentation" (D4, D6); DECISIONS "Documentation folders"; internal/cloud/cloud.go `modules` (line 350 ff.); internal/resource/resource.go `Registrar`, `Module`; internal/resources/actions/actions.go `RoutesFor`; internal/resources/zones/zones.go `Routes` (the `{sub}` wildcard); internal/api/deviations.go; test/docs/docs_test.go DOC-03; docs/development/api-coverage.md "Endpoints" and the sections for networks, primary IPs, floating IPs, firewalls, load balancers
  - Size: ~300 lines in 4 files
  - Adjusts: test/docs/docs_test.go DOC-03 (extended, its existing checks stay)
  - [ ] internal/cloud/routes.go: `APIRoutes()` builds the modules through the same code path as cloud start (`(&Cloud{}).modules()` or a factored-out builder both use; whatever the constructors need is given as zero values, no store, platform or agent is opened) and collects the patterns with a recording `resource.Registrar`; nothing in cloud start changes in behaviour
  - [ ] test/docs/docs_test.go: `TestDocs/DOC-03` also requires every pattern of `cloud.APIRoutes()` to appear literally (in backticks) in docs/development/api-coverage.md; the one internal wildcard `GET /zones/{id}/{sub}` is matched through an explicit alias map in the test to the routes it serves (`GET /zones/actions/{id}`), and a wildcard pattern without an alias fails the test; the checks of `api.Deviations()` stay
  - [ ] internal/api/deviations.go and docs/development/api-coverage.md: complete `api.Deviations()` with the deviations of phases 05 to 08 that api-coverage.md already describes (networks, primary IPs, floating IPs, firewalls, load balancers, certificates), one entry per described deviation, with its route; write out every abbreviated route of the Endpoints table (`.../update_service` and the other LB actions) in full, so `task test -- ./test/docs/...` is green

- [ ] B2-03 e2e harness: stop before the build in every holding state, retry actions on the shared server
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: RDNS-02, SRV-10 (unchanged checks) | Tests: test/e2e/main_test.go, test/e2e/dns_test.go, test/e2e/server_test.go, test/e2e/network_test.go
  - Reads: SPEC "Machine prerequisites" (dev service); ARCHITECTURE "Cloud start and stop"; internal/cli/cli.go `cloudStatus`; internal/sysint/windows/service.go `Execute`, `pending`; internal/cloud/cloud.go `Start`; cmd/hlabd/main.go `service`; test/e2e/main_test.go `setup`, `stopRunningCloud`, `cloudState`, `waitCloudState`; test/e2e/network_test.go `postAction`, `runLockedAction`; test/e2e/dns_test.go `testRDNS02`; test/e2e/server_test.go `testSRV10`
  - Size: ~120 lines in 4 files
  - Adjusts: -
  - [ ] test/e2e/main_test.go `stopRunningCloud` by the state `hlab cloud status` reports: `starting` waits (deadline 15 minutes, same as the cloud start wait) until the state is no longer `starting`, then acts on the new state; `running` runs `hlab cloud stop` and waits for `stopped`; `stopping` waits for `stopped`; `stopped` and `refused` need no stop (see the finding below). `wasRunning` is true when the cloud was `running` or came up from `starting`, so the harness leaves it running as found. `waitCloudState("stopped")` must not fail on a stale `refused` state file while it waits after a stop
  - [ ] test/e2e/network_test.go: `postActionInto` (see Interfaces); `postAction` calls it with `out == nil`
  - [ ] test/e2e/dns_test.go `testRDNS02`: setting the PTR uses `runLockedAction`, the reset in `t.Cleanup` uses `postAction` (retry on `locked` like a real client); test/e2e/server_test.go `testSRV10`: `reset_password` uses `postActionInto` to get `root_password`, because SRV-10 is the parallel row that acts on the shared server and fails the same way while RDNS-02 holds it. What both rows check stays the same
  - Finding (checked in the code for CONTEXT.md): in `refused` the service process does not keep running. `Cloud.Start` writes `refused` to the state file, tears down and returns the refusal; `handler.Execute` (internal/sysint/windows/service.go) then returns `(true, 1)` before it reports `Running`, `svc.Run` ends and `service()` in cmd/hlabd/main.go returns 1, so the process exits. `hlab cloud status` reports `refused` only when the SCM state is `stopped` (internal/cli/cli.go `cloudStatus`), so `bin\hlabd.exe` is free and `hlab cloud stop` would fail with "service not active". While the refusal is being torn down the SCM state is `START_PENDING`, which the harness sees as `starting` and waits through. So `refused` is handled like `stopped`, not like `running`
  - Measurement (noted under "Close"): before, at the phase 10 close, 2 of 2 full runs failed RDNS-02 with `423 locked`. After: `task e2e -- -run '^TestE2E$/^(RDNS-02|SRV-10)$' -count=3 -v` (RDNS-02 together with SRV-10, the parallel row that acts on the shared server; the other shared-server rows only read or use SSH), all 6 row runs green. The `stopRunningCloud` fix is shown by one `task e2e -- -run '^TestE2E$/^SRV-01$'` started with the dev service in `starting` and one started after a `refused` start; both builds succeed and the cloud is left as found

- [ ] B2-04 Documentation
  - Wave: 2 | Depends on: B2-01, B2-02, B2-03 | Review: alone
  - Requirements: DOC-03, DOC-04, DOC-06 | Tests: test/docs/docs_test.go (existing rows stay green)
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC-01 to DOC-07; docs/development/api-coverage.md (introduction), docs/development/adding-a-resource.md "8. Document deviations", docs/development/build-and-test.md (harness paragraph), docs/guide.md (project deletion)
  - Size: ~40 lines in 3 files
  - Adjusts: -
  - [ ] docs/development/api-coverage.md introduction and docs/development/adding-a-resource.md section 8: DOC-03 now also checks that every route hlabd registers (`cloud.APIRoutes()`) is listed on the coverage page, so a new endpoint goes into the Endpoints table
  - [ ] docs/development/build-and-test.md: the harness stops the development cloud before the build also while it is starting (it waits until it runs) or stopping, and leaves it as found
