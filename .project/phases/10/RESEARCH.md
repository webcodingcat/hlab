# Phase 10 research: tray program (Wails v3.1.0, Svelte 5, Playwright)

Checked on 2026-11-19. Primary source for Wails is the module source of v3.1.0
in the Go module cache (`%USERPROFILE%\go\pkg\mod\github.com\wailsapp\wails\v3@v3.1.0`,
cited below as `wails/…`), plus the installed `ui/frontend/node_modules`.
No shell was available, so nothing here was executed; "confirmed" means read in
source or documentation.

## Versions

| Item | Pinned / installed | Current stable | Maintained |
|---|---|---|---|
| Wails v3 | v3.1.0 | v3.1.0 (released 2026-11-01; nightlies of 3.2.0 since 2026-11-01, prerelease) | yes |
| `@wailsio/runtime` (npm) | not yet installed | `latest` = 3.1.0, matches the Go module (`wails/internal/runtime/desktop/@wailsio/runtime/package.json` says 3.1.0) | yes |
| Playwright Test | 1.65.1 | 1.65.x (1.65 released 2026-11-05) | yes |
| `@playwright/experimental-ct-svelte` | wanted 1.65.1 | **does not exist**: npm `latest` is 1.58.2; removed in 1.59 | removed |
| Vite | 8.3.8 | n/a | yes |

Sources: https://api.github.com/repos/wailsapp/wails/releases ,
https://registry.npmjs.org/@wailsio/runtime ,
https://registry.npmjs.org/@playwright/experimental-ct-svelte ,
https://playwright.dev/docs/release-notes ("1.59: Removed `@playwright/experimental-ct-svelte` package").

## 1. System tray (Windows)

Confirmed in `wails/pkg/application/systemtray.go`, `systemtray_windows.go`,
`menuitem.go`, `menuitem_windows.go`, `popupmenu_windows.go`, `wails/pkg/w32/icon.go`.

- Create: `tray := app.SystemTray.New()`; `tray.SetIcon([]byte)`, `tray.SetTooltip(string)`,
  `tray.SetLabel(string)`, `tray.SetMenu(*Menu)`, `tray.OnClick(func())`, `tray.OnRightClick(func())`,
  `tray.OnDoubleClick(func())`, `tray.OpenMenu()`, `tray.Show()/Hide()`, `tray.Destroy()`.
  Menu: `m := app.NewMenu(); item := m.Add("Label").OnClick(func(ctx *application.Context){…})`,
  `m.AddSeparator()`, `m.AddSubmenu`, `m.AddCheckbox`.
- Runtime changes are safe from any goroutine for `SetIcon`, `SetTooltip`, `SetLabel`, `SetMenu` (all go
  through `InvokeSync` once the tray runs). Before `app.Run()` they only store the value.
- Icon: `SetIcon` accepts **PNG or ICO bytes only** (`iconResourceData`: PNG magic or ICO magic, else
  "unsupported file format"). For ICO the entry nearest to `SM_CXSMICON` (system DPI, not per monitor) is
  chosen; a PNG is handed to `CreateIconFromResourceEx` and scaled by Windows. Plan: one multi-size ICO per
  state (16, 20, 24, 32, 48) embedded with `//go:embed`; no template or dark-mode handling is needed
  (`SetDarkModeIcon` is optional; if unset, dark follows light, and `SetIcon` keeps both in sync).
  Each `SetIcon` call creates a new HICON and frees the old owned one; no leak, but do not call it in a tight loop.
- Tooltip: on Windows `SetTooltip` and `SetLabel` both show `tooltip` (or `label` when the tooltip is
  empty); truncated to 127 UTF-16 units.
