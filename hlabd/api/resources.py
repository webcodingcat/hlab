"""What every resource changes the same way: its fields on PUT, its delete protection.

The resource modules use these instead of writing their own; web.py has the HTTP side.
"""

from ..errors import check_unique_name
from ..lab import Lab, Plan, fields_of
from ..records import ProtectedRecord, Record
from ..state import Kind


def update_fields(item: Record, body: dict, keys: tuple[str, ...]) -> None:
    """Change the given fields of an item on PUT (null labels mean no labels)."""
    fields = fields_of(item)
    for key in keys:
        if key in body:
            fields[key] = (body[key] or {}) if key == "labels" else body[key]


def update_resource[R: Record](
    lab: Lab, kind: Kind[R], item: R, body: dict, keys: tuple[str, ...], what: str
) -> None:
    """Apply a PUT to a resource of a kind: names stay unique within the kind."""
    with lab.state.change():
        check_unique_name(lab.state[kind].values(), body.get("name"), item, what)
        update_fields(item, body, keys)


async def change_delete_protection(lab: Lab, item: ProtectedRecord, body: dict) -> Plan:
    """Set a resource's delete protection: the change_protection action.

    A body without `delete` keeps the current value. Every resource with a plain
    delete protection uses this in its action table. Servers have their own function,
    because they set delete and rebuild protection together.
    """
    with lab.state.change():
        item["protection"]["delete"] = bool(
            body.get("delete", item["protection"]["delete"])
        )
    return Plan()
