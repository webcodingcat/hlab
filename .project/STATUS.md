STATUS: IN_PROGRESS

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 23 Server console (next)
- Base branch: main
- Last done: phase 21 merged into main (2026-11-23): storage boxes and own storage box types in the console; hcloud, OpenTofu and Ansible pass their storage box rows; project deletion now also removes storage boxes and actions
- Next: /wcc:change for Q24 (or leave it), then /wcc:phase in a new session builds phase 23 (server console), then phase 16 (release checks in test VMs)

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- INST-01 to INST-06 (12): the elevated installer suite passes in the development test VM
- INST-03 (12): the setup creates shortcuts that open only the tray program, and "Launch" starts it without admin rights
- INST-05 (12): uninstalling asks whether to keep the data; with the default answer no file of hlab remains
- UI-09 (14): the example environment can be created and deleted by hand in the console
- COMPACT (19): a deleted box gives its space on the host back after a cloud stop (data.vhdx compaction)
- All of them, and those of later phases, are done in phase 16: the suite in the development test VM, the others in the user test VM (SPEC.md "Test VMs for the release checks").

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- Q24 (phase 21, 21-14 docs): Should `tofu destroy` of a storage box with subaccounts and snapshots succeed in one run?
  - Context: OpenTofu deletes a box's snapshot and subaccount in parallel; hlab locks the box during any action (phase 18 decision "Every storage box action locks the box"), so the second delete gets 423 `locked` and the first `tofu destroy` stops; a second run deletes the rest. Documented in docs/guide.md and docs/deviations.md.
  - Options: 1. Keep it and document it (as now) 2. Queue a delete that arrives while the box is locked instead of answering 423 3. Let deletes of subaccounts and snapshots run while another delete of the same box runs
  - Recommendation: 1, if real Hetzner answers the same way; otherwise 2, because users of OpenTofu expect one destroy to clean up.
  - Affects: CLI-06, SBOX-02, ARCHITECTURE.md "Storage boxes" (box lock)

## If BLOCKED

- Reason: -
- Decision needed: -
