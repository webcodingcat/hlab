"""Request checks from Hetzner's OpenAPI specification.

The specification is fetched into the image at build time. Request bodies are checked
against their schemas, query parameters against their enums. Without the specification
nothing is checked.
"""

import json
import re
from functools import lru_cache
from pathlib import Path

from jsonschema import Draft202012Validator
from jsonschema.exceptions import best_match
from jsonschema.validators import extend

SPEC_DIR = Path("/opt/hlab/spec")

# The specification types IDs-or-names as strings; clients (hcloud-go, the CLI) also
# send plain integers. And the API takes null for any field like a missing one (the
# hcloud provider sends them), while a required field may be null where the
# specification says so – so null passes every type check, and "required" still sees the
# key.
_base = Draft202012Validator.TYPE_CHECKER
_types = _base.redefine_many(
    {
        t: (lambda t: lambda c, v: v is None or _base.is_type(v, t))(t)
        for t in ("object", "array", "integer", "number", "boolean")
    }
    | {
        "string": lambda c, v: (
            v is None
            or isinstance(v, str)
            or (isinstance(v, int) and not isinstance(v, bool))
        )
    }
)


def _skip_null(fn):
    def check(validator, value, instance, schema):
        if instance is None:
            return
        yield from fn(validator, value, instance, schema) or ()

    return check


# keywords that combine schemas must still look at a null (a "oneOf" of objects); the
# others skip it
_COMBINE = {"allOf", "anyOf", "oneOf", "not", "if", "$ref", "$dynamicRef", "type"}
Validator = extend(
    Draft202012Validator,
    type_checker=_types,
    validators={
        k: _skip_null(v)
        for k, v in Draft202012Validator.VALIDATORS.items()
        if k not in _COMBINE
    },
)


class Spec:
    """The operations of an OpenAPI specification, matched by method and path."""

    def __init__(self, path: Path):
        """Load the specification at path; without the file there are no operations."""
        self.ops: list[tuple[str, re.Pattern, str, dict]] = []
        if not path.is_file():
            return
        spec = json.loads(path.read_text())
        base = re.sub(
            r"^https?://[^/]+", "", spec.get("servers", [{}])[0].get("url", "")
        ).rstrip("/")
        for template, methods in spec["paths"].items():
            pattern = re.compile(
                "^" + base + re.sub(r"\{[^}]+\}", "[^/]+", template) + "$"
            )
            for method, op in methods.items():
                if method in ("get", "post", "put", "delete", "patch"):
                    self.ops.append((method.upper(), pattern, template, op))
        self.ops.sort(
            key=lambda o: o[2].count("{")
        )  # literal segments win over parameters

    def op(self, method: str, path: str) -> dict | None:
        """Return the operation for a method and request path, or None."""
        for m, rx, _, op in self.ops:
            if m == method and rx.match(path):
                return op
        return None

    def body_errors(self, operation: dict, body) -> list[dict]:
        """Return the invalid_input details: [{"name": field, "messages": [...]}]."""
        schema = (
            (operation.get("requestBody") or {})
            .get("content", {})
            .get("application/json", {})
            .get("schema")
        )
        if not schema:
            return []
        fields: dict[str, list[str]] = {}
        for e in (r for top in Validator(schema).iter_errors(body) for r in _real(top)):
            path = [str(p) for p in e.absolute_path]
            if e.validator == "required":
                path.append(e.message.split("'")[1])
                message = "is required"
            elif e.validator == "enum":
                message = f"must be one of {', '.join(map(str, e.validator_value))}"
            elif e.validator == "type":
                message = f"must be of type {e.validator_value}"
            else:
                message = e.message
            fields.setdefault(".".join(path) or "body", []).append(message)
        for path in _null_required(schema, body):
            fields.setdefault(path, []).append("is required")
        return [{"name": k, "messages": v} for k, v in fields.items()]

    def query_enum(self, operation: dict, name: str) -> list[str] | None:
        """Return the allowed values of a query parameter, or None if it has no enum."""
        for p in operation.get("parameters") or []:
            if p.get("in") == "query" and p.get("name") == name:
                schema = p.get("schema") or {}
                return (schema.get("items") or schema).get("enum")
        return None


