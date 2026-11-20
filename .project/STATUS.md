STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 11 Tray console: shared frame, servers, SSH keys (built on branch wcc/phase-11, unmerged; waits for the manual check)
- Base branch: main
- Last done: phase B2 merged into main (2026-11-20): networks deleted last at project deletion, DOC-03 checks every registered route, e2e harness stops the dev service in every holding state, RDNS-02 and SRV-10 retry on `locked`; LB `https` health-check item dropped (the real API rejects it)
- Next: /wcc:report for the UI-02 (11) frame check, then /wcc:phase to close phase 11; DOC-07 (01) waits until the repository is on GitHub

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- UI-02 (11): the console frame with Servers and SSH Keys looks right and is pleasant to operate

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
