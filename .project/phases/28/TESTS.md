# Phase 28 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| UI-16 | a refused fault create answers `400 invalid_input` with its message and `details.fields` naming the refused field (table: `params.percent` out of range, `duration` "2d", `target.from` missing, `kind` unknown); `404 not_found` and `409 conflict` carry no `details` | unit | internal/mgmt/faults_test.go: TestMgmt/UI-16 | 28-01 |
| UI-16 | `#/projects/p/faults` and `#/projects/p/faults/new` parse to view `faults` (list, new) and `href` gives them back; `#/projects/p/faults/3` is undefined | unit | ui/frontend/tests/unit/routes.test.ts: "UI-16 fault routes" | 28-03 |
| UI-17 | `#/faults` parses to area `faults` and `href` gives it back | unit | ui/frontend/tests/unit/routes.test.ts: "UI-17 active faults route" | 28-03 |
| UI-16 | `paramsSummary` per kind (table: `api_error` with code, percent, only and resource -> "20 % server_error, writes, servers", without only and resource; `rate_limit`; `network` cut and loss; `latency` with and without jitter; `disk_slow`; `server_crash` -> "–"); `targetText` (table: null -> "Project", locations -> "fsn1 ↔ hel1", known server, volume and storage box name, unknown server -> "Server 42") | unit | ui/frontend/tests/unit/faults.test.ts: "UI-16 fault table texts" | 28-04 |
| UI-16 | `remaining` (table: null -> "until ended", 4 min 1 s -> "5 min left", exactly 4 min -> "4 min left", 45 s -> "45 s left", past -> "0 s left") | unit | ui/frontend/tests/unit/faults.test.ts: "UI-16 remaining time" | 28-04 |
| UI-16 | `requestBody` from `defaultForm` plus the fields of each kind (table, one case per kind): only that kind's params with numbers as numbers, target as the fault object's shape (server, volume, storage box, locations), "only"/"resource" left out for All; `seed` sent for `api_error` and `network` below 100 % loss when set, never for `network` at 100 % or other kinds (with `showsSeed` agreeing); duration left out for "Until ended", "5m" for 5 minutes, the custom text as typed; `formField` maps "params.percent", "target.from" and "duration" to their form fields | unit | ui/frontend/tests/unit/faults.test.ts: "UI-16 create request" | 28-04 |
| UI-16 | in a project with servers, an attached volume and a storage box, "Faults" in the sidebar shows the empty state; "Create fault" creates one fault of every kind with its parameters through the form (one `api_error` without seed, one `network` loss fault with seed 42, one fault with 5 minutes); the table shows each with its kind, target, parameter line, seed (chosen and shown when empty, 42 when given, empty for kinds without seed) and remaining time ("5 min left" or less, "until ended"); a percent of 0 shows its error under the field; "End" in a row menu removes that fault (and `hlab fault list` no longer has it); "End all" with its confirm dialog leaves the empty state and `hlab fault list` empty | e2e | ui/frontend/tests/e2e/faults.spec.ts: "UI-16"; harness test/e2e/ui_test.go: TestE2E/UI-16 | 28-06 |
| UI-17 | with faults in two projects made by `hlab fault create`, the top bar shows "N faults active" with their number; a click opens "Active faults" with each fault and its project as a link to that project's Faults; "End" in a row removes one and the pill's number follows; "End all" with its confirm dialog ends all, the page shows "No faults are active." and the pill disappears | e2e | ui/frontend/tests/e2e/faults.spec.ts: "UI-17"; harness test/e2e/ui_test.go: TestE2E/UI-17 | 28-07 |
| UI-18 | the polled state carries the fault count of `GET /status` into `State.Faults` and `Tray(...).Faults` (table: running with 2 -> 2, running with 0 -> 0, stopped -> 0, connection lost after a count of 2 -> 2) | unit | internal/bridge/cloud_test.go: TestBridge/UI-18/model | 28-02 |
| UI-18 | `EndAllFaults` sends `DELETE /mgmt/v1/faults` to the management API and returns nil on 204; a `cloud_not_running` answer returns an error with its message | unit | internal/bridge/cloud_test.go: TestBridge/UI-18/end_all | 28-02 |

Showing and hiding the tray item from `TrayModel.Faults` and calling
`EndAllFaults` from its click are glue in `cmd/hlab-tray` (Wails menu), as
are `api.Codes` and `Service.ErrorCodes` feeding the select. DOC-05 and
UI-07 pick up the new views `faults` and `active-faults` through
`views.json` in their existing tests (test/docs/docs_test.go,
ui/frontend/tests/unit/help.test.ts); DOC-04 keeps its existing test;
DOC-07 is manual. `TestBridge` registers the UI-18 subtests in
internal/bridge/service_test.go next to the UI-01 ones.

## Writers

1. Go unit: internal/mgmt/faults_test.go (UI-16 subtest of `TestMgmt`),
   internal/bridge/cloud_test.go and the registration in
   internal/bridge/service_test.go (UI-18), ~170 lines.
2. Frontend unit: ui/frontend/tests/unit/routes.test.ts (two describes),
   ui/frontend/tests/unit/faults.test.ts (new), ~280 lines.
3. e2e: ui/frontend/tests/e2e/faults.spec.ts (new, UI-16 and UI-17, using
   the helpers of tests/e2e/console.ts and fixtures.ts), test/e2e/ui_test.go
   (`testUI16` passing the fault fixture's project, token, resource IDs and
   names; `testUI17` creating two projects with their faults and deleting
   them), test/e2e/e2e_test.go (order as in PLAN.md "Order of the e2e
   rows"), ~400 lines.
