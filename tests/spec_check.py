"""Checks of API responses against Hetzner's OpenAPI specifications.

The specifications are fetched into the image at build time.
"""

import json
import re
from functools import lru_cache
from pathlib import Path

from jsonschema import Draft202012Validator

SPECS = {
    "cloud": Path("/opt/hlab/spec/cloud.json"),
    "hetzner": Path("/opt/hlab/spec/hetzner.json"),
}


@lru_cache(maxsize=None)
def _routes(which: str) -> list[tuple[str, re.Pattern, str, dict]]:
    path = SPECS[which]
    if not path.is_file():
        return []
    spec = json.loads(path.read_text())
    out = []
    for template, ops in spec["paths"].items():
        rx = re.compile("^" + re.sub(r"\{[^}]+\}", "[^/]+", template) + "$")
        for method, op in ops.items():
            if method in ("get", "post", "put", "delete", "patch"):
                out.append((method.upper(), rx, template, op.get("responses", {})))
    out.sort(key=lambda r: r[2].count("{"))  # literal segments win over parameters
    return out


def available() -> bool:
    return SPECS["cloud"].is_file()


def check(which: str, method: str, path: str, status: int, body) -> list[str]:
    """Return the errors of a response against the spec.

    The list is empty when the response conforms or the spec does not describe it.
    """
    for m, rx, template, responses in _routes(which):
        if m != method or not rx.match(path):
            continue
        resp = (
            responses.get(str(status))
            or responses.get(f"{str(status)[0]}XX")
            or responses.get(f"{str(status)[0]}xx")
            or responses.get("default")
        )
        if resp is None:
            if status < 400:
                return [
                    f"{method} {template}: status {status} not in the spec "
                    f"({', '.join(responses)})"
                ]
            return []
        schema = (resp.get("content") or {}).get("application/json", {}).get("schema")
        if schema is None:
            return (
                []
                if body in (None, "", {})
                else [f"{method} {template} {status}: spec has no body"]
            )
        validator = Draft202012Validator(schema)
        return [
            f"{method} {template} {status}: "
            f"{'/'.join(map(str, e.absolute_path))}: {e.message[:300]}"
            for e in sorted(
                validator.iter_errors(body),
                key=lambda e: list(map(str, e.absolute_path)),
            )
        ][:10]
    return []