- Menu labels and enabled state at runtime: `item.SetLabel(s)`, `item.SetEnabled(bool)`, `SetHidden`,
  `SetChecked` work on the live popup, but they call `SetMenuItemInfo` directly on the caller's goroutine
  (no `InvokeSync`) and rebuilds replace `item.impl`. Safer pattern: mutate the items, then call
  `menu.Update()`; for tray menus it ends in `refreshSystemTrayMenus`, which rebuilds the popup on the
  main thread and **defers the rebuild while the popup is open** (`requestUpdate`, fix for wails#4719).
  Items whose label changes in the example `systray-menu/main.go` use `ctx.ClickedMenuItem().SetLabel(...)`
  directly, so both work; I did not run either under load.
- Left click: `tray.OnClick(func(){…})` fires on `WM_LBUTTONUP`, on the main thread's window procedure
  (keep it short; menu item `OnClick` callbacks run in their own goroutine). Without `AttachWindow` and
  without `OnClick`, left click does nothing. Right click (`WM_CONTEXTMENU`) opens the menu by default when
  a menu is set. Do not use `AttachWindow` (it hides/positions the window like a flyout); use
  `OnClick` -> `window.Show(); window.Focus()` (see 2).
- Open and focus a window: `window.Show()` (SW_SHOW and WebView2 show) then `window.Focus()`
  (SetForegroundWindow, unminimises if minimised, focuses WebView2). `window.Restore()` also works for a
  minimised window. `window.Hide()` hides it and the WebView controller.
- Pitfall: `SetForegroundWindow` can be refused by Windows when the call is not triggered by user input;
  from a tray click it is allowed, from a second-instance callback it usually works because the second process
  was just started by the user. Not tested here.
- Pitfall: if Explorer restarts, Wails re-adds the icon (`reshowSystrays`); nothing to do.

## 2. Application options

Confirmed in `wails/pkg/application/application.go`, `application_options.go`, `single_instance*.go`,
`webview_window.go`, `application_windows.go`, and `wails/examples/{single-instance,hide-window,systray-menu}`.

- Single instance:
  ```go
  application.Options{
    SingleInstance: &application.SingleInstanceOptions{
      UniqueID: "com.example.hlab-tray",           // required
      OnSecondInstanceLaunch: func(d application.SecondInstanceData) { /* d.Args, d.WorkingDir */ },
      ExitCode: 0,                                  // exit code of the second process
    },
  }
  ```
  Windows implementation: mutex `wails-app-<UniqueID>-sim` (session-local, not `Global\`) plus a hidden
  message-only window that receives the data with `WM_COPYDATA`. Encryption key is optional.
  Pitfalls:
  - The second process exits with `os.Exit(ExitCode)` **inside `application.New`**. Everything before
    `application.New` also runs in the second process (log files, goroutines, SCM calls); call it first.
  - `OnSecondInstanceLaunch` runs in a goroutine; `window.Show()/Restore()/Focus()` are safe there. Guard
    against the window variable still being nil (the example does). The internal channel has capacity 1 and is
    fed from the main thread, so a callback that blocks for a long time plus rapid repeated launches could
    stall the window procedure; keep the callback short.
  - The e2e build runs next to a real user instance: give it a different `UniqueID` (or skip the option)
    or the e2e launch exits at once and focuses the user's tray.
- Hide on close: no option exists; use a hook:
  ```go
  window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
      if !quitting.Load() { window.Hide(); e.Cancel() }
  })
  ```
  (`RegisterHook` hooks run before listeners; `Cancel()` stops the close.) Because the window is not
  destroyed, `Windows.DisableQuitOnLastWindowClosed` is not needed for this path, but set it to `true` anyway
  as the tray application must survive if a window is ever destroyed. `app.Quit()` -> `destroy()` ->
  `cleanup()` calls `window.Close()` on every window, which re-enters the same hook; the app still quits
  (it posts `WM_QUIT`), but a `quitting` flag (set before `app.Quit()` and in `ShouldQuit`/`OnShutdown`) keeps
  the hook from fighting the shutdown.
- Quit from the tray menu: `menu.Add("Quit").OnClick(func(*application.Context){ quitting.Store(true); app.Quit() })`.
  `Options.ShouldQuit func() bool` can veto quits (e.g. a confirm), `Options.OnShutdown` runs before windows close.
- Window options used by the examples: `WebviewWindowOptions{Name, Title, URL: "/", Width, Height, Hidden: true,
  Windows: application.WindowsWindow{HiddenOnTaskbar: false}}`. `HideOnEscape` exists. A window created
  `Hidden` still loads the frontend (the WebView is created and then hidden), so log streaming and state keep running.
- Dialogs for confirmations (discard-state): `app.Dialog.Question().SetTitle(..).SetMessage(..)`
  with `AddButton("Yes").OnClick(..)`. Without an application manifest, custom-button dialogs fall back to
  `MessageBox` (`dialogs_windows.go`); the manifest is optional. Build with `-ldflags "-H windowsgui"`
  to avoid a console window (plain Go).

## 3. Bindings, runtime, events, assets

- **The `wails3` CLI is not needed.** Generating bindings is optional. A bound method is callable by its fully
  qualified name with `Call.ByName` from the npm package (`calls.ts`, `bindings.go`, `messageprocessor_call.go`):
  ```ts
  import { Call } from "@wailsio/runtime";
  const FQN = "github.com/webcodingcat/hlab/internal/bridge.Service.Call"; // <pkgpath>.<Type>.<Method>
  const res = await Call.ByName(FQN, method, path, bodyBase64OrNull);
  ```
  FQN is `reflect` `PkgPath() + "." + TypeName + "." + Method`, so the **full import path** of the package
  (for `package main` it is `main.Type.Method`). Register with `application.NewService(&bridge.Service{})` in
  `Options.Services`. The service type must be a named, non-generic pointer; only exported methods; a
  `context.Context` first parameter is allowed and is not counted; unknown names reject with an error.
- Pitfalls for the planned `Call(method, path string, body []byte) (status int, body []byte)`:
  - Arguments are JSON; a `[]byte` parameter is a **base64 string** (or `null`), not an array.
  - A method with more than one non-error result resolves to a **JSON array** `[status, "<base64>"]`; a
    returned `error` rejects the promise with `RuntimeError` (message = error text). Returning one struct
    (`{Status int; Body json.RawMessage}` or `Body string`) is easier to type than a tuple.
  - Argument count is checked exactly (`expects N arguments, got M`), so pass every argument, `null` for nil.
  - A panic in a bound method becomes a rejected call, not a crash.
- `@wailsio/runtime` npm version matching Go v3.1.0: **3.1.0** (exact pin; no runtime dependencies; ESM with
  types; importing it in Node is harmless because of the `hasDOM` guard). It talks to `http://wails.localhost/wails/runtime`
  through `fetch`, so the frontend only works inside the Wails window (not under plain `vite dev` in a browser).
