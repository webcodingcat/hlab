"""Errors as the Hetzner API reports them.

They are raised anywhere in the lab (the core, the API modules); api/web.py turns them
into the API's error answers.
"""


class ApiError(Exception):
    """An error answer of the API: HTTP status, error code, message and details."""

    def __init__(
        self, status: int, code: str, message: str, details: dict | None = None
    ):
        """Store the status, code, message and details of the error."""
        self.status, self.code, self.message, self.details = (
            status,
            code,
            message,
            details,
        )


def not_found(what: str) -> ApiError:
    """Return a 404 not_found error for `what`."""
    return ApiError(404, "not_found", f"{what} not found")


def invalid(message: str, field: str | None = None) -> ApiError:
    """Return a 422 invalid_input error, naming the field if one is given."""
    details = {"fields": [{"name": field, "messages": [message]}]} if field else None
    return ApiError(
        422,
        "invalid_input",
        f"invalid input in field '{field}': {message}" if field else message,
        details,
    )


def unique_name(
    items, name, current: dict | None = None, what: str = "resource"
) -> None:
    """Raise a 409 uniqueness_error if another item already has this name.

    Names are unique per project, on create and on rename.
    """
    if name and any(x.get("name") == name and x is not current for x in items):
        raise ApiError(
            409,
            "uniqueness_error",
            f"{what} name is already used",
            {"fields": [{"name": "name"}]},
        )


def unsupported(what: str) -> ApiError:
    """Return a 422 unsupported_error for something the lab does not do."""
    return ApiError(422, "unsupported_error", f"the lab does not support {what}")


def limit_exceeded(message: str, name: str) -> ApiError:
    """Return a 403 resource_limit_exceeded error naming the limit."""
    return ApiError(
        403, "resource_limit_exceeded", message, {"limits": [{"name": name}]}
    )


def locked(message: str) -> ApiError:
    """Return a 423 locked error."""
    return ApiError(423, "locked", message)
