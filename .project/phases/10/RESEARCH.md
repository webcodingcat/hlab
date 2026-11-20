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

## Findings fixes (10-20 onward)

Checked on 2026-11-20. Wails facts read in the v3.1.0 module cache (cited as `wails/…`) and on the v3.1.0 tag
on GitHub. Nothing was executed.

### F1. Where the Windows app icon comes from (Wails v3.1.0)

Confirmed in source:
- `wails/pkg/application/webview_window_windows.go` (~line 660, in `run()`): unless `Windows.DisableIcon`, the
  window loads `NewIconFromResource(GetModuleHandle(""), 3)`, i.e. **icon resource ID 3** of the exe. Only if that
  fails and `Options.Icon != nil` does it use `w32.CreateLargeHIconFromImage(Options.Icon)` (PNG or ICO bytes).
  Then `setIcon` sends `WM_SETICON` with `ICON_BIG` only. So on Windows **`Options.Icon` is a fallback**, not
  the primary source; there is no window-level icon option (`Windows.DisableIcon` only hides it). Alt+Tab and
  taskbar use this big icon. No `ICON_SMALL` is sent; the title bar small icon falls back to the class icon, else
  a scaled big icon (Windows behaviour, not verified here).
- `wails/pkg/application/application_windows.go:214`: the window class icon (`Icon` and `IconSm`) is
  `LoadIconWithResourceID(instance, IDI_APPLICATION)` with `IDI_APPLICATION = 32512`
  (`wails/pkg/w32/constants.go`). With a non-null instance, `LoadIcon` looks for an icon resource with
  numeric ID 32512 in the exe; without one it returns 0 (no default shell icon). To also cover the class icon,
  the .syso may add the same group icon under ID 32512 (not needed for window/taskbar/Alt+Tab because of
  WM_SETICON; my inference, not tested).
- `wails/pkg/application/dialogs_windows.go`: the error/message dialog uses `LoadIconWithResourceID(handle, 3)`
  (MessageBoxIndirect user icon and TaskDialog `mainIcon`), again **ID 3**.
- Explorer shows the exe icon as the first RT_GROUP_ICON in the resource table (lowest ID / first name; Windows rule).
  A group icon with ID 3 alone is enough; if more groups exist, ID 3 must sort first or be the only one.

Resource layout the .syso needs: **RT_GROUP_ICON (type 14), ID 3, language neutral or 0x409**, referencing
**RT_ICON (type 3) entries** with IDs 1..N (one per ICO image, e.g. 16/20/24/32/48/256). Optional: RT_VERSION
(16) ID 1, RT_MANIFEST (24) ID 1. This matches `wails3 generate syso`, which calls
`winres.SetIcon(winres.RT_ICON, ico)` (`winres.RT_ICON` is the constant `ID(3)`, so the *group* is named 3; the
comment in `webview_window_windows.go` says "App icon ID is 3").
Sources: Wails files above; https://raw.githubusercontent.com/wailsapp/wails/v3.1.0/v3/internal/commands/syso.go ;
https://raw.githubusercontent.com/tc-hib/winres/master/winres.go (constants `RT_ICON ID = 3`, `RT_GROUP_ICON ID = 14`)
and `icon.go` (`SetIconTranslation`: RT_ICON images get IDs `lastIconID+1…`, then `Set(RT_GROUP_ICON, resID, …)`).
Note that the tray icon (`SystemTray.SetIcon`) is separate and takes bytes; the tray does not use the exe resource.

### F2. Producing the .syso without a new module dependency

- Wails' `internal/commands/syso.go` uses **`github.com/tc-hib/winres` v0.3.1** (+ `winres/version`): `LoadICO`,
  `SetIcon(RT_ICON, ico)`, `SetManifest`, optional `SetVersionInfo`, `WriteObject(file, ArchAMD64)`; default file
  name `rsrc_windows_<arch>.syso`. It is in Wails' `go.mod` (line 24) but **not** in this repo's `go.sum`, because
  only `cmd/wails3` imports it. Using it means a new module in `tools/icons` (or its own module); that violates the
  "no new module" condition. License not confirmed (README did not state it).
