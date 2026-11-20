# Phase 11 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| UI-02 | `waitAction` polls `GET /actions/{id}` until the action is no longer running, reports every polled progress, returns a `success` action and throws `ActionError` with the action's code and message on `error`; `runAction` waits for `action` and then every `next_actions`, shows a progress toast with the percentage that ends as success, or as an error toast with code and message that stays; a failing request ends as an error toast without polling (table) | unit | ui/frontend/tests/unit/actions.test.ts: UI-02 polling | 11-01 |
| UI-02 | `parse` and `href` agree for the project list, the list, `new` and a detail with tab of `servers` and `ssh-keys`, the tokens list and the admin views; a detail without a valid tab gets `overview`; unknown views, IDs that are not numbers and unknown areas give undefined; `consoleStart` is the Servers list (table) | unit | ui/frontend/tests/unit/routes.test.ts: UI-02 routes | 11-02 |
| UI-02 | `formErrors` puts each `details.fields` message under its known field, joins several messages, maps `labels.x`, `networks[0]` and `a.b` to their top-level field, and sends unknown field names and errors without fields to the general line as `code: message` (table) | unit | ui/frontend/tests/unit/forms.test.ts: UI-02 field errors | 11-04 |
| UI-02 | `toRows` lists labels sorted by key; `toLabels` skips empty rows, keeps an empty value and refuses a duplicate key naming its second row (table) | unit | ui/frontend/tests/unit/forms.test.ts: UI-02 labels | 11-04 |
| UI-02 | `request` builds method, path and body for every entry of `menuActions` and `tabActions` from its input, one case per entry (power on, shutdown, reboot, reset, power off, reset root password, rebuild, change type with `upgrade_disk`, rename, add to and remove from placement group, delete; backups on and off, create backup, create snapshot, restore, delete image, rescue on and off, ISO attach and detach, network attach and detach, alias IPs, reverse DNS set and reset, firewall apply and remove, volume attach and detach, protection); Console is disabled with "Not available in HLab" and builds no request (table) | unit | ui/frontend/tests/unit/serverActions.test.ts: UI-02 requests | 11-05 |
| UI-02 | confirmation is plain for power off, reset, rebuild, change type, enable rescue, restore and delete image, a typed name only for delete, none for power on, shutdown and reboot; the password flag is set exactly for reset root password, rebuild, restore and enable rescue (table) | unit | ui/frontend/tests/unit/serverActions.test.ts: UI-02 confirmation | 11-05 |
| UI-02 | in a new project the SSH Keys section opens its help text; creating a key with an invalid public key shows the API's message under "Public key"; a valid key is created and listed with the fingerprint the API reports; a label added in its Labels tab is in the API; deleting it after the confirmation removes it from the list and the API | e2e | test/e2e/ui_test.go: TestE2E/UI-02 running ui/frontend/tests/e2e/sshkeys.spec.ts: UI-02 ssh keys | 11-06 |
| UI-02 | in a new project the console opens on Servers, whose help text opens; the create form (location, image, type, name, no SSH key) creates a server, the root password shows once in its dialog, the list and the API show it running; Rename on the detail page changes the name in the API; Power off asks for confirmation, a toast shows the running action with progress and ends in success, and the API reports `off`; Delete needs the name typed and removes the server from the list and the API | e2e | test/e2e/ui_test.go: TestE2E/UI-02 running ui/frontend/tests/e2e/servers.spec.ts: UI-02 servers | 11-08 |

Notes:

- The UI-02 Go subtest runs `playwright test --grep UI-02` against the
  shared e2e tray, so both specs run in it: SSH keys first, then servers.
  Each spec creates its own `e2e-ui02-*` project and a token through
  `hlab` (fixtures `hlab()`), checks the API with that token at
  `apiEndpoint`, and deletes the project in its cleanup. It runs with the
  other UI rows, after UI-05, with a time limit of about 40 minutes
  (the first server boot may download the image).
- Help texts: UI-07 (`help.test.ts`) and DOC-05 (`test/docs/docs_test.go`)
  iterate views.json, so the new views `servers` and `ssh-keys` need
  their help texts without a new row.
- Existing rows: DOC-01 to DOC-06 keep their rows in test/docs/docs_test.go;
  DOC-07 and UI-08 are manual. The manual check of this phase (look and
  operation of the frame, labels and protection tabs of a server, the
  tabs and actions not walked by the e2e flow) goes into CHECKS.md at the
  close.
- Adjusted, not new: ui/frontend/tests/e2e/projects.spec.ts (UI-05) and
  ui/frontend/tests/screenshots/console.spec.ts open "API tokens" through
  the sidebar, since a project console now opens on Servers (11-02).
