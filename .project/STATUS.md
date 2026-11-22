STATUS: IN_PROGRESS

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 19 Storage VM and SSH access (next)
- Base branch: main
- Last done: phase 18 merged into main (2026-11-22): the Hetzner API serves storage box types, storage boxes, subaccounts and their actions under `/hetzner/v1`, and `task catalog` writes the storage box types from the real API
- Next: /wcc:phase in a new session builds phase 19 (Storage VM and SSH access), then phases 20 and 21; /wcc:change takes the open decision Q21 and items from "Later" before phase 16

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
- All of them, and those of later phases, are done in phase 16: the suite in the development test VM, the others in the user test VM (SPEC.md "Test VMs for the release checks").

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- Q21 (phase 18, verifier): Should SPEC.md D4 raise the limit of `docs/deviations.md` from about 150 lines?
  - Context: the file has 177 lines after phase 18 (163 before), the new "Storage boxes" topic added about 16; phases 19 and 20 add protocol and snapshot text.
  - Options: 1. raise the D4 limit to about 200 lines 2. shorten other topics to get back under 150 3. keep the wording and accept the overrun as "about"
  - Recommendation: 1, because the storage box topic keeps growing with phases 19 and 20.
  - Affects: DOC-03, SPEC.md D4

## If BLOCKED

- Reason: -
- Decision needed: -
