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
- [ ] later: servers: `Reconcile` (internal/resources/servers/reconcile.go) takes an existing VM as it is and never checks an existing disk file, so a VM left without disk, adapter, seed or boot order by an aborted create stays broken; phase 09 (LIFE-02/03/04) restores servers through `Reconcile` and needs it to do only the difference (source: 03 03-06 review)
- [ ] later: sysint uninstall: `hlabd uninstall-cleanup` does not remove the hlab CA from LocalMachine\Root or the WSL2 distros, so after uninstalling the user's machine still trusts the hlab CA (decisions/managed-certificates.md says uninstall removes it); phase 12 (INST-04) needs it, until then README says to run `hlab ca trust off` first (source: 08 doc-writer)
- [ ] bug: ui/frontend (Projects.svelte, Tokens.svelte): the Created column shows the stored UTC time without a zone, so users see times off by their UTC offset from `hlab project list` and `hlab token list`, which print local time (source: 10 10-33 review)
- [ ] bug: servers (power actions): under a full parallel `task e2e` the status of a rebooted server stayed `stopping` for over 2 minutes after the guest was back on SSH with a new boot_id (SRV-07 at the B2 close; 3 of 3 green alone), so a user rebooting under load sees a running server as stopping and actions on it may be refused (source: B2 close)
- [ ] bug: ui/frontend (lib/LabelsField.svelte): text left in the labels box that breaks the label rule is not sent when a create form or "Save labels" is submitted, and the error disappears with the form, so the user does not learn the label was not saved (source: 14 verifier)