- Go -> frontend events: `app.Event.Emit("log", payload)` (all windows), `window.EmitEvent(name, data)` (one
  window). Frontend: `import { Events } from "@wailsio/runtime"; const off = Events.On("log", ev => ev.data);`
  (`ev.data` is the single payload; with several Go arguments it is an array). Each emit does an `ExecJS` through
  `InvokeSync`, blocking until the main thread ran it (`transport_event_ipc.go`), so **batch log lines** (for
  example one event per 50-100 ms carrying an array) rather than one event per line. Events reach hidden windows too.
- Serving `ui/frontend/dist`: `Assets: application.AssetOptions{Handler: application.BundledAssetFileServer(fsys)}`
  (also serves `/wails/runtime.js`). The asset server searches the `fs.FS` for `index.html` and sub-roots itself
  (`findPathToFile`), so an `embed.FS` that still has the `frontend/dist` prefix works without `fs.Sub`.
  Unknown paths return 404 and a path without an extension is tried with `.html`: **no history-API routing**,
  switch views in state or use hash routes. Keep Vite `base` at `/`.
- go:embed limits: no `..` in the pattern; the embedding package's directory must be at or above the files.
  So the embed package must live in `ui/` (for example `ui/embed.go`, `package ui`,
  `//go:embed all:frontend/dist`, `var Dist embed.FS`), not in `cmd/hlab-tray`. `all:` includes files starting
  with `.` or `_`. **Pitfall for this repository:** `ui/frontend/dist/` is gitignored, and `task test` and
  `task lint` do not depend on `frontend:build` (Taskfile.yml), so on a clean checkout `go vet`, `go test ./...`
  and golangci-lint fail with "pattern frontend/dist: no matching files found" for every package that imports
  `ui`. Options for the plan: make `test`/`lint` depend on `frontend:build`, or keep the embed behind a build
  tag, or commit a placeholder that Vite's `emptyOutDir: true` does not delete. Not decided here.
