# Phase 10 manual checks

## UI-08 The console looks and behaves as UI-SPEC.md describes

- Start: `task build`, then start `bin\hlab-tray.exe` from the repository root (the dev service `hlab-dev` must be registered; `scripts\dev-setup.ps1` once, elevated).
- Setup: the dev cloud stopped at first (`bin\hlab.exe cloud stop`); a Hetzner API token at hand for step 9 (optional); Claude can prepare a project with a few servers if you want filled counts.
- Steps:
  1. Look at the notification area: the hlab icon shows the stopped state; hover it, the tooltip reads "hlab: stopped" (or similar).
  2. Right-click the icon: the menu has "Open hlab", "Start cloud", "Stop cloud" (greyed out) and "Quit". Left-click opens the window.
  3. In the window: sidebar with project switcher on top and Projects, Server Types, Settings, Logs; top bar with the status pill and a Start button. Projects shows "Start the cloud to manage projects"; Server Types, Settings and Logs work while stopped.
  4. Click "Start cloud" in the tray menu: the icon and the pill change to starting, then running; both menu items are greyed out while it starts.
  5. Projects: create a project, open it, create a token (the value is shown once with a copy button and a warning), revoke it; delete the project, which asks you to type its name and shows a progress toast.
  6. Server Types: the list shows catalog and own types with a source column; add an own type (try an invalid value first: the error appears under the field), then remove it.
  7. Settings: every key is in a grouped form with hints ("Applies at the next cloud start.", the stop_behaviour hint); an invalid public range shows its error under the field; the trust switch and "Save CA certificate" work only while the cloud runs.
  8. Logs: lines appear in monospace, the level filter narrows them, "Follow" keeps the view at the end while new lines arrive.
  9. Server Types, "Update from Hetzner" with your token: a toast shows the result and the token field is empty afterwards.
  10. Press `?` in a view title row: the help panel opens on the side; Escape closes it.
  11. Close the window with the X: it disappears, the icon stays; start `bin\hlab-tray.exe` again: the existing window comes to the front.
  12. Choose "Quit" in the tray menu: the tray ends, the cloud keeps running (`bin\hlab.exe cloud status`).
- Expected: colours, spacing, building blocks (buttons, pills, tables with search and row menu "Actions", confirm dialogs, toasts bottom right, copy fields) look as UI-SPEC.md describes, and the icon is clear at the notification-area size in light and dark taskbar themes.
- Result: open
