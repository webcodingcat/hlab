# BACKLOG

A parking lot: work worth a plan that no phase asked for. Nothing here
is scheduled by itself; `/wcc:change` takes chosen items into a backlog
phase. Every item states what goes wrong for whom and has a severity:
`security`, `data`, `bug`, `later` (a named later phase needs it) or
`minor` (a cosmetic finding the user wants to keep). Cleanups, rare cases
and taste do not go here; a finding that needs a change to REQUIREMENTS.md,
SPEC.md or ARCHITECTURE.md goes to "Open decisions" in STATUS.md. Checked items move to
BACKLOG-DONE.md when a backlog phase closes.

- [ ] <severity>: <component>: <what goes wrong for whom> (source: <phase> <plan> review)
