# Phase 11 context

Confirmed at the start of phase 11. The planner's assumptions stand with these corrections and answers. No new research; RESEARCH.md stands.

## Corrections

- One-time secrets: the `root_password` from server create without SSH keys, `reset_password`, `enable_rescue` and `rebuild` shows in one shared dialog with a CopyField and a "shown only once" warning; dropped when the dialog closes.
- Console: HLab always answers `request_console` with `unsupported_error` (docs/deviations.md); the actions menu shows a disabled entry "Console" with the hint "Not available in HLab" and the window never calls `request_console`.
- Confirmation before: power off, reset, rebuild, change type (the dialog says a disk upgrade cannot be undone), enable rescue, restoring a server from a backup or snapshot, deleting a backup or snapshot. Shutdown, reboot and power on run without confirmation. Only deleting a server needs its name typed; deleting an SSH key needs a plain confirmation.
- The server actions menu is a pure TypeScript module (`lib/serverActions.ts`): label, method, path, body builder, confirmation flag, returns-password flag per entry; the Svelte menu only renders it.

## Answers

- Q1 (e2e depth for "every server action"): the UI-02 server flow creates through the form, changes one field, runs one power action and deletes after typing the name; the SSH key flow creates, changes a label and deletes. A unit test over the actions menu (and the tab actions) proves every entry calls the right endpoint with the right body and the confirmation flags match the list above.
