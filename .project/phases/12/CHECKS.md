# Phase 12 manual checks

## INST-01 to INST-06 The elevated installer suite passes on this machine

- Start: an elevated PowerShell in the repository root, then `task e2e:elevated`
  (one row: `task e2e:elevated -- -run 'TestInstall/INST-04$'`).
- Setup: no release hlab installed (no service `hlab`, no "HLab" under
  Apps); the dev service `hlab-dev` registered and its cloud started once
  (so the dev switch, `hlab-dev-appliance` and the service exist for
  INST-04/dev-profile); a running Ubuntu WSL2 distro; the WiX extensions
  7.0.1 in the global cache. The suite stops a running dev cloud and starts
  it again at the end. A full run can take several hours (limit 420 min).
- Steps: 1. Run the command. 2. Wait for the end; each row logs its start
  and end.
- Expected: `ok` for the package and every subtest `TestInstall/INST-01`,
  `INST-04`, `INST-04/dev-profile`, `INST-05`, `INST-05/keep`, `INST-06`,
  `INST-06/downgrade` passed. A failure names the object or log line;
  report it with `/wcc:report`.
- Result: open

## INST-03 The setup creates shortcuts that open only the tray program

- Start: `task installer VERSION=0.1.0`, then double-click
  `bin\hlab-setup-0.1.0.exe` (not elevated; Windows asks for admin rights
  itself; SmartScreen may warn because the package is unsigned: "More info"
  → "Run anyway").
- Setup: no hlab installed (run this after the elevated suite, which leaves
  nothing installed).
- Steps: 1. Click "Install" and confirm the admin prompt. 2. On the success
  page click "Launch". 3. Open the start menu and type "HLab". 4. Look at
  the desktop.
- Expected: "Launch" opens the HLab tray icon and window without an admin
  prompt (Task Manager, Details, column "Elevated": No for hlab-tray.exe).
  The start menu and the desktop each have one "HLab" shortcut; it opens the
  tray, nothing else. HLab is not in the startup apps (Task Manager,
  Startup apps).
- Result: open

## INST-05 Uninstalling asks whether to keep the data

- Start: Settings → Apps → Installed apps → "HLab" → Uninstall.
- Setup: HLab installed by the check above, the tray running.
- Steps: 1. Start the uninstall. 2. Look at the page that opens. 3. Leave
  the checkbox unchecked and click "Uninstall". 4. Afterwards look for
  `C:\Program Files\hlab`, `C:\ProgramData\hlab`,
  `%AppData%\hlab-tray.exe` and the "HLab" shortcuts.
- Expected: the page shows a checkbox "Keep projects, servers and
  settings", unchecked. The tray closes, and after the uninstall none of the
  folders or shortcuts exist and "HLab" is gone from Apps. Run `task openapi`
  afterwards if you work on the repository (the uninstall deletes
  `%LocalAppData%\hlab`, which holds the development OpenAPI cache).
- Result: open
