STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 16 Release checks in test VMs (waits for the manual checks on branch wcc/phase-16), the last phase of milestone 1
- Base branch: main
- Last done: backlog phase B4 merged into main (2026-11-24): a network attach right after a boot no longer costs the server its seed ISO (Q26), deleting and pruning snapshots a failed rollback left behind works, the guest hook keeps user netplan settings after a carrier drop, and the network drops after a resume are documented (Q27)
- Next: /wcc:phase builds the findings in phases/16/PLAN.md (WSL trust for the interactive user (Q28), uninstall during a pending service start, cleanup scripts that exit 1 silently); then task e2e:elevated again in release-check-dev and /wcc:report for the user-VM checks

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
- NAMES (16): the appliance shows its name `hlab-appliance` after the first cloud start in the user test VM
- INST-06 (16): an update from 0.1.0 to 0.1.1 in the user test VM keeps project, server and settings, and the server answers over SSH
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
