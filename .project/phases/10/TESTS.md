# Phase 10 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| UI-01 | `Tray` maps each state (stopped, starting, running, stopping, refused, stopped after a failed start, connection lost) to icon key, tooltip, status line and enabled Start/Stop items (table) | unit | internal/bridge/cloud_test.go: TestBridge/UI-01/tray | 10-09 |
| UI-01 | `Start` and `Stop` go to the service control, `Start(true)` passes `discard-state`; the status combines the SCM state, the problems of `/status` and the refusal or error of the status file, and `OnChange` fires on every change | unit | internal/bridge/cloud_test.go: TestBridge/UI-01/control | 10-09 |
| UI-01 | connection lost is reported only after three failed `/status` polls while the SCM reports running and clears with the next answer; a regular stop reports only `stopped` | unit | internal/bridge/cloud_test.go: TestBridge/UI-01/banner | 10-09 |
| UI-01 | the default build passes no remote-debugging argument and no WebView user data path to the window | unit | cmd/hlab-tray/options_test.go: TestTray/UI-01 | 10-13 |
| UI-01 | through the tray menu handlers the cloud stops and starts again; tray state (icon key, tooltip, enabled items) and the top bar pill follow each step and `hlab cloud status` agrees | e2e | test/e2e/ui_test.go: TestE2E/UI-01 running ui/frontend/tests/e2e/tray.spec.ts: UI-01 | 10-14 |
| UI-03 | `Validate` names the key of every invalid value (one case per key with a rule); `Write` writes valid settings in place, keeps `trust_ca` from the file, refuses invalid ones and leaves the file unchanged | unit | internal/settings/settings_test.go: TestSettings/UI-03 | 10-04 |
| UI-03 | `SaveSettings` returns the field of an invalid value and writes nothing; valid settings are written and `POST /settings/reload` is sent while the cloud runs, not while it is stopped | unit | internal/bridge/service_test.go: TestBridge/UI-03 | 10-08 |
| UI-03 | reload applies a changed `logging.level` at once; an invalid settings file changes nothing and returns the error | unit | internal/cloud/reload_test.go: TestCloud/UI-03 | 10-07 |
| UI-03 | the settings view changes every key (stop_behaviour, public_range, data_folder, reserve_ram_mb, reserve_disk_gb, logging.level, trust_ca through the running cloud) and settings.toml holds the new values; an invalid public_range shows its message under the field; the original file is restored | e2e | test/e2e/ui_test.go: TestE2E/UI-03 running ui/frontend/tests/e2e/settings.spec.ts: UI-03 | 10-16 |
| UI-04 | `AddOwn` gives the next free ID from 100000, zero prices, x86 and the chosen locations and writes the file; it refuses a taken name and invalid fields with the field; `RemoveOwn` removes the type and reports an unknown name | unit | internal/catalog/catalog_test.go: TestCatalog/UI-04/add-remove | 10-01 |
| UI-04 | `ReloadOwn` serves the own types of a changed file; an invalid or colliding file drops them with a warning, a valid file clears it | unit | internal/catalog/catalog_test.go: TestCatalog/UI-04/reload | 10-01 |
| UI-04 | power on, rebuild and reconcile of a server whose own type was removed use the cores, memory and disk of its stored `server_type`; create with that type is refused | unit | internal/resources/servers/servers_test.go: TestServers/UI-04 | 10-02 |
| UI-04 | the cloud start creates `server-types.custom.json` with `[]` when missing and keeps an existing one; reload serves changed own types and sets or clears the `custom_server_types` problem | unit | internal/cloud/reload_test.go: TestCloud/UI-04 | 10-07 |
| UI-04 | `ServerTypes` lists catalog and own types with their source, also while stopped, and the problem of an invalid file; `AddServerType` returns the field of an invalid spec; add and remove send the reload only while running | unit | internal/bridge/service_test.go: TestBridge/UI-04 | 10-08 |
| UI-04 | the server types view lists the catalog with its source, adds an own type that `/server_types` of a project then serves, removes it again; "Update from Hetzner" with the token of a usable hcloud context reports the count, without one an invalid token shows Hetzner's refusal; the password field is empty afterwards | e2e | test/e2e/ui_test.go: TestE2E/UI-04 running ui/frontend/tests/e2e/servertypes.spec.ts: UI-04 | 10-17 |
| UI-05 | the projects view creates a project, creates a token shown once that then reads the project's API, revokes it (the API then answers 401), and deletes the project after its name is typed | e2e | test/e2e/ui_test.go: TestE2E/UI-05 running ui/frontend/tests/e2e/projects.spec.ts: UI-05 | 10-15 |
| UI-06 | while running the last lines and then new ones arrive in batches; after a broken stream it reconnects and goes on after the last emitted line without duplicates; while stopped it reads the files and returns to the stream at start | unit | internal/bridge/logs_test.go: TestBridge/UI-06 | 10-10 |
| UI-06 | the logs view shows the service log and, while following, a line the service writes later (after a project create and delete) | e2e | test/e2e/ui_test.go: TestE2E/UI-06 running ui/frontend/tests/e2e/logs.spec.ts: UI-06 | 10-18 |
| UI-07 | `helpFor` returns a rendered help text with a heading for every view of views.json, loaded through Vite's glob as the app does; `renderHelp` renders headings, paragraphs, lists, inline code, code blocks, bold and links (table) | unit | ui/frontend/tests/unit/help.test.ts: UI-07 | 10-12 |
| LIFE-09 | a client following `GET /logs?follow=1` ends at once when `Done` closes, so a cloud stop does not wait for it | unit | internal/mgmt/mgmt_test.go: TestMgmt/LIFE-09 | 10-07 |

