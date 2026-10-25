# Style

Official standards first, checked by `bin/hlab lint` (also part of `bin/hlab test`);
`bin/hlab format` applies what the formatters can fix.

| Files | Standard | Tool |
|---|---|---|
| Python | [PEP 8](https://peps.python.org/pep-0008/), [PEP 257](https://peps.python.org/pep-0257/) | `ruff format`, `ruff check` (`pyproject.toml`) |
| Bash | [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html) | `shfmt -i 2 -ci -bn`, `shellcheck` |
| PowerShell | [PowerShell Practice and Style](https://poshcode.gitbook.io/powershell-practice-and-style/), approved verbs | by review |
| Terraform | [OpenTofu/Terraform style conventions](https://developer.hashicorp.com/terraform/language/style) | `tofu fmt` |
| YAML | yamllint defaults | `yamllint` (`.yamllint`) |
| Commits | [Git's conventions](https://git-scm.com/docs/SubmittingPatches#describe-changes): imperative subject (~50 characters), blank line, why | by review |

## On top of the standards

- English throughout.
- Readable for developers before short: descriptive names; no single letters
  except loop variables and one-line lambdas; understandable abbreviations
  (`cfg`, `ip`, `cmd`, IDs like `sid`) are fine.
- One job per function; no `name()` that only calls `_name()`.
- Comments and docstrings say why (Hetzner's behaviour, caveats); the code says
  what.
- Layout of `hlabd/`: `api/` decides (HTTP, checks, state, actions) and never
  runs scripts; `fabric/` carries out and knows no HTTP; `lab.py` is the core.
  API modules are module-level functions taking `lab` first, with thin routes;
  actions are tables of small functions; errors come from `errors.py`; lookups
  are `<thing>_or_404`, JSON shapes `<thing>_json`.
- Tests: one file per feature; test names are sentences; a comment notes where
  a test checks Hetzner's behaviour.
