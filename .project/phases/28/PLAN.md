# Phase 28: Faults in the tray

## Interfaces

Go, additive (28-01):

```go
package api

// Codes returns every error code of Status in a fixed order: the general
// Hetzner error table first (json_error ... timeout, as in the frozen
// description), then the operation-specific codes, each group in the
// order of the source table.
func Codes() []CodeStatus

type CodeStatus struct {
	Code   string `json:"code"`
	Status int    `json:"status"`
}
```

```go
package bridge

// ErrorCodes is api.Codes() for the fault form's error code select; no
// cloud needed.
func (s *Service) ErrorCodes() []api.CodeStatus
```

Management API (28-01): a refused fault create (`400 invalid_input` from a
`*faults.InputError`) additionally carries Hetzner's field details; code
and message stay as they are:

```json
{"error": {"code": "invalid_input", "message": "params.percent: 0 is not between 1 and 100",
  "details": {"fields": [{"name": "params.percent", "messages": ["0 is not between 1 and 100"]}]}}}
```

Go, additive (28-02):

```go
package bridge

type State struct {
	// ... existing fields ...
	Faults int `json:"faults"` // active faults of all projects from GET /status;
	// 0 unless running; a lost connection keeps the last count
}

type TrayModel struct {
	// ... existing fields ...
	Faults int `json:"faults"` // > 0: the menu shows "End all faults"
}

// EndAllFaults sends DELETE /mgmt/v1/faults; a status other than 204 is an
// error carrying the management API's message.
func (c *Cloud) EndAllFaults() error
```

`cmd/hlab-tray`: label constant `labelEndFaults = "End all faults"`; the
item sits after the cloud item, `SetHidden(m.Faults == 0)` and
`Menu.Update()` in `apply` next to the cloud item's `SetLabel`
(RESEARCH.md "Wails tray menu"); `click("End all faults")` calls
`cloud.EndAllFaults()` while the item is shown and returns `errDisabled`
otherwise; `TrayTest.Click` accepts it.

TypeScript (28-03, `ui/frontend/src/lib/routes.ts`):

```ts
export type ProjectView = /* existing */ | "faults";
export type Route = /* existing */ | { area: "faults" };
// "#/projects/<p>/faults" (list) and ".../faults/new"; no detail page
// (".../faults/<id>" is undefined); "#/faults" is { area: "faults" }.
```

`views.json` gets `faults` and `active-faults`; `consoleNav.ts` gets the
entry `{ id: "faults", title: "Faults", tabs: [{ view: "faults", title: "Faults" }] }`
after Security in the group without heading.

TypeScript (28-04, `ui/frontend/src/lib/faults.ts`, pure, no bridge):

```ts
export type FaultKind = "server_crash" | "server_freeze" | "network" | "latency" | "api_error"
  | "rate_limit" | "volume_unavailable" | "disk_slow" | "storage_box_unavailable";
export type FaultTarget =
  | { type: "server" | "volume" | "storage_box"; id: number }
  | { type: "locations"; from: string; to: string };
export type Fault = {
  id: number; project: string; kind: FaultKind; target: FaultTarget | null;
  params: Record<string, string | number> | null; seed: number | null;
  created: string; ends: string | null;
};

/** One entry per kind in form order: readable label, one-line explanation,
 *  the target types it takes and whether it can take a seed. */
export const faultKinds: { kind: FaultKind; label: string; explanation: string;
  targets: ("server" | "volume" | "storage_box" | "locations")[]; seed: boolean }[];
export function kindLabel(kind: FaultKind): string;

/** "20 % server_error, writes, servers", "public, 100 % loss", "50 ms ± 10 ms",
 *  "100 per hour", "200 IOPS"; "–" without parameters. */
export function paramsSummary(f: Pick<Fault, "kind" | "params">): string;

/** "Project" (no target), "fsn1 ↔ hel1", the resource's name from names
 *  ("server:42" -> "web-1"), or "Server 42" / "Volume 7" / "Storage Box 3"
 *  when the name is unknown. */
export function targetText(t: FaultTarget | null, names: Map<string, string>): string;

/** "until ended", "4 min left" (whole minutes rounded up), "45 s left"
 *  under one minute; never negative ("0 s left"). */
export function remaining(ends: string | null, now: Date): string;

/** "1 fault active", "N faults active". */
export function faultsPill(n: number): string;

/** The create form's values. Empty strings are unset number fields. */
export type FaultForm = {
  kind: FaultKind; targetMode: "server" | "locations"; target: string; // "server:42"
  from: string; to: string; code: string; percent: string; only: "read" | "write" | "";
  resource: string; perHour: string; traffic: "public" | "private" | "all";
  lossPercent: string; delayMs: string; jitterMs: string; iops: string;
  seed: string; duration: "" | "1m" | "5m" | "15m" | "60m" | "custom"; customDuration: string;
};
export function defaultForm(kind: FaultKind): FaultForm;
/** Whether the seed field shows: api_error, network with loss below 100. */
export function showsSeed(f: FaultForm): boolean;
/** The POST body: kind, target, only the kind's params as numbers or
 *  strings (absent "only"/"resource" left out), seed only where showsSeed
 *  and set, duration left out for "until ended", the custom text as typed. */
export function requestBody(f: FaultForm): { kind: FaultKind; target: FaultTarget | null;
  params?: Record<string, string | number>; seed?: number; duration?: string };
/** The form field a management API field name belongs to
 *  ("params.percent" -> "percent", "target.from" -> "from", "duration" -> "duration"). */
export function formField(apiName: string): string;
/** Top-level resource paths of both APIs for the resource type select. */
export const faultResources: string[];
```

