"""The versions of the saved state, and the migrations between them.

The saved state carries a `version`. When the shape of a record changes, the version
goes up by one and a migration brings older files to the new shape (DEVELOPING.md,
Recipes). `State` runs them when it loads the file, before anything else reads it.

Each migration is a plain function on the data as it was loaded from JSON: IDs are
still strings, and nothing in it is typed yet. It changes the data in place and must
not use the lab's current code (record types, constants), because those describe the
newest shape, not the one the step starts from.

Version 0 is every file written before the state had a version. Such a file may hold
any mix of the old shapes that the first steps below upgrade, so each of them changes
only what is still old. No hlabd wrote versions 1 to 3; they only order those steps.
"""

from collections.abc import Callable
from pathlib import Path
from typing import Any

type Migration = Callable[[dict[str, Any]], None]


class StateVersionError(RuntimeError):
    """The state file comes from a newer hlabd than this one."""


def migrate_0_to_1(data: dict[str, Any]) -> None:
    """Drop the servers' old `locked` field.

    Before the flag `_locked` existed, a server stored `locked` (True or False). No
    action survives a restart, so the field goes without a replacement.
    """
    for server in data.get("servers", {}).values():
        server.pop("locked", None)


def migrate_1_to_2(data: dict[str, Any]) -> None:
    """Turn the faults' text references (s:<id>, c:<name>) into typed ones.

    Older labs stored a fault's server or client as the text the API shows. Now a
    reference is {"server": <id>} or {"client": <name>} (`TargetRef` in records.py).
    """
    for fault in data.get("faults", []):
        for ref_field in ("first_ref", "second_ref", "target_ref"):
            ref = fault.get(ref_field)
            if isinstance(ref, str):
                kind, key = ref.split(":", 1)
                fault[ref_field] = (
                    {"server": int(key)} if kind == "s" else {"client": key}
                )


def migrate_2_to_3(data: dict[str, Any]) -> None:
    """Give every server `_lab.target`, the status its create or rebuild ends in.

    Servers created before the field existed were always started after their create.
    """
    for server in data.get("servers", {}).values():
        server["_lab"].setdefault("target", "running")


def migrate_3_to_4(data: dict[str, Any]) -> None:
    """Fill in fields that older records lack: a server's keys, a snapshot's size.

    A server without `public_keys` was made without SSH keys; a snapshot without
    `size` shows 0 bytes until it is taken again.
    """
    for server in data.get("servers", {}).values():
        server.setdefault("public_keys", [])
    for snapshot in data.get("box_snapshots", {}).values():
        snapshot.setdefault("size", 0)


# the migrations in order: MIGRATIONS[n] takes the state from version n to n + 1
MIGRATIONS: tuple[Migration, ...] = (
    migrate_0_to_1,
    migrate_1_to_2,
    migrate_2_to_3,
    migrate_3_to_4,
)
# the version that this hlabd writes
STATE_VERSION = len(MIGRATIONS)


def migrate(data: dict[str, Any], path: Path | None = None) -> None:
    """Bring loaded state data to `STATE_VERSION`, in place.

    A file without a version is version 0. A file from a newer hlabd raises
    StateVersionError: this code cannot know its shapes, and saving would lose them.
    """
    version = data.get("version", 0)
    if not isinstance(version, int) or isinstance(version, bool) or version < 0:
        raise StateVersionError(f"the state file {path} has no valid version")
    if version > STATE_VERSION:
        raise StateVersionError(
            f"the state file {path} has version {version}, but this hlabd knows "
            f"versions up to {STATE_VERSION}: update hlab, or reset the lab "
            "(`bin/hlab purge`) to start with an empty state"
        )
    for step in MIGRATIONS[version:]:
        step(data)
    data["version"] = STATE_VERSION
