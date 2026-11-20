# Phase 14 research

## 1. Wails v3 native open-file dialog (go.mod: github.com/wailsapp/wails/v3 v3.1.0)

Source: module source `pkg/application/dialogs.go`, `dialog_manager.go`, `dialogs_windows.go` in v3.1.0 (local module cache).

- No package-level `application.OpenFileDialog()` exists in v3.1.0. Use the manager: `app.Dialog.OpenFile()` returns `*OpenFileDialogStruct` (`app` is the `*application.App`, e.g. `application.Get()`).
- Minimal call from a bound service method:
  `path, err := app.Dialog.OpenFile().SetTitle("Select ISO").AddFilter("ISO images", "*.iso").CanChooseFiles(true).AttachToWindow(win).PromptForSingleSelection()`
  - `AddFilter(displayName, pattern string)`: pattern is a semicolon-separated list, e.g. `*.iso`. `AttachToWindow(Window)` is optional (parent handle on Windows via `NativeWindow()`).
  - Returns `(string, error)`; the path is empty when the user cancels (no error for cancel). `PromptForMultipleSelection()` returns `[]string`.
- Blocking: yes. `PromptForSingleSelection` runs `InvokeSyncWithResultAndError(impl.show)` on the main/UI thread and waits for the user (Windows: go-cfd COM file dialog, modal). The bound method therefore blocks until the user closes the dialog.
- If the application has not started (platform not ready) it returns `"", nil` and logs a warning; no dialog.
- CDP e2e: not confirmed by documentation. The dialog is a native Win32 dialog, independent of the WebView2 DevTools protocol, so it will not be driven or auto-dismissed by CDP; it would simply stay open and block the call (and the UI-thread invoke) until someone closes it. Nothing in the Wails source switches it off in an e2e build. Recommendation: put the dialog behind a Go interface/hook that the e2e build replaces (or let the e2e build skip the dialog and take a path from a test source); do not drive the native dialog in e2e. Not verified by running it.
- Maintenance: v3.1.0 is the pinned version; I did not check the latest release date.

## 2. Hetzner OpenAPI `iso` schema

Source: frozen copy `%LocalAppData%\hlab\openapi\cloud.spec.json` (OpenAPI 3.1.2, pinned in `api/openapi.lock`; SPEC.md "References"). Not in the repo. Schema is inline (no `components/schemas/iso`): GET `/isos/{id}` response (`iso` object at lines ~18123-18205), the same object is repeated for `server.iso` (~48106) and in the list response.

- `type` enum: `["public","private"]`, declared as `type: ["string","null"]` (null is allowed by the schema; `enum` lacks null, but with a 3.1 type including null a null value would fail the enum in strict validators, so send a real value).
- Required fields of an ISO (all six): `id`, `name`, `description`, `type`, `deprecation`, `architecture`.
  - `id`: integer int64, minimum 1, maximum 9007199254740991.
  - `name`: string or null ("Only set for public ISOs"): for a private ISO send `null`.
  - `description`: string (not nullable), always present.
  - `type`: `"private"` (or `"public"`).
  - `deprecation`: object or null; null = not deprecated. If an object, it needs both `unavailable_after` and `announced` (date-time strings). A private ISO should carry `"deprecation": null`.
  - `architecture`: `"x86"`, `"arm"` or null (null = wildcard).
  - Note: the schema has no `deprecated` field (only `deprecation`); do not emit one.
- A valid private ISO: `{"id":N,"name":null,"description":"...","type":"private","deprecation":null,"architecture":"x86"}`.
- Not confirmed: whether the real Hetzner API lets API users create private ISOs at all (the spec has no upload/create ISO endpoint; only list, get, attach, detach). Private ISOs in the spec are only readable/attachable.
