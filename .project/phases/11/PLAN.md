# Phase 11: Tray console: shared frame, servers, SSH keys

## Interfaces

The modules the unit tests load (`lib/actions.ts`, `lib/formErrors.ts`,
`lib/labels.ts`, `lib/routes.ts`, `lib/serverActions.ts`) import neither
`lib/bridge.ts` nor `@wailsio/runtime`, so Playwright's `unit` project
loads them without a browser. They recognise API errors by shape
(`code`, `fields`), not with `instanceof ApiError`.

```ts
// lib/api.ts: the Hetzner API of one project through the management
// API's project proxy (bridge.Call -> /mgmt/v1/projects/{name}/api/v1...).
export function api<T>(project: string, method: string, path: string, body?: unknown): Promise<T>; // throws ApiError
// listAll reads every page (per_page=50) of a list endpoint and returns the items under key.
export function listAll<T>(project: string, path: string, key: string): Promise<T[]>;
// runApi runs one request that answers with `action` and/or `next_actions`
// under an action toast (runAction with the project's GET /actions/{id}).
export function runApi<T extends ActionResult>(project: string, title: string, method: string, path: string, body?: unknown): Promise<T | undefined>;

// lib/actions.ts
export type Action = {
  id: number;
  command: string;
  status: "running" | "success" | "error";
  progress: number;
  error: { code: string; message: string } | null;
};
export type ActionResult = { action?: Action; next_actions?: Action[] };
export class ActionError extends Error { code: string; }
export type Poll = { get: (id: number) => Promise<Action>; sleep?: (ms: number) => Promise<void> }; // sleep defaults to 1 s
// waitAction polls until the action is no longer running; onProgress gets
// every polled state. An action ending in "error" throws ActionError(code, message).
export function waitAction(a: Action, poll: Poll, onProgress?: (a: Action) => void): Promise<Action>;
// runAction shows a progress toast (title, progress in %), runs run(), waits
// for its action and then every next_action, and ends the toast as success
// (closes by itself) or error (code: message, stays). Resolves to the
// result, or undefined after an error.
export function runAction<T extends ActionResult>(title: string, run: () => Promise<T>, poll: Poll): Promise<T | undefined>;

// lib/toast.ts (extended): Toast gets `progress?: number` (0..100) for kind "progress".

// lib/formErrors.ts
export type FormErrors = { fields: Record<string, string>; general?: string };
// formErrors maps an error with `fields` (details.fields) to the form's known
// fields: "labels.x", "networks[0]" and "a.b" go to their top-level name;
// messages of one field are joined with a space; unknown names and errors
// without fields go to general as "<code>: <message>".
export function formErrors(err: unknown, known: string[]): FormErrors;

// lib/labels.ts
export type LabelRow = { key: string; value: string };
export function toRows(labels: Record<string, string>): LabelRow[]; // sorted by key
// toLabels skips rows with an empty key and value, keeps empty values and
// refuses a key that occurs twice, naming the index of its second row.
export function toLabels(rows: LabelRow[]): { labels: Record<string, string> } | { error: string; row: number };

// lib/routes.ts (pure; state.ts keeps the stores and uses it)
export type ProjectView = "servers" | "ssh-keys" | "tokens";
export type AdminView = "server-types" | "settings" | "logs";
export type Page = { kind: "list" } | { kind: "new" } | { kind: "detail"; id: number; tab: string };
export type Route =
  | { area: "projects" }
  | { area: "project"; project: string; view: ProjectView; page: Page }
  | { area: "admin"; view: AdminView };
// href: #/projects, #/projects/<p>/<view>, #/projects/<p>/<view>/new,
// #/projects/<p>/<view>/<id>/<tab>, #/admin/<view>. tokens has only "list".
export function href(r: Route): string;
// parse returns undefined for an unknown route; a detail without tab or
// with a tab not of the form [a-z-]+ gets tab "overview".
export function parse(hash: string): Route | undefined;
// consoleStart is the first page of a project console: its Servers list.
export function consoleStart(project: string): Route;

// lib/serverActions.ts
export type Input = Record<string, unknown>;
export type Confirm = "none" | "plain" | "typed-name";
export type ServerAction = {
  id: string;                // "poweron", "rename", "delete", "restore", "attach_volume", ...
  label: string;             // menu or button label
  method?: "POST" | "PUT" | "DELETE";
  path?: (server: number, input: Input) => string;
  body?: (input: Input) => unknown;
  confirm: Confirm;
  password: boolean;         // the answer carries root_password
  needsInput: boolean;       // opens a dialog before it runs
  disabledHint?: string;     // set: shown disabled, never sent
};
export const menuActions: ServerAction[]; // the detail page's actions menu, in order
export const tabActions: Record<string, ServerAction>; // actions of the detail tabs, by id
// request builds method, path and body; undefined for a disabled entry.
export function request(a: ServerAction, server: number, input?: Input): { method: string; path: string; body?: unknown } | undefined;
```

