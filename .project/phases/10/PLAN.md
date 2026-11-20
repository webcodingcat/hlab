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

### Findings fixes (10-20 onward)

```go
// internal/bridge (changed): one cloud item instead of CanStart/CanStop.
type TrayModel struct {
	Icon        string `json:"icon"`         // running|stopped|busy|error (unchanged keys)
	Tooltip     string `json:"tooltip"`      // "HLab: <status>"
	Status      string `json:"status"`       // menu status line
	CloudItem   string `json:"cloud_item"`   // "Start cloud" | "Stop cloud" | "Starting cloud…" | "Stopping cloud…" | "Checking cloud…"
	CloudAction string `json:"cloud_action"` // "start" | "stop" | "" (item disabled)
}
// Start and Stop (unchanged signatures): after the service manager accepted
// the call the state is "starting" / "stopping" at once and OnChange fires;
// a poll that began before the call does not overwrite it; an error leaves
// the state unchanged.

// internal/bridge (additions)
type Location struct {
	Name string `json:"name"`
	City string `json:"city"`
}
func (s *Service) Locations() ([]Location, error) // catalog locations, also while stopped
type SearchResult struct {
	Lines  []string `json:"lines"`  // raw log lines, newest first
	Capped bool     `json:"capped"` // more than max matched
}
func (l *Logs) Search(query, minLevel string) (SearchResult, error) // logfile.Search with max 1000
type OlderResult struct {
	Lines   []string `json:"lines"`    // oldest first, the lines before the given one
	AtStart bool     `json:"at_start"` // the start of the oldest file is reached
}
func (l *Logs) Older(oldest string) (OlderResult, error) // logfile.Before with n 1000

// internal/logfile (additions). Both read hlabd.log, then name.1 .. name.Keep
// backwards in fixed-size chunks (never a whole file in memory); a file that
// is rotated away while reading is read on under its new name (os.SameFile)
// or skipped, never an error. Lines are returned without their newline.
// minLevel: "" all, else DEBUG < INFO < WARN < ERROR and above; a line without
// a known level always passes. The query matches case-insensitively the text
// the Logs view shows: level, msg and the key=value fields of a JSON line
// (time excluded), or the raw text of a line that is not JSON.
func Search(dir, name, query, minLevel string, max int) (lines []string, capped bool, err error) // newest first
// Before returns the n lines before the newest line equal to line, oldest
// first; a line not found returns no lines and atStart true.
func Before(dir, name, line string, n int) (lines []string, atStart bool, err error)

// internal/catalog (addition): Location gets City string `json:"city"`.

// tools/icons (new, `task icons`): renders the app icon (white "H" on a
// rounded square in the accent blue) and the four state icons (app icon with
// a status dot) and writes the committed outputs.
func Generate(root string) error // writes the files below under the repository root
// cmd/hlab-tray/icons/{app,running,busy,stopped,error}.ico,
// cmd/{hlab-tray,hlab,hlabd}/rsrc_windows_amd64.syso (group icon IDs 3 and 32512, github.com/tc-hib/winres),
// ui/frontend/src/assets/app-icon.svg, docs/images/banner.svg

// test/traylaunch (new, no build tag): starts the e2e tray build for the UI
// e2e tests and the screenshot tool (moved out of test/e2e/ui_test.go).
type Tray struct{ Port int /* CDP port */ }
func Start(ctx context.Context, exe, webviewDir string, env []string) (*Tray, error) // waits until CDP answers
func (t *Tray) Stop()
```

```ts
// ui/frontend/src/state.ts (changed)
export type ViewId = "projects" | "tokens" | "server-types" | "settings" | "logs"; // ids of views.json
export type Route =
  | { area: "projects" }
  | { area: "project"; project: string; view: "tokens" }
  | { area: "admin"; view: "server-types" | "settings" | "logs" };
export const route: Readable<Route>;          // from location.hash: #/projects, #/projects/<name>/tokens, #/admin/<view>
export function href(r: Route): string;
export const views: { id: ViewId; title: string; area: "projects" | "project" | "admin" }[]; // ids from views.json
export const lastProject: Writable<string>;   // localStorage "hlab.project"; the window opens its console when it exists

// ui/frontend/src/lib/Dialog.svelte: props open (bindable), title, children, onclose; Escape closes.
// ui/frontend/src/lib/SectionSidebar.svelte: props label, items: { href, title, icon, current }[].
```

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

