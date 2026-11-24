## Wails tray menu

Installed and required: `github.com/wailsapp/wails/v3 v3.1.0` (go.mod). Source read in `%GOMODCACHE%\github.com\wailsapp\wails\v3@v3.1.0\pkg\application\`. The "latest release" date was not checked online.

**Answer: `SetHidden(bool)` followed by `Menu.Update()` hides and shows a tray item reliably, including while the menu is open. This is confirmed from the source only. It was not run on a real tray.**

- `MenuItem.SetHidden` (`menuitem.go:390`) sets `m.hidden`. If the item already has a native impl, it also calls `impl.setHidden`.
- On Windows, `windowsMenuItem.setHidden` (`menuitem_windows.go:36`) calls `RemoveMenu` to hide the item. To show it again, it calls `InsertMenuItem` at the computed visible position.
- That immediate native edit works on the item's current HMENU. While the popup is being tracked, it is not guaranteed to be visible. The source gives no evidence either way.
- `Menu.Update()` (`menu.go:88`) does nothing if the app is not running. Otherwise it calls `impl.update()`, which reaches `refreshSystemTrayMenus` (`systemtray_windows.go:524`) and then `Win32Menu.requestUpdate()`.
- `Win32Menu.buildMenu` (`popupmenu_windows.go:130`) skips hidden items (`if item.Hidden() { ... continue }`), so a rebuild always matches the `hidden` flags.
- While the popup is showing (`isShowing`), `requestUpdate` (`popupmenu_windows.go:427`) does not rebuild. It sets `updatePending`, and `ShowAt` triggers the rebuild after `TrackPopupMenuEx` returns. The change fixes issue #4719.
- So while the menu is open, a hidden or shown item changes in the open menu only if the immediate `RemoveMenu`/`InsertMenuItem` takes effect on the displayed HMENU. The state is correct from the next opening at the latest.
- `ShowAt` calls `onMenuOpen` before it shows the menu. Set the item states there, or call `Update()` before the open. That is the only way to be sure the state is right when the menu appears.
- `Menu.Update()` has to run after the application has started. Before that it is a no-op, but initial hidden flags are honoured by the first build.
- `Menu.Add`/`RemoveMenuItem` (`menu.go:53`, `:228`) change only `m.items`. They also need `Menu.Update()`. The fallback is no more immediate than `SetHidden`. It has the same deferral while the menu is open.
- Pitfalls:
  - Item pointers and command IDs are renumbered on each rebuild. A click that arrives during a rebuild is handled by the deferral logic.
  - A hidden item with a submenu is detached and destroyed through `detachedSubmenus` (handled by Wails).
  - Call `SetHidden` and `Update` from the main thread, or through `application.InvokeSync`. The Win32 calls are not thread-safe. This was not verified for every call path.

Unclear: whether the open Windows popup visibly updates immediately (needs a manual or e2e check), and the date of the last Wails v3 release.
