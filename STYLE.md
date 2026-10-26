# Style

Official standards first, checked by `bin/hlab lint` (also part of `bin/hlab test`);
`bin/hlab format` applies what the formatters can fix.

| Files | Standard | Tool |
|---|---|---|
| Python | [PEP 8](https://peps.python.org/pep-0008/), [PEP 257](https://peps.python.org/pep-0257/) | `ruff format`, `ruff check` (`pyproject.toml`) |
| Python types | [typing](https://typing.python.org/) | `mypy hlabd` (`pyproject.toml`) |
| Bash | [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html) | `shfmt -i 2 -ci -bn`, `shellcheck` |
| Terraform | [OpenTofu/Terraform style conventions](https://developer.hashicorp.com/terraform/language/style) | `tofu fmt` |
| YAML | yamllint defaults | `yamllint` (`.yamllint`) |
| Commits | [Git's conventions](https://git-scm.com/docs/SubmittingPatches#describe-changes): imperative subject (~50 characters), blank line, why | by review |

## Rules on top of the standards

- English throughout.
- Readable for developers before short: descriptive names; no single letters
  except loop variables and one-line lambdas; understandable abbreviations
  (`cfg`, `ip`, `cmd`, IDs like `sid`) are fine.
- One job per function; no `name()` that only calls `_name()`.
- Comments and docstrings say why (Hetzner's behaviour, caveats); the code says
  what. Docstrings and shell function headers are sentences; other comments
  (Python and shell) may be short lowercase fragments (a deliberate deviation
  from PEP 8), one style per comment.
- Wrappers (`bin/`): functions may read the script's read-only constants
  (image names, the repository path) instead of taking them as parameters.
- Layout of `hlabd/` (the layers are in DEVELOPING.md, The pieces):
  API modules are module-level functions taking `lab` first, with thin routes;
  every resource's actions are a table of `ActionSpec` rows run by `lab.act()`;
  errors come from `errors.py`; lookups go through `lab.get_or_404()` or a
  `<thing>_or_404()` helper; JSON shapes are `<thing>_json(lab, thing)`.
- Types where they help reading: `lab: Lab`, `state: State`, and the records of
  `records.py` (`server: Server`, `nic: Nic`, ...), so mypy catches a mistyped field;
  code that treats every kind alike takes `Record` (or a type variable bound to it),
  never `Any`; where a field is picked by a variable key, one commented `cast`
  says why. Annotating everything is not the goal.
- Tests: one file per feature; test names are sentences; a comment notes where
  a test checks Hetzner's behaviour.
