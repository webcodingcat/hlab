# Phase 10 context

Confirmed at the start of phase 10. The planner's assumptions stand with these corrections and answers.

## Corrections

- Own server type removal: servers keep working. Power on (power.go), rebuild (rebuild.go) and reconcile at cloud start (reconcile.go) take cores, memory and disk from the server's stored `server_type` copy when the type is no longer in the catalog; create and change_type still need the catalog. One unit test.
- Server Types list: the bridge uses the same Go package hlabd uses to merge and check catalog and own types; an invalid or colliding custom file shows as a problem like `custom_server_types` in /status. The catalog shows while stopped too; UI-SPEC.md updated.
- Project delete: the confirm dialog requires typing the project name; UI-SPEC.md updated.
- Settings write: the bridge validates with the `settings` package and writes only valid content, in one write; before saving it rereads the file and keeps `trust_ca` from the file (owned by `PUT /trust`).
- stop_behaviour in the form: hint "Applies at the next stop; at a Windows shutdown only after the next cloud start." (Hyper-V refuses AutomaticStopAction changes on running VMs, phase 09.)
- Fix backlog bug in this phase: hlabd ends log followers at stop at once instead of waiting for the shutdown timeout; move the item to BACKLOG-DONE.md.

## Answers

- Q1 server-types.custom.json: hlabd creates it at start when missing (`[]`) with the settings.toml ACL (Interactive Users modify); the tray validates with the hlabd package, writes directly and calls `POST /mgmt/v1/settings/reload` while the cloud runs.
- Q2 Quit: ends only the tray; the cloud keeps running; no question.
- Q3 UI-01: the e2e build (tag `e2e`) exposes the tray state (icon key, tooltip, menu items with enabled state) through a bridge method; the test drives start/stop through the menu handlers of the same controller; the real icon is judged in the UI-08 manual check.

## Additions

- Connection banner: only when the SCM reports running and `/mgmt/v1/status` fails three polls in a row (about 6 s); the poller keeps its 2 s interval; the banner clears by itself when the pipe answers; a regular stop shows only "stopped".
- Log stream: a broken follow stream reconnects automatically while the cloud runs and the view follows, at the poller's interval, continuing after the last shown line without duplicates; when the cloud stops the view switches to the files, and back to the stream at start.
- Where the research says otherwise, the research wins; record that in DECISIONS.md.

## Findings fixes (10-20 onward)

- Assumptions of the planner confirmed by the user without corrections (2026-11-20), including the four the research changed: icon group under resource ID 3 and 32512 in a committed `rsrc_windows_amd64.syso` per main package, no `Options.Icon`; icon look checked by hand under UI-08, generator gets a `debug/pe` unit test; screenshot plan starts with a CDP probe (dark mode, 1200x800, screenshot) and stops on failure; static shields.io badges and relative `<picture>` paths, checked under DOC-07.
- README screenshots: the script creates sample projects ("demo", "staging", "ci") and filters the list with the search field so only these show; real project names are never published.
- Application icon: a white "H" on a rounded square in the accent blue; also the banner motif and the base of the four tray state icons.
- Last opened project: the window opens the console of the last opened project if it still exists, otherwise the project list.
- `.syso`: produced by `tools/icons` with `github.com/tc-hib/winres` v0.3.1 (0BSD, used by Wails' own `generate syso`); SPEC "Stack" names it; the generated files are committed so `task build` does not run the tool.