Menu entries in order: Power on, Shutdown, Reboot, Reset (plain),
Power off (plain), Reset root password (password), Rebuild (plain,
password, input `image`), Change type (plain, input `server_type`,
`upgrade_disk`), Rename (`PUT /servers/{id}` with `name`), Add to placement
group (input `placement_group`), Remove from placement group, Console
(disabled, hint "Not available in HLab"), Delete (typed-name, `DELETE
/servers/{id}`). Tab actions: `enable_backup`, `disable_backup`,
`create_backup` (`create_image` type backup), `create_snapshot`
(`create_image` type snapshot, `description`), `restore` (`rebuild` with
the image ID; plain, password), `delete_image` (`DELETE /images/{image}`;
plain), `enable_rescue` (plain, password, `ssh_keys`, type `linux64`),
`disable_rescue`, `attach_iso`, `detach_iso`, `attach_to_network`
(`network`, optional `ip`, `alias_ips`), `detach_from_network`,
`change_alias_ips`, `change_dns_ptr` (`ip`, `dns_ptr` or null),
`apply_firewall` and `remove_firewall` (`POST
/firewalls/{firewall}/actions/apply_to_resources` and
`remove_from_resources` with `[{type: "server", server: {id}}]`),
`attach_volume` (`POST /volumes/{volume}/actions/attach` with
`assignee: {id, type: "server"}`, `automount`; `server` is deprecated),
`detach_volume`, `change_protection` (`delete`, `rebuild`).

UI texts the e2e specs rely on (tests/e2e/console.ts, sshkeys.spec.ts,
servers.spec.ts): create buttons "Create server" and "Add SSH key"; the
rename dialog's button "Rename"; Location, Image and Type in the server
form are a select or radio group with those accessible names; label row
inputs have names containing "key" and "value"; a one-time password shows
in a `<code>` CopyField next to text matching "not shown again"; action
progress shows as "N%" in the `aria-live` toast region; the gear returns
to the last console route, not to `consoleStart`.

Svelte building blocks (props):

```ts
// lib/DetailPage.svelte
{ title: string; status?: { tone: "success" | "warning" | "muted" | "error"; text: string };
  help: ViewId; tabs: { id: string; label: string; href: string }[]; current: string;
  actions?: Snippet; children: Snippet; notFound?: boolean; back: string }
// lib/ActionsMenu.svelte: a "…" button labelled "Actions" with role menu
{ items: { label: string; onclick?: () => void; danger?: boolean; disabled?: boolean; hint?: string }[] }
// lib/SecretDialog.svelte: shows a value once with CopyField and the warning
{ open: boolean (bindable); title: string; label: string; value: string | undefined }
// lib/FormPage.svelte: one column, the general error line, submit bottom right
{ title: string; help: ViewId; submitLabel: string; busy: boolean; general?: string;
  onsubmit: () => void; oncancel: () => void; children: Snippet }
// lib/LabelsField.svelte: label rows for create forms
{ rows: LabelRow[] (bindable); error?: string }
// lib/LabelsTab.svelte: edits and saves labels with PUT <path> {labels}
{ project: string; path: string; labels: Record<string, string>; onsaved: () => void }
// lib/ProtectionTab.svelte: one switch per flag, saved through change_protection as an action
{ project: string; path: string; flags: { key: string; label: string; value: boolean }[]; onchanged: () => void }
```

`lib/Table.svelte`'s `RowAction` gets optional `disabled` and `hint`.

