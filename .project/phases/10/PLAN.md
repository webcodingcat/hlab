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

- [x] 10-01 Catalog: own server types add, remove and reload
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-04 | Tests: internal/catalog/catalog_test.go
  - Reads: ARCHITECTURE "Files and formats"; DECISIONS "Tray writes server-types.custom.json directly", "Server Types view shows the catalog while the cloud is stopped"; OpenAPI `server_type` schema (`/server_types` response) in the reference
  - Size: ~250 lines in 2 files
  - [x] internal/catalog/custom.go: `Own`, `ReloadOwn` (under `c.mu`, sets or clears the own-types warning), `OwnSpec`, `FieldError`, `AddOwn` (field checks, collisions, next free ID, object in Hetzner shape, in-place single write), `RemoveOwn`.

- [x] 10-02 Servers: stored server type when the own type is gone
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-04 | Tests: internal/resources/servers/servers_test.go
  - Reads: DECISIONS "Existing servers outlive a removed own server type"; CONTEXT "Corrections"
  - Size: ~120 lines in 4 files
  - [x] One helper (internal/resources/servers/servers.go or a new types.go) that returns the catalog type or, when missing, cores, memory and disk from the server's stored `server_type`; power.go, rebuild.go and reconcile.go use it; create and change_type keep requiring the catalog.

- [x] 10-03 Shared management client and status file
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-01 | Tests: - (behaviour unchanged; internal/cli/cli_test.go keeps passing)
  - Reads: ARCHITECTURE "hlab (management command)", "Management API (named pipe)"; internal/cli/cli.go `mgmt`, `send`, `readStatusFile`
  - Size: ~250 lines in 5 files
  - [x] New internal/mgmtclient (`Client`, `Do`, `Stream`, `ErrNotRunning`) and internal/cloudstatus (`File`, `Problem`, `Read`) from the code in internal/cli/cli.go.
  - [x] internal/cli/cli.go and cmd/hlab/main.go use them; no output change.

