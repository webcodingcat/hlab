STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 10 Tray program: shell, status, settings, server types, projects, logs (waits for manual check UI-08, branch wcc/phase-10 unmerged)
- Base branch: main
- Last done: phase 09 merged into main (2026-11-19): servers saved or shut down at cloud stop and restored at start, configurable public range, incompatible state refused with `--discard-state`, rotated logs with `hlab logs`, LB IPv6 prefix 0xA0000000 (Q14)
- Next: /wcc:phase fixes the UI-08 findings (app icon, product name "HLab", one Start/Stop tray item, tray icon with status dot, shell like the Hetzner Console: project list, project console, Administration; log search and older lines; Hetzner token in a dialog; locations as checkboxes; Start/Stop button flicker; README without the developer setup (DOC-07); docs/ for users, docs/development/ for contributors; short deviations page for users; compact README with banner and screenshots; views from views.json (Q15); e2e runs delete their projects) on wcc/phase-10, then /wcc:feedback for UI-08 again

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:feedback walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons (does not fit, fix in phase 10 plan)
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- UI-08 (10): the console looks and behaves as UI-SPEC.md describes

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
