STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 10 Tray program: shell, status, settings, server types, projects, logs (findings fixes 10-20..10-35 done on wcc/phase-10, unmerged; waits for manual checks UI-08 and DOC-07)
- Base branch: main
- Last done: phase 09 merged into main (2026-11-19): servers saved or shut down at cloud stop and restored at start, configurable public range, incompatible state refused with `--discard-state`, rotated logs with `hlab logs`, LB IPv6 prefix 0xA0000000 (Q14)
- Next: /wcc:report walks through UI-08 (phase 10) and DOC-07 (phase 01) again; when both fit, /wcc:phase closes phase 10 and merges it

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- UI-08 (10): the console looks and behaves as UI-SPEC.md describes

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- Q16 (feedback, UI-08 check 2026-11-20): Should the tray program open views in a window of their own, so the logs can run alongside the rest?
  - Context: Wails v3.1.0 supports several windows (`app.Window.NewWithOptions`), each loading the same frontend with its own route; the bridge and its events are shared. Today hlab-tray has exactly one window (ARCHITECTURE "hlab-tray"), and the UI e2e tests attach to that one page over CDP. The home icon in the breadcrumb is being built either way.
  - Options: 1. Logs get "Open in new window"; Server Types and Settings stay in Administration 2. Administration (Server Types, Settings, Logs) opens as a window of its own from the gear 3. no second window
  - Recommendation: 1, because only the logs are watched alongside other work, while Settings and Server Types are short tasks that fit the shell.
  - Affects: UI-06, UI-08; ARCHITECTURE "hlab-tray (tray program)"; UI-SPEC "Views"

## If BLOCKED

- Reason: -
- Decision needed: -
