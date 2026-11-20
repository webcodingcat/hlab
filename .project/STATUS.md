STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 10 Tray program: shell, status, settings, server types, projects, logs (findings fixes 10-20..10-37 done on wcc/phase-10, unmerged; gear toggle checked, the scrollbar shift fix 10-37 waits for its check)
- Base branch: main
- Last done: phase 09 merged into main (2026-11-19): servers saved or shut down at cloud stop and restored at start, configurable public range, incompatible state refused with `--discard-state`, rotated logs with `hlab logs`, LB IPv6 prefix 0xA0000000 (Q14)
- Next: /wcc:report for UI-08 (the scrollbar shift), then /wcc:phase closes phase 10; DOC-07 (01) waits until the repository is on GitHub

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

- Q17 (phase 10, UI-08 check): Should own server types be editable in the Server Types view?
  - Context: the user asked for it during the UI-08 check on 2026-11-20; UI-04 promises only adding and removing own types (`ui/frontend/src/views/ServerTypes.svelte`), so changing one today means removing and adding it again.
  - Options: 1. add "Edit" to the row menu of own types, opening the add dialog filled in (name stays fixed), saved through the bridge and the own types file 2. keep remove and add again
  - Recommendation: 1, because the dialog and the types file exist and the change stays inside UI-04 and the bridge
  - Affects: UI-04, CAT-02, UI-SPEC "Views" (Server Types)

## If BLOCKED

- Reason: -
- Decision needed: -
