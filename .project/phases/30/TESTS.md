# Phase 30: test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|

No new rows. REL-01, REL-02 and REL-03 are `manual`: GitHub shows them
(the repository, a green CI run, the release workflow), and the close
checks them through the first CI run (PLAN.md "Close"). DOC-07 is manual.
DOC-01 to DOC-06 already have their tests in
`test/docs/docs_test.go: TestDocs/DOC-01` to `TestDocs/DOC-06`. 30-01 and
30-05 run them unchanged. 30-01 also runs the whole existing `task test`
against the renewed OpenAPI pins.

Writers: none.
