STATUS: BLOCKED

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 12 Installer and uninstaller (blocked, branch wcc/phase-12)
- Base branch: main
- Last done: phase 15 merged into main (2026-11-21): project display names with derived names (schema version 2 and its migration), `hlab project create <display-name>`, and the tray's project list as cards with grid/list, sort, resource counts per tab and the "+ New project" tile
- Next: the user adds the two WiX extensions (see "If BLOCKED"), then /wcc:phase in a new session resumes phase 12 at 12-07; before the release /wcc:report walks through UI-09, LIFE-05 and DOC-07

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

- Reason: plan 12-07 (MSI) cannot build: WiX extensions WixToolset.Util.wixext and WixToolset.BootstrapperApplications.wixext 7.0.1 are missing from the global cache (WIX0144), and installing them was refused by the permission check in this session; 12-09, 12-10 and 12-11 depend on it. 12-01 to 12-06 and 12-08 are done and merged into wcc/phase-12; 12-07 is written on wcc/phase-12-12-07 (worktree ../hlab-wt/12-07).
- Decision needed: action by the user, without elevation: `wix extension add -g WixToolset.Util.wixext/7.0.1 -acceptEula wix7` and `wix extension add -g WixToolset.BootstrapperApplications.wixext/7.0.1 -acceptEula wix7`; then choose "done" at the next /wcc:phase.