`lib/FaultsTable.svelte` (28-05) with props
`{ faults: Fault[]; names: Map<string, string>; withProject?: { name: string; display: string }[]; onEnd: (f: Fault) => void }`
is shared by the project's Faults and Active faults.

## Plans

- [x] 28-01 Error codes and field errors for the fault form
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-16 | Tests: internal/mgmt/faults_test.go
  - Reads: ARCHITECTURE.md "Management API (named pipe)" (fault routes and
    errors), "hlab-tray (tray program)"; DECISIONS.md "Fault interfaces";
    CONTEXT.md "Technical choices"
  - Size: ~110 lines in 4 files
  - [x] `api.Codes()` and `CodeStatus` in `internal/api/errors.go` (keep
    `statuses` as the one source; an ordered list of the general codes
    decides the order); `Service.ErrorCodes()` in
    `internal/bridge/service.go`
  - [x] `failFault` in `internal/mgmt/faults.go` adds `details.fields`
    with the `InputError`'s field and message (a `writeFieldError` helper
    in `internal/mgmt/mgmt.go`); other errors unchanged

- [x] 28-02 Tray menu item "End all faults"
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-18 | Tests: internal/bridge/cloud_test.go, internal/bridge/service_test.go
  - Reads: ARCHITECTURE.md "hlab-tray (tray program)"; UI-SPEC.md "Tray
    menu"; RESEARCH.md "Wails tray menu"; DECISIONS.md "Phase 10 findings:
    tray cloud item and pending state", "Tray quit and tray state in e2e"
  - Size: ~120 lines in 3 files
  - [x] `internal/bridge/cloud.go`: `statusAnswer.Faults`, `State.Faults`
    (0 unless running, kept on a lost connection like the problems),
    `TrayModel.Faults` from `Tray`, `Cloud.EndAllFaults()`
  - [x] `cmd/hlab-tray/tray.go` and `cmd/hlab-tray/e2e.go`: the hidden item
    after the cloud item, shown while `Faults > 0`, its click through
    `click` (failure in the existing message box), `TrayTest.Click`
    doc comment

- [x] 28-03 Faults routes, sidebar entry and view list
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-16, UI-17, DOC-05 | Tests: ui/frontend/tests/unit/routes.test.ts
  - Reads: UI-SPEC.md "Views" (Shell, Project console, Faults, Active
    faults); ARCHITECTURE.md "hlab-tray (tray program)" (routes);
    DECISIONS.md "Q15 DOC-05 views from views.json"; SPEC.md
    "Documentation" D5
  - Size: ~170 lines in 8 files
  - [x] `lib/routes.ts` (view `faults` list and new, area `faults` for
    `#/faults`), `lib/consoleNav.ts` (entry after Security),
    `views/ProjectConsole.svelte` (icon `TriangleAlert`)
  - [x] `views.json` and `state.ts` `viewInfo` (`faults` in area project,
    `active-faults` in area faults), first help texts
    `help/faults.md` and `help/active-faults.md` (each with a heading;
    the documentation plan completes them)

- [x] 28-04 Fault display and request logic
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-16, UI-17 | Tests: ui/frontend/tests/unit/faults.test.ts
  - Reads: UI-SPEC.md "Faults", "Active faults"; ARCHITECTURE.md
    "Management API" (fault object and kind table); CONTEXT.md
    "Assumptions"
  - Size: ~260 lines in 1 file
  - [x] `ui/frontend/src/lib/faults.ts` as in Interfaces: kinds with
    labels and explanations, `paramsSummary`, `targetText`, `remaining`,
    `faultsPill`, `defaultForm`, `showsSeed`, `requestBody`, `formField`,
    `faultResources`

