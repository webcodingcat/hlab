"""Request checks from Hetzner's OpenAPI specification (fetched into the image at build time): request bodies
against their schemas, query parameters against their enums. Without the specification nothing is checked."""

import json
import re
from functools import lru_cache
from pathlib import Path

from jsonschema import Draft202012Validator
from jsonschema.exceptions import best_match
from jsonschema.validators import extend

SPEC_DIR = Path("/opt/hlab/spec")

# The specification types IDs-or-names as strings; clients (hcloud-go, the CLI) also send plain integers. And the
# API takes null for any field like a missing one (the hcloud provider sends them), while a required field may be
# null where the specification says so – so null passes every type check, and "required" still sees the key.
_base = Draft202012Validator.TYPE_CHECKER
_types = _base.redefine_many({
    t: (lambda t: lambda c, v: v is None or _base.is_type(v, t))(t)
    for t in ("object", "array", "integer", "number", "boolean")
} | {"string": lambda c, v: v is None or isinstance(v, str) or (isinstance(v, int) and not isinstance(v, bool))})


def _skip_null(fn):
    def check(validator, value, instance, schema):
        if instance is None:
            return
        yield from fn(validator, value, instance, schema) or ()
    return check


# keywords that combine schemas must still look at a null (a "oneOf" of objects); the others skip it
_COMBINE = {"allOf", "anyOf", "oneOf", "not", "if", "$ref", "$dynamicRef", "type"}
Validator = extend(Draft202012Validator, type_checker=_types,
                   validators={k: _skip_null(v) for k, v in Draft202012Validator.VALIDATORS.items()
                               if k not in _COMBINE})


class Spec:
    def __init__(self, path: Path):
        self.ops: list[tuple[str, re.Pattern, str, dict]] = []
        if not path.is_file():
            return
        spec = json.loads(path.read_text())
        base = re.sub(r"^https?://[^/]+", "", spec.get("servers", [{}])[0].get("url", "")).rstrip("/")
        for template, methods in spec["paths"].items():
            rx = re.compile("^" + base + re.sub(r"\{[^}]+\}", "[^/]+", template) + "$")
            for method, op in methods.items():
                if method in ("get", "post", "put", "delete", "patch"):
                    self.ops.append((method.upper(), rx, template, op))
        self.ops.sort(key=lambda o: o[2].count("{"))  # literal segments win over parameters

    def op(self, method: str, path: str) -> dict | None:
        for m, rx, _, op in self.ops:
            if m == method and rx.match(path):
                return op
        return None

    def body_errors(self, op: dict, body) -> list[dict]:
        """invalid_input details: [{"name": field, "messages": [...]}]."""
        schema = (op.get("requestBody") or {}).get("content", {}).get("application/json", {}).get("schema")
        if not schema:
            return []
        fields: dict[str, list[str]] = {}
        for e in (r for top in Validator(schema).iter_errors(body) for r in _real(top)):
            path = [str(p) for p in e.absolute_path]
            if e.validator == "required":
                path.append(e.message.split("'")[1])
                msg = "is required"
            elif e.validator == "enum":
                msg = f"must be one of {', '.join(map(str, e.validator_value))}"
            elif e.validator == "type":
                msg = f"must be of type {e.validator_value}"
            else:
                msg = e.message
            fields.setdefault(".".join(path) or "body", []).append(msg)
        for path in _null_required(schema, body):
            fields.setdefault(path, []).append("is required")
        return [{"name": k, "messages": v} for k, v in fields.items()]

    def query_enum(self, op: dict, name: str) -> list[str] | None:
        for p in op.get("parameters") or []:
            if p.get("in") == "query" and p.get("name") == name:
                s = p.get("schema") or {}
                return (s.get("items") or s).get("enum")
        return None


def _real(e) -> list:
    """The errors of a validation error that the API would reject: unknown fields pass (the API ignores them),
    and of a oneOf/anyOf only the alternative that was meant counts – the one whose
    discriminator (an enum such as protocol) matched; an alternative with nothing left satisfies it."""
    if e.validator == "additionalProperties":
        return []
    if not e.context:
        return [e]
    alts: dict = {}
    for c in e.context:
        alts.setdefault(c.relative_schema_path[0] if c.relative_schema_path else 0, []).extend(_real(c))
    if e.validator in ("oneOf", "anyOf"):
        if any(not errs for errs in alts.values()):
            return []
        fitting = [errs for errs in alts.values() if not any(x.validator in ("enum", "const") for x in errs)]
        return [best_match(min(fitting or list(alts.values()), key=len))]
    return [x for errs in alts.values() for x in errs]


def _nullable(schema: dict) -> bool:
    t = schema.get("type")
    return schema.get("nullable") is True or t == "null" or (isinstance(t, list) and "null" in t) \
        or any(_nullable(s) for s in schema.get("anyOf", []) + schema.get("oneOf", []))


def _null_required(schema: dict, value, path: str = "") -> list[str]:
    """Required fields given as null where the specification does not allow null (plain objects and arrays)."""
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
    return Spec(SPEC_DIR / name)


# --- rules the specification only describes in prose ---------------------------------------------------------------

_LABEL_NAME = re.compile(r"^[a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?$")
_LABEL_VALUE = re.compile(r"^([a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?)?$")
_DNS_LABEL = re.compile(r"^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")


def label_errors(labels) -> list[str]:
    """Hetzner's label rules: key = optional DNS-subdomain prefix + '/' + name, value at most 63 characters."""
    if not isinstance(labels, dict):
        return []
    out = []
    for k, v in labels.items():
        prefix, _, name = k.rpartition("/")
        if prefix and (len(prefix) > 253 or not all(_DNS_LABEL.match(p) for p in prefix.split("."))):
            out.append(f"label key {k!r}: invalid prefix")
        if not _LABEL_NAME.match(name):
            out.append(f"label key {k!r}: invalid name")
        if prefix == "hetzner.cloud" or prefix.endswith(".hetzner.cloud"):
            out.append(f"label key {k!r}: the hetzner.cloud/ prefix is reserved")
        if not isinstance(v, str) or not _LABEL_VALUE.match(v):
            out.append(f"label value of {k!r}: invalid value")
    return out


def hostname_ok(name: str) -> bool:
    """RFC 1123 hostname (server names)."""
    return 0 < len(name) <= 253 and all(re.fullmatch(r"[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?", p)
                                        for p in name.split("."))


def sort_items(items: list[dict], sorts: list[str]) -> list[dict]:
    """sort=field[:asc|:desc], several allowed; applied in order (the first is the primary key)."""
    seen: dict[str, str] = {}
    for s in sorts:
        field, _, direction = s.partition(":")
        direction = direction or "asc"
        if field in seen and seen[field] != direction:
            seen[field] = "ignore"  # contrary orders are ignored
        else:
            seen.setdefault(field, direction)
    def get(item: dict, field: str):
        for part in field.split("."):   # nested fields: stats.size
            item = item.get(part) if isinstance(item, dict) else None
        return item

    for field, direction in reversed([(f, d) for f, d in seen.items() if d != "ignore"]):
        items = sorted(items, key=lambda i: (get(i, field) is None, get(i, field) if get(i, field) is not None else 0),
                       reverse=direction == "desc")
    return items
