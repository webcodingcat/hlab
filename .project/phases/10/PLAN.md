# Phase 10: Tray program: shell, status, settings, server types, projects, logs

## Interfaces

Test contracts: the tests written first fix names and labels listed in `TEST-CONTRACTS.md`; every implementer reads the lines of its plan there.

```go
// internal/mgmtclient: HTTP/1.1 over the management pipe, shared by hlab
// and hlab-tray (moved out of internal/cli).
var ErrNotRunning = errors.New("the cloud is not running") // dial failed
type Client struct{ /* dial func */ }
func New(dial func(ctx context.Context) (net.Conn, error)) *Client
// Do sends one request to /mgmt/v1<path>; body nil sends none.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (status int, resp []byte, err error)
// Stream GETs /mgmt/v1<path> and calls line for every NDJSON line until
// ctx ends, the stream ends or line returns an error.
func (c *Client) Stream(ctx context.Context, path string, line func([]byte) error) error

// internal/cloudstatus: hlabd's status.json (moved out of internal/cli).
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}
type File struct {
	State   string   `json:"state"`
	Problem *Problem `json:"problem,omitempty"`
	Error   string   `json:"error,omitempty"`
}
func Read(path string) (File, bool) // missing or unreadable: false

// internal/settings (additions; the structs also get json tags equal to
// their toml keys)
type FieldError struct{ Key, Message string } // Key e.g. "network.public_range"
func (e *FieldError) Error() string
func Validate(s Settings) error // nil or *FieldError
// Write validates s, takes trust_ca from the file at path (owned by
// PUT /trust), and writes the whole file in place in one write.
func Write(path string, s Settings) error
func CopySecurity(src, dst string) error // exported copySecurity

// internal/catalog (additions)
type OwnSpec struct {
	Name, Description, CPUType string // CPUType "shared" | "dedicated"
	Cores                      int
	Memory                     float64 // GB
	Disk                       int     // GB
	Locations                  []string // location names; empty = all
}
type FieldError struct{ Field, Message string } // Field: "name", "cores", ...
func (e *FieldError) Error() string
func (c *Catalog) Own() []ServerType // own types served now
// ReloadOwn re-reads customFile; invalid or colliding own types are
// dropped and OwnTypesWarning reports why; a valid file clears it.
func (c *Catalog) ReloadOwn(customFile string)
// AddOwn checks s against c (fields, name and ID collisions), builds the
// Hetzner server_type object (next free ID from MinOwnID, x86, local
// storage, zero prices, the given locations) and rewrites customFile in
// place in one write.
func AddOwn(c *Catalog, customFile string, s OwnSpec) (ServerType, error)
func RemoveOwn(customFile, name string) error // fs.ErrNotExist-wrapped when absent

// internal/mgmt (additions to Deps)
Reload func(ctx context.Context) error // nil leaves POST /settings/reload out; 200 {} or 500 server_error
Done   <-chan struct{}                 // closed when the cloud stops; ends log followers

// internal/bridge: Wails-bound services, no Wails import (events through Emit).
type Response struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}
type Invalid struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
type Deps struct {
	Client  *mgmtclient.Client
	Paths   sysint.Paths
	Running func(ctx context.Context) bool // SCM state is running
}
type Service struct{ /* ... */ }
func NewService(d Deps) *Service
func (s *Service) Call(method, path, body string) (Response, error) // body "" sends none
func (s *Service) Settings() (settings.Settings, error)
func (s *Service) SaveSettings(set settings.Settings) (*Invalid, error)
type ListedType struct {
	Type   json.RawMessage `json:"type"`   // Hetzner server_type object
	Source string          `json:"source"` // "catalog" | "own"
}
type ServerTypeList struct {
	Types   []ListedType `json:"types"`
	Problem string       `json:"problem,omitempty"` // like custom_server_types
}
func (s *Service) ServerTypes() (ServerTypeList, error)
func (s *Service) AddServerType(spec catalog.OwnSpec) (*Invalid, error)
func (s *Service) RemoveServerType(name string) error

type State struct {
	State          string               `json:"state"` // stopped|starting|running|stopping|refused
	Problems       []cloudstatus.Problem `json:"problems"`
	Refused        *cloudstatus.Problem  `json:"refused,omitempty"`
	Error          string               `json:"error,omitempty"` // failed start
	ConnectionLost bool                 `json:"connection_lost"`
	Version        string               `json:"version,omitempty"`
}
type CloudDeps struct {
	Service    sysint.ServiceControl
	Client     *mgmtclient.Client
	StatusFile string
	Interval   time.Duration // 2 s; tests shorten it
	OnChange   func(State)   // tray icon/menu and the "cloud" event
}
type Cloud struct{ /* ... */ }
func NewCloud(d CloudDeps) *Cloud
func (c *Cloud) Run(ctx context.Context) // poller
func (c *Cloud) Status() State
func (c *Cloud) Start(discard bool) error
func (c *Cloud) Stop() error
type TrayModel struct {
	Icon     string `json:"icon"` // running|stopped|busy|error
	Tooltip  string `json:"tooltip"`
	Status   string `json:"status"` // menu status line
	CanStart bool   `json:"can_start"`
	CanStop  bool   `json:"can_stop"`
}
func Tray(s State) TrayModel

type LogsDeps struct {
	Client   *mgmtclient.Client
	LogDir   string
	Running  func() bool
	Emit     func(lines []string) // one "logs" event per batch
	Interval time.Duration        // reconnect interval (2 s)
	Batch    time.Duration        // 100 ms
}
type Logs struct{ /* ... */ }
func NewLogs(d LogsDeps) *Logs
func (l *Logs) Follow(on bool) // on: emits the last 1000 lines, then new ones
```