- [x] 28-05 Faults table and the project's Faults list
  - Wave: 2 | Depends on: 28-03, 28-04 | Review: alone
  - Requirements: UI-16 | Tests: ui/frontend/tests/e2e/faults.spec.ts
  - Reads: UI-SPEC.md "Building blocks" (Resource table, Confirm dialog,
    Action toast), "Faults", "Behaviour"; the existing `lib/Table.svelte`
    and one `views/<view>/Section.svelte` as pattern
  - Size: ~300 lines in 3 files
  - [x] `lib/FaultsTable.svelte`: kind, target (link to the server,
    volume or storage box detail; "Project"; "fsn1 ↔ hel1"), parameters,
    seed, remaining time; optional Project column first (display name,
    link to the project's Faults); row menu "End"
  - [x] `views/faults/Section.svelte` list page: loads
    `GET /projects/{name}/faults` through `mgmt()` and the project's
    servers, volumes and storage boxes for the names, refreshes every 5 s
    and after every action; toolbar "Create fault" and "End all" (confirm
    dialog, `DELETE /projects/{name}/faults`); "End" per row; empty state
    "No faults are active in this project." with "Create fault"; errors as
    toast

- [ ] 28-06 Create fault form
  - Wave: 3 | Depends on: 28-01, 28-05 | Review: alone
  - Requirements: UI-16 | Tests: ui/frontend/tests/e2e/faults.spec.ts
  - Reads: UI-SPEC.md "Building blocks" (Form), "Faults"; CONTEXT.md
    "Assumptions"; ARCHITECTURE.md "Management API" (kind table)
  - Size: ~350 lines in 3 files
  - [ ] `views/faults/FaultForm.svelte`: kind select with its explanation,
    only the kind's fields (target selects of fitting servers, attached
    volumes, storage boxes, servers and volumes grouped for `disk_slow`;
    latency segmented control Server | Between locations with two
    location selects from `Service.Locations`; error code select from
    `Service.ErrorCodes` as "code (status)"; percent; Only Reads | Writes |
    All; resource type; requests per hour; traffic Public | Private | All;
    loss percent; delay and jitter; IOPS), seed field with hint where
    `showsSeed`, duration select with custom text; "No ... in this
    project" and Create disabled when nothing fits; body from
    `requestBody`; field errors through `formErrors` with `formField`
  - [ ] `views/faults/Section.svelte` shows the form for page `new` and
    returns to the list after a create

- [ ] 28-07 Active faults page and top bar pill
  - Wave: 3 | Depends on: 28-02, 28-05 | Review: alone
  - Requirements: UI-17 | Tests: ui/frontend/tests/e2e/faults.spec.ts
  - Reads: UI-SPEC.md "Views" (Shell top bar, Active faults), "Colours";
    ARCHITECTURE.md "hlab-tray (tray program)"
  - Size: ~220 lines in 4 files
  - [ ] `state.ts` `CloudState.faults`; `lib/TopBar.svelte` pill
    `faultsPill(n)` in the warning colour left of the cloud status pill
    while running and `faults > 0`, a link to `#/faults`
  - [ ] `views/ActiveFaults.svelte` (breadcrumb "Active faults", help
    button for `active-faults`, `GET /faults` and `GET /projects` for
    display names and per project the target names, 5 s refresh,
    `FaultsTable` with the Project column, "End" per row, "End all" with
    confirm dialog through `DELETE /faults`, empty text "No faults are
    active.", cloud stopped text) and its route in `App.svelte`

- [ ] 28-08 Documentation
  - Wave: 4 | Depends on: 28-01, 28-02, 28-03, 28-04, 28-05, 28-06, 28-07 | Review: alone
  - Requirements: DOC-04, DOC-05, DOC-07 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC.md "Documentation"; UI-SPEC.md "Faults", "Active faults",
    "Tray menu"; README.md "The tray program"; docs/guide.md "Fault
    injection"
  - Size: ~150 lines in 4 files
  - [ ] `ui/frontend/src/help/faults.md` and `help/active-faults.md`
    complete (views and buttons as built); README.md "The tray program"
    names the Faults entry, the top bar pill and "End all faults" in the
    tray menu; docs/guide.md "Fault injection" describes creating, seeing
    and ending faults in the tray next to `hlab fault`

## Order of the e2e rows

UI-16 runs in the sequential FAULT block of `TestE2E` after
FAULT-08/restart and before `releaseFaultFixture`, on the fault fixture's
resources (server A with its mounted volume, servers B and C, the storage
box); it starts the shared tray. It creates the kinds in an order that
keeps each creatable (`server_freeze` created and ended before
`server_crash`, since a freeze needs a running VM) and ends all faults at
its end. UI-17 runs in the UI block after UI-14; its harness creates two
small projects and their `api_error` and `rate_limit` faults through
`hlab fault create` and deletes the projects afterwards.
