# Phase B3: Backlog: console times and labels, server power and delete fixes

Taken in by the user with /wcc:change on 2026-11-22.

## Items

- [ ] bug: ui/frontend (Projects.svelte, Tokens.svelte): the Created column shows the stored UTC time without a zone, so users see times off by their UTC offset from `hlab project list` and `hlab token list`, which print local time (source: 10 10-33 review)
- [ ] bug: servers (power actions): under a full parallel `task e2e` the status of a rebooted server stayed `stopping` for over 2 minutes after the guest was back on SSH with a new boot_id (SRV-07 at the B2 close; 3 of 3 green alone), so a user rebooting under load sees a running server as stopping and actions on it may be refused (source: B2 close)
- [ ] bug: ui/frontend (lib/LabelsField.svelte): text left in the labels box that breaks the label rule is not sent when a create form or "Save labels" is submitted, and the error disappears with the form, so the user does not learn the label was not saved (source: 14 verifier)
- [ ] bug: servers (delete): deleting a server removes its load balancer targets in the store but pushes no agent document, so the appliance keeps balancing to the deleted server until a later push (source: 13 13-04 fix)
- [ ] bug: platform/hyperv (shutdown): `PowerShutdown` right after poweron is refused by a still booting guest (32768) and does not retry like reboot does, so a user who shuts down a booting server gets an error (source: 13 13-08)

Note on the Projects item: the project list no longer has a Created
column (cards and rows, UI-11; `created` only sorts), so only Tokens and
the shared `createdText` change.

