# BACKLOG

A parking lot: work worth a plan that no phase asked for. Nothing here
is scheduled by itself; `/wcc:change` takes chosen items into a backlog
phase. Every item states what goes wrong for whom and has a severity:
`security`, `data`, `bug`, `later` (a named later phase needs it) or
`minor` (a cosmetic finding the user wants to keep). Cleanups, rare cases
and taste do not go here; a finding that needs a change to REQUIREMENTS.md,
SPEC.md or ARCHITECTURE.md goes to "Open decisions" in STATUS.md. Checked items move to
BACKLOG-DONE.md when a backlog phase closes.

- [ ] minor: internal/resources/conventions_test.go: TestResources/API-05 and API-07 repeat over every endpoint the paging and label cases that internal/api/list_test.go and internal/labels/labels_test.go already cover in full; about 200 to 300 lines more to read and keep up, take in only when the tests get in the way, e.g. a rework of api.List (the per-endpoint sort rows of API-06 stay: each resource has its own sort table) (source: user after phase 02)
- [ ] later: docs/hcloud: `hcloud all list` (v1.70.2) always calls the Hetzner endpoint for storage boxes, so a user whose hcloud context has no `hetzner-endpoint` pointing to hlab sends the hlab project token to api.hetzner.com; phase 21 (CLI-05) must test `hcloud all list` and the README must name the setting (source: 13 e2e triage)
- [ ] later: internal/resources/storageboxes/subaccounts.go: `createSubaccount` does not refuse a box that is not `active`; once phase 19 sets `Apply`, a failed `create_storage_box` leaves the box `initializing` with `_hlab.n` 0 and a subaccount created then is named `u0-sub1` for good; phase 19 must refuse subaccounts (and other actions) on a box that is not active (source: phase 18 verifier)