- [x] 10-04 Settings: field errors, in-place write, exported ACL copy
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-03 | Tests: internal/settings/settings_test.go
  - Reads: ARCHITECTURE "Files and formats" (Settings); DECISIONS "Tray settings writes", "settings.Save rewrites the file"
  - Size: ~180 lines in 4 files
  - [x] internal/settings/settings.go: json tags equal to the toml keys, `FieldError`, `Validate` (the existing rules returning the key), `Write` (validate, reread `trust_ca` from the file, encode, one in-place write that keeps the file's ACL); `Load` keeps its messages.
  - [x] internal/settings/security_windows.go, security_other.go: export `CopySecurity`; `Save` uses it.

- [x] 10-05 Frontend tooling, embed and bridge wrapper
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-01 | Tests: - (used by all frontend tests)
  - Reads: RESEARCH topics 3 and 6; SPEC "Stack"; Taskfile.yml
  - Size: ~180 lines in 9 files
  - [x] ui/frontend/package.json (`@wailsio/runtime` 3.1.0 exact; scripts `test:unit`, `test:e2e`), tsconfig.json (`"types": ["svelte", "vite/client"]`), playwright.config.ts (projects `unit` = tests/unit without browser, `e2e` = tests/e2e, `workers: 1`, `fullyParallel: false`), Taskfile.yml task `test:frontend` (`npm run test:unit`).
  - [x] ui/embed.go (`package ui`, `//go:embed all:frontend/dist`, `var Dist embed.FS`); committed placeholder `ui/frontend/dist/embed-placeholder.txt` and `ui/frontend/public/embed-placeholder.txt` (Vite copies it back after `emptyOutDir`), .gitignore `ui/frontend/dist/*` with `!ui/frontend/dist/embed-placeholder.txt`, so `go vet`/`go test`/lint work on a fresh checkout.
  - [x] ui/frontend/src/lib/bridge.ts as in "Interfaces" (`Call.ByName`, `Events.On`, Hetzner and management error objects to `ApiError`).

- [x] 10-06 Frontend building blocks
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-08 | Tests: - (manual UI-08; used by the view tests)
  - Reads: UI-SPEC "Foundation", "Colours", "Typography", "Spacing and grid", "Building blocks", "Behaviour", "Language of the interface"
  - Size: ~400 lines in 9 files
  - [x] ui/frontend/src/theme.css (custom properties light/dark), lib/Button.svelte, lib/StatusPill.svelte, lib/CopyField.svelte.
  - [x] lib/Table.svelte (header, count, search, sortable columns, skeleton rows, empty state, row menu), lib/Field.svelte (label, inline error), lib/ConfirmDialog.svelte (typed name option, Escape closes), lib/Toast.svelte with lib/toast.ts (action progress, errors stay until closed).

- [x] 10-07 hlabd: settings reload, own types file, log followers at stop
  - Wave: 2 | Depends on: 10-01, 10-04 | Review: alone
  - Requirements: UI-03, UI-04, LIFE-09 | Tests: internal/cloud/reload_test.go, internal/mgmt/mgmt_test.go
  - Reads: ARCHITECTURE "Management API (named pipe)", "Cloud start and stop"; DECISIONS "Tray writes server-types.custom.json directly", "Changed stop_behaviour reaches VMs at the next cloud start"; BACKLOG item "log followers"; CONTEXT
  - Size: ~250 lines in 5 files
  - [x] internal/mgmt/mgmt.go: `Deps.Reload` with `POST /settings/reload`; `Deps.Done` ends the follow of `GET /logs` (backlog bug: no 10 s wait at stop).
  - [x] internal/cloud (cloud.go, new reload.go): at start create `server-types.custom.json` with `[]` when missing and give it settings.toml's ACL via `settings.CopySecurity`; `reload` re-reads settings.toml (invalid file: error, nothing changed), sets `logging.level`, calls `cat.ReloadOwn` and refreshes the `custom_server_types` problem; stop_behaviour is already re-read at stop; public_range, data_folder and the reserves apply at the next start; close `Done` before `mgmtSrv.Shutdown`.

- [x] 10-08 Bridge: management calls, settings, server types
  - Wave: 2 | Depends on: 10-01, 10-03, 10-04 | Review: alone
  - Requirements: UI-03, UI-04 | Tests: internal/bridge/service_test.go
  - Reads: ARCHITECTURE "hlab-tray (tray program)", "Files and formats"; RESEARCH topic 3 (argument and result shapes); DECISIONS "Tray settings writes", "Tray writes server-types.custom.json directly"
  - Size: ~300 lines in 2 files
  - [x] internal/bridge/service.go: `Service` with `Call` (string body, `Response`), `Settings`, `SaveSettings` (`settings.Write`, `*Invalid` for a `FieldError`, `POST /settings/reload` when running), `ServerTypes` (`catalog.LoadAll` over shipped, data and custom file with source and problem), `AddServerType`, `RemoveServerType` (reload when running).

- [x] 10-09 Bridge: cloud status and control
  - Wave: 2 | Depends on: 10-03 | Review: alone
  - Requirements: UI-01 | Tests: internal/bridge/cloud_test.go
  - Reads: ARCHITECTURE "system integration", "hlab (management command)" (status file, incompatible state); DECISIONS "Tray connection banner and log stream reconnect", "Tray quit and tray state in e2e"; EXPERIMENTS E10
  - Size: ~300 lines in 2 files
  - [x] internal/bridge/cloud.go: `Cloud` poller (SCM state every interval, `/status` while running, `cloudstatus.Read` while stopped; `ConnectionLost` after three failed polls while running, cleared by the next answer), `Start(discard)` (`discard-state` parameter), `Stop`, `OnChange` on every change, `Tray(State) TrayModel`.

- [x] 10-10 Bridge: log stream
  - Wave: 2 | Depends on: 10-03 | Review: alone
  - Requirements: UI-06 | Tests: internal/bridge/logs_test.go
  - Reads: ARCHITECTURE "Management API" (`GET /logs`); DECISIONS "Tray connection banner and log stream reconnect", "Small phase 09 implementation choices"; RESEARCH topic 3 (batched events)
  - Size: ~250 lines in 2 files
  - [x] internal/bridge/logs.go: `Logs.Follow(on)`: while running `Stream("/logs?follow=1")`, while stopped `logfile.Tail` on `LogDir`; batches lines per `Batch`; a broken stream reconnects every `Interval` and continues after the last emitted line (matched in the replayed 1000) without duplicates; switches to the files at stop and back to the stream at start.

- [x] 10-11 Frontend shell
  - Wave: 2 | Depends on: 10-05, 10-06 | Review: alone
  - Requirements: UI-01, UI-08 | Tests: - (UI-01 e2e in 10-14)
  - Reads: UI-SPEC "Views" (Shell, Tray menu), "Behaviour"; RESEARCH topic 3 (no history routing); DECISIONS "Tray connection banner and log stream reconnect"
  - Size: ~380 lines in 8 files
  - [x] ui/frontend/src/App.svelte, lib/Sidebar.svelte (project switcher from `GET /projects`, kept in localStorage, disabled while stopped; app sections Projects, Server Types, Settings, Logs), lib/TopBar.svelte (status pill and Start/Stop through `Cloud`), lib/Banner.svelte (connection lost, refused with "Discard state and start" through ConfirmDialog, failed start), stores in src/state.ts (cloud state from the `cloud` event, active view in `location.hash`).
  - [x] Placeholder view files src/views/Projects.svelte, ServerTypes.svelte, Settings.svelte, Logs.svelte, each with its title row, so the view plans only replace their own file.

- [x] 10-12 Help texts and panel
  - Wave: 2 | Depends on: 10-05 | Review: alone
  - Requirements: UI-07, DOC-05 | Tests: ui/frontend/tests/unit/help.test.ts
  - Reads: UI-SPEC "Building blocks" (Help button); SPEC "Documentation" D5; RESEARCH topic 6; test/docs/docs_test.go DOC-05
  - Size: ~220 lines in 9 files
  - [x] ui/frontend/src/help.ts (`renderHelp`, `helpFor`), src/helpTexts.ts (`import.meta.glob("./help/*.md", { query: "?raw", import: "default", eager: true })`), lib/HelpButton.svelte with the side panel.
  - [x] src/views.json with `projects`, `server-types`, `settings`, `logs`; a short help text per view in src/help/<view>.md (the documentation plan completes them).

- [x] 10-13 Tray program
  - Wave: 3 | Depends on: 10-05, 10-08, 10-09, 10-10 | Review: alone
  - Requirements: UI-01 | Tests: cmd/hlab-tray/options_test.go
  - Reads: ARCHITECTURE "hlab-tray (tray program)", "Profiles"; RESEARCH topics 1, 2, 3, 4; EXPERIMENTS E9; DECISIONS "Tray UI tests through a CDP port in an e2e-only build", "Tray quit and tray state in e2e"
  - Size: ~350 lines in 7 files
  - [x] go.mod/go.sum: `github.com/wailsapp/wails/v3` v3.1.0 (the indirect klauspost/compress rises to v1.18.7; nothing else in SPEC changes).
  - [x] cmd/hlab-tray/main.go, options.go (application options: services `bridge.Service`, `bridge.Cloud`, `bridge.Logs`; assets `ui.Dist` through `BundledAssetFileServer`; single instance with UniqueID per profile, focusing the window; window 1000x640 minimum, shown at start, hidden on close with a `quitting` flag; `DisableQuitOnLastWindowClosed`; `application.New` first, so a second instance does nothing before it exits).
  - [x] cmd/hlab-tray/tray.go (icon per `TrayModel.Icon`, tooltip, menu status line, Open hlab, Start cloud / Stop cloud, Quit only the tray; `OnClick` shows and focuses; `Cloud.OnChange` updates tray and emits `cloud`; `Logs.Emit` emits `logs`), icons/*.ico (running, stopped, busy, error; 16 to 48 px).
  - [x] cmd/hlab-tray/e2e.go (`//go:build e2e`): own UniqueID, `--remote-debugging-port` and `WebviewUserDataPath` from `HLAB_E2E_CDP_PORT`/`HLAB_E2E_WEBVIEW_DIR`, service `TrayTest` with `State() TrayModel` and `Click(item)` calling the menu handlers.

- [x] 10-14 E2E harness: tray over CDP and UI-01
  - Wave: 4 | Depends on: 10-11, 10-13 | Review: alone
  - Requirements: UI-01 | Tests: test/e2e/ui_test.go, ui/frontend/tests/e2e/tray.spec.ts
  - Reads: RESEARCH topic 5; EXPERIMENTS E9; test/e2e/main_test.go (setup, buildAll, waitFor), test/e2e/e2e_test.go (order); test/e2e/catalog_test.go (hcloud context checks)
  - Size: ~350 lines in 5 files
  - [x] Early check first: build the e2e tray, start it, connect with `chromium.connectOverCDP("http://127.0.0.1:<port>")`, find the `wails.localhost` page and evaluate a bound call through `/wails/runtime.js`; if this fails, stop the plan and report.
  - [x] test/e2e/main_test.go/ui_test.go: build `hlab-tray` with `-tags e2e` once, start it lazily for all UI rows with a free port and a per-run user data folder, end it at teardown; one Go subtest per UI row running `npx playwright test --project e2e --grep "<ID>"` in ui/frontend (logged start and end, deadline); the UI rows run alone after the LIFE rows; extract the Hetzner token helper of testCAT03 for UI-04.
  - [x] ui/frontend/tests/e2e/fixtures.ts (worker-scoped CDP connection, existing page, reload per test) and the UI-01 spec.
  - Adjusts: test/e2e/catalog_test.go (uses the extracted token helper), test/e2e/e2e_test.go (UI rows in the serial part)

- [x] 10-15 Projects view
  - Wave: 5 | Depends on: 10-11, 10-12, 10-14 | Review: alone
  - Requirements: UI-05 | Tests: ui/frontend/tests/e2e/projects.spec.ts
  - Reads: UI-SPEC "Views" (Projects), "Building blocks"; ARCHITECTURE "Management API" (projects, tokens, actions); DECISIONS "Deleting a project needs its name typed"
  - Size: ~330 lines in 3 files
  - [x] src/views/Projects.svelte (table with resource counts, create form, delete with typed name and the deletion action as a progress toast via `/actions/{id}`), src/views/ProjectDetail.svelte (token table, "Create token" dialog with read-only switch showing the value once in a CopyField with a warning, revoke with confirm).

- [x] 10-16 Settings view
  - Wave: 5 | Depends on: 10-07, 10-08, 10-11, 10-12, 10-14 | Review: alone
  - Requirements: UI-03 | Tests: ui/frontend/tests/e2e/settings.spec.ts
  - Reads: UI-SPEC "Views" (Settings); ARCHITECTURE "Files and formats" (Settings); decisions/managed-certificates.md (risk text); CONTEXT (stop_behaviour hint)
  - Size: ~300 lines in 2 files
  - [x] src/views/Settings.svelte: grouped form for every key, errors under the field from `*Invalid`, hints (stop_behaviour as in CONTEXT; public_range, data_folder and reserves "Applies at the next cloud start"; data_folder "Existing data is not moved"); trust switch with risk text through `PUT /trust` and "Save CA certificate" from `GET /ca.pem`, both only while running.

- [x] 10-17 Server Types view
  - Wave: 5 | Depends on: 10-08, 10-11, 10-12, 10-14 | Review: alone
  - Requirements: UI-04 | Tests: ui/frontend/tests/e2e/servertypes.spec.ts
  - Reads: UI-SPEC "Views" (Server Types); ARCHITECTURE "Management API" (`POST /catalog/update`); DECISIONS "Server Types view shows the catalog while the cloud is stopped"
  - Size: ~300 lines in 2 files
  - [x] src/views/ServerTypes.svelte: table (name, cores, memory, disk, CPU type, locations, source), problem banner, add own type form (errors under fields), remove with confirm, "Update from Hetzner" password field (only while running; cleared after success and failure; result or Hetzner's message as toast).

- [x] 10-18 Logs view
  - Wave: 5 | Depends on: 10-10, 10-11, 10-12, 10-14 | Review: alone
  - Requirements: UI-06 | Tests: ui/frontend/tests/e2e/logs.spec.ts
  - Reads: UI-SPEC "Views" (Logs); ARCHITECTURE "Files and formats" (hlabd.log)
  - Size: ~200 lines in 2 files
  - [x] src/views/Logs.svelte: monospace list of parsed JSON lines (time, level, msg, fields), at most 5000 kept, level filter, follow toggle (`Logs.Follow`, scroll to the end), `logs` events.

- [x] 10-19 Documentation
  - Wave: 6 | Depends on: 10-15, 10-16, 10-17, 10-18 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: test/docs/docs_test.go
  - Reads: SPEC "Documentation"; REQUIREMENTS "Documentation (DOC)"; DECISIONS phase 10 entries
  - Size: ~250 lines in 9 files
  - [x] README.md (tray program: start, status, start/stop, quit, settings, server types, projects and tokens, logs; the "no tray program" line goes, the installer stays missing), docs/user/configuration.md (server-types.custom.json created by hlabd and written by the tray, when each setting applies), docs/dev/build-and-test.md (`task test:frontend`, tray UI tests over CDP, e2e build), docs/dev/components.md (bridge, tray, frontend).
  - [x] The help texts ui/frontend/src/help/{projects,server-types,settings,logs}.md naming only existing views and buttons.

## Findings from manual testing

- [ ] bug (UI-08): no application icon. The user started `bin\hlab-tray.exe` and opened the window: its title bar shows the generic Windows placeholder icon next to the title "hlab" (screenshot `phases/10/feedback/window-title-no-icon.png`: a small grey-green default window glyph, then the text "hlab"). The taskbar button and `bin\hlab-tray.exe` in Explorer have no icon of their own either. Cause: only the four tray state icons exist (`cmd/hlab-tray/icons/{stopped,busy,running,error}.ico`); neither `application.Options`/the window options in `cmd/hlab-tray/options.go` and `tray.go` set an icon, and the build embeds no Windows resource. Expected: one application icon in the style of the tray icons (neutral, without a state colour), multi-size `.ico` (16, 20, 24, 32, 48, 256 px), clear on light and dark backgrounds, shown in the window title bar, the taskbar and Alt+Tab, and embedded as the EXE resource of `hlab-tray.exe` through `task build` (also `hlab.exe` and `hlabd.exe` if it costs nothing extra). No Hetzner logo (UI-SPEC "Foundation").
- [ ] change (UI-08, user decision 2026-11-19): the product is named "HLab" instead of "hlab" in every user-facing text, as the new rule in UI-SPEC "Language of the interface" says. This covers the window title (`cmd/hlab-tray/tray.go` `Title`, `ui/frontend/index.html` `<title>`), the application name and error dialog title (`options.go`, `tray.go`), the tray tooltip "HLab: <status>" and the menu item "Open HLab" (`internal/bridge/cloud.go`, tray menu), view texts such as "Trust the HLab CA" (`ui/frontend/src/views/Settings.svelte`), the help texts in `ui/frontend/src/help/`, API error messages (SPEC.md now says `server console is not supported by HLab yet` and `vswitch subnets are not supported by HLab`) and other API/CLI messages that name the product, README.md and the prose of `docs/`. Program, command, service, file, folder, module, hcloud context and identifier names stay lowercase (`hlab.exe`, `hlabd`, `hlab-tray`, `hlab-dev`, `%LocalAppData%\hlab`, `hlab-ca.pem`, `hlab.project` storage key, `github.com/webcodingcat/hlab`). Tests that compare these texts follow the changed wording; CHECKS.md UI-08 steps then read "HLab".
- [ ] change (UI-08, user decision 2026-11-19): the tray menu has one cloud item instead of "Start cloud" and "Stop cloud". Today (screenshot `phases/10/feedback/tray-menu-two-items.png`: dark menu with the disabled status line "Cloud running", then "Open hlab", a greyed-out "Start cloud", an enabled "Stop cloud", a separator and "Quit") one of the two items is always greyed out. Expected, as UI-SPEC "Views" now says: the item reads "Start cloud" and starts the cloud while it is stopped (also after a failed or refused start), "Stop cloud" and stops it while it runs (also with the connection lost), and is disabled with "Starting cloud…", "Stopping cloud…" or "Checking cloud…" while it starts, stops or the state is unknown. Code: `bridge.TrayModel`/`bridge.Tray` in `internal/bridge/cloud.go` (CanStart/CanStop become one label plus action), the menu in `cmd/hlab-tray/tray.go` (`labelStart`, `labelStop`, `t.start`, `t.stop`, `click`). Tests of UI-01 that address the two items follow the new item; CHECKS.md UI-08 steps 2 and 4 describe the one item.
- [ ] change (UI-08, user decision 2026-11-19): the tray icon shows the application icon and the cloud state. Today (screenshot `phases/10/feedback/tray-icon-plain-dot.png`: dark taskbar notification area, the tray icon is only a plain filled green circle while the cloud runs) the four icons `cmd/hlab-tray/icons/{running,busy,stopped,error}.ico` are coloured circles without the product. Expected, as UI-SPEC "Views" now says: each of the four is the new application icon (first finding) with a status dot in the lower-right corner, about a third of the icon wide with a 1 px contrasting rim, coloured with the UI-SPEC role colours (running: success, busy: warning, stopped: text muted, error: error); multi-size `.ico` (16, 20, 24, 32 px), recognisable on light and dark taskbars. The icon keys and `bridge.Tray` stay as they are; only the icon files change.
- [ ] change (UI-08, user decision 2026-11-20): rebuild the shell like the Hetzner Cloud Console, as UI-SPEC "Views" (Shell, Cloud stopped, Projects) now says. Today (screenshots `phases/10/feedback/sidebar-project-switcher.png` and `phases/10/feedback/window-single-sidebar.webp`: one window "hlab"; sidebar with the label "Project" and a select "docs" on top, then Projects (active), Server Types, Settings, Logs; top bar with the pill "Running" left and "Stop cloud" right; content "Projects ?" with "Projects 70", search field, "Create project" and a table Name / Resources / Created with row menus "…") Projects, Server Types, Settings and Logs share one sidebar with a project switcher that has no visible effect. Expected: (1) top bar on every page with the application icon and "HLab" (click: project list), breadcrumb, status pill, Start/Stop button and a gear icon button labelled "Administration"; (2) start page = project list (the existing projects table, full width, no sidebar), a row click opens the project console; (3) project console with breadcrumb "Projects / <name> ▾" (the name opens a menu to switch project, "Projects" goes back) and a sidebar with this project's sections, in phase 10 only "API tokens" (the existing token table and "Create token" dialog of the project detail move there); the last opened project is remembered (the `project` store in `ui/frontend/src/state.ts`); (4) Administration with its own sidebar Server Types, Settings, Logs; (5) cloud stopped: project list and console show "Start the cloud to manage projects" with the Start button, Administration stays usable. The sidebar project switcher (`ui/frontend/src/lib/Sidebar.svelte`, `project-switcher`) goes. Views and hash routes follow the areas (for example `#/projects`, `#/projects/<id>/tokens`, `#/admin/server-types`, `#/admin/settings`, `#/admin/logs`); every view keeps its `?` help (UI-07; `views.json` and `ui/frontend/src/help/` follow, an "API tokens" help text is added if it becomes a view of its own). UI e2e tests of UI-03 to UI-06 adjust how they navigate, not what they check; CHECKS.md UI-08 steps 3, 5 to 10 follow the new navigation.
- [ ] bug (test harness, user decision 2026-11-20): every e2e run leaves its project behind. The user's dev instance listed 70 projects, almost all `e2e-<yyyymmdd-hhmmss>` with 1 resource each (screenshot `phases/10/feedback/window-single-sidebar.webp`). Cause: the harness creates the project `harness.runID` (`test/e2e/main_test.go`), but `teardown()` only deletes the servers (`deleteAllServers`), not the project with its SSH key and token. Expected: `teardown()` deletes the run's project and everything left in it after the rows ran (also after a failed row), and setup removes leftover projects of earlier runs whose name matches exactly `^e2e-\d{8}-\d{6}$`, so the 70 existing ones go with the next `task e2e`; no other project is touched. The shared helper for deleting a project is used, not copied.
- [ ] change (UI-06, UI-08, user decision 2026-11-20): search and older lines in the Logs view. Today (screenshot `phases/10/feedback/logs-view.png`: title "Logs ?", a "Level" select "All", a checked "Follow" box and "1000 of 1000 lines" on the right, below a monospace list of INFO lines such as `api request method=GET path=/v1/actions status=200`) the view shows only the last 1000 lines (`logLines` in `internal/bridge/logs.go`) and has no search; older lines in `hlabd.log` and the rotated `hlabd.log.1` .. `.5` (`internal/logfile`, 10 MB each) cannot be seen. Expected, as UI-SPEC "Views" now says: (1) a search field: case-insensitive text search in the service (bridge `Logs`, using `internal/logfile` to read the current and all rotated files) returning the matches newest first, capped at 1000, shown with the matched text highlighted and the count ("1000+ matches" when capped); follow switches off while a search is shown, clearing the field returns to the live view; the level filter also applies to the matches; (2) a "Load older lines" button above the list that prepends the 1000 lines before the oldest shown line, crossing into rotated files, disabled at the start of the oldest file; following keeps working at the end. Reading backwards must not load whole 10 MB files into the window; reading files concurrently with rotation must not fail (a rotated-away file is read from its new name or skipped). The logs help text names the two controls. Tests: UI-06 gets the search and the older-lines step at the cheapest level (logfile unit tests for search and paging across rotated files, one UI e2e case each).
- [ ] change (UI-04, UI-08, user decision 2026-11-20): the Hetzner token is asked for in a dialog. Today (screenshot `phases/10/feedback/server-types-token-field.png`: title "Server Types ?", below it a field "Hetzner token" with the hint "Used once for the update, never stored." and a disabled "Update from Hetzner" button next to it) the token field sits on the page. Expected, as UI-SPEC "Views" now says: the page shows only the "Update from Hetzner" button (enabled); a click opens a dialog (the shared dialog building block, Escape and "Cancel" close it) with the required password field "Hetzner token", the hint, and "Update" (loading state while the update runs, Enter submits); on success the dialog closes and the result toast appears, on an error the message stays in the dialog under the field; the token is cleared whenever the dialog closes and never stored. The UI-04 e2e step follows the dialog.
- [ ] change (UI-04, UI-08, user decision 2026-11-20): locations of an own server type are chosen, not typed. Today (screenshot `phases/10/feedback/add-server-type-locations.png`: dialog "Add server type" with Name (hint "Must not match a catalog type"), Description, Cores 2, Memory (GB) 4, Disk (GB) 40, CPU type "shared", and a free-text field "Locations" with the hint "Comma-separated location names; empty means all", then "Cancel" and "Add") the user has to type location names (`ui/frontend/src/views/ServerTypes.svelte`, `spec.locations` split at commas). Expected, as UI-SPEC "Views" now says: a checkbox list of the locations of the catalog the tray already loads (`catalog.Catalog.Locations`; available while the cloud is stopped), each labelled "<name> (<city>)", led by an "All locations" checkbox, checked by default, that disables the others and saves the empty list (all locations, as today); unchecked, at least one location is required (inline error under the list). The saved format of `server-types.custom.json` does not change. The UI-04 e2e step that adds an own type ticks a location instead of typing it.
- [ ] bug (UI-01, UI-08): the Start/Stop button flickers. The user clicked "Start cloud" in the top bar: the button became disabled with the spinner (screenshot `phases/10/feedback/start-button-loading.png`: the primary button greyed out with a spinner, the play icon and "Start cloud"), then for a moment returned to its normal enabled look, then became disabled with the spinner again. Cause: `run()` in `ui/frontend/src/lib/TopBar.svelte` clears `sending` as soon as `Cloud.Start` returns, which happens once the service manager has accepted the start (`internal/bridge/cloud.go` `Start`), while the recorded state stays "stopped" until the next `poll` reports "starting"; the same gap applies to Stop and to the tray menu item. Expected (UI-SPEC "Building blocks": loading keeps the spinner): from the click until the cloud reaches its next stable state the button stays in its loading state without a gap. Fix in the bridge, so window and tray agree: after a successful `Start` or `Stop` the `Cloud` records and emits "starting" or "stopping" at once (a later poll then moves it on; a failed call leaves the state unchanged and shows the error). A bridge unit test covers the state right after `Start` and `Stop`.
- [ ] bug (DOC-07, found during its manual check 2026-11-20): README.md mixes the user guide with the developer setup. Its section "Prerequisites and setup" (screenshot `phases/10/feedback/readme-dev-prerequisites.png`: "For developing hlab you need:" followed by Windows edition, Hyper-V and "Hyper-V Administrators", 16 GB RAM and 100 GB disk for the end-to-end tests, Go 1.27.4, Node.js, Task, golangci-lint, WiX Toolset, Wails, hcloud CLI, OpenTofu, WSL2 with uv, ansible-core and the collection, internet access) addresses contributors, while SPEC "Documentation" D1 makes README.md the guide for developers who test Hetzner setups and D6 puts tools and `dev-setup.ps1` into `docs/development/setup.md`. Expected: the build and test tool list (Go, Node.js, Task, golangci-lint, WiX, Wails, the e2e RAM and disk figures) and the steps `task build` and `scripts\dev-setup.ps1` move to `docs/development/setup.md` (merged with what it already says, no duplicate); README.md keeps "Prerequisites" with what using HLab needs (Windows edition with hardware virtualization, Hyper-V, WSL2 in NAT mode with dnsTunneling for access from WSL2, the clients one wants to use: hcloud, OpenTofu, Ansible with the collection, internet access for downloads) and an "Installation" section that says in one or two sentences that there is no installer yet and HLab is built from source and its service registered as described in `docs/development/setup.md` (link); the optional hcloud context for the real API stays where `task catalog` is described for contributors. DOC-04 (links) and DOC-06 (Taskfile commands) keep passing. CHECKS.md DOC-07 of phase 01 is checked again after the run.
- [ ] change (DOC-01..DOC-07, user decision 2026-11-20): new documentation layout. Today (screenshot `phases/10/feedback/docs-folders.png`: explorer tree `docs` with the folders `dev` (collapsed) and `user` containing `commands.md`, `configuration.md`, `deviations.md`) user and developer documents sit in `docs/user/` and `docs/dev/`. Expected, as SPEC "Documentation", REQUIREMENTS DOC-01..DOC-03 and IDEA now say: the user documents move to `docs/` itself (`docs/commands.md`, `docs/configuration.md`, `docs/deviations.md`) and the developer guide to `docs/development/` (`setup.md`, `build-and-test.md`, `components.md`, `adding-a-resource.md`), with `git mv` so history follows. Every reference follows: links in README.md and the documents, `test/docs/docs_test.go` paths, code comments and messages that name a document (e.g. `cmd/hlabd/main.go`, `internal/sysint/windows/install.go`, `internal/resources/servers/vendordata.go`, `internal/agent/*_linux.go`, `internal/labels/labels_test.go`, `test/e2e/{life,network,volume}_test.go`), the help texts, CLAUDE.md "Build and test commands" if it names a path, and the open backlog line in BACKLOG.md (already updated). DOC-04 (links) and DOC-06 keep passing.
- [ ] change (DOC-03, DOC-07, user decision 2026-11-20): the deviations page is too complex for users. Today `docs/user/deviations.md` (578 lines) starts with a table of every endpoint hlab serves, followed by sections per resource ("POST /servers", "Server actions", "Zones", ...) made of many bullet points that name routes; the user found it unreadable, too long and full of routes users do not care about. Expected, as SPEC "Documentation" D4/D6 and REQUIREMENTS DOC-03 now say: (1) the complete content (endpoint table, per-route deviations, every `unsupported_error`) moves to `docs/development/api-coverage.md` for contributors, and the DOC-03 test (`test/docs/docs_test.go`, `api.Deviations()`) checks that file; (2) `docs/deviations.md` is rewritten for users as short prose paragraphs grouped by topic (servers; images, snapshots, backups, rescue and ISOs; volumes; networks, primary and floating IPs; firewalls; DNS and reverse DNS; load balancers and certificates; projects, server types and prices; WSL2), saying what a user notices compared with Hetzner and what is not available, without routes or endpoint lists, bullet points only where a short list is clearer, about 150 lines at most, linking to `docs/development/api-coverage.md` for the details. README links that point at sections of the old page follow (DOC-04).
- [ ] change (DOC-07, user decision 2026-11-20): README.md is not attractive and too long (over 650 lines, plain "# hlab" title, no images, every resource in detail). Expected, as SPEC "Documentation" D1 now says: (1) a centred header: an SVG banner `docs/images/banner.svg` built from the new application icon (first finding) with "HLab" and the subtitle "A local Hetzner-compatible cloud on Hyper-V", readable on GitHub's light and dark theme; below it badges for the Go version and the platform (Windows) from shields.io (no licence badge: the repository has no LICENSE file), and a link line "Guide · Commands · Configuration · Deviations · Development"; (2) screenshots in `docs/images/`, each in light and dark mode and embedded with `<picture>` and `prefers-color-scheme`: the project list (start page with the top bar) and a project console with the "API tokens" section; they are generated from the running tray program through the existing CDP/Playwright setup of the UI e2e tests by a Taskfile target (for example `task docs:screenshots`, documented in `docs/development/build-and-test.md`) with a sample project and token, never real secrets (the token value is masked or from a throwaway project); (3) a terminal example as a code block: `hcloud server create ...` against HLab with its output, then `ssh root@<ip>`; (4) README shrinks to banner, what HLab is, screenshots, prerequisites, installation, first steps, tray program, WSL2, uninstallation and links; the sections per resource (servers, snapshots and backups, ISOs, volumes, networks, IPs, firewalls, DNS, load balancers and certificates, OpenTofu and Ansible, stopping and starting, profiles) move to `docs/guide.md` with working anchors. DOC-04 and DOC-06 keep passing.
- [ ] change (DOC-05, decision Q15 2026-11-20): the app takes its view list from `ui/frontend/src/views.json`, so the views the shell offers (`ui/frontend/src/state.ts` `views`, including the new areas of the shell rework) and the help texts have one source; the DOC-05 unit test (`test/docs`) keeps checking help file per view and view per help file. The clause "names only views and buttons that exist" is no longer a unit check: it moved to DOC-07 [manual]; CHECKS.md DOC-07 of phase 01 gets the step "every help text names only views and buttons that exist".

## Close

- Unit suite (`task test`), `task test:frontend` and `task lint` green on af3971a.
- End-to-end: all rows (`task e2e -- -v`) on af3971a, because the phase changed cloud start and stop, server power-on, rebuild and reconcile, and the shared harness (test/e2e/e2e_test.go). 59 rows passed; RDNS-02 failed with `423 locked` on the shared server (a parallel row's action), then passed 3 of 3 with `task e2e -- -run '^TestE2E$/^RDNS-02$' -v`; recorded as a backlog bug. Later commits changed only docs/user/configuration.md and planning documents.
- Manual: UI-08 in CHECKS.md.