The UI e2e rows share one tray process (e2e build) and its window; each Go
subtest runs the Playwright test of its ID. They run alone after the LIFE
rows and before the parallel rows: UI-01 stops and starts the cloud, UI-03
changes settings.toml and restores it. UI-08 (manual) has no row; it also
covers the real tray icon. DOC-01 to DOC-06 keep their rows in
test/docs/docs_test.go (TestDocs/DOC-0n); DOC-05 covers views.json and the
help texts; DOC-07 is manual.

## Findings fixes (10-20 onward)

New rows, and rows that replace an earlier row because the promise changed:

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| UI-01 | `Generate` writes app.ico with 16, 20, 24, 32, 48 and 256 px and the four state icons with 16, 20, 24 and 32 px, each differing from the app icon only in the lower-right corner, and a `.syso` whose resources (read with `debug/pe`) hold the icon group under IDs 3 and 32512 | unit | tools/icons/icons_test.go: TestIcons/UI-01 | 10-20 |
| UI-01 | (replaces TestBridge/UI-01/tray above) `Tray` maps each state (stopped, starting, running, stopping, refused, stopped after a failed start, connection lost, unknown) to icon key, tooltip "HLab: …", status line, cloud item label and action ("Start cloud"/start, "Stop cloud"/stop, "Starting cloud…", "Stopping cloud…", "Checking cloud…" disabled) (table) | unit | internal/bridge/cloud_test.go: TestBridge/UI-01/tray | 10-23 |
| UI-01 | right after a successful `Start` or `Stop` the state is "starting" or "stopping" and `OnChange` has fired, a poll begun before the call does not bring back the old state, and a failed call leaves the state unchanged | unit | internal/bridge/cloud_test.go: TestBridge/UI-01/pending | 10-23 |
| UI-01 | (replaces the UI-01 e2e row above) the one cloud item reads "Stop cloud" while running; clicking it stops the cloud, the item then reads "Start cloud" and starts it again; icon key, tooltip, item label and the top bar pill follow each step (busy states disabled with their label) and `hlab cloud status` agrees | e2e | test/e2e/ui_test.go: TestE2E/UI-01 running ui/frontend/tests/e2e/tray.spec.ts: UI-01 | 10-23 |
| UI-04 | `Locations` lists the catalog's locations with name and city, also while the cloud is stopped | unit | internal/bridge/service_test.go: TestBridge/UI-04/locations | 10-27 |
| UI-04 | (replaces the UI-04 e2e row above) the server types view lists the catalog with its source; adding an own type with "All locations" unchecked and no location shows the error under the list, with one location ticked it is added and `/server_types` of a project serves it with that location; removing it works; "Update from Hetzner" opens a dialog with the password field "Hetzner token": with the token of a usable hcloud context the dialog closes and the count shows, without one Hetzner's refusal of an invalid token stays in the dialog; reopened, the field is empty | e2e | test/e2e/ui_test.go: TestE2E/UI-04 running ui/frontend/tests/e2e/servertypes.spec.ts: UI-04 | 10-30 |
| UI-05 | (replaces the UI-05 e2e row above) the project list creates a project; a row click opens its project console with "API tokens", where a token is created and shown once, reads the project's API, is revoked (the API then answers 401); back in the list the project is deleted after its name is typed | e2e | test/e2e/ui_test.go: TestE2E/UI-05 running ui/frontend/tests/e2e/projects.spec.ts: UI-05 | 10-29 |
| UI-06 | `Search` finds a text case-insensitively in msg and fields across hlabd.log and rotated files, newest first, applies the minimum level, stops at max with `capped`; a file rotated away during the search does not fail it (table) | unit | internal/logfile/logfile_test.go: TestLogfile/UI-06/search | 10-24 |
| UI-06 | `Before` returns the n lines before a given line oldest first, across into rotated files, with `atStart` at the start of the oldest file and for a line not found; a rotation between two calls does not fail it (table) | unit | internal/logfile/logfile_test.go: TestLogfile/UI-06/older | 10-24 |
| UI-06 | the logs view searches for a text the service logged (a created project's name): matches show highlighted with the count, follow is off; clearing the field returns to the live view | e2e | test/e2e/ui_test.go: TestE2E/UI-06 running ui/frontend/tests/e2e/logs.spec.ts: UI-06 search | 10-26 |
| UI-06 | "Load older lines" prepends the lines before the oldest shown one (the count grows), or is disabled when the log starts within the shown lines | e2e | test/e2e/ui_test.go: TestE2E/UI-06 running ui/frontend/tests/e2e/logs.spec.ts: UI-06 older | 10-26 |
| DOC-03 | every deviation and `unsupported_error` of `api.Deviations()` is listed in docs/development/api-coverage.md, and docs/deviations.md names none of their endpoints | unit | test/docs/docs_test.go: TestDocs/DOC-03 | 10-34 |

Tests that change only how they reach a view or which file they read (Adjusts:, what they check stays):

- ui/frontend/tests/e2e/fixtures.ts `openView` (UI-03, UI-04, UI-06 specs) through the gear and the Administration sidebar: 10-29.
- ui/frontend/tests/e2e/settings.spec.ts switch label "Trust the HLab CA": 10-23.
- test/docs/docs_test.go DOC-01, DOC-02, DOC-06 paths under docs/ and docs/development/: 10-32.
- internal/resources/servers/servers_test.go and networks_test.go fixed messages naming HLab (SPEC "Stack"): 10-21; tests comparing CLI, cloud and host messages: 10-22.
- test/e2e/ui_test.go tray start through test/traylaunch: 10-33.

Unchanged and still covering the fixes: UI-07 help.test.ts and DOC-05 iterate views.json, so the new `tokens` view needs its help text (10-28, 10-34). The app icon, tray icon look, shell layout, token dialog look, README and help wording are checked by hand under UI-08 and DOC-07 (CHECKS.md at the close). The e2e harness fix (10-31) has no row; the close checks that `hlab project list` shows no `e2e-*` project after `task e2e`.
