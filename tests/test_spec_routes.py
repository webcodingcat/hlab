"""Every route of the lab's APIs exists in Hetzner's specification and answers with the specified success status."""

import re

import pytest

import spec_check
from hlabd.api import storagebox
from hlabd.api.app import create_app


def spec_codes(which: str) -> dict[tuple[str, str], set[str]]:
    out = {}
    for method, rx, template, responses in spec_check._routes(which):
        out[(method, re.sub(r"\{[^}]+\}", "{}", template))] = {c for c in responses if c.startswith("2")}
    return out


def lab_routes(app) -> list[tuple[str, str, int]]:
    out = []
    for r in app.routes:
        if not getattr(r, "path", "").startswith("/v1/"):
            continue
        path = re.sub(r"\{[^}]+\}", "{}", r.path[3:])
        for m in r.methods or ():
            if m in ("GET", "POST", "PUT", "DELETE"):
                out.append((m, path, r.status_code or 200))
    return out


@pytest.mark.skipif(not spec_check.available(), reason="specification not in the image")
@pytest.mark.parametrize("which", ["cloud", "hetzner"])
def test_routes_match_the_specification(lab, which):
    app = create_app(lab) if which == "cloud" else storagebox.create_app(lab)
    codes = spec_codes(which)
    generic = {"/{}/actions", "/{}/actions/{}", "/{}/{}/actions/{}", "/{}/{}/actions"}   # the lab's catch-all action routes
    problems = []
    for method, path, status in lab_routes(app):
        if path in generic:
            continue
        want = codes.get((method, path))
        if want is None and path.endswith("/actions/{}") and method == "POST":
            # one lab route for all actions of a resource: every specified action must answer like it, apart from
            # the one action the lab answers itself with 200 (update_records of an RRSet)
            prefix = path[:-len("{}")]
            actions = {p: c for (m, p), c in codes.items() if m == "POST" and p.startswith(prefix)
                       and "/" not in p[len(prefix):] and not p.endswith("/update_records")}
            bad = [f"{p} {sorted(c)}" for p, c in actions.items() if str(status) not in c]
            if not actions:
                problems.append(f"{method} {path}: no actions in the specification")
            problems += [f"{method} {b}: specified differently from the lab's {status}" for b in bad]
            continue
        if want is None:
            problems.append(f"{method} {path}: not in the specification")
        elif str(status) not in want:
            problems.append(f"{method} {path}: answers {status}, the specification says {sorted(want)}")
    assert not problems, "\n".join(problems)
