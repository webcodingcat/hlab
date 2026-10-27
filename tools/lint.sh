#!/usr/bin/env bash
# Style and type checks (STYLE.md) inside the development image, with the
# repository mounted at /src. Called by bin/hlab:
#   lint.sh check   report every deviation, fail if there is one
#   lint.sh fix     apply what the formatters can fix
set -euo pipefail
cd /src
shell_files=(scripts/*.sh bin/hlab tests/tofu-smoke/run.sh tools/*.sh)

case "${1:-check}" in
  check)
    ruff format --check .
    ruff check .
    mypy hlabd
    shfmt -i 2 -ci -bn -d "${shell_files[@]}"
    shellcheck -x "${shell_files[@]}"
    # Google Shell Style Guide: 80 characters at most (shfmt does not check it)
    python3 - "${shell_files[@]}" <<'EOF'
import sys

long_lines = [
    f"{path}:{number}: {len(line.rstrip())} characters"
    for path in sys.argv[1:]
    for number, line in enumerate(open(path, encoding="utf-8"), 1)
    if len(line.rstrip()) > 80
]
print("\n".join(long_lines), file=sys.stderr)
sys.exit(1 if long_lines else 0)
EOF
    yamllint .
    ;;
  fix)
    # fixes only; what is left is reported by `check`
    ruff check --fix --exit-zero .
    ruff format .
    shfmt -i 2 -ci -bn -w "${shell_files[@]}"
    ;;
  *)
    echo "usage: lint.sh check|fix" >&2
    exit 2
    ;;
esac