Plans 10-20 onward fix the findings from manual testing (CONTEXT "Findings fixes"). At most two plans run at once and at most one of them runs UI e2e tests; a dependency marked "(order)" only sequences the waves.

- [x] 10-20 App icon, tray state icons and EXE resources
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-01, UI-08 | Tests: tools/icons/icons_test.go
  - Reads: UI-SPEC "Foundation", "Colours", "Views" (Tray icon); SPEC "Stack" (winres); RESEARCH "Findings fixes" F1, F2; CONTEXT "Findings fixes"; findings 1, 4
  - Size: ~380 lines in 5 files plus generated files
  - [x] go.mod/go.sum: `github.com/tc-hib/winres` v0.3.1. tools/icons/main.go and icons.go: `Generate` draws the app icon (white "H" on a rounded square in the accent blue, supersampled anti-aliasing, standard library `image`) at 16, 20, 24, 32, 48, 256 px and the four state icons at 16, 20, 24, 32 px (app icon plus a lower-right dot about a third wide with a 1 px contrasting rim: running success, busy warning, stopped text muted, error error, light-mode values), encodes ICO (PNG frame for 256), writes the `.syso` per main package with the group icon under IDs 3 and 32512 through winres, and writes `ui/frontend/src/assets/app-icon.svg` and `docs/images/banner.svg` (icon, "HLab", subtitle "A local Hetzner-compatible cloud on Hyper-V", readable on light and dark through `prefers-color-scheme` inside the SVG) from the same geometry.
  - [x] Taskfile.yml task `icons` (`go run ./tools/icons`); run it once and commit cmd/hlab-tray/icons/*.ico, cmd/{hlab-tray,hlab,hlabd}/rsrc_windows_amd64.syso, the two SVGs; `task build` links the `.syso` without running the tool.

- [x] 10-24 Log files: search and older lines
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-06 | Tests: internal/logfile/logfile_test.go
  - Reads: UI-SPEC "Views" (Logs); ARCHITECTURE "Files and formats" (hlabd.log); PLAN "Interfaces" (logfile); finding 7
  - Size: ~280 lines in 2 files
  - [x] internal/logfile/search.go: a backward line reader over hlabd.log and its rotated files in 64 KiB chunks (open with `openRead`, files identified with `os.SameFile` so a rotation during the read continues under the new name or skips the file), `Search` (match text as in "Interfaces", level filter, newest first, stops after max+1 with `capped`) and `Before`.

- [x] 10-23 Tray: one cloud item, state at once after Start/Stop, name HLab in the window
  - Wave: 2 | Depends on: 10-20 | Review: alone
  - Requirements: UI-01, UI-08 | Tests: internal/bridge/cloud_test.go, ui/frontend/tests/e2e/fixtures.ts, ui/frontend/tests/e2e/tray.spec.ts
  - Reads: UI-SPEC "Views" (Tray icon, Tray menu), "Building blocks" (Button loading), "Language of the interface"; RESEARCH topic 1 (menu updates); DECISIONS "One Start/Stop item in the tray menu", "Product name HLab and an app icon"; findings 2, 3, 10
  - Size: ~250 lines in 6 files
  - Adjusts: ui/frontend/tests/e2e/settings.spec.ts (switch label "Trust the HLab CA")
  - [x] internal/bridge/cloud.go: `TrayModel` with `CloudItem`/`CloudAction` and tooltip "HLab: <status>" as in "Interfaces"; `Start`/`Stop` record and emit "starting"/"stopping" on success, with a poll generation counter so an older poll result is dropped.
  - [x] cmd/hlab-tray/tray.go (one cloud item whose label and enabled state follow the model, "Open HLab", window title and error dialog title "HLab"; `click` takes the current item label), e2e.go (`Click` labels), options.go (`Name: "HLab"`).
  - [x] ui/frontend/index.html `<title>HLab</title>`; src/views/Settings.svelte texts "Trust the HLab CA", "Stop trusting the HLab CA", "HLab CA certificate", "HLab's domains".

- [x] 10-25 Bridge: log search and older lines
  - Wave: 2 | Depends on: 10-24 | Review: alone
  - Requirements: UI-06 | Tests: - (pass-through; the UI-06 e2e rows cover it)
  - Reads: PLAN "Interfaces" (bridge Logs, logfile); internal/bridge/logs.go
  - Size: ~60 lines in 1 file
  - [x] internal/bridge/logs.go: `Search(query, minLevel)` and `Older(oldest)` on `LogDir` with 1000 as in "Interfaces"; an empty query returns no lines.

- [x] 10-21 Name HLab in API messages
  - Wave: 3 | Depends on: 10-23 (order) | Review: alone
  - Requirements: UI-08 | Tests: - (fixed answers in SPEC; the tests below follow the wording)
  - Reads: SPEC "Stack" (fixed API answers); UI-SPEC "Language of the interface"; finding 2
  - Size: ~60 lines in 9 files
  - Adjusts: internal/resources/servers/servers_test.go (console message), internal/resources/networks/networks_test.go (vswitch message), any other test comparing a renamed message
  - [x] internal/resources/servers: actions.go (`server console is not supported by HLab yet`), changetype.go and servers.go ("HLab runs only x86 servers"), with servers_test.go.
  - [x] internal/resources/networks/validate.go (`vswitch subnets are not supported by HLab`), networks_test.go, loadbalancers/targets.go ("HLab's transit network"), zones/rrsets.go and zones.go ("managed by HLab").
  - [x] internal/api/deviations.go: descriptions name "HLab" where they name the product (the CRL path `/hlab/v1/ca.crl` stays).

- [x] 10-27 Bridge: catalog locations
  - Wave: 3 | Depends on: 10-25 (order) | Review: alone
  - Requirements: UI-04 | Tests: internal/bridge/service_test.go
  - Reads: UI-SPEC "Views" (Server Types); PLAN "Interfaces" (Location); internal/bridge/service.go `catalog()`; internal/catalog/catalog.go `Location`; finding 9
  - Size: ~60 lines in 2 files
  - [x] internal/catalog/catalog.go: `Location.City` (`json:"city"`, read from the shipped object); internal/bridge/service.go: `Locations()` from the catalog the bridge already loads (works while stopped), sorted by name.

- [x] 10-28 Shell: routes, top bar, Administration
  - Wave: 4 | Depends on: 10-20, 10-23, 10-27 (order) | Review: alone
  - Requirements: UI-07, UI-08, DOC-05 | Tests: - (UI-07 help.test.ts and DOC-05 docs_test.go cover views.json; navigation e2e in 10-29)
  - Reads: UI-SPEC "Foundation", "Spacing and grid", "Views" (Shell, Cloud stopped), "Behaviour"; DECISIONS "Console shell like the Hetzner Cloud Console", "Q15 DOC-05 views from views.json"; CONTEXT "Findings fixes" (last opened project); PLAN "Interfaces" (state.ts, SectionSidebar); finding 5
  - Size: ~380 lines in 8 files
  - [x] ui/frontend/src/views.json `["projects","tokens","server-types","settings","logs"]`; src/state.ts (`views` built from views.json with title and area, `Route`, `route` from the hash, `href`, `lastProject` in localStorage `hlab.project`; at startup an empty hash opens `lastProject`'s console when `GET /projects` lists it, else the list; unknown routes fall back to `#/projects`); src/help.ts `ViewId`; src/help/tokens.md (short; the documentation plan completes it).
  - [x] src/lib/TopBar.svelte (app icon from src/assets/app-icon.svg and "HLab" linking to `#/projects`, breadcrumb slot, status pill, Start/Stop button, icon button "Administration" linking to `#/admin/server-types`), src/lib/SectionSidebar.svelte, src/App.svelte (areas: project list full width, project area renders the existing Projects/ProjectDetail until 10-29, admin area with SectionSidebar Server Types, Settings, Logs and breadcrumb "Administration"); remove src/lib/Sidebar.svelte and the project switcher.

- [x] 10-22 Name HLab in CLI, cloud and host messages
  - Wave: 4 | Depends on: 10-21 | Review: alone
  - Requirements: UI-08 | Tests: - (wording; tests comparing it follow)
  - Reads: UI-SPEC "Language of the interface"; finding 2
  - Size: ~60 lines in 10 files
  - Adjusts: tests that compare a renamed message (internal/cli/cli_test.go, internal/hostnet/windows tests, internal/cloud tests, internal/ca/ca_test.go where they use the CA name constant)
  - [x] internal/cli/cli.go: "HLab CA written to …", "the HLab CA is (no longer) trusted on this host", `hlab version` text output "HLab <version>" (JSON unchanged); usage lines, flag messages naming `hlab <command>` and the `hlab:` error prefix stay (command name).
  - [x] internal/cloud/ca.go (new CAs get CN "HLab CA"; an existing CA file is kept), lifecycle.go ("readable HLab state", "newer HLab version"), internal/store/store.go ("newer HLab version").
  - [x] internal/hostnet/windows/hostnet.go ("the HLab cloud of profile …"), wsl.go ("which HLab does not support", "resolve HLab names"), internal/settings/settings.go ("which HLab cannot use").

- [ ] 10-29 Project list and project console
  - Wave: 5 | Depends on: 10-28 | Review: alone
  - Requirements: UI-05, UI-08 | Tests: ui/frontend/tests/e2e/projects.spec.ts
  - Reads: UI-SPEC "Views" (Shell, Cloud stopped, Projects), "Building blocks"; PLAN "Interfaces" (state.ts, SectionSidebar); finding 5
  - Size: ~350 lines in 6 files
  - Adjusts: ui/frontend/tests/e2e/fixtures.ts (`openView` goes through the gear and the Administration sidebar for Server Types, Settings, Logs; UI-03, UI-04, UI-06 specs keep what they check)
  - [ ] src/views/Projects.svelte: the project list full width, row click opens `#/projects/<name>/tokens` and sets `lastProject`; cloud stopped: "Start the cloud to manage projects" with the Start button.
  - [ ] src/views/ProjectConsole.svelte (breadcrumb "Projects / <name> ▾" with a menu of the other projects, SectionSidebar with a separator and "API tokens", the stopped text), src/views/Tokens.svelte (`git mv` of ProjectDetail.svelte: token table and "Create token" dialog under the title "API tokens" with its help button); App.svelte renders ProjectConsole for the project area.

- [x] 10-31 E2E harness: delete the run's project and leftovers
  - Wave: 5 | Depends on: 10-22 (order) | Review: alone
  - Requirements: - (test harness) | Tests: - (proof at the close: after `task e2e`, `hlab project list` shows no `e2e-*` project)
  - Reads: test/e2e/main_test.go `setup`, `teardown`, `newEnv`, `deleteAllServers`; ARCHITECTURE "Management API" (`DELETE /projects/{name}`); finding 6
  - Size: ~80 lines in 1 file
  - [x] test/e2e/main_test.go: shared `deleteProject(name)` (`hlab project delete`, which deletes every resource); `teardown` deletes the run's project when `envVal != nil` instead of `deleteAllServers` (removed); setup lists projects (`hlab project list --output json`) and deletes every name matching exactly `^e2e-\d{8}-\d{6}$` other than the run's own, after the cloud runs.

- [ ] 10-30 Server Types view: token dialog and location checkboxes
  - Wave: 6 | Depends on: 10-27, 10-29 | Review: alone
  - Requirements: UI-04, UI-08 | Tests: ui/frontend/tests/e2e/servertypes.spec.ts
  - Reads: UI-SPEC "Building blocks" (Form, Confirm dialog), "Views" (Server Types), "Behaviour" (Keyboard); PLAN "Interfaces" (Locations, Dialog); findings 8, 9
  - Size: ~300 lines in 2 files
  - [ ] src/lib/Dialog.svelte (native `<dialog>`, title, Escape and "Cancel" close, `onclose`); src/views/ServerTypes.svelte: the add form and the token form use it.
  - [ ] ServerTypes.svelte: "Update from Hetzner" (enabled while the cloud runs) opens the dialog with the required password field "Hetzner token", the hint, "Update" (loading, Enter submits); success closes it and shows the toast, an error stays under the field; the token is cleared on every close. Locations: "All locations" (checked by default, disables the others, saves `[]`) and one checkbox "<name> (<city>)" per `Locations()` entry; unchecked without a location shows the error under the list.

- [ ] 10-32 Documentation layout: docs/ and docs/development/
  - Wave: 6 | Depends on: 10-31 (order) | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-04, DOC-06 | Tests: test/docs/docs_test.go (paths only)
  - Reads: SPEC "Documentation"; REQUIREMENTS "Documentation (DOC)"; DECISIONS "Documentation folders"; finding 12
  - Size: ~60 lines in 10 files (moves)
  - Adjusts: test/docs/docs_test.go (DOC-01 docs/commands.md, DOC-02 docs/configuration.md, DOC-03 docs/development/api-coverage.md, DOC-06 docs/development/*.md)
  - [ ] `git mv` docs/user/commands.md, configuration.md to docs/; docs/user/deviations.md to docs/development/api-coverage.md (its full content); test/docs/docs_test.go follows the paths.
  - [ ] `git mv` docs/dev/{setup,build-and-test,components,adding-a-resource}.md to docs/development/; fix every link in README.md and the moved documents (DOC-04 green).

- [ ] 10-26 Logs view: search and older lines
  - Wave: 7 | Depends on: 10-25, 10-29, 10-30 (order) | Review: alone
  - Requirements: UI-06, UI-08 | Tests: ui/frontend/tests/e2e/logs.spec.ts
  - Reads: UI-SPEC "Views" (Logs), "Behaviour"; PLAN "Interfaces" (bridge Logs); ui/frontend/src/views/Logs.svelte; finding 7
  - Size: ~220 lines in 1 file
  - [ ] src/views/Logs.svelte: search field (Enter or 300 ms pause) calling `Logs.Search` with the level filter, matches newest first with the matched text highlighted (`<mark>`), count "N matches" / "1000+ matches", follow off while a search shows, clearing returns to the live follow; "Load older lines" above the list calling `Logs.Older` with the oldest shown raw line, prepending the result, disabled after `at_start`; loaded older lines are not trimmed by the 5000 cap, which applies to the live part.

- [ ] 10-33 README screenshots from the running tray
  - Wave: 8 | Depends on: 10-29, 10-26 (order) | Review: alone
  - Requirements: DOC-07 | Tests: - (manual DOC-07)
  - Reads: RESEARCH "Findings fixes" F3, topic 5; CONTEXT "Findings fixes" (sample projects); test/e2e/ui_test.go `startTray`, `stopTray`, `freePort`; finding 14 (2)
  - Size: ~320 lines in 6 files
  - Adjusts: test/e2e/ui_test.go (uses test/traylaunch; UI rows unchanged)
  - [ ] test/traylaunch/launch.go: tray start and stop moved out of test/e2e/ui_test.go as in "Interfaces"; ui_test.go uses it.
  - [ ] Probe first: tools/screenshots/main.go builds `bin\hlab-tray-e2e.exe` (tag e2e), starts it, and ui/frontend/tests/screenshots/console.spec.ts (Playwright project `screenshots` in playwright.config.ts) connects over CDP, sets `emulateMedia({ colorScheme: "dark" })`, `setViewportSize({ width: 1200, height: 800 })` and takes a screenshot; if this fails, stop the plan and report.
  - [ ] The tool needs the dev cloud running, creates the sample projects `demo-web`, `demo-staging`, `demo-ci` (only those it created are deleted afterwards) and a token "ci" in `demo-web` through bin\hlab.exe; the spec types `demo-` into the list's search, captures the project list and the `demo-web` console "API tokens" in light and dark into docs/images/{projects,tokens}-{light,dark}.png (no token value on screen) and resets media and viewport; Taskfile.yml `docs:screenshots`; run it and commit the images.

- [ ] 10-34 Documentation: deviations, references and help texts
  - Wave: 8 | Depends on: 10-21, 10-26, 10-30, 10-32 | Review: alone
  - Requirements: DOC-03, DOC-05, DOC-07 | Tests: test/docs/docs_test.go (DOC-03)
  - Reads: SPEC "Documentation" D2-D6; REQUIREMENTS DOC-03, DOC-05, DOC-07; UI-SPEC "Views", "Language of the interface"; internal/api/deviations.go; findings 2, 13, 15
  - Size: ~400 lines in 10 files
  - [ ] docs/deviations.md new for users: short prose by topic (servers; images, snapshots, backups, rescue and ISOs; volumes; networks, primary and floating IPs; firewalls; DNS and reverse DNS; load balancers and certificates; projects, server types and prices; WSL2), no routes, at most about 150 lines, linking docs/development/api-coverage.md; api-coverage.md gets a contributor introduction and the descriptions renamed in 10-21.
  - [ ] ui/frontend/src/help/{projects,tokens,server-types,settings,logs}.md for the new shell (project list, project console "API tokens", Administration; token dialog, location checkboxes, search and "Load older lines"), naming only existing views and buttons, "HLab" as name.
  - [ ] docs/commands.md, docs/configuration.md, docs/development/components.md: "HLab" in prose, the shell, logfile search, tools/icons and test/traylaunch.

- [ ] 10-35 Documentation
  - Wave: 9 | Depends on: 10-32, 10-33, 10-34 | Review: alone
  - Requirements: DOC-04, DOC-06, DOC-07 | Tests: test/docs/docs_test.go (DOC-04, DOC-06 unchanged)
  - Reads: SPEC "Documentation" D1, D6, "Machine prerequisites", "Accounts and secrets" (hlab-docs); RESEARCH "Findings fixes" F4; CONTEXT "Findings fixes"; findings 11, 14
  - Size: ~400 lines changed in 5 files (mostly moved text)
  - [ ] docs/guide.md: the per-resource sections of README.md (servers, snapshots and backups, ISOs, volumes, networks, IPs, firewalls, DNS, load balancers and certificates, OpenTofu and Ansible, stopping and starting, profiles) with working anchors; links to them follow.
  - [ ] README.md compact: centred docs/images/banner.svg, static badges (`badge/Go-1.27-00ADD8`, `badge/platform-Windows-0078D4`), link line "Guide · Commands · Configuration · Deviations · Development", what HLab is, the screenshots with `<picture>` light/dark (relative paths), Prerequisites for using HLab, Installation (no installer yet, built from source per docs/development/setup.md), first steps, tray program for the new shell, a terminal example `hcloud server create …` with its real output and `ssh root@<ip>` run through the `hlab-docs` context (server deleted afterwards), WSL2, uninstallation, links; "HLab" in prose.
  - [ ] docs/development/setup.md merges the developer tool list (Go, Node.js, Task, golangci-lint, WiX, Wails, the e2e RAM and disk figures), `task build` and `scripts\dev-setup.ps1` without duplicates; build-and-test.md documents `task icons` and `task docs:screenshots`.

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
