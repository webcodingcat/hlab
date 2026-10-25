"""HTTP plumbing shared by all API modules: errors, lists (filter, sort, pages), request checks, rate limit."""

import asyncio
import json
import logging
import time

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException

from ..labels import match_labels
from . import hetzner as H
from . import spec as S

log = logging.getLogger("hlabd.web")


class ApiError(Exception):
    def __init__(self, status: int, code: str, message: str, details: dict | None = None):
        self.status, self.code, self.message, self.details = status, code, message, details


def not_found(what: str) -> ApiError:
    return ApiError(404, "not_found", f"{what} not found")


def invalid(message: str, field: str | None = None) -> ApiError:
    details = {"fields": [{"name": field, "messages": [message]}]} if field else None
    return ApiError(422, "invalid_input", f"invalid input in field '{field}': {message}" if field else message, details)


def unique_name(items, name, current: dict | None = None, what: str = "resource") -> None:
    """Names are unique per project (409 uniqueness_error), on create and on rename."""
    if name and any(x.get("name") == name and x is not current for x in items):
        raise ApiError(409, "uniqueness_error", f"{what} name is already used", {"fields": [{"name": "name"}]})


def unsupported(what: str) -> ApiError:
    return ApiError(422, "unsupported_error", f"the lab does not support {what}")


def limit_exceeded(message: str, name: str) -> ApiError:
    return ApiError(403, "resource_limit_exceeded", message, {"limits": [{"name": name}]})


def error_response(e: ApiError) -> JSONResponse:
    return JSONResponse(H.error(e.code, e.message, e.details), status_code=e.status)


async def body(request: Request) -> dict:
    """The JSON body (already checked by the middleware); {} when there is none."""
    raw = await request.body()
    try:
        data = json.loads(raw) if raw else {}
    except ValueError:
        return {}
    return data if isinstance(data, dict) else {}


