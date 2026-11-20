# Phase 10 test contracts

What the tests written first expect from the implementation plans. Implementers of the named plans read this.

- 10-05 `ui/frontend/playwright.config.ts`: project `unit` (testDir `tests/unit`), project `e2e` (testDir `tests/e2e`), workers 1, no trace or video; `@types/node` as devDependency. The Go subtests call `node node_modules/@playwright/test/cli.js test --config <frontend>/playwright.config.ts --project e2e --grep <ID>`.
- 10-12: `src/helpTexts.ts` exports `helpTexts: Record<string,string>`; `src/help` is the module ui/frontend/tests/unit/help.test.ts imports.
- 10-13: the tray reads `HLAB_E2E_CDP_PORT` and `HLAB_E2E_WEBVIEW_DIR` (e2e build only); the harness builds it as `bin\hlab-tray-e2e.exe` with the dev profile; `main.TrayTest.Click` takes the menu labels "Start cloud" and "Stop cloud".
- 10-14 `test/e2e/e2e_test.go`: `t.Cleanup(func() { releaseTray(t) })`; after `releaseLifeFixture(t)` run serially `UI-01`, `UI-03`, `UI-04`, `UI-05`, `UI-06` (`testUI0x` in ui_test.go). `testCAT03` uses `usableHetzner(ctx)` from ui_test.go and skips with its reason.
- 10-11 shell: sidebar `navigation` with links Projects, Server Types, Settings, Logs; one h1 per view; status pill `role=status` inside the `banner` header.
- Views: row menu button "Actions" with menuitems Remove, Revoke, Delete; dialogs "Create project" and "Create token" (field Name, button Create, then Close); typed-name textbox with Delete disabled until the name matches.
- 10-17 Server Types: fields Name, Description, Cores, Memory (GB), Disk (GB), select CPU type, button Add; password field "Hetzner token", button "Update from Hetzner"; success text matches `/\d+ server types/`.
- 10-16 Settings: labels Stop behaviour, Public range, Data folder, Reserved RAM (MB), Reserved disk (GB), Log level; switch "Trust the hlab CA" (aria-checked); button "Save settings"; field errors linked through aria-describedby.
- 10-18 Logs: `role=log` list and a "Follow" checkbox.
- 10-01 catalog: `OwnSpec`, `FieldError`, `Own`, `ReloadOwn`, `AddOwn`, `RemoveOwn`; `FieldError.Field` is "cpu_type" for an unknown CPU type and "locations" for an unknown location.
- 10-02 servers: `ReloadOwn` as the test uses it.
- 10-04 settings: `Validate`, `Write`, `FieldError`.
- 10-07: `mgmt.Deps.Done`; in cloud `(*Cloud).reload(ctx) error`, `ensureCustomServerTypes(configDir string) error`, fields `c.cat`, `c.shadow`, `c.d.Level`; `TestCloud` calls `testReload(t)`.
- 10-08/10-09/10-10 bridge: `Invalid.Field` for settings is the settings key (e.g. "network.public_range"); packages `bridge`, `mgmtclient`, `cloudstatus`; the tray table in internal/bridge/cloud_test.go fixes icon keys and enabled items (starting/stopping enable neither; refused and failed start enable Start; connection lost enables Stop); tooltip and status line contain the state word.
- 10-13: `appOptions(*bridge.Service, *bridge.Cloud, *bridge.Logs) application.Options` in cmd/hlab-tray; options_test.go has `//go:build !e2e`.
