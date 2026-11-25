STATUS: MANUAL_CHECK

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: milestone 1 complete; all phases done, tag `wcc-milestone-1` set locally
- Base branch: main
- Last done: phase 30 merged into main (2026-11-26): private repository `webcodingcat/hlab` created and `main` pushed, CI green on f19f01a (second run, after the WiX EULA step and an SBOX-18 test fix), release workflow in place; full `task e2e` green apart from the known CLI-02/load_balancers flake (three reruns green)
- Next: /wcc:report walks through the manual checks REL-01 to REL-03, then /wcc:phase sets DONE; then /wcc:release publishes v0.1.0 from main

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- REL-01 (30): the repository webcodingcat/hlab holds the history of main under Apache-2.0, with LICENSE and README links
- REL-02 (30): every push and pull request to main runs build, Go tests, lint, frontend tests and the installer build on a Windows runner
- REL-03 (30): a tag vX.Y.Z builds and publishes MSI, setup EXE and SHA256SUMS.txt as a GitHub release

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