Note on the shutdown marker (CONTEXT.md "User additions"): the shutdown
action ends as soon as the guest has accepted the request, usually before
the VM is off. While the action runs, the server is locked and the status
watch skips it, so the action's lock is the marker the user asked for: it
exists from the start to the end (success or error) of the action and is
kept nowhere else. After the action the status stays `stopping` because
the watch tells apart who set it: a `stopping` the watch itself read from
the VM (as during a guest's own reboot) goes back to `running` when the VM
runs; a `stopping` set by the shutdown action stays until the VM is off.
The watch remembers in memory only which servers it moved to `stopping`
itself; after an hlabd restart the cloud stop and restore have already set
every status from the VM.

## Interfaces

```go
// internal/platform/hyperv/hyperv.go (package hyperv)

// shutdown asks the guest through ask (InitiateShutdown) every interval
// until it accepts or timeout has passed (untilAccepted); a guest that
// still refuses then gives a *platform.Error of Kind InvalidState naming
// vm name and the last return value. Power(PowerShutdown) calls it with
// guestReadyTimeout and 2 s.
func shutdown(ctx context.Context, name string, timeout, interval time.Duration,
	ask func(context.Context) (bool, int64, error)) error
```

```ts
// ui/frontend/src/lib/format.ts: signature unchanged, meaning changed.

/** createdText shows an API timestamp in local time as "YYYY-MM-DD hh:mm". */
export function createdText(created: string): string;
```

## Plans

- [ ] B3-01 Server status after reboot and agent push after delete
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-07, LB-06 | Tests: internal/resources/servers/servers_test.go, internal/resources/loadbalancers/targets_test.go, internal/resources/loadbalancers/loadbalancers_test.go
  - Reads: REQUIREMENTS SRV-07, LB-06; ARCHITECTURE "hlabd (Windows service)" (servers module, status watch, agent document); DECISIONS "2026-11-21 Reboot of a booting guest retries for two minutes"; the note on the shutdown marker above; internal/resources/servers/watch.go, power.go, servers.go `delete`, `remove`, `SetLoadBalancers`; internal/cloud/cloud.go `PushAgent`; internal/mgmt/mgmt.go project deletion (pushes once at the end)
  - Size: ~110 lines in 3 files
  - Adjusts: -
  - [ ] internal/resources/servers/watch.go: the module keeps in memory the IDs of servers the watch itself moved to `stopping` from a VM reading. The rule "stopping stays while the VM runs" applies only to servers not in that set; a server in it goes back to `running` when its VM runs. The entry is removed when the watch writes another status and in `touch` (every status an action sets). Doc comment of `watch` updated: the shutdown action's lock covers the action itself, the stopping it leaves stays until the VM is off, and a stopping read during a guest's own reboot does not stick.
  - [ ] internal/resources/servers/servers.go `delete`: before `remove`, read the IDs of the load balancers targeting the server through the `loadBalancers` hook (nil hook: none); after `remove` succeeds, call `m.d.PushAgent`. A failed push does not fail the action: `slog.Warn` names the server ID and the load balancer IDs, with a one-sentence comment that the next push repairs the appliance's state. `Delete` (project deletion) does not push.

- [ ] B3-02 Shutdown of a booting guest retries
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-07 | Tests: internal/platform/hyperv/hyperv_test.go
  - Reads: REQUIREMENTS SRV-07; DECISIONS "2026-11-21 Reboot of a booting guest retries for two minutes"; internal/platform/hyperv/hyperv.go `askGuest`, `untilAccepted`, `reboot`, `Power`
  - Size: ~40 lines in 1 file
  - Adjusts: -
  - [ ] internal/platform/hyperv/hyperv.go: `shutdown` as in Interfaces; `Power` case `PowerShutdown` returns `shutdown(ctx, name, guestReadyTimeout, 2*time.Second, <ask calling p.askGuest(ctx, name, "InitiateShutdown")>)`; the doc comments of `guestReadyTimeout` and `Power` name shutdown too.

- [x] B3-03 Console Created times in local time
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/format.test.ts
  - Reads: REQUIREMENTS UI-02, UI-05; ui/frontend/src/lib/format.ts; ui/frontend/src/views/servers/servers.ts; ui/frontend/src/views/Tokens.svelte; internal/cli/cli.go project and token list (local time)
  - Size: ~20 lines in 3 files
  - Adjusts: -
  - [x] ui/frontend/src/lib/format.ts: `createdText` parses the timestamp with `new Date` and builds year, month, day, hours and minutes from the local getters, zero-padded, as "YYYY-MM-DD hh:mm"; an unparsable value is shown as given.
  - [x] ui/frontend/src/views/servers/servers.ts: its own `createdText` becomes a re-export of the one in lib/format.ts; ui/frontend/src/views/Tokens.svelte: the Created column uses `createdText`.

- [ ] B3-04 Labels box refuses a submit with invalid text
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/e2e/placementgroups.spec.ts
  - Reads: REQUIREMENTS UI-02; UI-SPEC "Labels field"; E9 in research/EXPERIMENTS.md; ui/frontend/src/lib/LabelsField.svelte, LabelsTab.svelte, FormPage.svelte; ui/frontend/tests/e2e/console.ts `addLabel`, placementgroups.spec.ts
  - Size: ~30 lines in 1 file
  - Adjusts: -
  - [ ] ui/frontend/src/lib/LabelsField.svelte: `commit` calls `input.setCustomValidity(message)` when the text breaks the label rule or repeats a key, and `setCustomValidity("")` when the text becomes a chip or is empty, so the enclosing form (FormPage create forms, LabelsTab "Save labels") does not submit. The input's `oninvalid` calls `preventDefault()` (no browser bubble) and focuses the input; the field's own error line stays, linked with `aria-describedby` as now. Header comment updated.

- [ ] B3-05 Documentation
  - Wave: 2 | Depends on: B3-01, B3-02, B3-03, B3-04 | Review: alone
  - Requirements: DOC-01 to DOC-08 (no new rows; the existing test/docs tests stay green) | Tests: -
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC; docs/development/api-coverage.md "Server actions"; docs/deviations.md servers paragraph; ui/frontend/src/help/ texts that mention Created times or labels
  - Size: ~20 lines in 2 to 3 files
  - Adjusts: -
  - [ ] docs/development/api-coverage.md "Server actions" and docs/deviations.md (servers): `shutdown` asks a guest that is still booting again for up to 2 minutes before it fails; the server shows `stopping` until the guest turns off, and a reboot from inside the guest no longer leaves it there. Console times are local time wherever a help text or the guide names them.
