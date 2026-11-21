# Phase 15: Project display names and Hetzner-style project list

## Interfaces

Go, new or changed in this phase:

```go
// internal/projectname: the one derivation of a project name, used by mgmt
// and the tray bridge (no TypeScript copy).
package projectname

const MaxLen = 255 // runes of a display name; bytes of a derived name (ASCII)

// ValidateDisplay trims display (strings.TrimSpace) and returns it, or an
// error whose message the API returns as is: empty after trimming, more
// than 255 runes, or a control character (unicode.IsControl).
func ValidateDisplay(display string) (string, error)

// Derive returns the project name of display: TrimSpace, strings.ToLower,
// ä→ae ö→oe ü→ue ß→ss (strings.NewReplacer), norm.NFD.String, one rune loop
// dropping unicode.Mn and turning every run outside a-z0-9 into one "-",
// cut to 255, then "-" trimmed at both ends. "" when nothing is left.
// Letters without a decomposition (ø æ œ ł đ ...) become "-".
func Derive(display string) string
```

```go
// internal/store
const SchemaVersion = 2

type Project struct {
	ID          int64
	Name        string
	DisplayName string
	Created     time.Time
}

// CreateProject replaces CreateProject(tx, name); an existing name is ErrConflict.
func CreateProject(tx Tx, name, displayName string) (Project, error)

// CountResourcesByType counts the resources of project projectID by console
// tab: kind "images" split by json_extract(doc, '$.type') into "snapshots"
// and "backups", every other kind under its kind name; only counts above
// zero; never nil.
func CountResourcesByType(tx Tx, projectID int64) (map[string]int, error)
```

```go
// internal/mgmt
type Project struct {
	Name            string         `json:"name"`
	DisplayName     string         `json:"display_name"`
	Created         time.Time      `json:"created"`
	Resources       int            `json:"resources"`
	ResourcesByType map[string]int `json:"resources_by_type"` // {} when empty, never null
}
// POST /projects body: {"display_name": "..."} only.

// internal/bridge
func (s *Service) ProjectName(display string) string // projectname.Derive; works with the cloud stopped
```

TypeScript, `ui/frontend/src/lib/projects.ts` (pure: imports neither
`lib/bridge.ts` nor `@wailsio/runtime`, so the Playwright `unit` project
loads it; `state.ts` re-exports `Project` from here):

```ts
import type { ProjectView } from "./routes";

export type Project = {
  name: string;
  display_name: string;
  created: string;                         // RFC 3339
  resources: number;
  resources_by_type: Record<string, number>;
};
export type SortKey = "display_name" | "created";
export type Direction = "asc" | "desc";
export type Layout = "grid" | "list";
export type ProjectSort = { key: SortKey; dir: Direction }; // default { key: "created", dir: "asc" }

// sortProjects returns a sorted copy; display names compare with
// localeCompare (sensitivity "base"), ties by name in the same direction.
export function sortProjects(ps: Project[], sort: ProjectSort): Project[];
// filterProjects keeps projects whose display name or name contains query
// (trimmed, case-insensitive); an empty query keeps all.
export function filterProjects(ps: Project[], query: string): Project[];

export type ResourceEntry = { key: string; count: number; label: string; view: ProjectView };
// resourceEntries lists the counts above zero in sidebar order: servers,
// snapshots, backups, placement_groups, volumes, firewalls, load_balancers,
// networks, primary_ips, floating_ips, zones (view dns), ssh_keys,
// certificates; unknown keys are skipped. label is "<n> <noun>" with the
// noun singular for 1: Server, Snapshot, Backup, Placement Group, Volume,
// Firewall, Load Balancer, Network, Primary IP, Floating IP, DNS Zone,
// SSH Key, Certificate.
export function resourceEntries(byType: Record<string, number>): ResourceEntry[];
export function resourcesText(n: number): string; // "1 resource", "3 resources", "0 resources"
```

`state.ts` adds two stores kept in localStorage like `lastProject`:
`projectSort: Writable<ProjectSort>` (`hlab.projectSort`) and
`projectLayout: Writable<Layout>` (`hlab.projectLayout`, default `grid`);
invalid stored values fall back to the defaults.

Svelte: `lib/ProjectCard.svelte` with props
`{ project: Project; layout: Layout; ondelete: (p: Project) => void }`.

Accessible names the UI-11 e2e tests rely on (15-07, 15-08): a card is the
smallest element holding the display name, an "Actions" button and the text
"N resource(s)" or "Create server"; the layout switch is a button or radio
named "Grid" / "List"; the sort menu is a button whose name starts with
"Sort", with items "Display name" and "Created"; the direction arrow is a
button named /ascending|descending|direction|reverse/i; the tile is a button
named /New project/; the dialog shows "Create project", the field label
"Display name", the text "Name: <derived>" and the buttons "Create" and
"Cancel"; the conflict text contains "already exists" and the name; the
resource menu entry reads "1 SSH Key". In grid a card is under half the width
of `main`, in list over 80 %.

