"""HTTP plumbing shared by all API modules.

This module has the error answers, lists (filter, sort, pages), request checks, IDs in
request bodies and the rate limit. What every resource changes the same way (PUT,
delete protection) is in resources.py.
"""

import asyncio
import json
import logging
import time
from collections.abc import Callable, Mapping
from typing import Any

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException

from ..errors import (
    ApiError,
    invalid,
    invalid_fields,
    json_error,
    not_found,
    rate_limit_exceeded,
    server_error,
    unavailable,
)
from ..lab import Lab
from ..labels import match_labels
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
    """Return the JSON body as a dict; raise 400 json_error if it is not valid JSON.

    An empty body, or JSON that is not an object, gives {}. Under /v1 the middleware
    has already refused broken JSON with the same error; the lab's control routes
    (/_lab) have no middleware, so this check is theirs.
    """
    raw = await request.body()
    try:
        data = json.loads(raw) if raw else {}
    except ValueError:
        raise json_error() from None
    return data if isinstance(data, dict) else {}


def ref_id(value: int | str | None, field: str) -> int:
    """Return a resource ID from a request body, or raise invalid_input for the field.

    The request check lets only numbers (or null) through where the specification
    wants an ID.
    """
    if value is None or isinstance(value, bool):
        raise invalid(f"{field} must be an ID", field)
    try:
        return int(value)
    except (TypeError, ValueError):
        raise invalid(f"{field} must be an ID", field) from None


# --- lists ----------------------------------------------------------------------------


def positive_int_or_default(value: str | None, default: int) -> int:
    """Return a query value as a number of at least 1, else the default (Hetzner)."""
    if value is None:
        return default
    try:
        number = int(value)
    except ValueError:
        return default
    return number if number >= 1 else default


def sort_items[T: Mapping[str, Any]](items: list[T], sorts: list[str]) -> list[T]:
    """Sort items by the sort=field[:asc|:desc] parameters.

    Several are allowed; they apply in order (the first is the primary key).
    """
    seen: dict[str, str] = {}
    for sort in sorts:
        field, _, direction = sort.partition(":")
        direction = direction or "asc"
        if field in seen and seen[field] != direction:
            seen[field] = "ignore"  # a field sorted both ways is ignored
        else:
            seen.setdefault(field, direction)

    for field, direction in reversed(
        [(f, d) for f, d in seen.items() if d != "ignore"]
    ):
        # null sorts last in both directions, so it stays out of the sorted values
        keyed = [(field_value(item, field), item) for item in items]
        present = [pair for pair in keyed if pair[0] is not None]
        present.sort(key=lambda pair: pair[0], reverse=direction == "desc")
        items = [item for _, item in present]
        items += [item for value, item in keyed if value is None]
    return items


def field_value(item: Mapping[str, Any], field: str) -> Any:
    """Return a field's value, nested ones like stats.size too; None if missing."""
    value: Any = item
    for part in field.split("."):
        value = value.get(part) if isinstance(value, dict) else None
    return value


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
    for parameter, matches in (extra or {}).items():
        # a filter given several times (status=running&status=off) matches any value
        if parameter in query:
            value = ",".join(query.getlist(parameter))
            out = [i for i in out if matches(i, value)]
    return sorted(out, key=lambda i: i.get("id", 0))


class RateLimit:
    """Hetzner's rate limit: N requests per hour per project.

    The budget is refilled gradually (N/3600 per second).
    """

    # Buckets kept at most: with more, the full ones go (a full bucket is the same
    # as none), or else the oldest half, so many tokens cannot grow the memory.
    MAX_BUCKETS = 1000

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
        if key not in self.buckets and len(self.buckets) >= self.MAX_BUCKETS:
            full = [
                bucket_key
                for bucket_key, (tokens, last) in self.buckets.items()
                if tokens + (now - last) * rate >= self.limit
            ]
            oldest_half = list(self.buckets)[: self.MAX_BUCKETS // 2]
            for bucket_key in full or oldest_half:
                del self.buckets[bucket_key]
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
        return unavailable("injected fault")
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
            raise json_error() from None
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
    for sort in request.query_params.getlist("sort"):
        if allowed is not None and sort not in allowed:
            raise invalid(f"must be one of {', '.join(allowed)}", "sort")


def install_error_handlers(app: FastAPI) -> None:
    """Answer every error as JSON in Hetzner's shape ({"error": {"code", ...}})."""

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


def install(app: FastAPI, lab: Lab, spec_name: str, prefix: str) -> None:
    """Install the error handlers and the request middleware for an API under prefix.

    The prefix is e.g. /v1; the middleware does auth, rate limit, injected faults and
    spec checks.
    """
    spec = openapi.load(spec_name)
    limiter = RateLimit(lab.cfg["api"].get("rate_limit", 3600))
    install_error_handlers(app)

    @app.middleware("http")
    async def middleware(request: Request, call_next):
        if not request.url.path.startswith(prefix + "/"):
            return await call_next(request)
        headers: dict[str, str] = {}  # the rate limit headers, once the token is known
        try:
            if lab.resetting:
                raise unavailable("the lab is being reset")
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