- A stdlib-only writer is **practical but not trivial**; I did not write or run one. Rough size 200-300 lines of Go
  plus a test, using `encoding/binary`, `bytes`, `image`-free (ICO images are copied as raw bytes; if the ICO holds PNG
  frames, copy them unchanged). Needed: (1) build the resource directory tree: type level (3, 14, optionally 16, 24),
  name/ID level, language level, then `IMAGE_RESOURCE_DATA_ENTRY` records (OffsetToData, Size, CodePage 0, Reserved);
  (2) the group icon data `GRPICONDIR` (reserved, type 1, count) plus `GRPICONDIRENTRY` of 14 bytes each (width, height,
  colors, reserved, planes, bitcount, bytes, **RT_ICON ID instead of file offset**); (3) a COFF object: 20-byte file
  header with `Machine = 0x8664` (IMAGE_FILE_MACHINE_AMD64), 1 section named `.rsrc` with characteristics
  `0x40000040` (initialized data, read), raw data = the tree + data, **one `IMAGE_REL_AMD64_ADDR32NB` (type 3)
  relocation per data entry** pointing at a symbol for the section start (the OffsetToData fields hold the offset
  inside the section and are fixed up by the linker into RVAs), a symbol table with one `.rsrc` section symbol (plus
  aux record) and a minimal string table (4 bytes). Pitfalls: entries sorted (named before ID, IDs ascending) in each
  directory; 4-byte alignment of data entries; relocation `VirtualAddress` is the offset of each OffsetToData field;
  `PointerToRelocations`/`NumberOfRelocations` must match; `rsrc_windows_amd64.syso` file name picks GOOS/GOARCH
  by suffix so no build tag is needed; Go's internal linker merges .rsrc from syso files (this is how `rsrc` and
  `winres` work); only one .syso with a resource section per binary (resources of several cmds need separate package
  directories, since the .syso belongs to the `main` package directory). Alternative with no code: the `llvm-windres`
  or `windres` binary, but those are external tools (not confirmed present).
- Verdict: feasible as a small repo tool; the risk is a malformed COFF that the linker rejects or Explorer ignores.
  It needs a check by the e2e or a unit test that links a tiny main and loads the icon (or reads the PE with `debug/pe`).
  If that effort is not wanted, a new module (`tc-hib/winres`, last pinned by Wails at v0.3.1) is the only simple
  route. Release date of winres v0.3.1 not checked.

### F3. Playwright 1.65.1 over CDP against a WebView2 window

- `page.emulateMedia({ colorScheme: "dark" })`: documented as emulating `prefers-color-scheme` with `'light'`,
  `'dark'`, or `null` to reset (since v1.9; `'no-preference'` deprecated)
  (https://playwright.dev/docs/api/class-page#page-emulate-media). Playwright implements it with the CDP command
  `Emulation.setEmulatedMedia`, which a WebView2 (Chromium) target accepts. **Unclear**: no source states it works on
  a `connectOverCDP` WebView2 page, and I did not run it. Reset with `emulateMedia({ colorScheme: null })` in a
  fixture teardown so the setting does not leak (the default context persists).
- `page.setViewportSize` and `page.screenshot`: the WebView2 docs page
  (https://playwright.dev/docs/webview2) lists no limitations and says nothing on viewport or screenshots. Via
  CDP they map to `Emulation.setDeviceMetricsOverride` and `Page.captureScreenshot`; both are generic Chromium
  commands, so they should work, but **unclear** and untested here. Expected behaviour: the override changes the
  layout viewport of the page, not the size of the host window; an existing-context page has no viewport emulation
  until `setViewportSize` is called. After the screenshot, reset with `setViewportSize` back to the window size or
  reload; a hidden window may stall `captureScreenshot` (see risk (b) in section 5). Recommended: do a 10-second
  probe in the plan first (connect, `emulateMedia`, `setViewportSize({width:1200,height:800})`, `screenshot`).

### F4. README badges and dark/light images

- Go version badge from go.mod: `https://img.shields.io/github/go-mod/go-version/<user>/<repo>` with optional
  `?filename=<path/to/go.mod>` (https://shields.io/badges/git-hub-go-mod-go-version). It reads the repo through
  the GitHub API, so it works only for repos GitHub-public (private repo: the badge shows an error). Full URL
  template is my inference from the badge's service path; the docs page shows only the parameter names.
  Otherwise static: `https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white`
  (static badge format `label-message-color`, `-` in text written as `--`, `_`/`%20` = space, `logo=` takes a
  simple-icons slug; https://shields.io/badges/static-badge). The slugs `go` and `windows` were **not verified** in
  the simple-icons list (`windows` was removed from simple-icons in some versions for brand reasons; if it does not
  render, drop the `logo` parameter).
- Platform Windows: `https://img.shields.io/badge/platform-Windows-0078D4` (static; no `logo` needed).
- `<picture>` with `<source media="(prefers-color-scheme: dark)" srcset="…">` and a fallback `<img src="…">` is
  supported in GitHub Markdown (GitHub changelog, 2022-05-19, GA:
  https://github.blog/changelog/2022-05-19-specify-theme-context-for-images-in-markdown-ga/). GitHub docs recommend
  **relative links for images in the repository** (https://docs.github.com/en/get-started/writing-on-github/getting-started-with-writing-and-formatting-on-github/basic-writing-and-formatting-syntax),
  but no page I read shows relative paths inside `srcset` specifically. **Unclear for `srcset` with relative paths**;
  commonly used in READMEs, but not confirmed from GitHub documentation. Pitfall: the README renders correctly only
  on GitHub (and on a local viewer that lacks `prefers-color-scheme` support it shows the `<img>` fallback), and
  paths are resolved against the branch being viewed.
