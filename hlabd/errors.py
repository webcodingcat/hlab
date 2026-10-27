"""Errors as the Hetzner API reports them.

They are raised anywhere in the lab (the core, the API modules); api/web.py turns them
into the API's error answers. An error code used in more than one place has its factory
here, so its text is the same everywhere.
"""

from collections.abc import Iterable, Mapping
from typing import Any


class ApiError(Exception):
    """An error answer of the API: HTTP status, error code, message and details."""

    def __init__(
        self, status: int, code: str, message: str, details: dict | None = None
    ):
        """Store the status, code, message and details of the error."""
        super().__init__(message)  # str(error) is the message, e.g. in an action
        self.status = status
        self.code = code
        self.message = message
        self.details = details


def not_found(what: str) -> ApiError:
    """Return a 404 not_found error for `what`."""
    return ApiError(404, "not_found", f"{what} not found")


def invalid_fields(fields: list[dict]) -> ApiError:
    """Return a 422 invalid_input error for fields: [{"name": ..., "messages": [...]}].

    The message names the first field and its first message.
    """
    first = fields[0]
    return ApiError(
        422,
        "invalid_input",
        f"invalid input in field '{first['name']}': {first['messages'][0]}",
        {"fields": fields},
    )


def invalid(message: str, field: str | None = None) -> ApiError:
    """Return a 422 invalid_input error, naming the field if one is given."""
    if field:
        return invalid_fields([{"name": field, "messages": [message]}])
    return ApiError(422, "invalid_input", message)


def not_unique(message: str, field: str) -> ApiError:
    """Return a 409 uniqueness_error for a field whose value another item has."""
    return ApiError(409, "uniqueness_error", message, {"fields": [{"name": field}]})


def check_unique_name(
    items: Iterable[Mapping[str, Any]],
    name: str | None,
    current: object = None,  # the item being renamed (compared by identity)
    what: str = "resource",
    message: str | None = None,
) -> None:
    """Raise a 409 uniqueness_error if another item already has this name.

    Names are unique per project, on create and on rename. The message defaults to
    "<what> name is already used".
    """
    if name and any(x.get("name") == name and x is not current for x in items):
        raise not_unique(message or f"{what} name is already used", "name")


def unsupported(what: str) -> ApiError:
    """Return a 422 unsupported_error for something the lab does not do."""
    return ApiError(422, "unsupported_error", f"the lab does not support {what}")


def limit_exceeded(message: str, name: str) -> ApiError:
    """Return a 403 resource_limit_exceeded error naming the limit."""
    return ApiError(
        403, "resource_limit_exceeded", message, {"limits": [{"name": name}]}
    )


def protected(message: str) -> ApiError:
    """Return a 423 protected error (delete or rebuild protection is set)."""
    return ApiError(423, "protected", message)


def locked(message: str) -> ApiError:
    """Return a 423 locked error."""
    return ApiError(423, "locked", message)


def must_be_unassigned(message: str) -> ApiError:
    """Return a 422 must_be_unassigned error (an IP that is assigned to a server)."""
    return ApiError(422, "must_be_unassigned", message)


def server_not_stopped() -> ApiError:
    """Return a 422 server_not_stopped error (the action needs the server off)."""
    return ApiError(422, "server_not_stopped", "the server must be powered off")


def resource_in_use(message: str) -> ApiError:
    """Return a 422 resource_in_use error."""
    return ApiError(422, "resource_in_use", message)


def placement_error(message: str) -> ApiError:
    """Return a 422 placement_error (the placement group cannot take the server)."""
    return ApiError(422, "placement_error", message)


def already_in_placement_group() -> ApiError:
    """Return a 422 already_in_placement_group error."""
    return ApiError(
        422,
        "already_in_placement_group",
        "the server is already part of a placement group",
    )


def firewall_resource_not_found(sid, status: int) -> ApiError:
    """Return a firewall_resource_not_found error for a server.

    Hetzner answers 422 on create and 404 in the firewall actions.
    """
    return ApiError(status, "firewall_resource_not_found", f"server {sid} not found")


def ip_not_available(address) -> ApiError:
    """Return a 422 ip_not_available error (the address is taken or not in range)."""
    return ApiError(422, "ip_not_available", f"IP {address} is not available")


def no_subnet_available(message: str) -> ApiError:
    """Return a 422 no_subnet_available error (no subnet, or no free IP in it)."""
    return ApiError(422, "no_subnet_available", message)


def server_error(message: str, status: int = 500) -> ApiError:
    """Return a server_error (a failure of the lab itself, or an injected fault)."""
    return ApiError(status, "server_error", message)


def rate_limit_exceeded(message: str) -> ApiError:
    """Return a 429 rate_limit_exceeded error."""
    return ApiError(429, "rate_limit_exceeded", message)


def json_error() -> ApiError:
    """Return a 400 json_error: the request body is not valid JSON."""
    return ApiError(400, "json_error", "request body is not valid JSON")