## Plans

- [x] 15-01 Project name derivation
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: PROJ-06 | Tests: internal/projectname/projectname_test.go
  - Reads: REQUIREMENTS PROJ-06; ARCHITECTURE "Management API" (`POST /projects`); phases/15/RESEARCH.md; phases/15/CONTEXT.md; DECISIONS "Project names transliterate only ä ö ü ß"
  - Size: ~120 lines in 3 files
  - Adjusts: -
  - [x] `internal/projectname/projectname.go` with `ValidateDisplay` and `Derive` per Interfaces (RESEARCH option A); `go.mod`/`go.sum`: `golang.org/x/text` becomes a direct requirement (`go mod tidy`)

- [x] 15-02 Store: schema version 2, display names, counts per type
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: PROJ-06, UI-11 | Tests: internal/store/store_test.go
  - Reads: ARCHITECTURE "store", "Data schema"; REQUIREMENTS LIFE-08, PROJ-06; DECISIONS "resources_by_type has one key per console tab"; CONTEXT.md
  - Size: ~150 lines in 6 files
  - Adjusts: internal/apitest/apitest.go, internal/cloud/recover_test.go, internal/cloud/reload_test.go (`CreateProject(tx, name)` becomes `CreateProject(tx, name, name)`)
  - [x] `internal/store/store.go`: `SchemaVersion = 2`, fresh schema with `display_name TEXT NOT NULL`; `migrate` case 1 in the same transaction: `ALTER TABLE projects ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`, `UPDATE projects SET display_name = name`, `PRAGMA user_version = 2`; other versions stay `*IncompatibleError`
  - [x] `internal/store/projects.go`: `Project.DisplayName`, `CreateProject(tx, name, displayName)`, every project select (`ProjectByName`, `ListProjects`, `TokenByHash`) reads `display_name`; `CountResourcesByType` per Interfaces
  - [x] adjust the three callers named under Adjusts

- [x] 15-03 Management API: create by display name, list with display name and counts
  - Wave: 2 | Depends on: 15-01, 15-02 | Review: alone
  - Requirements: PROJ-06, UI-11 | Tests: internal/mgmt/mgmt_test.go
  - Reads: ARCHITECTURE "Management API" (`GET /projects`, `POST /projects`); REQUIREMENTS PROJ-06, UI-11; CONTEXT.md
  - Size: ~90 lines in 1 file
  - Adjusts: -
  - [x] `internal/mgmt/mgmt.go`: `mgmt.Project` per Interfaces; `createProject` decodes `{"display_name"}`, `projectname.ValidateDisplay` (400 `invalid_input` with its message, also for a body with only `name`), `projectname.Derive` ("" gives 400 `invalid_input` "the display name gives an empty project name"), `store.CreateProject` (ErrConflict gives 409 `conflict` "project <derived> already exists" through `fail`), 201 with the project and `resources_by_type` `{}`; `listProjects` adds `display_name` and `store.CountResourcesByType`; the regexp `projectName` goes

- [x] 15-04 hlab: project create by display name, list with display name
  - Wave: 3 | Depends on: 15-03 | Review: alone
  - Requirements: PROJ-06 | Tests: internal/cli/cli_test.go
  - Reads: ARCHITECTURE "hlab (management command)"; DECISIONS "hlab project list puts the display name last"; CONTEXT.md
  - Size: ~40 lines in 1 file
  - Adjusts: -
  - [x] `internal/cli/cli.go`: `project create` takes `<display-name>` (one argument, usage line), sends `{"display_name"}`, prints `project <derived name> created` from the response; `project list` columns NAME, RESOURCES, CREATED, DISPLAY NAME; `--output json` passes the body through

- [x] 15-05 Tray bridge: derived name preview
  - Wave: 2 | Depends on: 15-01 | Review: alone
  - Requirements: UI-11 | Tests: -
  - Reads: ARCHITECTURE "hlab-tray (tray program)" (bridge); CONTEXT.md
  - Size: ~15 lines in 1 file (plus the regenerated Wails bindings, if the build generates them)
  - Adjusts: -
  - [x] `internal/bridge/service.go`: `ProjectName(display string) string` returning `projectname.Derive(display)`; reachable from the frontend as `service<string>("Service", "ProjectName", text)`

- [x] 15-06 Tray: project list logic
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-11 | Tests: ui/frontend/tests/unit/projects.test.ts
  - Reads: UI-SPEC "Views" (Projects, Project console sidebar and tabs); REQUIREMENTS UI-11; ARCHITECTURE "Management API" (`GET /projects`); CONTEXT.md
  - Size: ~120 lines in 2 files
  - Adjusts: -
  - [x] `ui/frontend/src/lib/projects.ts` per Interfaces; `ui/frontend/src/state.ts`: `Project` re-exported from `lib/projects.ts` (the old local type goes), stores `projectSort` and `projectLayout`