- TypeScript: `tsconfig.json` has `"types": ["svelte"]`. `import.meta.glob`, `?raw` imports and `import.meta.env`
  need `"types": ["svelte", "vite/client"]` (or a `/// <reference types="vite/client" />`), otherwise
  `svelte-check --fail-on-warnings` fails. `.md?raw` additionally needs the `vite/client` shim for `*?raw`.

## 4. Module compatibility with the pinned Go modules

`wails/go.mod`: `go 1.26.0`, `toolchain go1.26.10`; pinned Go 1.27.4 is newer, fine. Comparing every module that
appears in both graphs with `go.mod` of this repository:

- Wails requires `golang.org/x/sys v0.48.0` (pinned v0.50.0 is higher), `x/term v0.46.0` (v0.48.0), `x/net v0.60.0`
  (v0.62.0), `x/sync v0.23.0` (v0.25.0), `x/text v0.42.0` (v0.43.0), `go-winio v0.6.2` (v0.6.4),
  `modernc.org/sqlite v1.44.3` (v1.62.0), `modernc.org/libc v1.67.6` (v1.80.1), `modernc.org/memory v1.11.0`
  (v1.12.1), `ncruces/go-strftime v1.0.0` (v1.1.0), `go-humanize v1.0.1` (v1.1.0), `mattn/go-isatty v0.0.20`
  (v0.0.24), `BurntSushi/toml v1.6.0` (equal), `google/uuid`, `ulikunitz/xz v0.5.15`, `mathutil` (equal).
  Minimum-version selection keeps all of the pinned versions.
- **One pinned indirect module would be raised:** Wails requires `github.com/klauspost/compress v1.18.7`; the
  repository has `v1.18.5` (indirect). `go get`/`go mod tidy` will bump it to v1.18.7. It is not a module named in
  SPEC.md; I did not check which dependency of the repository uses it (probably image or compression code).
- New modules arrive with Wails (x/crypto v0.57.0, x/image v0.45.0, x/exp, x/mod, x/tools via the module graph, go-ole,
  wincred, go-toast, coder/websocket, godbus, purego, lmittmann/tint, and more). With module graph pruning only
  those whose packages are imported by the tray appear in `go.mod`; `go.sum` grows. The `wails3` CLI package
  (`cmd/wails3`) pulls far more (bubbletea, nfpm, pterm); do not import or `go run` it.
- Could not run `go get` (no shell), so the list above is derived from reading both `go.mod` files, not from a resolver run.

## 5. Playwright 1.65.1 `chromium.connectOverCDP` against the Wails window

Sources: https://playwright.dev/docs/webview2 , https://playwright.dev/docs/api/class-browsertype ,
https://playwright.dev/docs/release-notes ; Wails source `request_cancellation_windows.go`. **Not run.**

- Official WebView2 recipe: `const browser = await chromium.connectOverCDP("http://127.0.0.1:PORT");
  const context = browser.contexts()[0]; const page = context.pages()[0];` The default context and the window's page
  are the existing ones. Playwright Test's own `page` fixture creates a fresh context and cannot be used; define a
  **worker-scoped fixture** that connects once and a `page` fixture that returns the existing page.
- Use `127.0.0.1`, not `localhost` (Node resolves `localhost` to `::1` first on some setups; WebView2 listens on IPv4
  loopback). `connectOverCDP` fetches `/json/version`, which E9 confirmed answers.
- Page discovery: filter `context.pages()` by URL `http://wails.localhost/…` (WebView2 can expose other targets, for
  example DevTools or extension pages); if empty, `await context.waitForEvent("page")`.
- Stable context across tests: the default context persists as long as the app runs, so state (localStorage, open
  dialogs, scroll) leaks between tests. Reset with `page.goto("http://wails.localhost/")` or `page.reload()` in a
  per-test fixture. Use `workers: 1` and `fullyParallel: false` (one window). `browser.close()` on a CDP connection
  only disconnects (not documented explicitly; I did not verify that the app stays up).
- Version notes: `isLocal` (1.58+) and `noDefaults` (1.60+) exist. By default Playwright applies its default
  overrides (focus emulation, media, downloads) to the existing default context; `noDefaults: true` turns that off.
  Playwright 1.65 does not list changes to `connectOverCDP`, but "Playwright no longer tests on Node.js 20" (use 22, 24
  or 26) and `.css` imports now load as empty modules.
