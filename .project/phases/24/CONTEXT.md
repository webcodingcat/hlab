# Phase 24 context

Confirmed by the user at the start of the phase.

## Assumptions

- Deleting a subaccount or a snapshot while another action on the same box runs is accepted at once (201, action `running` until its turn), never 423 `locked` because of the box.
- Waiting deletes run in the order they were sent, after earlier actions on that box; the storage VM still runs one job per box at a time.
- Every other box and subaccount action still answers 423 `locked` while any action of the box runs, a waiting delete included (also deleting the box itself).
- A waiting delete that finds its subaccount or snapshot already gone (box deleted, rollback, double delete) ends successfully.
- A failed delete still restores the subaccount and the action ends with an error, as today.
- One new OpenTofu e2e row (CLI-06 destroy): box with subaccount and snapshot, one `tofu destroy`, all three gone; existing rows stay.
- docs/guide.md and docs/deviations.md drop the second `tofu destroy`; deviations.md lists the two deletes among actions that wait; api-coverage.md and the served deviation list change the same way.

## Technical choices

- `delete_subaccount` in `subAction` and `deleteSnapshot` start with `acts.StartQueued(tx, p.ID, command, [box, sub|snap], nil)` instead of `begin`; `waitTurn` keeps the order; `actions.Manager` unchanged.
- At its turn, `deleteSnapshot` reloads box and snapshot; if either is gone it skips the agent job and succeeds; the subaccount delete path does the same.
- `api.Deviations()`: storage box entry reworded, two new entries for the DELETE subaccount and snapshot routes ("waits as running instead of answering 423 locked").
- Unit rows go into the existing storageboxes tests (`holdAction`, gate); no existing unit test expects 423 for these deletes.

## Research

- none needed (phase 21 RESEARCH.md covers the provider's delete behaviour).
