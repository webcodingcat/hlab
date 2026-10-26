"""Checks of API responses against Hetzner's OpenAPI specifications.

The specifications (the image's, or those of tools/fetch_specs.py) are loaded through
hlabd.api.spec, so the tests route requests exactly like the lab does.
"""

from jsonschema import Draft202012Validator

from hlabd.api import spec

SPECS = ("cloud", "hetzner")


def missing() -> list[str]:
    """Return the specifications that are not in spec.SPEC_DIR (none in the image)."""
    return [which for which in SPECS if not spec.load(f"{which}.json").ops]


def check(which: str, method: str, path: str, status: int, body) -> list[str]:
    """Return the errors of a response against the spec named which.

    The path is the request path including /v1. The list is empty when the response
    conforms or the spec does not describe it.
    """
    operation = spec.load(f"{which}.json").operation(method, path)
    if operation is None:
        return []
    responses = operation.get("responses", {})
    response = (
        responses.get(str(status))
        or responses.get(f"{str(status)[0]}XX")
        or responses.get(f"{str(status)[0]}xx")
        or responses.get("default")
    )
    if response is None:
        if status < 400:
            return [
                f"{method} {path}: status {status} not in the spec "
                f"({', '.join(responses)})"
            ]
        return []
    schema = (response.get("content") or {}).get("application/json", {}).get("schema")
    if schema is None:
        if body in (None, "", {}):
            return []
        return [f"{method} {path} {status}: spec has no body"]
    validator = Draft202012Validator(schema)
    errors = sorted(
        validator.iter_errors(body), key=lambda e: list(map(str, e.absolute_path))
    )
    return [
        f"{method} {path} {status}: "
        f"{'/'.join(map(str, error.absolute_path))}: {error.message[:300]}"
        for error in errors[:10]
    ]
