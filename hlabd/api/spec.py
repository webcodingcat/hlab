"""Request checks from Hetzner's OpenAPI specification.

The specification is fetched into the image at build time. Request bodies are checked
against their schemas, query parameters against their enums. Without the specification
nothing is checked.
"""

import json
import os
import re
from functools import lru_cache
from pathlib import Path

from jsonschema import Draft202012Validator
from jsonschema.exceptions import best_match
from jsonschema.validators import extend

# the image's copy; outside Docker, the unit tests use the copy that
# tools/fetch_specs.py downloads (through HLAB_SPEC_DIR or tests/conftest.py)
SPEC_DIR = Path(os.environ.get("HLAB_SPEC_DIR", "/opt/hlab/spec"))

# keywords that still check a null value; all others skip it. Keywords that combine
# schemas (a "oneOf" of objects) or refer to a schema ("$ref", "$dynamicRef") pass
# the null on to the schemas they name. "type" is in this list because its checker
# (below) accepts null itself.
_SEES_NULL = {"allOf", "anyOf", "oneOf", "not", "if", "$ref", "$dynamicRef", "type"}

# rules the specification only describes in prose (labels, host names)
_LABEL_NAME = re.compile(r"^[a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?$")
_LABEL_VALUE = re.compile(r"^([a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?)?$")
_DNS_LABEL = re.compile(r"^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")

# The specification types fields that take an ID or a name as strings, but clients
# (hcloud-go, the CLI) also send plain integers. The API also treats null in any field
# like a missing field (the hcloud provider sends nulls), and a required field may be
# null where the specification allows it. So null passes every type check, and
# "required" still counts the key as present.
BASE_TYPE_CHECKER = Draft202012Validator.TYPE_CHECKER


def null_or(type_name: str):
    """Return a type check that passes null and what the base check passes."""

    def check(checker, value) -> bool:
        return value is None or BASE_TYPE_CHECKER.is_type(value, type_name)

    return check


def null_string_or_int(checker, value) -> bool:
    """Check a string field: null, a string, or an integer (an ID given as number)."""
    return (
        value is None
        or isinstance(value, str)
        or (isinstance(value, int) and not isinstance(value, bool))
    )


TYPE_CHECKER = BASE_TYPE_CHECKER.redefine_many(
    {
        type_name: null_or(type_name)
        for type_name in ("object", "array", "integer", "number", "boolean")
    }
    | {"string": null_string_or_int}
)


def skip_null(validate):
    """Return a keyword check that lets null pass and checks everything else."""

    def check(validator, value, instance, schema):
        if instance is None:
            return
        yield from validate(validator, value, instance, schema) or ()

    return check


Validator = extend(
    Draft202012Validator,
    type_checker=TYPE_CHECKER,
    validators={
        k: skip_null(v)
        for k, v in Draft202012Validator.VALIDATORS.items()
        if k not in _SEES_NULL
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
        # literal segments win over parameters
        self.ops.sort(key=lambda o: o[2].count("{"))

    def operation(self, method: str, path: str) -> dict | None:
        """Return the operation for a method and request path, or None."""
        for op_method, pattern, _, operation in self.ops:
            if op_method == method and pattern.match(path):
                return operation
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
        errors = (
            rejected
            for top in Validator(schema).iter_errors(body)
            for rejected in rejected_errors(top)
        )
        for e in errors:
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
        for missing in null_required(schema, body):
            fields.setdefault(missing, []).append("is required")
        return [{"name": k, "messages": v} for k, v in fields.items()]

    def query_enum(self, operation: dict, name: str) -> list[str] | None:
        """Return the allowed values of a query parameter, or None if it has no enum."""
        for p in operation.get("parameters") or []:
            if p.get("in") == "query" and p.get("name") == name:
                schema = p.get("schema") or {}
                return (schema.get("items") or schema).get("enum")
        return None


def rejected_errors(error) -> list:
    """Return the errors of a validation error that the API would reject.

    Unknown fields pass, because the API ignores them. For a oneOf or anyOf, only the
    intended alternative counts: the one whose discriminator (an enum such as
    protocol) matched. If an alternative has no errors left, the oneOf or anyOf passes.
    """
    if error.validator == "additionalProperties":
        return []
    if not error.context:
        return [error]
    alternatives: dict = {}
    for c in error.context:
        alternatives.setdefault(
            c.relative_schema_path[0] if c.relative_schema_path else 0, []
        ).extend(rejected_errors(c))
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


def is_nullable(schema: dict) -> bool:
    """Return whether a schema allows null (nullable, a null type, or a null option)."""
    schema_type = schema.get("type")
    return (
        schema.get("nullable") is True
        or schema_type == "null"
        or (isinstance(schema_type, list) and "null" in schema_type)
        or any(
            is_nullable(s) for s in schema.get("anyOf", []) + schema.get("oneOf", [])
        )
    )


def null_required(schema: dict, value, path: str = "") -> list[str]:
    """Return the required fields given as null where the specification forbids null.

    Only plain objects and arrays are followed.
    """
    out = []
    if isinstance(value, dict) and isinstance(schema.get("properties"), dict):
        props = schema["properties"]
        for k in schema.get("required", []):
            if k in value and value[k] is None and not is_nullable(props.get(k) or {}):
                out.append(f"{path}{k}")
        for k, v in value.items():
            if isinstance(props.get(k), dict) and v is not None:
                out += null_required(props[k], v, f"{path}{k}.")
    elif isinstance(value, list) and isinstance(schema.get("items"), dict):
        for i, v in enumerate(value):
            out += null_required(schema["items"], v, f"{path}{i}.")
    return out


@lru_cache(maxsize=None)
def load(name: str) -> Spec:
    """Return the specification with this file name, loaded once."""
    return Spec(SPEC_DIR / name)


# --- rules the specification only describes in prose ----------------------------------


def label_errors(labels: object) -> list[str]:
    """Return the violations of Hetzner's label rules.

    A key is an optional DNS subdomain prefix with '/', then a name. A value has at
    most 63 characters.
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
