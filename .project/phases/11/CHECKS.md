# Phase 11 manual checks

Phase 11 stops for a manual check of the look and operation of the console
frame before phase 14 builds the remaining sections on the same pattern.

## UI-02 The console frame with Servers and SSH Keys looks right and is pleasant to operate

- Start: `task build`, then restart the dev service (`Restart-Service hlab-dev`, elevated) and start `bin\hlab-tray.exe`; open the window from the tray icon and start the cloud if it is stopped.
- Setup: a project with an API token, e.g. `bin\hlab.exe project create demo`; an SSH public key at hand (e.g. the content of `~\.ssh\id_ed25519.pub`). Claude can prepare the project on request.
- Steps:
  1. Click the project in the project list: the console opens on Servers; the sidebar shows Servers, SSH Keys, a separator and API tokens. Open the help with "?".
  2. SSH Keys: "Add SSH key", paste an invalid key and submit (the error shows under "Public key"), then a valid key. Open it, change the name on Overview, add a label in the Labels tab, then delete it from the actions menu (plain confirmation).
  3. Servers: "Create server"; look through the form (Location, Image, Type, Networking, SSH keys, Volumes, Firewalls, Backups, Placement group, Labels, Cloud config, Name with its default). Create a server without an SSH key: the root password dialog shows once.
  4. Watch the progress toast until the server runs; open it and walk through the tabs Overview, Networking, Firewalls, Volumes, Backups, Snapshots, Rescue, ISO, Graphs, Labels, Protection.
  5. In the actions menu: Shutdown, Power on and Reboot run without a question; Power off, Reset, Rebuild and Change type ask first; Console is disabled with "Not available in HLab". Try Enable rescue (asks, shows a password) and Disable backups after enabling them (asks first).
  6. Turn on delete protection in Protection, try Delete (the API refuses with a toast), turn it off, then Delete: the name must be typed.
  7. Switch the Windows theme between light and dark once and look at list, form, detail and dialogs.
- Expected: layout, spacing, tables, menus, dialogs and toasts look consistent with the existing Projects and API tokens views in both themes; every action ends in a toast; errors appear at their field; nothing feels slow or confusing. Note anything you would want different before the other sections follow this pattern.
- Result: open
