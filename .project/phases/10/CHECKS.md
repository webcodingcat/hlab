# Phase 10 manual checks

## UI-08 The console looks and behaves as UI-SPEC.md describes

- Start: `task build`, then start `bin\hlab-tray.exe` from the repository root (the dev service `hlab-dev` must be registered; `scripts\dev-setup.ps1` once, elevated; re-running it updates the service display name to "HLab (dev)").
- Setup: the dev cloud stopped at first (`bin\hlab.exe cloud stop`); a Hetzner API token at hand for step 10 (optional); Claude can prepare a project with a few servers if you want filled counts.
- Steps:
  1. Look at `bin\hlab-tray.exe` in Explorer: it shows the HLab icon (white "H" on blue), so do `bin\hlab.exe` and `bin\hlabd.exe`.
  2. Look at the notification area: the tray icon is the HLab icon with a grey status dot in the lower-right corner; hover it, the tooltip reads "HLab: stopped". Check it on a light and a dark taskbar.
  3. Right-click the icon: the menu has the status line, "Open HLab", one item "Start cloud" and "Quit". Left-click opens the window; its title bar, the taskbar button and Alt+Tab show the HLab icon and the title "HLab".
  4. In the window: top bar with the icon and "HLab", breadcrumb, status pill, Start button and a gear "Administration"; below it the project list over the full width, no sidebar. With the cloud stopped it shows "Start the cloud to manage projects" with a Start button.
  5. Click "Start cloud" in the tray menu: the item reads "Starting cloud…" (disabled), the dot turns amber, then green; the item reads "Stop cloud". Click Start/Stop in the top bar once: the button stays in its loading state without flickering back until the cloud reaches running or stopped.
  6. Project list: create a project; click its row: the project console opens with the breadcrumb "Projects / <name> ▾" and a sidebar with "API tokens". Create a token (value shown once with a copy button and a warning), revoke it. Open the name in the breadcrumb: a menu switches to another project; "Projects" goes back to the list.
  7. Close and reopen the window (or restart the tray): it opens the console of the project you opened last; delete that project from the list (type its name, progress toast), restart the tray: it opens the list.
  8. Gear "Administration": its own sidebar with Server Types, Settings, Logs; all three work while the cloud is stopped.
  9. Server Types: catalog and own types with a source column; "Add server type": locations are checkboxes "<name> (<city>)" led by a checked "All locations"; untick it and save without a location: the error appears under the list; tick one and add; then remove the type.
  10. Server Types, "Update from Hetzner": a dialog asks for the token (Escape and Cancel close it, Enter submits, the button shows loading); with a wrong token the error stays in the dialog; with your token the dialog closes and a toast shows the result; reopening the dialog shows an empty field.
  11. Settings: grouped form with hints; an invalid public range shows its error under the field; "Trust the HLab CA" and "Save CA certificate" work only while the cloud runs.
  12. Logs: type a text the service logged (for example a project name) into the search field: matches appear highlighted with "N matches", Follow switches off; clear the field: the live view returns. "Load older lines" above the list prepends older lines and is disabled once the oldest file starts.
  13. Press `?` in a view title row (also "API tokens"): the help panel opens on the side and names only what you see; Escape closes it.
  14. Close the window with the X: it disappears, the icon stays; start `bin\hlab-tray.exe` again: the existing window comes to the front. "Quit" ends the tray, the cloud keeps running (`bin\hlab.exe cloud status`).
  15. Open services.msc: the service `hlab-dev` shows the display name "HLab (dev)" (after re-running `scripts\dev-setup.ps1`).
- Expected: everything says "HLab" (program, file and command names stay lowercase); colours, spacing and building blocks (buttons, pills, tables with search and row menu, dialogs, toasts, copy fields) look as UI-SPEC.md describes; the shell feels like the Hetzner Cloud Console; the icons are clear at small sizes on light and dark backgrounds.
- Result: open
