STATUS: IN_PROGRESS

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 09 Cloud lifecycle and persistence
- Base branch: main
- Last done: phase 08 merged into main (2026-11-19): load balancers with services, targets, network, health and metrics on HAProxy in the appliance; uploaded and managed certificates from the hlab CA with CRL; CA trust switch for Windows and WSL2
- Next: /wcc:phase in a new session (phase 09)

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:feedback walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- Q14 (08 close, doc-writer): Which formal IPv6 should load balancers get, now that the Q11 choice collides with standalone IPv6 primary IPs?
  - Context: Q11 gave LBs `2001:db8:<0x80000000 | ID>::1`; standalone IPv6 primary IPs (phase 05) already use `2001:db8:<0x80000000 | ID>::/64` (internal/resources/primaryips/primaryips.go:117), so an LB and a primary IP with the same ID show addresses in the same /64 and their PTR entries can overlap. Floating IPs use 0xC0000000, servers plain IDs.
  - Options: 1. LBs move to `2001:db8:<0xA0000000 | ID>::1` (IDs below 2^29) 2. LBs use `3fff:0:<ID>::1` 3. keep it and document the overlap
  - Recommendation: 1, because it keeps one documentation prefix and separates all four kinds
  - Affects: LB-01, LB-09, DECISIONS Q11, docs/user/deviations.md

## If BLOCKED

- Reason: -
- Decision needed: -
