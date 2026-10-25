"""HTTP plumbing shared by all API modules.

Error answers, lists (filter, sort, pages), request checks, rate limit.
"""

import asyncio
import json
import logging
import time

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException

from ..errors import ApiError, invalid, not_found
from ..labels import match_labels
from . import hetzner
from . import spec as openapi

log = logging.getLogger("hlabd.web")


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


def paginate(request: Request, items: list, key: str) -> dict:
    """Return a sorted page of items under key, with the pagination meta."""
    query = request.query_params
    items = (
        openapi.sort_items(items, query.getlist("sort"))
        if query.getlist("sort")
        else items
    )

    def number(
        value: str | None, default: int
    ) -> int:  # Hetzner: invalid values fall back to the default
        try:
            return int(value) if value is not None and int(value) >= 1 else default
        except ValueError:
            return default

    page = number(query.get("page"), 1)
    per_page = min(50, number(query.get("per_page"), 25))
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


def filtered(
    request: Request, items: list[dict], extra: dict | None = None
) -> list[dict]:
    """Return the items matching the name, label_selector and extra filters, by ID."""
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
        # a filter may be given several times (status=running&status=off): any of the
        # values
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
            for k in (
                full or list(self.buckets)[:500]
            ):  # full buckets are the same as none
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


def install(app: FastAPI, lab, spec_name: str, prefix: str) -> None:
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
        if error.status_code == 405:
            return error_response(
                ApiError(405, "method_not_allowed", "the request method is not allowed")
            )
        return error_response(
            ApiError(
                error.status_code,
                "not_found" if error.status_code == 404 else "server_error",
                str(error.detail),
            )
        )

    @app.exception_handler(Exception)
    async def unexpected(_: Request, error: Exception):
        """Answer a failure of the lab itself like the cloud's server errors: JSON.

        Such a failure is, say, a shell command.
        """
        log.exception("unexpected error")
        return error_response(
            ApiError(500, "server_error", f"the lab failed: {str(error)[:300]}")
        )

    @app.exception_handler(RequestValidationError)
    async def validation_error(_: Request, error: RequestValidationError):
        if any(err.get("loc", [None])[0] == "path" for err in error.errors()):
            return error_response(not_found("resource"))
        return error_response(invalid(str(error.errors()[0].get("msg")), "query"))

    @app.middleware("http")
    async def middleware(request: Request, call_next):
        lab.loop = asyncio.get_running_loop()
        path = request.url.path
        if not path.startswith(prefix + "/"):
            return await call_next(request)
        token = lab.cfg["api"]["token"]
        auth = request.headers.get("authorization", "")
        if not auth.startswith("Bearer ") or (token and auth[7:] != token):
            return error_response(
                ApiError(401, "unauthorized", "unable to authenticate")
            )
        ok, headers = limiter.take(auth[7:])
        if not ok:
            response = error_response(
                ApiError(
                    429, "rate_limit_exceeded", "limit of requests per hour reached"
                )
            )
            response.headers.update(headers)
            return response
        if lab.fault["delay_ms"]:
            await asyncio.sleep(lab.fault["delay_ms"] / 1000)
        if lab.fault["count"] > 0:
            lab.fault["count"] -= 1
            code = lab.fault["status"]
            response = error_response(
                ApiError(
                    code,
                    {429: "rate_limit_exceeded", 503: "unavailable"}.get(
                        code, "server_error"
                    ),
                    "injected fault",
                )
            )
            response.headers.update(headers)
            return response
        try:
            check_request(
                spec,
                request,
                await request.body()
                if request.method in ("POST", "PUT", "PATCH")
                else b"",
            )
        except ApiError as error:
            response = error_response(error)
            response.headers.update(headers)
            return response
        answer = await call_next(request)
        answer.headers.update(headers)
        return answer


def check_request(spec: openapi.Spec, request: Request, raw: bytes) -> None:
    """Check a request's JSON body, labels and sort values; raise an ApiError if bad."""
    operation = spec.op(request.method, request.url.path)
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
            field = fields[0]
            raise ApiError(
                422,
                "invalid_input",
                f"invalid input in field '{field['name']}': {field['messages'][0]}",
                {"fields": fields},
            )
    allowed = spec.query_enum(operation, "sort")
    for s in request.query_params.getlist("sort"):
        if allowed is not None and s not in allowed:
            raise invalid(f"must be one of {', '.join(allowed)}", "sort")
