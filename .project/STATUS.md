STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 16 Release checks in test VMs (waits for the manual checks on branch wcc/phase-16), the last phase of milestone 1
- Base branch: main
- Last done: backlog phase B4 merged into main (2026-11-24): a network attach right after a boot no longer costs the server its seed ISO (Q26), deleting and pruning snapshots a failed rollback left behind works, the guest hook keeps user netplan settings after a carrier drop, and the network drops after a resume are documented (Q27)
- Next: DOC-07 (reading README and guides) with /wcc:report; /wcc:phase then closes phase 16 (full task e2e) and tags wcc-milestone-1

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons
 uninstalling asks whether to keep the data; with the default answer no file of hlab remains
- All of them, and those of later phases, are done in phase 16: the suite in the development test VM, the others in the user test VM (SPEC.md "Test VMs for the release checks").

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- none

## If BLOCKED

- Reason: -
- Decision needed: -
