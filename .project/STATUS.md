STATUS: DONE

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: milestone 1 complete; all phases done, tag `wcc-milestone-1` set locally; phase folders 01-29 and B1-B4 removed, the tag keeps them (folder 30 stays for REL-03)
- Base branch: main
- Last done: phase 30 merged into main (2026-11-26): private repository `webcodingcat/hlab` created and `main` pushed, CI green on f19f01a (second run, after the WiX EULA step and an SBOX-18 test fix), release workflow in place; full `task e2e` green apart from the known CLI-02/load_balancers flake (three reruns green)
- Released (2026-11-26): `main` holds milestone 1 (3936af8, CI green) and is pushed; v0.1.0 published at https://github.com/webcodingcat/hlab/releases/tag/v0.1.0 with hlab-0.1.0.msi, hlab-setup-0.1.0.exe and SHA256SUMS.txt
- Next: REL-03 fits (2026-11-26), no manual checks open; /wcc:change plans milestone 2

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

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
