# Phase B3: test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| SRV-07 | after a reboot whose VM the watch reads as stopping and then running again, the server status is `running` one watch interval later; after a shutdown action has ended with the VM still running, the status stays `stopping` across watch intervals and becomes `off` once the VM is off | unit | internal/resources/servers/servers_test.go: TestServers/SRV-07/reboot_status | B3-01 |
| LB-06 | deleting a server that is a load balancer target pushes an agent document after the delete action, and that document has no upstream for the deleted server | unit | internal/resources/loadbalancers/targets_test.go: TestLoadBalancers/LB-06/server_delete_push (registered in loadbalancers_test.go) | B3-01 |
| SRV-07 | a shutdown that a booting guest refuses (32768) is asked again until accepted and succeeds; a guest that refuses until the deadline gives InvalidState | unit | internal/platform/hyperv/hyperv_test.go: TestGuestRequest/SRV-07/shutdown | B3-02 |
| UI-02 | Created times show the API's UTC timestamp in local time as "YYYY-MM-DD hh:mm" (cases: a timestamp with `Z`, one with `+00:00`) | unit | ui/frontend/tests/unit/format.test.ts: "UI-02 created times" | B3-03 |
| UI-02 | text breaking the label rule left in the labels box: submitting the create placement group form, and "Save labels" on the Labels tab, sends no request, the form stays and the error line under the box stays visible; the API shows no group created and the labels unchanged | e2e | ui/frontend/tests/e2e/placementgroups.spec.ts: "UI-02 invalid labels" | B3-04 |

Notes for the writer:

- SRV-07/reboot_status uses `platformtest.Fake` (`SetState`, `HoldShutdown`), `newFakeEnv`, `runAction`, `tickWatch` from servers_test.go; the stopping reading comes after the reboot action has ended (the action locks the server while it runs).
- LB-06/server_delete_push uses the push recorder of `lbEnv` in loadbalancers_test.go (`e.push`, `e.gate`).
- TestGuestRequest/SRV-07/shutdown calls `shutdown` with millisecond timeouts and an ask function; the existing subtest of `untilAccepted` stays.
- "UI-02 created times" builds the expected text from the local getters of the same instant, so it holds in any time zone.
- "UI-02 invalid labels" records the page's requests to `/wails/runtime` (the Wails bridge, E9) around each submit and expects none; for the Labels tab case it uses its own placement group made through the API and deletes it at the end.
