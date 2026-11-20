STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 14 Tray console: remaining resources (built; the changes "console sidebar in groups, own ISOs" and "volume create form like Hetzner" and the placement group Type select and labels as chips wait in phases/14/PLAN.md, then the manual check UI-09, on branch wcc/phase-14)
- Base branch: main
- Last done: phase 11 merged into main (2026-11-20): console frame (API client, action toasts, detail/form/labels/protection building blocks, routes) with the complete Servers and SSH Keys sections; UI-02 frame check fits
- Next: /wcc:phase builds the changes of phase 14 (grouped sidebar, section tabs, own ISOs; volume create form), then /wcc:report for UI-09, then phase 15 (project display names, Hetzner-style project list); DOC-07 (01) waits until the repository is on GitHub

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- UI-09 (14): the example environment can be created and deleted by hand in the console

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
