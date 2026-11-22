STATUS: IN_PROGRESS

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 17 MCP server (next)
- Base branch: main
- Last done: phase 13 merged into main (2026-11-22): hcloud, OpenTofu and Ansible pass the client matrix for every Cloud API type and build, reach and delete the example environment; actions sharing a resource now queue instead of answering `locked`, with effects shown at `success` and restored after errors and stops; `task e2e` takes 54 min, limit 120m
- Next: /wcc:phase in a new session builds phase 17 (MCP server `hlab mcp`), then phases 18 to 21 (Storage Boxes); /wcc:change takes the answer to Q19 and items from "Later" before phase 16, which runs all manual checks and the installer suite in two test VMs

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- INST-01 to INST-06 (12): the elevated installer suite passes in the development test VM
- INST-03 (12): the setup creates shortcuts that open only the tray program, and "Launch" starts it without admin rights
- INST-05 (12): uninstalling asks whether to keep the data; with the default answer no file of hlab remains
- UI-09 (14): the example environment can be created and deleted by hand in the console
- All of them, and those of later phases, are done in phase 16: the suite in the development test VM, the others in the user test VM (SPEC.md "Test VMs for the release checks").

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- Q19 (13 13-19): Should a load balancer action that changes its own state (change_algorithm, change_type, the public interface actions) queue behind a running target or service action instead of answering 423 `locked`?
  - Context: `hcloud load-balancer delete-service` (v1.70.2) does not wait for its action; in hlab the action runs for the length of an agent push, so a script running `delete-service` and then `change-algorithm` gets `locked`, while at Hetzner the action is done almost at once. The CLI-01 row now waits for the service to be gone.
  - Options: 1. keep `locked` and document it in deviations.md 2. let these LB state changes queue as well 3. finish service and target actions on the LB faster (push in the background)
  - Recommendation: 1, because the user set the rule "locked stays for state changes of the own resource" and only one hcloud command is affected.
  - Affects: CLI-01, CLI-02, DECISIONS "Which actions queue instead of locked", docs/deviations.md

## If BLOCKED

- Reason: -
- Decision needed: -