def _real(error) -> list:
    """Return the errors of a validation error that the API would reject.

    Unknown fields pass (the API ignores them), and of a oneOf/anyOf only the
    alternative that was meant counts – the one whose discriminator (an enum such as
    protocol) matched; an alternative with nothing left satisfies it.
    """
    if error.validator == "additionalProperties":
        return []
    if not error.context:
        return [error]
    alternatives: dict = {}
    for c in error.context:
        alternatives.setdefault(
            c.relative_schema_path[0] if c.relative_schema_path else 0, []
        ).extend(_real(c))
    if error.validator in ("oneOf", "anyOf"):
        if any(not errs for errs in alternatives.values()):
            return []
        fitting = [
            errs
            for errs in alternatives.values()
            if not any(x.validator in ("enum", "const") for x in errs)
        ]
        return [best_match(min(fitting or list(alternatives.values()), key=len))]
    return [x for errs in alternatives.values() for x in errs]


def _nullable(schema: dict) -> bool:
    schema_type = schema.get("type")
    return (
        schema.get("nullable") is True
        or schema_type == "null"
        or (isinstance(schema_type, list) and "null" in schema_type)
        or any(_nullable(s) for s in schema.get("anyOf", []) + schema.get("oneOf", []))
    )


def _null_required(schema: dict, value, path: str = "") -> list[str]:
    """Return the required fields given as null where the specification forbids null.

    Only plain objects and arrays are followed.
    """
    out = []
    if isinstance(value, dict) and isinstance(schema.get("properties"), dict):
        props = schema["properties"]
        for k in schema.get("required", []):
            if k in value and value[k] is None and not _nullable(props.get(k) or {}):
                out.append(f"{path}{k}")
        for k, v in value.items():
            if isinstance(props.get(k), dict) and v is not None:
                out += _null_required(props[k], v, f"{path}{k}.")
    elif isinstance(value, list) and isinstance(schema.get("items"), dict):
        for i, v in enumerate(value):
            out += _null_required(schema["items"], v, f"{path}{i}.")
    return out


@lru_cache(maxsize=None)
def load(name: str) -> Spec:
    """Return the specification with this file name, loaded once."""
    return Spec(SPEC_DIR / name)


# --- rules the specification only describes in prose ----------------------------------

_LABEL_NAME = re.compile(r"^[a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?$")
_LABEL_VALUE = re.compile(r"^([a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?)?$")
_DNS_LABEL = re.compile(r"^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")


def label_errors(labels) -> list[str]:
    """Return the violations of Hetzner's label rules.

    Key = optional DNS-subdomain prefix + '/' + name, value at most 63 characters.
    """
    if not isinstance(labels, dict):
        return []
    out = []
    for k, v in labels.items():
        prefix, _, name = k.rpartition("/")
        if prefix and (
            len(prefix) > 253 or not all(_DNS_LABEL.match(p) for p in prefix.split("."))
        ):
            out.append(f"label key {k!r}: invalid prefix")
        if not _LABEL_NAME.match(name):
            out.append(f"label key {k!r}: invalid name")
        if prefix == "hetzner.cloud" or prefix.endswith(".hetzner.cloud"):
            out.append(f"label key {k!r}: the hetzner.cloud/ prefix is reserved")
        if not isinstance(v, str) or not _LABEL_VALUE.match(v):
            out.append(f"label value of {k!r}: invalid value")
    return out


def hostname_ok(name: str) -> bool:
    """Check that a name is an RFC 1123 hostname (server names)."""
    return 0 < len(name) <= 253 and all(
        re.fullmatch(r"[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?", p)
        for p in name.split(".")
    )


def sort_items(items: list[dict], sorts: list[str]) -> list[dict]:
    """Sort items by the sort=field[:asc|:desc] parameters.

    Several are allowed; they apply in order (the first is the primary key).
    """
    seen: dict[str, str] = {}
    for s in sorts:
        field, _, direction = s.partition(":")
        direction = direction or "asc"
        if field in seen and seen[field] != direction:
            seen[field] = "ignore"  # contrary orders are ignored
        else:
            seen.setdefault(field, direction)

    def get(item: dict, field: str):
        for part in field.split("."):  # nested fields: stats.size
            item = item.get(part) if isinstance(item, dict) else None
        return item

    for field, direction in reversed(
        [(f, d) for f, d in seen.items() if d != "ignore"]
    ):
        items = sorted(
            items,
            key=lambda i: (
                get(i, field) is None,
                get(i, field) if get(i, field) is not None else 0,
            ),
            reverse=direction == "desc",
        )
    return items