- Evaluating bound calls: the **app bundle imports the npm runtime, which does not set `window.wails`**; only the
  script `/wails/runtime.js` (served by `BundledAssetFileServer`) sets `window.wails` (`runtime.debug.js` line 3135).
  In a test: `await page.evaluate(async () => { const m = await import("/wails/runtime.js"); return m.Call.ByName(fqn, ...args); })`.
  This is derived from source, not run; a second runtime instance overwrites `window._wails.invoke` and `clientId`
  (harmless for Call, unknown for cancellation). How E9 evaluated the bound method is not recorded in EXPERIMENTS.md;
  reuse that if its code still exists. Driving the UI and reading the DOM avoids the question.
- Unconfirmed risks: (a) Wails itself uses CDP on the same WebView (`Network.enable`, `Fetch.enable` for
  `http://wails.localhost/*`, `Target.setAutoAttach`) for request cancellation; a second CDP client (Playwright) should
  coexist, but this was not tested. (b) A window that is hidden (tray start state) has no visible WebView
  (`chromium.Hide()`), so `requestAnimationFrame`-based actionability checks (click, "stable") may hang; the harness
  should show the window first (second-instance launch or a harness-only call) before UI tests.

## 6. Unit tests for TypeScript/Svelte code with only the pinned stack

- `@playwright/experimental-ct-svelte` 1.65.1: **not available**. The package was removed in Playwright 1.59; the last
  published version is 1.58.2 (npm). Do not plan on it.
- **Playwright Test without a browser** works: tests that use neither `page` nor `browser` start no browser and
  need no installed browser binaries. Its TypeScript transform handles `.ts` (enums, param properties, tsconfig paths)
  but does **not** handle `.svelte`, `?raw` or `import.meta.glob` (it is not Vite). Runs on Node 24.
- **Node 24 `node --test` with type stripping**: unflagged and stable in v24.12+, no warning from v24.3 on
  (https://nodejs.org/docs/latest-v24.x/api/typescript.html). Limits: erasable syntax only (no `enum`, no parameter
  properties, no runtime `namespace`), `import type` required for types, **file extensions mandatory in import
  specifiers** (`./x.ts`), `tsconfig.json` ignored, no `.svelte`, no `?raw`/`import.meta.glob`, JSON needs
  `with { type: "json" }`. Importing `./x.ts` is accepted by svelte-check with `noEmit` (TS 5.9 `allowImportingTsExtensions`
  is implied; the project's `@tsconfig/svelte` base sets `noEmit` in the repo's tsconfig).
- Testing a module that uses `import.meta.glob` / `?raw` (the help-text index): two workable ways.
  1. Split it: a pure module `help.ts` with `checkHelp(texts: Record<string,string>, views: string[], buttons: …)`
     and a thin `helpTexts.ts` holding the glob
     `import.meta.glob("./help/*.md", { query: "?raw", import: "default", eager: true })`. Unit-test the pure function
     with `node --test` or Playwright Test using inline fixture strings. (`as: "raw"` is deprecated in Vite 8; use `query`.)
  2. Load the real module through Vite: `import { runnerImport } from "vite"; const { module } = await
     runnerImport<typeof import("../src/helpTexts.ts")>("/abs/path/src/helpTexts.ts", { root })` inside a Playwright
     Test or `node --test` test. `runnerImport` exists in the installed Vite 8.3.8 (`index.d.ts` line 3824,
     marked `@experimental`), uses `configFile: false` (so `?raw`, `import.meta.glob`, JSON are handled by Vite core;
     pass `plugins: [svelte()]` in the second argument if `.svelte` files are imported), and exercises the same glob
     transform as the production build. `server.ssrLoadModule()` (not marked deprecated in the typings) is the
     fallback. This ties DOC-05/UI-07 to the real files without a test that reads source files itself. Whether
     `@sveltejs/vite-plugin-svelte` 7.3.1 compiles `.svelte` correctly in this inline "server" environment was not
     checked; for components, `svelte/server` `render()` would be the route, but interaction tests belong in the
     CDP e2e against the real window.
- `views.json` (existing `src/views.json`) is importable by Vite (and by `node --test` with the `with { type: "json" }`
  attribute); under Playwright Test's transform `import views from "./views.json"` also works.