```ts
// ui/frontend/src/lib/bridge.ts
export type Response = { status: number; body: unknown };
export class ApiError extends Error { code: string; fields?: Record<string, string[]> }
export function call(method: string, path: string, body?: unknown): Promise<Response>;
export function mgmt<T>(method: string, path: string, body?: unknown): Promise<T>; // throws ApiError
export function service<T>(type: "Service" | "Cloud" | "Logs", method: string, ...args: unknown[]): Promise<T>;
export function on<T>(event: "cloud" | "logs", fn: (data: T) => void): () => void;

// ui/frontend/src/help.ts (pure) and helpTexts.ts (Vite glob)
export type ViewId = "projects" | "server-types" | "settings" | "logs";
export function renderHelp(markdown: string): string; // headings, paragraphs, lists, code, bold, links
export function helpFor(view: string, texts: Record<string, string>): string | undefined;
```

Bound method names for `Call.ByName`: `github.com/webcodingcat/hlab/internal/bridge.<Type>.<Method>`; the e2e-only tray service is `main.TrayTest.State` / `main.TrayTest.Click`.

## Plans

- [ ] 10-01 Catalog: own server types add, remove and reload
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-04 | Tests: internal/catalog/catalog_test.go
  - Reads: ARCHITECTURE "Files and formats"; DECISIONS "Tray writes server-types.custom.json directly", "Server Types view shows the catalog while the cloud is stopped"; OpenAPI `server_type` schema (`/server_types` response) in the reference
  - Size: ~250 lines in 2 files
  - [ ] internal/catalog/custom.go: `Own`, `ReloadOwn` (under `c.mu`, sets or clears the own-types warning), `OwnSpec`, `FieldError`, `AddOwn` (field checks, collisions, next free ID, object in Hetzner shape, in-place single write), `RemoveOwn`.

- [ ] 10-02 Servers: stored server type when the own type is gone
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-04 | Tests: internal/resources/servers/servers_test.go
  - Reads: DECISIONS "Existing servers outlive a removed own server type"; CONTEXT "Corrections"
  - Size: ~120 lines in 4 files
  - [ ] One helper (internal/resources/servers/servers.go or a new types.go) that returns the catalog type or, when missing, cores, memory and disk from the server's stored `server_type`; power.go, rebuild.go and reconcile.go use it; create and change_type keep requiring the catalog.