- [x] 15-07 Tray: project card
  - Wave: 2 | Depends on: 15-06 | Review: alone
  - Requirements: UI-11 | Tests: ui/frontend/tests/e2e/projects.spec.ts (through 15-08)
  - Reads: UI-SPEC "Foundation", "Colours", "Typography", "Spacing and grid", "Building blocks" (Button, row menu, Confirm dialog), "Views" (Projects), "Behaviour" (keyboard and accessibility); CONTEXT.md
  - Size: ~200 lines in 1 file
  - Adjusts: -
  - [x] `ui/frontend/src/lib/ProjectCard.svelte`:
    - Lucide `Folder` icon, the display name at 16 px semibold, the name at 12 px muted in the normal typeface, a "…" menu with Delete at the top right (button label "Actions", items `role="menuitem"` like the table row menu).
    - Footer button `resourcesText(n)` with a chevron opening a menu of `resourceEntries` links to `href({area: "project", project, view, page: {kind: "list"}})`; with no resources a "+ Create server" link to the servers create page.
    - A click on the card outside those controls opens `consoleStart(name)`. Grid layout as an equal-size card, list layout as one full-width row with the same content. Escape and an outside click close the open menu.

- [x] 15-08 Tray: project list page
  - Wave: 3 | Depends on: 15-03, 15-05, 15-07 | Review: alone
  - Requirements: UI-11 | Tests: ui/frontend/tests/e2e/projects.spec.ts, test/e2e/ui_test.go
  - Reads: UI-SPEC "Views" (Projects, Cloud stopped), "Building blocks" (Confirm dialog, Action toast), "Behaviour"; ARCHITECTURE "hlab-tray (tray program)" (UI tests); CONTEXT.md; DECISIONS "E2E rows of a plan run after its merge"
  - Size: ~330 lines in 6 files
  - Adjusts: ui/frontend/tests/e2e/projects.spec.ts (UI-05: create through the "+ New project" tile with field "Display name", open and delete through the card; checks unchanged), ui/frontend/tests/e2e/console.ts (`openConsole` clicks the project's card instead of a table row), ui/frontend/tests/screenshots/console.spec.ts (sample projects found as cards, opened by their card)
  - [x] `ui/frontend/src/views/Projects.svelte` rebuilt:
    - Toolbar: a search field (`role="searchbox"`, label "Search", `filterProjects`), a sort menu (Display name, Created) with a direction arrow button, and a grid/list switch, bound to `projectSort`/`projectLayout`.
    - The cards in grid or list (CSS grid, auto-fill, about 280 px minimum), then the dashed "+ New project" tile; with no projects the sentence "There are no projects yet." plus the tile; skeleton while loading; CloudStopped while stopped; refresh every 5 s.
    - "Create project" dialog: field "Display name", below it "Name: <derived>" from `ProjectName` live; POST `{"display_name"}`; `invalid_input`/`conflict` message under the field.
    - Delete: ConfirmDialog with `typedName` the display name, toast title `actionTitle("Project", display name, "Delete")`, the deletion still by name.
  - [x] adjust the tests named under Adjusts
  - [x] `test/e2e/e2e_test.go` and `test/e2e/ui_test.go`: subtest `UI-11` running `runSpec(t, "UI-11", 5*time.Minute, nil)` (lower to about twice the first full run)

- [x] 15-09 Tray: breadcrumb and project switcher with display names
  - Wave: 3 | Depends on: 15-03, 15-06 | Review: alone
  - Requirements: UI-11 | Tests: -
  - Reads: UI-SPEC "Views" (Project console breadcrumb); CONTEXT.md
  - Size: ~40 lines in 1 file
  - Adjusts: -
  - [x] `ui/frontend/src/lib/ProjectMenu.svelte`: loads `GET /projects` when mounted and when `project` changes; the button shows the display name of `project` (the name until loaded or when not listed); the menu lists the other projects by display name, sorted with `sortProjects(…, {key: "display_name", dir: "asc"})`, linking by name

- [ ] 15-10 Documentation
  - Wave: 4 | Depends on: 15-01, 15-02, 15-03, 15-04, 15-05, 15-06, 15-07, 15-08, 15-09 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation", "Accounts and secrets" (hcloud context `hlab-docs`); REQUIREMENTS DOC; UI-SPEC "Language of the interface"; CONTEXT.md
  - Size: ~150 lines in 6 files plus 2 screenshots
  - Adjusts: -
  - [ ] `docs/commands.md` (`hlab project create <display-name>` with a quoted example and the derived name, the derivation rule, the refusals, `project list` with DISPLAY NAME last), `docs/guide.md` and `README.md` (projects, display name and name, the project list)
  - [ ] `ui/frontend/src/help/projects.md`: cards and list, search, sort, grid/list switch, "N resources", "+ Create server", "+ New project" with the derived name, delete by typing the display name, breadcrumb with display names; `docs/development/api-coverage.md` and `docs/deviations.md` only if a project statement there changed
  - [ ] regenerate `docs/images/projects-{light,dark}.png` with `task docs:screenshots`
