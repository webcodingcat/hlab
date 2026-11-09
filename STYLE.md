# Style

Official standards first, checked by `bin/dev lint` (CI runs it);
`bin/dev format` applies what the formatters can fix.

| Files | Standard | Tool |
|---|---|---|
| Go | [Effective Go](https://go.dev/doc/effective_go), [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) | `gofmt`, `go vet`, `golangci-lint` (`.golangci.yml`) |
| Bash | [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html) | `shfmt -i 2 -ci -bn`, `shellcheck`, 80 characters per line |
| PowerShell | [PowerShell Practice and Style](https://poshcode.gitbook.io/powershell-practice-and-style) | `PSScriptAnalyzer` (default rules) |
| YAML | yamllint defaults | `yamllint --strict` (`.yamllint`) |
| Commits | [Git's conventions](https://git-scm.com/docs/SubmittingPatches#describe-changes): imperative subject (~50 characters), blank line, why | by review |

golangci-lint runs its standard linters (errcheck, govet, ineffassign, staticcheck,
unused) plus errorlint, misspell, revive, unconvert and unparam, and checks gofmt
and goimports; both `bin/dev lint` and `bin/dev.ps1 lint` run it for Linux and for
Windows.

## Rules on top of the standards

- English throughout.
- Readable for developers before short: descriptive names (`server`, `volume`, not
  `s`, `v`); single letters only for loop variables, receivers and short closures;
  understandable abbreviations (`cfg`, `ip`, `cmd`, IDs like `sid`) are fine.
- One job per function; no function that only passes its arguments on to another
  (the one exception, `Lab.Act`, `ActAll` and `Create`, says why in DEVELOPING.md,
  Actions).
- One way per thing: the same thing is done the same way everywhere; an exception
  says why next to it.
- Comments and doc comments say why (Hetzner's behaviour, caveats); the code says
  what. Doc comments and shell function headers are sentences; other comments may be
  short lowercase fragments.
- Scripts in `bin/`: functions may read the script's read-only constants (the
  repository path, tool flags) instead of taking them as parameters.
- Tests: one file per feature; test names say what they check
  (`TestPackagesImportOnlyWhatTheirLayerAllows`); a comment notes where a test checks
  Hetzner's behaviour.
