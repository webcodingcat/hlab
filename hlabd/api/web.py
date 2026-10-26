"""HTTP plumbing shared by all API modules.

Error answers, lists (filter, sort, pages), request checks, rate limit, and what every
resource does the same way (IDs in request bodies, PUT, delete protection).
"""

import asyncio
import json
import logging
import time
from collections.abc import Callable, Mapping
from typing import Any, cast

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException

from ..errors import (
    ApiError,
    check_unique_name,
    invalid,
    invalid_fields,
    not_found,
    rate_limit_exceeded,
    server_error,
)
from ..lab import Lab, Plan
from ..labels import match_labels
from ..records import ProtectedRecord, Record
from . import hetzner
from . import spec as openapi

log = logging.getLogger(__name__)


def error_response(error: ApiError) -> JSONResponse:
    """Return the API's JSON answer for an error."""
    return JSONResponse(
        hetzner.error(error.code, error.message, error.details),
        status_code=error.status,
    )


async def read_body(request: Request) -> dict:
    """Return the JSON body, already checked by the middleware; {} if there is none."""
    raw = await request.body()
    try:
        data = json.loads(raw) if raw else {}
    except ValueError:
        return {}
    return data if isinstance(data, dict) else {}


def ref_id(value, field: str) -> int:
    """Return a resource ID from a request body, or raise invalid_input for the field.

    The request check lets only numbers (or null) through where the specification
    wants an ID.
    """
    if isinstance(value, bool):
        raise invalid(f"{field} must be an ID", field)
    try:
        return int(value)
    except (TypeError, ValueError):
        raise invalid(f"{field} must be an ID", field) from None


def update_fields(lab: Lab, item: Record, body: dict, keys: tuple[str, ...]) -> None:
    """Change the given fields of an item on PUT (null labels mean no labels)."""
    # the keys come from the caller's list, more than mypy can match to the record type
    fields = cast(dict[str, object], item)
    for key in keys:
        if key in body:
            fields[key] = (body[key] or {}) if key == "labels" else body[key]


def update_resource(
    lab: Lab, kind: str, item: Record, body: dict, keys: tuple[str, ...], what: str
) -> None:
    """Apply a PUT to a resource of a kind: names stay unique within the kind."""
    with lab.state.change():
        check_unique_name(lab.state[kind].values(), body.get("name"), item, what)
        update_fields(lab, item, body, keys)


def change_delete_protection(lab: Lab, item: ProtectedRecord, body: dict) -> Plan:
    """Set a resource's delete protection: the change_protection action.

    A body without `delete` keeps the current value. Used by the tables of networks,
    primary IPs, Floating IPs, volumes and Storage Boxes (servers set delete and
    rebuild together).
    """
    item["protection"]["delete"] = bool(
        body.get("delete", item["protection"]["delete"])
    )
    return Plan()


# --- lists ----------------------------------------------------------------------------


def positive_int_or_default(value: str | None, default: int) -> int:
    """Return a query value as a number of at least 1, else the default (Hetzner)."""
    try:
        return int(value) if value is not None and int(value) >= 1 else default
    except ValueError:
        return default


def sort_items[T: Mapping[str, Any]](items: list[T], sorts: list[str]) -> list[T]:
    """Sort items by the sort=field[:asc|:desc] parameters.

    Several are allowed; they apply in order (the first is the primary key).
    """
    seen: dict[str, str] = {}
    for s in sorts:
        field, _, direction = s.partition(":")
        direction = direction or "asc"
        if field in seen and seen[field] != direction:
            seen[field] = "ignore"  # a field sorted both ways is ignored
        else:
            seen.setdefault(field, direction)

    for field, direction in reversed(
        [(f, d) for f, d in seen.items() if d != "ignore"]
    ):
        items = sorted(items, key=_sort_key(field), reverse=direction == "desc")
    return items


def _sort_key(field: str):
    """Return the sort key of a field (nested ones like stats.size); null sorts last."""

    def key(item: Mapping[str, Any]) -> tuple:
        value: Any = item
        for part in field.split("."):
            value = value.get(part) if isinstance(value, dict) else None
        return (value is None, value if value is not None else 0)

    return key


