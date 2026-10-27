"""Routes of the lab's APIs against Hetzner's specification.

Every route exists in the specification and answers with the specified success status.
"""

import re

import pytest

from hlabd.api import spec, storagebox
from hlabd.api.app import create_app


def spec_codes(which: str) -> dict[tuple[str, str], set[str]]:
    """Return the success statuses of every operation of a specification.

    The key is (method, path) with every path parameter written as {}.
    """
    out = {}
    for method, _, template, operation in spec.load(f"{which}.json").ops:
        out[(method, re.sub(r"\{[^}]+\}", "{}", template))] = {
            code for code in operation.get("responses", {}) if code.startswith("2")
        }
    return out


def lab_routes(app) -> list[tuple[str, str, int]]:
    """Return (method, path, success status) of the app's /v1 routes.

    Paths are written like spec_codes() writes them, without the /v1 prefix.
    """
    out = []
    for r in app.routes:
        if not getattr(r, "path", "").startswith("/v1/"):
            continue
        path = re.sub(r"\{[^}]+\}", "{}", r.path[3:])
        for m in r.methods or ():
            if m in ("GET", "POST", "PUT", "DELETE"):
                out.append((m, path, r.status_code or 200))
    return out


@pytest.mark.parametrize("which", ["cloud", "hetzner"])
def test_routes_match_the_specification(lab, which):
    app = create_app(lab) if which == "cloud" else storagebox.create_app(lab)
    codes = spec_codes(which)
    # the lab's catch-all action routes
    generic = {"/{}/actions", "/{}/actions/{}", "/{}/{}/actions/{}", "/{}/{}/actions"}
    problems = []
    for method, path, status in lab_routes(app):
        if path in generic:
            continue
        want = codes.get((method, path))
        if want is None and path.endswith("/actions/{}") and method == "POST":
            # one lab route for all actions of a resource: every specified action must
            # answer like it; update_records of an RRSet is left out: Hetzner answers
            # it with 200 (api/zones.py answers so), the other RRSet actions with 201
            prefix = path[: -len("{}")]
            actions = {
                p: c
                for (m, p), c in codes.items()
                if m == "POST"
                and p.startswith(prefix)
                and "/" not in p[len(prefix) :]
                and not p.endswith("/update_records")
            }
            bad = [
                f"{p} {sorted(c)}" for p, c in actions.items() if str(status) not in c
            ]
            if not actions:
                problems.append(f"{method} {path}: no actions in the specification")
            problems += [
                f"{method} {b}: specified differently from the lab's {status}"
                for b in bad
            ]
            continue
        if want is None:
            problems.append(f"{method} {path}: not in the specification")
        elif str(status) not in want:
            problems.append(
                f"{method} {path}: answers {status}, "
                f"the specification says {sorted(want)}"
            )
    assert not problems, "\n".join(problems)
