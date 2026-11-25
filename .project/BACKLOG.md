# BACKLOG

A parking lot: work worth a plan that no phase asked for. Nothing here
is scheduled by itself; `/wcc:change` takes chosen items into a backlog
phase. Every item states what goes wrong for whom and has a severity:
`security`, `data`, `bug`, `later` (a named later phase needs it) or
`minor` (a cosmetic finding the user wants to keep). Cleanups, rare cases
and taste do not go here; a finding that needs a change to REQUIREMENTS.md,
SPEC.md or ARCHITECTURE.md goes to "Open decisions" in STATUS.md. Checked items move to
BACKLOG-DONE.md when a backlog phase closes.
- [ ] minor: storageboxes tests: `TestStorageBoxes/SBOX-18/api` failed once under load (`POST /hetzner/v1/storage_boxes/1/snapshots` answered 423 locked, want 201) and passed in five reruns; the test does not wait for the previous action to finish (source: phase 16)
- [ ] minor: e2e storage box rows: in a full `task e2e` run `SBOX-04/smb` and `SBOX-12/smb` waited 5 min on 423 locked for the shared box while SBOX-14 held it (5m31s); alone they pass; the shared box serialises them and the 5-minute wait is too short under full load (source: phase 16)
