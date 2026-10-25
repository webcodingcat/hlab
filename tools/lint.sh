#!/usr/bin/env bash
# Style checks (STYLE.md) inside the development image, with the repository
# mounted at /src. Called by bin/hlab and bin/hlab.ps1:
#   lint.sh check   report every deviation, fail if there is one
#   lint.sh fix     apply what the formatters can fix
set -euo pipefail
cd /src
shell_files=(scripts/*.sh bin/hlab tests/tofu-smoke/run.sh tools/*.sh)

case "${1:-check}" in
  check)
    ruff format --check .
    ruff check .
    shfmt -i 2 -ci -bn -d "${shell_files[@]}"
    shellcheck -x "${shell_files[@]}"
    yamllint .
    ;;
  fix)
    ruff format .
    ruff check --fix .
    shfmt -i 2 -ci -bn -w "${shell_files[@]}"
    ;;
  *)
    echo "usage: lint.sh check|fix" >&2
    exit 2
    ;;
esac
