# Phase B3: context

Confirmed with the user on 2026-11-22.

## Assumptions

- Created times: `createdText` in ui/frontend/src/lib/format.ts shows local time as "YYYY-MM-DD hh:mm"; the copy in views/servers/servers.ts and the inline slice in Tokens.svelte use the shared function, so all lists and detail pages show local time like the CLI.
- Projects.svelte has no Created column any more (`created` only sorts); only Tokens is affected.
- Created times test: one unit row in tests/unit/format.test.ts, expected text computed from the local Date getters of a fixed UTC instant.
- Reboot stuck at stopping: the status watch read a stopping VM state during the guest's own reboot, and the rule "stopping stays while the VM runs" held it for good.
- Reboot fix: that rule applies only while a shutdown action is running; a stopping status the watch read from the VM goes back to running once the VM runs. No schema field.
- Reboot test: one unit row (SRV-07) in internal/resources/servers/servers_test.go with platformtest.Fake; the SRV-07 e2e stays unchanged.
- Labels: fixed in lib/LabelsField.svelte only, with `setCustomValidity` on a failed commit so the enclosing form refuses the submit; no change to the forms.
- Labels test: one e2e row (UI-02) in ui/frontend/tests/e2e/placementgroups.spec.ts covering a create form and "Save labels" on the Labels tab.
- Server delete: the API delete action pushes an agent document after the server is removed; a failed push only logs a warning; project deletion does not push per server (mgmt pushes once at the end).
- Server delete test: one unit row (LB-06) in internal/resources/servers/servers_test.go.
- Shutdown of a booting guest: `PowerShutdown` retries with `untilAccepted(ctx, guestReadyTimeout, 2s, InitiateShutdown)` like reboot and reports InvalidState after the deadline.
- Shutdown test: one unit row (SRV-07) in internal/platform/hyperv/hyperv_test.go on a small shutdown function taking the ask function.
- Grouping: plan servers/hyperv (items 2, 4, 5), plan frontend (items 1, 3), then Documentation (docs/development/api-coverage.md, docs/deviations.md if needed).

## User additions

- Shutdown marker: created per server when the shutdown action starts, removed when that action ends with success or error; in memory only, so after an hlabd restart the status simply follows the VM; for a guest that shuts itself down the status becomes off once the VM is off.
- LabelsField: the field's own error line is linked to the input with aria-describedby; the `invalid` event gets preventDefault so the browser bubble does not show.
- Labels e2e row: in both cases it checks that no request is sent and the error line stays visible.
- Server delete warning: a failed push logs a warning naming the server ID and the load balancer; a one-sentence code comment says the next push repairs the state.

## Changed by the planner

- Shutdown marker: the shutdown action ends once the guest accepted the request, before the VM is off; the running action's lock is the marker (start to end), and afterwards a `stopping` set by the action stays until the VM is off, while one the watch read itself returns to `running` when the VM runs.
