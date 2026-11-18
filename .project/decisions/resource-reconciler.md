# Decision: Real resources are reconciled by their resource module

- Date: 2026-11-18
- Status: decided by the user (open decision Q1 from the phase 01 verify)
- Cost of change: medium now (about 170 lines move from `internal/cloud`
  into `internal/resources/servers`, no schema, API or file format change);
  grows with every later phase that adds platform work outside the module
- Affects: SRV-01, SRV-16, the later SRV, VOL, LB and FIP requirements;
  ARCHITECTURE.md "resource"

## Decision

- ARCHITECTURE.md "resource" stands as written: a module that owns real
  resources (VMs, disks, and later volumes, floating IPs, load balancers)
  implements the optional `resource.Reconciler`
  (`Reconcile(ctx, id)`, `Delete(ctx, id)`), detected by type assertion.
- The module does the platform work itself. It receives what it needs
  through its `Deps`: `platform.Platform`, the image store, a function that
  pushes the agent document, and the settings it reads (data folder, stop
  behaviour). `internal/cloud` only wires the modules and calls the
  interface; it holds no per-resource-type logic.
- `Reconcile` is idempotent: it compares the desired state in the store
  with what exists on the platform and does only the difference, so it can
  run again after an aborted action and, in phase 09, after a restart of
  the cloud.
- Operations that are not "make reality match state" (power actions,
  rebuild, change type in phase 03) stay methods or actions inside the
  module; the `Reconciler` covers creating, matching and deleting.
- The refactor of the servers module is the first plan of phase 03:
  `servers.Deps.Machines` and `internal/cloud/servers.go` are replaced by
  `servers` implementing `resource.Reconciler`; `resource.Reconciler` is
  added to `internal/resource`. SRV-01 and SRV-16 run again end-to-end
  after it. Phase 02 does not touch it.

## Reasons

- It keeps the promise that a new resource type is one package plus one
  line in `Cloud.modules()`, before phases 03, 04, 05 and 08 add more
  modules owning real resources.
- The cost is lowest now: phase 02 adds no platform work, and phase 03
  would otherwise grow `internal/cloud/servers.go` with power, rebuild and
  change-type code that then has to move as well.
- Phase 03 changes exactly this code, so the refactor lands where it is
  needed and is checked by the same end-to-end tests.
- `platform.Platform` is hypervisor-neutral, so module code can be tested
  with a fake platform without the Windows-only `internal/cloud` package.
- A single, idempotent `Reconcile` gives phase 09 (LIFE-02, LIFE-03,
  LIFE-04) one way to restore every resource after stop and start.

## Rejected alternatives

- Keep the `Machines` pattern (one dependency interface per module,
  implemented in `internal/cloud`) and change ARCHITECTURE.md: no refactor
  now, and all platform code in one place, but `internal/cloud` would
  collect the logic of every resource type, each module would bring its
  own interface, phase 09 would have no common way to reconcile everything,
  and moving the code later would cost more with every phase.
- Refactor at the end of phase 02: possible, but phase 02 has no other
  reason to touch the servers module; doing it first in phase 03 keeps it
  next to the changes that use it.
- Move the logic into the module now but introduce the generic
  `Reconciler` only in phase 09: splits one change into two and leaves the
  code different from ARCHITECTURE.md until then.
