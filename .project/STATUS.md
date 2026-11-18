STATUS: IN_PROGRESS

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 01 Walking skeleton
- Base branch: main
- Last done: spec approved by the user (2026-11-18)
- Next: phase 01 runs on branch wcc/phase-01

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:feedback walks through them. -->

- none

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- Q1 (01 verify): Should resource modules that own real resources (VMs, disks) get a `Reconciler` with platform, images and agent push passed in, instead of the `Machines` dependency implemented in `internal/cloud`?
  - Context: ARCHITECTURE.md "resource" describes an optional `Reconciler` and "one package plus one line" per resource type; phase 01 built `servers.Deps.Machines`, implemented in `internal/cloud/servers.go`, so every later module with real resources (volumes, load balancers, floating IPs) would add code to `internal/cloud`.
  - Options: 1. Refactor to a `Reconciler` on the module before phase 03 2. Keep the `Machines` pattern and change ARCHITECTURE.md accordingly
  - Recommendation: 1, because it keeps adding a resource type to one package plus one line before more such modules are built.
  - Affects: SRV-01, SRV-16, later SRV/VOL/LB/FIP requirements; ARCHITECTURE.md "resource"
- Q2 (01 verify): Do you approve the guest-visible identifiers phase 01 fixed without asking: server MAC `02:48:4c:1x:xx:xx` (0x1 plus the low 20 bits of the server ID), formal IPv6 `2001:db8:<ID>::/64`, root login via vendor-data `disable_root: false`, and the volume identifier byte order?
  - Context: DECISIONS.md "Server MACs, IPv6 and root login" and "Volume identifier byte order" (both Cost of change: high); they follow "Guest-visible identifiers" and E6, but guests and scripts see them.
  - Options: 1. Approve as recorded 2. Change the IPv6 scheme (e.g. a configurable prefix) 3. Change the MAC layout
  - Recommendation: 1, because the values follow the spec's identifiers and the documentation range.
  - Affects: SRV-01, SRV-03, SRV-04; DECISIONS.md entries above

## If BLOCKED

- Reason: -
- Decision needed: -
