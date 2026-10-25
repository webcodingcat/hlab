"""Errors as the Hetzner API reports them. Raised anywhere in the lab (the core, the API modules); api/web.py turns
them into the API's error answers."""


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


def locked(message: str) -> ApiError:
    return ApiError(423, "locked", message)
