STATUS: IN_PROGRESS

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: B1 Backlog: restart recovery, cloud-init and dev-loop bugs (then 05)
- Base branch: main
- Last done: phase 04 Images, backups, rescue, ISOs and volumes merged into main (2026-11-19): snapshots and backups (daily job), rescue on its own disk, ISO catalog and attach, volumes with format, automount and online resize
- Next: /wcc:phase in a new session (phase B1)

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

- Q7 (B1 B1-03): Should `Metrics(ctx, vm)` stay in the `platform.Platform` interface now that nothing calls it?
  - Context: since B1-03 the sampler uses only `MetricsAll`; `Metrics` remains in the interface (ARCHITECTURE platform section), in the hyperv implementation and in the fake, with no caller in production or tests.
  - Options: 1. Keep it as a convenience for a later platform 2. Remove it from `Platform`, hyperv and the fake, and from ARCHITECTURE
  - Recommendation: 2, because every new platform would otherwise implement and maintain a method nothing uses.
  - Affects: SRV-21, ARCHITECTURE.md platform section

## If BLOCKED

- Reason: -
- Decision needed: -