## Plans

- [x] 11-01 API client and action polling
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/actions.test.ts
  - Reads: UI-SPEC "Building blocks" (Action toast), "Behaviour"; ARCHITECTURE "hlab-tray (tray program)", "Management API (named pipe)" (project proxy); OpenAPI `action` schema and `GET /actions/{id}` in the reference; ui/frontend/src/lib/bridge.ts, lib/toast.ts, lib/Toast.svelte
  - Size: ~220 lines in 4 files
  - [x] ui/frontend/src/lib/actions.ts (`Action`, `ActionResult`, `ActionError`, `waitAction`, `runAction`) and lib/toast.ts (`progress` on a progress toast, an update function used by `runAction`); lib/Toast.svelte shows the progress in % with a bar.
  - [x] ui/frontend/src/lib/api.ts (`api`, `listAll`, `runApi`) over `mgmt` of bridge.ts.

- [x] 11-02 Routes and project console sections
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/unit/routes.test.ts
  - Reads: UI-SPEC "Views" (Shell, Project console), "Building blocks" (Help button); ARCHITECTURE "hlab-tray (tray program)"; DECISIONS "Shell start route and last project", "Project console building blocks"; ui/frontend/src/state.ts, App.svelte, views/ProjectConsole.svelte, help.ts, views.json, lib/SectionSidebar.svelte
  - Size: ~260 lines in 11 files
  - Adjusts: ui/frontend/tests/e2e/projects.spec.ts (UI-05: the row click now opens Servers; the test opens "API tokens" through the sidebar before its checks), ui/frontend/tests/screenshots/console.spec.ts (opens "API tokens" through the sidebar)
  - [x] ui/frontend/src/lib/routes.ts (`Route`, `Page`, `href`, `parse`, `consoleStart`); state.ts uses it, and the start route and a project row click go to `consoleStart`; help.ts `ViewId` and views.json get `servers` and `ssh-keys`.
  - [x] views/ProjectConsole.svelte: sidebar Servers (Lucide `Server`), SSH Keys (`Key`), separator, API tokens; renders the section of the route. New section entry components views/servers/Servers.svelte and views/sshkeys/SshKeys.svelte with the title row ("Servers", "SSH Keys"), HelpButton and `CloudStopped`; help/servers.md and help/ssh-keys.md with a heading and a short text (the documentation plan completes them).
  - [x] Adjust the two specs under "Adjusts".

- [x] 11-03 Detail page, actions menu, form page and secret dialog
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-02 | Tests: - (used by the sections; covered by the UI-02 e2e rows)
  - Reads: UI-SPEC "Building blocks", "Behaviour", "Typography"; ui/frontend/src/lib/Table.svelte, Dialog.svelte, ConfirmDialog.svelte, CopyField.svelte, StatusPill.svelte, HelpButton.svelte, views/Tokens.svelte (token dialog)
  - Size: ~320 lines in 5 files
  - [x] ui/frontend/src/lib/DetailPage.svelte (title row with status pill, actions snippet and help button; tabs as links with `aria-current`; "not found" with a link back), lib/ActionsMenu.svelte (keyboard reachable, Escape closes, disabled items show their hint as text and `aria-describedby`).
  - [x] lib/FormPage.svelte, lib/SecretDialog.svelte ("Copy the password now. It is not shown again."), lib/Table.svelte `RowAction.disabled`/`hint`.