def paginate(request: Request, items: list, key: str) -> dict:
    """Return a sorted page of items under key, with the pagination meta."""
    query = request.query_params
    items = sort_items(items, query.getlist("sort"))
    page = positive_int_or_default(query.get("page"), 1)
    per_page = min(50, positive_int_or_default(query.get("per_page"), 25))
    total = len(items)
    last = max(1, -(-total // per_page))
    chunk = items[(page - 1) * per_page : page * per_page]
    return {
        key: chunk,
        "meta": {
            "pagination": {
                "page": page,
                "per_page": per_page,
                "previous_page": page - 1 or None,
                "next_page": page + 1 if page < last else None,
                "last_page": last,
                "total_entries": total,
            }
        },
    }


def filtered[T: Mapping[str, Any]](
    request: Request,
    items: list[T],
    extra: dict[str, Callable[[T, str], bool]] | None = None,
) -> list[T]:
    """Return the items matching the name, label_selector and extra filters, by ID.

    `extra` maps a query parameter to a test `(item, value) -> bool`.
    """
    query = request.query_params
    out = items
    if "name" in query and "name" not in (extra or {}):
        out = [i for i in out if i.get("name") == query["name"]]
    if "label_selector" in query:
        out = [
            i
            for i in out
            if match_labels(i.get("labels") or {}, query["label_selector"])
        ]
    for k, fn in (extra or {}).items():
        # a filter given several times (status=running&status=off) matches any value
        if k in query:
            value = ",".join(query.getlist(k))
            out = [i for i in out if fn(i, value)]
    return sorted(out, key=lambda i: i.get("id", 0))


class RateLimit:
    """Hetzner's rate limit: N requests per hour per project.

    The budget is refilled gradually (N/3600 per second).
    """

    def __init__(self, per_hour: int | None):
        """Allow per_hour requests per hour and key; none or 0 means no limit."""
        self.limit = per_hour
        self.buckets: dict[str, tuple[float, float]] = {}

    def take(self, key: str) -> tuple[bool, dict]:
        """Take one request for key; return whether it is allowed and the headers."""
        if not self.limit:
            return True, {}
        now = time.time()
        rate = self.limit / 3600
        if key not in self.buckets and len(self.buckets) >= 1000:
            full = [
                k
                for k, (n, t) in self.buckets.items()
                if n + (now - t) * rate >= self.limit
            ]
            # a full bucket is the same as no bucket, so it can go
            for k in full or list(self.buckets)[:500]:
                del self.buckets[k]
        tokens, last = self.buckets.get(key, (float(self.limit), now))
        tokens = min(self.limit, tokens + (now - last) * rate)
        ok = tokens >= 1
        if ok:
            tokens -= 1
        self.buckets[key] = (tokens, now)
        reset = int(now + (self.limit - tokens) / rate)
        return ok, {
            "RateLimit-Limit": str(self.limit),
            "RateLimit-Remaining": str(int(tokens)),
            "RateLimit-Reset": str(reset),
        }


# --- the request middleware -----------------------------------------------------------


def check_auth(lab: Lab, request: Request) -> str:
    """Return the request's bearer token, or raise 401 if it is missing or wrong.

    Without a configured token, any token is accepted.
    """
    token = lab.cfg["api"]["token"]
    auth = request.headers.get("authorization", "")
    given_token = auth.removeprefix("Bearer ")
    if given_token == auth or (token and given_token != token):
        raise ApiError(401, "unauthorized", "unable to authenticate")
    return given_token


def injected_fault_error(status: int) -> ApiError:
    """Return the error an injected API fault answers with (/_lab/api-fault)."""
    if status == 429:
        return rate_limit_exceeded("injected fault")
    if status == 503:
        return ApiError(503, "unavailable", "injected fault")
    return server_error("injected fault", status)


async def apply_injected_fault(lab: Lab) -> None:
    """Delay the request and raise the injected fault, as /_lab/api-fault asks."""
    api_fault = lab.api_fault
    if api_fault["delay_ms"]:
        await asyncio.sleep(api_fault["delay_ms"] / 1000)
    if api_fault["count"] > 0:
        api_fault["count"] -= 1
        raise injected_fault_error(api_fault["status"])


async def check_request(spec: openapi.Spec, request: Request) -> None:
    """Check a request's JSON body, labels and sort values; raise an ApiError if bad."""
    has_body = request.method in ("POST", "PUT", "PATCH")
    raw = await request.body() if has_body else b""
    operation = spec.operation(request.method, request.url.path)
    data = None
    if raw:
        try:
            data = json.loads(raw)
        except ValueError:
            raise ApiError(
                400, "json_error", "request body is not valid JSON"
            ) from None
    if operation is None:
        return
    if data is not None or (operation.get("requestBody") or {}).get("required"):
        fields = spec.body_errors(operation, data if data is not None else {})
        if isinstance(data, dict) and "labels" in data:
            messages = openapi.label_errors(data["labels"])
            if messages:
                fields.append({"name": "labels", "messages": messages})
        if fields:
            raise invalid_fields(fields)
    allowed = spec.query_enum(operation, "sort")
    for s in request.query_params.getlist("sort"):
        if allowed is not None and s not in allowed:
            raise invalid(f"must be one of {', '.join(allowed)}", "sort")


def install(app: FastAPI, lab: Lab, spec_name: str, prefix: str) -> None:
    """Install the error handlers and the request middleware for an API under prefix.

    The prefix is e.g. /v1; the middleware does auth, rate limit, injected faults and
    spec checks.
    """
    spec = openapi.load(spec_name)
    limiter = RateLimit(lab.cfg["api"].get("rate_limit", 3600))

    @app.exception_handler(ApiError)
    async def api_error(_: Request, error: ApiError):
        return error_response(error)

    @app.exception_handler(StarletteHTTPException)
    async def http_error(_: Request, error: StarletteHTTPException):
        if error.status_code == 404:
            return error_response(not_found("resource"))
        if error.status_code == 405:
            return error_response(
                ApiError(405, "method_not_allowed", "the request method is not allowed")
            )
        return error_response(server_error(str(error.detail), error.status_code))

    @app.exception_handler(Exception)  # anything else that went wrong in the lab
    async def unexpected(_: Request, error: Exception):
        """Answer a failure of the lab itself as JSON, like Hetzner's server errors.

        A shell command that failed is an example.
        """
        log.exception("unexpected error")
        return error_response(server_error(f"the lab failed: {str(error)[:300]}"))

    @app.exception_handler(RequestValidationError)
    async def validation_error(_: Request, error: RequestValidationError):
        if any(err.get("loc", [None])[0] == "path" for err in error.errors()):
            return error_response(not_found("resource"))
        return error_response(invalid(str(error.errors()[0].get("msg")), "query"))

    @app.middleware("http")
    async def middleware(request: Request, call_next):
        if not request.url.path.startswith(prefix + "/"):
            return await call_next(request)
        headers: dict[str, str] = {}  # the rate limit headers, once the token is known
        try:
            token = check_auth(lab, request)
            allowed, headers = limiter.take(token)
            if not allowed:
                raise rate_limit_exceeded("limit of requests per hour reached")
            await apply_injected_fault(lab)
            await check_request(spec, request)
        except ApiError as error:
            response = error_response(error)
        else:
            response = await call_next(request)
        response.headers.update(headers)
        return response