- [ ] 10-03 Shared management client and status file
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-01 | Tests: - (behaviour unchanged; internal/cli/cli_test.go keeps passing)
  - Reads: ARCHITECTURE "hlab (management command)", "Management API (named pipe)"; internal/cli/cli.go `mgmt`, `send`, `readStatusFile`
  - Size: ~250 lines in 5 files
  - [ ] New internal/mgmtclient (`Client`, `Do`, `Stream`, `ErrNotRunning`) and internal/cloudstatus (`File`, `Problem`, `Read`) from the code in internal/cli/cli.go.
  - [ ] internal/cli/cli.go and cmd/hlab/main.go use them; no output change.

- [ ] 10-04 Settings: field errors, in-place write, exported ACL copy
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-03 | Tests: internal/settings/settings_test.go
  - Reads: ARCHITECTURE "Files and formats" (Settings); DECISIONS "Tray settings writes", "settings.Save rewrites the file"
  - Size: ~180 lines in 4 files
  - [ ] internal/settings/settings.go: json tags equal to the toml keys, `FieldError`, `Validate` (the existing rules returning the key), `Write` (validate, reread `trust_ca` from the file, encode, one in-place write that keeps the file's ACL); `Load` keeps its messages.
  - [ ] internal/settings/security_windows.go, security_other.go: export `CopySecurity`; `Save` uses it.

- [ ] 10-05 Frontend tooling, embed and bridge wrapper
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-01 | Tests: - (used by all frontend tests)
  - Reads: RESEARCH topics 3 and 6; SPEC "Stack"; Taskfile.yml
  - Size: ~180 lines in 9 files
  - [ ] ui/frontend/package.json (`@wailsio/runtime` 3.1.0 exact; scripts `test:unit`, `test:e2e`), tsconfig.json (`"types": ["svelte", "vite/client"]`), playwright.config.ts (projects `unit` = tests/unit without browser, `e2e` = tests/e2e, `workers: 1`, `fullyParallel: false`), Taskfile.yml task `test:frontend` (`npm run test:unit`).
  - [ ] ui/embed.go (`package ui`, `//go:embed all:frontend/dist`, `var Dist embed.FS`); committed placeholder `ui/frontend/dist/embed-placeholder.txt` and `ui/frontend/public/embed-placeholder.txt` (Vite copies it back after `emptyOutDir`), .gitignore `ui/frontend/dist/*` with `!ui/frontend/dist/embed-placeholder.txt`, so `go vet`/`go test`/lint work on a fresh checkout.
  - [ ] ui/frontend/src/lib/bridge.ts as in "Interfaces" (`Call.ByName`, `Events.On`, Hetzner and management error objects to `ApiError`).

- [ ] 10-06 Frontend building blocks
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-08 | Tests: - (manual UI-08; used by the view tests)
  - Reads: UI-SPEC "Foundation", "Colours", "Typography", "Spacing and grid", "Building blocks", "Behaviour", "Language of the interface"
  - Size: ~400 lines in 9 files
  - [ ] ui/frontend/src/theme.css (custom properties light/dark), lib/Button.svelte, lib/StatusPill.svelte, lib/CopyField.svelte.
  - [ ] lib/Table.svelte (header, count, search, sortable columns, skeleton rows, empty state, row menu), lib/Field.svelte (label, inline error), lib/ConfirmDialog.svelte (typed name option, Escape closes), lib/Toast.svelte with lib/toast.ts (action progress, errors stay until closed).

- [ ] 10-07 hlabd: settings reload, own types file, log followers at stop
  - Wave: 2 | Depends on: 10-01, 10-04 | Review: alone
  - Requirements: UI-03, UI-04, LIFE-09 | Tests: internal/cloud/reload_test.go, internal/mgmt/mgmt_test.go
  - Reads: ARCHITECTURE "Management API (named pipe)", "Cloud start and stop"; DECISIONS "Tray writes server-types.custom.json directly", "Changed stop_behaviour reaches VMs at the next cloud start"; BACKLOG item "log followers"; CONTEXT
  - Size: ~250 lines in 5 files
  - [ ] internal/mgmt/mgmt.go: `Deps.Reload` with `POST /settings/reload`; `Deps.Done` ends the follow of `GET /logs` (backlog bug: no 10 s wait at stop).
  - [ ] internal/cloud (cloud.go, new reload.go): at start create `server-types.custom.json` with `[]` when missing and give it settings.toml's ACL via `settings.CopySecurity`; `reload` re-reads settings.toml (invalid file: error, nothing changed), sets `logging.level`, calls `cat.ReloadOwn` and refreshes the `custom_server_types` problem; stop_behaviour is already re-read at stop; public_range, data_folder and the reserves apply at the next start; close `Done` before `mgmtSrv.Shutdown`.

- [ ] 10-08 Bridge: management calls, settings, server types
  - Wave: 2 | Depends on: 10-01, 10-03, 10-04 | Review: alone
  - Requirements: UI-03, UI-04 | Tests: internal/bridge/service_test.go
  - Reads: ARCHITECTURE "hlab-tray (tray program)", "Files and formats"; RESEARCH topic 3 (argument and result shapes); DECISIONS "Tray settings writes", "Tray writes server-types.custom.json directly"
  - Size: ~300 lines in 2 files
  - [ ] internal/bridge/service.go: `Service` with `Call` (string body, `Response`), `Settings`, `SaveSettings` (`settings.Write`, `*Invalid` for a `FieldError`, `POST /settings/reload` when running), `ServerTypes` (`catalog.LoadAll` over shipped, data and custom file with source and problem), `AddServerType`, `RemoveServerType` (reload when running).

- [ ] 10-09 Bridge: cloud status and control
  - Wave: 2 | Depends on: 10-03 | Review: alone
  - Requirements: UI-01 | Tests: internal/bridge/cloud_test.go
  - Reads: ARCHITECTURE "system integration", "hlab (management command)" (status file, incompatible state); DECISIONS "Tray connection banner and log stream reconnect", "Tray quit and tray state in e2e"; EXPERIMENTS E10
  - Size: ~300 lines in 2 files
  - [ ] internal/bridge/cloud.go: `Cloud` poller (SCM state every interval, `/status` while running, `cloudstatus.Read` while stopped; `ConnectionLost` after three failed polls while running, cleared by the next answer), `Start(discard)` (`discard-state` parameter), `Stop`, `OnChange` on every change, `Tray(State) TrayModel`.

- [ ] 10-10 Bridge: log stream
  - Wave: 2 | Depends on: 10-03 | Review: alone
  - Requirements: UI-06 | Tests: internal/bridge/logs_test.go
  - Reads: ARCHITECTURE "Management API" (`GET /logs`); DECISIONS "Tray connection banner and log stream reconnect", "Small phase 09 implementation choices"; RESEARCH topic 3 (batched events)
  - Size: ~250 lines in 2 files
  - [ ] internal/bridge/logs.go: `Logs.Follow(on)`: while running `Stream("/logs?follow=1")`, while stopped `logfile.Tail` on `LogDir`; batches lines per `Batch`; a broken stream reconnects every `Interval` and continues after the last emitted line (matched in the replayed 1000) without duplicates; switches to the files at stop and back to the stream at start.

- [ ] 10-11 Frontend shell
  - Wave: 2 | Depends on: 10-05, 10-06 | Review: alone
  - Requirements: UI-01, UI-08 | Tests: - (UI-01 e2e in 10-14)
  - Reads: UI-SPEC "Views" (Shell, Tray menu), "Behaviour"; RESEARCH topic 3 (no history routing); DECISIONS "Tray connection banner and log stream reconnect"
  - Size: ~380 lines in 8 files
  - [ ] ui/frontend/src/App.svelte, lib/Sidebar.svelte (project switcher from `GET /projects`, kept in localStorage, disabled while stopped; app sections Projects, Server Types, Settings, Logs), lib/TopBar.svelte (status pill and Start/Stop through `Cloud`), lib/Banner.svelte (connection lost, refused with "Discard state and start" through ConfirmDialog, failed start), stores in src/state.ts (cloud state from the `cloud` event, active view in `location.hash`).
  - [ ] Placeholder view files src/views/Projects.svelte, ServerTypes.svelte, Settings.svelte, Logs.svelte, each with its title row, so the view plans only replace their own file.

- [ ] 10-12 Help texts and panel
  - Wave: 2 | Depends on: 10-05 | Review: alone
  - Requirements: UI-07, DOC-05 | Tests: ui/frontend/tests/unit/help.test.ts
  - Reads: UI-SPEC "Building blocks" (Help button); SPEC "Documentation" D5; RESEARCH topic 6; test/docs/docs_test.go DOC-05
  - Size: ~220 lines in 9 files
  - [ ] ui/frontend/src/help.ts (`renderHelp`, `helpFor`), src/helpTexts.ts (`import.meta.glob("./help/*.md", { query: "?raw", import: "default", eager: true })`), lib/HelpButton.svelte with the side panel.
  - [ ] src/views.json with `projects`, `server-types`, `settings`, `logs`; a short help text per view in src/help/<view>.md (the documentation plan completes them).

- [ ] 10-13 Tray program
  - Wave: 3 | Depends on: 10-05, 10-08, 10-09, 10-10 | Review: alone
  - Requirements: UI-01 | Tests: cmd/hlab-tray/options_test.go
  - Reads: ARCHITECTURE "hlab-tray (tray program)", "Profiles"; RESEARCH topics 1, 2, 3, 4; EXPERIMENTS E9; DECISIONS "Tray UI tests through a CDP port in an e2e-only build", "Tray quit and tray state in e2e"
  - Size: ~350 lines in 7 files
  - [ ] go.mod/go.sum: `github.com/wailsapp/wails/v3` v3.1.0 (the indirect klauspost/compress rises to v1.18.7; nothing else in SPEC changes).
  - [ ] cmd/hlab-tray/main.go, options.go (application options: services `bridge.Service`, `bridge.Cloud`, `bridge.Logs`; assets `ui.Dist` through `BundledAssetFileServer`; single instance with UniqueID per profile, focusing the window; window 1000x640 minimum, shown at start, hidden on close with a `quitting` flag; `DisableQuitOnLastWindowClosed`; `application.New` first, so a second instance does nothing before it exits).
  - [ ] cmd/hlab-tray/tray.go (icon per `TrayModel.Icon`, tooltip, menu status line, Open hlab, Start cloud / Stop cloud, Quit only the tray; `OnClick` shows and focuses; `Cloud.OnChange` updates tray and emits `cloud`; `Logs.Emit` emits `logs`), icons/*.ico (running, stopped, busy, error; 16 to 48 px).
  - [ ] cmd/hlab-tray/e2e.go (`//go:build e2e`): own UniqueID, `--remote-debugging-port` and `WebviewUserDataPath` from `HLAB_E2E_CDP_PORT`/`HLAB_E2E_WEBVIEW_DIR`, service `TrayTest` with `State() TrayModel` and `Click(item)` calling the menu handlers.

- [ ] 10-14 E2E harness: tray over CDP and UI-01
  - Wave: 4 | Depends on: 10-11, 10-13 | Review: alone
  - Requirements: UI-01 | Tests: test/e2e/ui_test.go, ui/frontend/tests/e2e/tray.spec.ts
  - Reads: RESEARCH topic 5; EXPERIMENTS E9; test/e2e/main_test.go (setup, buildAll, waitFor), test/e2e/e2e_test.go (order); test/e2e/catalog_test.go (hcloud context checks)
  - Size: ~350 lines in 5 files
  - [ ] Early check first: build the e2e tray, start it, connect with `chromium.connectOverCDP("http://127.0.0.1:<port>")`, find the `wails.localhost` page and evaluate a bound call through `/wails/runtime.js`; if this fails, stop the plan and report.
  - [ ] test/e2e/main_test.go/ui_test.go: build `hlab-tray` with `-tags e2e` once, start it lazily for all UI rows with a free port and a per-run user data folder, end it at teardown; one Go subtest per UI row running `npx playwright test --project e2e --grep "<ID>"` in ui/frontend (logged start and end, deadline); the UI rows run alone after the LIFE rows; extract the Hetzner token helper of testCAT03 for UI-04.
  - [ ] ui/frontend/tests/e2e/fixtures.ts (worker-scoped CDP connection, existing page, reload per test) and the UI-01 spec.
  - Adjusts: test/e2e/catalog_test.go (uses the extracted token helper), test/e2e/e2e_test.go (UI rows in the serial part)

- [ ] 10-15 Projects view
  - Wave: 5 | Depends on: 10-11, 10-12, 10-14 | Review: alone
  - Requirements: UI-05 | Tests: ui/frontend/tests/e2e/projects.spec.ts
  - Reads: UI-SPEC "Views" (Projects), "Building blocks"; ARCHITECTURE "Management API" (projects, tokens, actions); DECISIONS "Deleting a project needs its name typed"
  - Size: ~330 lines in 3 files
  - [ ] src/views/Projects.svelte (table with resource counts, create form, delete with typed name and the deletion action as a progress toast via `/actions/{id}`), src/views/ProjectDetail.svelte (token table, "Create token" dialog with read-only switch showing the value once in a CopyField with a warning, revoke with confirm).

- [ ] 10-16 Settings view
  - Wave: 5 | Depends on: 10-07, 10-08, 10-11, 10-12, 10-14 | Review: alone
  - Requirements: UI-03 | Tests: ui/frontend/tests/e2e/settings.spec.ts
  - Reads: UI-SPEC "Views" (Settings); ARCHITECTURE "Files and formats" (Settings); decisions/managed-certificates.md (risk text); CONTEXT (stop_behaviour hint)
  - Size: ~300 lines in 2 files
  - [ ] src/views/Settings.svelte: grouped form for every key, errors under the field from `*Invalid`, hints (stop_behaviour as in CONTEXT; public_range, data_folder and reserves "Applies at the next cloud start"; data_folder "Existing data is not moved"); trust switch with risk text through `PUT /trust` and "Save CA certificate" from `GET /ca.pem`, both only while running.

- [ ] 10-17 Server Types view
  - Wave: 5 | Depends on: 10-08, 10-11, 10-12, 10-14 | Review: alone
  - Requirements: UI-04 | Tests: ui/frontend/tests/e2e/servertypes.spec.ts
  - Reads: UI-SPEC "Views" (Server Types); ARCHITECTURE "Management API" (`POST /catalog/update`); DECISIONS "Server Types view shows the catalog while the cloud is stopped"
  - Size: ~300 lines in 2 files
  - [ ] src/views/ServerTypes.svelte: table (name, cores, memory, disk, CPU type, locations, source), problem banner, add own type form (errors under fields), remove with confirm, "Update from Hetzner" password field (only while running; cleared after success and failure; result or Hetzner's message as toast).

- [ ] 10-18 Logs view
  - Wave: 5 | Depends on: 10-10, 10-11, 10-12, 10-14 | Review: alone
  - Requirements: UI-06 | Tests: ui/frontend/tests/e2e/logs.spec.ts
  - Reads: UI-SPEC "Views" (Logs); ARCHITECTURE "Files and formats" (hlabd.log)
  - Size: ~200 lines in 2 files
  - [ ] src/views/Logs.svelte: monospace list of parsed JSON lines (time, level, msg, fields), at most 5000 kept, level filter, follow toggle (`Logs.Follow`, scroll to the end), `logs` events.

- [ ] 10-19 Documentation
  - Wave: 6 | Depends on: 10-15, 10-16, 10-17, 10-18 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: test/docs/docs_test.go
  - Reads: SPEC "Documentation"; REQUIREMENTS "Documentation (DOC)"; DECISIONS phase 10 entries
  - Size: ~250 lines in 9 files
  - [ ] README.md (tray program: start, status, start/stop, quit, settings, server types, projects and tokens, logs; the "no tray program" line goes, the installer stays missing), docs/user/configuration.md (server-types.custom.json created by hlabd and written by the tray, when each setting applies), docs/dev/build-and-test.md (`task test:frontend`, tray UI tests over CDP, e2e build), docs/dev/components.md (bridge, tray, frontend).
  - [ ] The help texts ui/frontend/src/help/{projects,server-types,settings,logs}.md naming only existing views and buttons.