def paginate(request: Request, items: list, key: str) -> dict:
    q = request.query_params
    items = S.sort_items(items, q.getlist("sort")) if q.getlist("sort") else items
    def number(v: str | None, default: int) -> int:   # Hetzner: invalid values fall back to the default
        try:
            return int(v) if v is not None and int(v) >= 1 else default
        except ValueError:
            return default

    page = number(q.get("page"), 1)
    per_page = min(50, number(q.get("per_page"), 25))
    total = len(items)
    last = max(1, -(-total // per_page))
    chunk = items[(page - 1) * per_page: page * per_page]
    return {key: chunk, "meta": {"pagination": {"page": page, "per_page": per_page, "previous_page": page - 1 or None,
                                                "next_page": page + 1 if page < last else None, "last_page": last,
                                                "total_entries": total}}}


def filtered(request: Request, items: list[dict], extra: dict | None = None) -> list[dict]:
    q = request.query_params
    out = items
    if "name" in q and "name" not in (extra or {}):
        out = [i for i in out if i.get("name") == q["name"]]
    if "label_selector" in q:
        out = [i for i in out if match_labels(i.get("labels") or {}, q["label_selector"])]
    for k, fn in (extra or {}).items():
        if k in q:   # a filter may be given several times (status=running&status=off): any of the values
            value = ",".join(q.getlist(k))
            out = [i for i in out if fn(i, value)]
    return sorted(out, key=lambda i: i.get("id", 0))


class RateLimit:
    """Hetzner's limit: N requests per hour per project, refilled gradually (N/3600 per second)."""

    def __init__(self, per_hour: int | None):
        self.limit = per_hour
        self.buckets: dict[str, tuple[float, float]] = {}

    def take(self, key: str) -> tuple[bool, dict]:
        if not self.limit:
            return True, {}
        now = time.time()
        rate = self.limit / 3600
        if key not in self.buckets and len(self.buckets) >= 1000:
            full = [k for k, (n, t) in self.buckets.items() if n + (now - t) * rate >= self.limit]
            for k in full or list(self.buckets)[:500]:   # full buckets are the same as none
                del self.buckets[k]
        tokens, last = self.buckets.get(key, (float(self.limit), now))
        tokens = min(self.limit, tokens + (now - last) * rate)
        ok = tokens >= 1
        if ok:
            tokens -= 1
        self.buckets[key] = (tokens, now)
        reset = int(now + (self.limit - tokens) / rate)
        return ok, {"RateLimit-Limit": str(self.limit), "RateLimit-Remaining": str(int(tokens)),
                    "RateLimit-Reset": str(reset)}


def install(app: FastAPI, lab, spec_name: str, prefix: str) -> None:
    """Error handlers and the request middleware (auth, rate limit, injected faults, spec checks) for an API under
    prefix (e.g. /v1)."""
    spec = S.load(spec_name)
    limiter = RateLimit(lab.cfg["api"].get("rate_limit", 3600))

    @app.exception_handler(ApiError)
    async def api_error(_: Request, e: ApiError):
        return error_response(e)

    @app.exception_handler(StarletteHTTPException)
    async def http_error(_: Request, e: StarletteHTTPException):
        if e.status_code == 405:
            return error_response(ApiError(405, "method_not_allowed", "the request method is not allowed"))
        return error_response(ApiError(e.status_code, "not_found" if e.status_code == 404 else "server_error",
                                       str(e.detail)))

    @app.exception_handler(Exception)
    async def unexpected(_: Request, e: Exception):
        """A failure of the lab itself (a shell command, say) answers like the cloud's server errors: JSON."""
        log.exception("unexpected error")
        return error_response(ApiError(500, "server_error", f"the lab failed: {str(e)[:300]}"))

    @app.exception_handler(RequestValidationError)
    async def validation_error(_: Request, e: RequestValidationError):
        if any(err.get("loc", [None])[0] == "path" for err in e.errors()):
            return error_response(not_found("resource"))
        return error_response(invalid(str(e.errors()[0].get("msg")), "query"))

    @app.middleware("http")
    async def middleware(request: Request, call_next):
        lab.loop = asyncio.get_running_loop()
        path = request.url.path
        if not path.startswith(prefix + "/"):
            return await call_next(request)
        token = lab.cfg["api"]["token"]
        auth = request.headers.get("authorization", "")
        if not auth.startswith("Bearer ") or (token and auth[7:] != token):
            return error_response(ApiError(401, "unauthorized", "unable to authenticate"))
        ok, headers = limiter.take(auth[7:])
        if not ok:
            r = error_response(ApiError(429, "rate_limit_exceeded", "limit of requests per hour reached"))
            r.headers.update(headers)
            return r
        if lab.fault["delay_ms"]:
            await asyncio.sleep(lab.fault["delay_ms"] / 1000)
        if lab.fault["count"] > 0:
            lab.fault["count"] -= 1
            code = lab.fault["status"]
            r = error_response(ApiError(code, {429: "rate_limit_exceeded", 503: "unavailable"}.get(
                code, "server_error"), "injected fault"))
            r.headers.update(headers)
            return r
        try:
            check_request(spec, request, await request.body() if request.method in ("POST", "PUT", "PATCH") else b"")
        except ApiError as e:
            r = error_response(e)
            r.headers.update(headers)
            return r
        resp = await call_next(request)
        resp.headers.update(headers)
        return resp


def check_request(spec: S.Spec, request: Request, raw: bytes) -> None:
    op = spec.op(request.method, request.url.path)
    data = None
    if raw:
        try:
            data = json.loads(raw)
        except ValueError:
            raise ApiError(400, "json_error", "request body is not valid JSON") from None
    if op is None:
        return
    if data is not None or (op.get("requestBody") or {}).get("required"):
        fields = spec.body_errors(op, data if data is not None else {})
        if isinstance(data, dict) and "labels" in data:
            msgs = S.label_errors(data["labels"])
            if msgs:
                fields.append({"name": "labels", "messages": msgs})
        if fields:
            f = fields[0]
            raise ApiError(422, "invalid_input", f"invalid input in field '{f['name']}': {f['messages'][0]}",
                           {"fields": fields})
    allowed = spec.query_enum(op, "sort")
    for s in request.query_params.getlist("sort"):
        if allowed is not None and s not in allowed:
            raise invalid(f"must be one of {', '.join(allowed)}", "sort")