- [x] 11-04 Form errors, labels and protection
  - Wave: 2 | Depends on: 11-01 | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/forms.test.ts
  - Reads: UI-SPEC "Building blocks" (Form); REQUIREMENTS API-07, API-09; OpenAPI error object (`details.fields`) and `change_protection` of servers in the reference; ui/frontend/src/lib/Field.svelte, bridge.ts `toApiError`
  - Size: ~300 lines in 5 files
  - [x] ui/frontend/src/lib/formErrors.ts, lib/labels.ts as in "Interfaces".
  - [x] lib/LabelsField.svelte, lib/LabelsTab.svelte (rows with key, value and remove, "Add label", "Save labels"; a duplicate key or the API's `labels` message under the row or the list), lib/ProtectionTab.svelte (one switch per flag, "Save" sends `change_protection` through `runApi`).

- [x] 11-05 Server actions module
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/serverActions.test.ts
  - Reads: CONTEXT "Corrections", "Answers"; OpenAPI request bodies of every `/servers/{id}/actions/*` endpoint, `PUT /servers/{id}`, `DELETE /images/{id}`, `/firewalls/{id}/actions/apply_to_resources`, `remove_from_resources`, `/volumes/{id}/actions/attach`, `detach` in the reference; docs/development/api-coverage.md "Server actions"
  - Size: ~260 lines in 1 file
  - [x] ui/frontend/src/lib/serverActions.ts as in "Interfaces", with every menu and tab entry, its confirmation and password flags.

- [ ] 11-06 SSH Keys section
  - Wave: 3 | Depends on: 11-02, 11-03, 11-04 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/sshkeys.spec.ts (through test/e2e/ui_test.go TestE2E/UI-02)
  - Reads: UI-SPEC "Building blocks", "Views", "Behaviour"; REQUIREMENTS KEY-01; OpenAPI `/ssh_keys` and `/ssh_keys/{id}` in the reference; CONTEXT "Corrections"
  - Size: ~300 lines in 3 files
  - [ ] ui/frontend/src/views/sshkeys/SshKeys.svelte: list (Name, Fingerprint, Labels, Created; refresh every 5 s; row menu Delete with a plain confirmation), switching to create and detail by the route.
  - [ ] views/sshkeys/SshKeyCreate.svelte (Public key text area, Name, Labels; field errors through `formErrors`), views/sshkeys/SshKeyDetail.svelte (tabs Overview: name edit, fingerprint, public key in a CopyField, created; Labels; actions menu Delete).

- [x] 11-07 Servers list and create form
  - Wave: 3 | Depends on: 11-02, 11-03, 11-04, 11-05 | Review: alone
  - Requirements: UI-02 | Tests: - (the UI-02 server row of 11-08 creates through this form)
  - Reads: UI-SPEC "Views" (create order for servers), "Building blocks", "Behaviour"; OpenAPI `POST /servers` request, `/locations`, `/images`, `/server_types`, `/ssh_keys`, `/networks`, `/primary_ips`, `/volumes`, `/firewalls`, `/placement_groups` lists in the reference; docs/development/api-coverage.md "POST /servers"
  - Size: ~400 lines in 2 files
  - [x] ui/frontend/src/views/servers/Servers.svelte: list (Name, Status pill, Public IPv4, Type, Location, Labels, Created; refresh every 5 s and after every action); row menu with the menu entries that need no input (Power on, Shutdown, Reboot, Reset, Power off, Delete) with their confirmations; switching to create and detail by the route.
  - [x] views/servers/ServerCreate.svelte in the UI-SPEC order: Location, Image (system images, own snapshots and backups), Type (only types available in the chosen location, ARM left out), Networking (public IPv4 and IPv6 switches, optional primary IPs, private networks), SSH keys, Volumes, Firewalls, Backups, Placement group, Labels, Cloud config, Name (default `<type>-<location>-<n>`); submit through `runApi`, the `root_password` in SecretDialog, then the new server's detail page.

- [ ] 11-08 Server detail page and actions menu
  - Wave: 4 | Depends on: 11-07 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/servers.spec.ts (through test/e2e/ui_test.go TestE2E/UI-02)
  - Reads: UI-SPEC "Building blocks" (Detail page, Confirm dialog), "Behaviour"; CONTEXT "Corrections"; OpenAPI `server` schema and `/server_types`, `/images`, `/placement_groups` in the reference
  - Size: ~380 lines in 3 files
  - [ ] ui/frontend/src/views/servers/ServerDetail.svelte: DetailPage with status pill, tabs Overview, Networking, Firewalls, Volumes, Backups, Snapshots, Rescue, ISO, Graphs, Labels, Protection (tabs not built yet stay out of the list until their plan adds them); Overview (status, type, location, image, public IPv4 and IPv6 in CopyFields, private IPs, placement group, created); Labels and Protection (delete, rebuild) through the shared tabs; refresh every 5 s.
  - [ ] views/servers/ServerActions.svelte: ActionsMenu from `menuActions`; dialogs for Rebuild (image), Change type (type, "Upgrade disk", saying that a disk upgrade cannot be undone), Rename, Add to placement group; ConfirmDialog by `confirm` (typed name only for Delete); SecretDialog for `password`; Console disabled with its hint; after Delete back to the list.
  - [ ] servers.ts in views/servers: the server types, images and status tone mapping shared by list, form and detail.

- [ ] 11-09 Server tabs: Networking, Firewalls, Volumes
  - Wave: 5 | Depends on: 11-08 | Review: alone
  - Requirements: UI-02 | Tests: - (requests proven by serverActions.test.ts)
  - Reads: OpenAPI `server.public_net`, `private_net`, `/networks`, `/firewalls`, `/volumes` in the reference; REQUIREMENTS RDNS-01, NET-01, FW-01, VOL-05; docs/development/api-coverage.md "Networks", "Firewalls", "Volumes"
  - Size: ~380 lines in 4 files
  - [ ] views/servers/tabs/Networking.svelte: public IPs with their reverse DNS (edit, reset to default), private networks with attach (network, optional IP, alias IPs), change alias IPs and detach (plain confirmation).
  - [ ] views/servers/tabs/Firewalls.svelte (applied firewalls, apply one, remove one), views/servers/tabs/Volumes.svelte (attached volumes, attach a volume of the same location with automount, detach); ServerDetail.svelte adds the three tabs.

- [ ] 11-10 Server tabs: Backups, Snapshots, Rescue, ISO
  - Wave: 6 | Depends on: 11-09 | Review: alone
  - Requirements: UI-02 | Tests: - (requests and confirmations proven by serverActions.test.ts)
  - Reads: OpenAPI `/images` (type backup and snapshot, `bound_to`, `created_from`), `/isos`, `enable_rescue` in the reference; REQUIREMENTS SRV-11 to SRV-15; docs/development/api-coverage.md "Snapshots and backups", "Rescue system", "ISOs"; CONTEXT "Corrections"
  - Size: ~380 lines in 5 files
  - [ ] views/servers/tabs/Backups.svelte (enable, disable, "Create backup", list with Restore and Delete, both confirmed), views/servers/tabs/Snapshots.svelte (create with description, list of this server's snapshots with Restore and Delete, confirmed).
  - [ ] views/servers/tabs/Rescue.svelte (enable with SSH keys after confirmation, password in SecretDialog; disable), views/servers/tabs/Iso.svelte (attached ISO, attach from `/isos`, detach); ServerDetail.svelte adds the four tabs.

- [ ] 11-11 Server tab: Graphs
  - Wave: 7 | Depends on: 11-10 | Review: alone
  - Requirements: UI-02 | Tests: - (metrics content is SRV-21)
  - Reads: OpenAPI `GET /servers/{id}/metrics` (type, start, end, step; `time_series`) in the reference; UI-SPEC "Colours", "Behaviour"
  - Size: ~260 lines in 3 files
  - [ ] ui/frontend/src/lib/LineChart.svelte: SVG line chart of one or more series with time axis, value axis, legend and an accessible title.
  - [ ] views/servers/tabs/Graphs.svelte: CPU, disk (IOPS, bandwidth) and network (PPS, bandwidth) for 1 h, 24 h, 7 d, reloaded every 30 s while visible; ServerDetail.svelte adds the tab.

- [ ] 11-12 Documentation
  - Wave: 8 | Depends on: 11-06, 11-11 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: test/docs/docs_test.go, ui/frontend/tests/unit/help.test.ts
  - Reads: SPEC "Documentation"; REQUIREMENTS "Documentation (DOC)"; CONTEXT; DECISIONS phase 11 entries
  - Size: ~250 lines in 6 files
  - [ ] ui/frontend/src/help/servers.md and help/ssh-keys.md: list, create form, detail tabs and actions, naming only existing views and buttons (Console not available in HLab).
  - [ ] README.md (tray program: project console with Servers and SSH Keys, the other sections follow), docs/guide.md (servers and SSH keys in the console), docs/development/components.md (the console frame in ui/frontend/src/lib: api, actions, routes, detail and form building blocks); regenerate the README console screenshots with `task docs:screenshots`.
