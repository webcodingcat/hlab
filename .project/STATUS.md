STATUS: IN_PROGRESS

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 30 Repository, CI and release v0.1.0, the last phase of milestone 1 (not started)
- Base branch: main
- Last done: phase 16 merged into main (2026-11-25): release checks in the two test VMs, all manual checks of milestone 1 fit; on the way the setup bundle, upgrade, uninstall, storage VM trim and stall, WSL trust for the interactive user (Q28) and several console fixes were built
- Next: /wcc:phase in a new session runs phase 30 (repository, CI, release v0.1.0, full task e2e, tag wcc-milestone-1)

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

 uninstalling asks whether to keep the data; with the default answer no file of hlab remains
- none

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
