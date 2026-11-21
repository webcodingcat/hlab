STATUS: IN_PROGRESS

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 12 Installer and uninstaller (next)
- Base branch: main
- Last done: phase 15 merged into main (2026-11-21): project display names with derived names (schema version 2 and its migration), `hlab project create <display-name>`, and the tray's project list as cards with grid/list, sort, resource counts per tab and the "+ New project" tile
- Next: /wcc:phase in a new session builds phase 12 (stops for a manual check); before the release /wcc:report walks through UI-09, LIFE-05 and DOC-07 (DOC-07 waits until the repository is on GitHub)

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- UI-09 (14): the example environment can be created and deleted by hand in the console (deferred to before the release)

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
