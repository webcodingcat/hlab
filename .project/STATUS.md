STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 10 Tray program: shell, status, settings, server types, projects, logs (waits for manual check UI-08, branch wcc/phase-10 unmerged)
- Base branch: main
- Last done: phase 09 merged into main (2026-11-19): servers saved or shut down at cloud stop and restored at start, configurable public range, incompatible state refused with `--discard-state`, rotated logs with `hlab logs`, LB IPv6 prefix 0xA0000000 (Q14)
- Next: /wcc:phase fixes the UI-08 findings (app icon, product name "HLab", one Start/Stop tray item, tray icon with status dot, shell like the Hetzner Console: project list, project console, Administration; log search and older lines; Hetzner token in a dialog; locations as checkboxes; Start/Stop button flicker; README without the developer setup (DOC-07); docs/ for users, docs/development/ for contributors; short deviations page for users; compact README with banner and screenshots; e2e runs delete their projects) on wcc/phase-10, then /wcc:feedback for UI-08 again

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:feedback walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience (does not fit, fix in phase 10 plan)
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- UI-08 (10): the console looks and behaves as UI-SPEC.md describes

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- Q15 (phase 10 verifier): How should DOC-05 be checked, given its test cannot see which buttons exist?
  - Context: test/docs DOC-05 checks that every view in views.json has a help text and the reverse; "names only views and buttons that exist" would need reading Svelte source, which the test rules forbid, and views.json is not compared with the views the app has (ui/frontend/src/state.ts). The buttons named today were checked by hand and are correct.
  - Options: 1. split DOC-05: help-file check stays [unit], the "only existing views and buttons" clause becomes manual next to DOC-07 2. the app reads its view list from views.json, so the unit test covers the real views, and the button clause becomes manual 3. keep as is
  - Recommendation: 2, because one source for the views closes the gap the test can close, and only the button names need eyes
  - Affects: DOC-05, DOC-07

## If BLOCKED

- Reason: -
- Decision needed: -
