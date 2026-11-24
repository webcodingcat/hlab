# Phase 16: test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|

No rows. Phase 16 has no `unit` or `e2e` requirement: DOC-08 and DOC-07 are
`manual` and are checked by the user (CHECKS.md). DOC-01 to DOC-06 already
have their tests in `test/docs/docs_test.go` (`TestDocs/DOC-01` to
`TestDocs/DOC-06`). DOC-04 (links) and DOC-06 (Taskfile commands in the
developer guide) cover the new `docs/development/release-checks.md`
without a change: they walk every Markdown file under `docs/` and
`docs/development/`. INST-01, INST-04, INST-05 and INST-06 run unchanged in
the existing elevated suite (`test/install`, `task e2e:elevated`) in the
development test VM.
