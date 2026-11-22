# Phase B3: Backlog: console times and labels, server power and delete fixes

Taken in by the user with /wcc:change on 2026-11-22.

## Items

- [ ] bug: ui/frontend (Projects.svelte, Tokens.svelte): the Created column shows the stored UTC time without a zone, so users see times off by their UTC offset from `hlab project list` and `hlab token list`, which print local time (source: 10 10-33 review)
- [ ] bug: servers (power actions): under a full parallel `task e2e` the status of a rebooted server stayed `stopping` for over 2 minutes after the guest was back on SSH with a new boot_id (SRV-07 at the B2 close; 3 of 3 green alone), so a user rebooting under load sees a running server as stopping and actions on it may be refused (source: B2 close)
- [ ] bug: ui/frontend (lib/LabelsField.svelte): text left in the labels box that breaks the label rule is not sent when a create form or "Save labels" is submitted, and the error disappears with the form, so the user does not learn the label was not saved (source: 14 verifier)
- [ ] bug: servers (delete): deleting a server removes its load balancer targets in the store but pushes no agent document, so the appliance keeps balancing to the deleted server until a later push (source: 13 13-04 fix)
- [ ] bug: platform/hyperv (shutdown): `PowerShutdown` right after poweron is refused by a still booting guest (32768) and does not retry like reboot does, so a user who shuts down a booting server gets an error (source: 13 13-08)
