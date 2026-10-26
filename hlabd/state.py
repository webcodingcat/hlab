"""The lab's state: everything the fake API knows.

It is saved as JSON in the state volume. Resources are kept by kind (KINDS) and ID;
their fields are the TypedDicts in records.py. Next to them and `next_id`, the state
keeps the lab's own records:

- `faults`: the faults set through /_lab/faults (see fabric/faults.py)
- `netem`: netem options per tap or client link (/_lab/netem)
- `net_routes`: the routes the fabric put in for cloud networks, to remove them again
- `box_users`, `box_uids`: the lab's own Storage Box accounts, with fixed UIDs

Every change to the state, and every loop over it, holds `lock`; `change()` also saves.
The lookups below take the lock themselves. See DEVELOPING.md, State and locking.
"""

import asyncio
import json
import os
import threading
from collections.abc import Iterator
from contextlib import contextmanager
from pathlib import Path
from typing import Any, Literal, overload

from .records import (
    Action,
    BoxSnapshot,
    Client,
    CloudNetwork,
    Firewall,
    FloatingIP,
    Nic,
    PrimaryIP,
    Server,
    SSHKey,
    StorageBox,
    Subaccount,
    Volume,
)

# First ID per kind, so IDs in logs tell what they are (server 1000001, network
# 2000001, ...): a million IDs per kind before the next kind's range, and actions, which
# grow fastest, far behind the rest.
ID_BASE = {
    "servers": 1_000_000,
    "networks": 2_000_000,
    "ssh_keys": 3_000_000,
    "firewalls": 4_000_000,
    "floating_ips": 5_000_000,
    "primary_ips": 6_000_000,
    "volumes": 10_000_000,
    "storage_boxes": 14_000_000,
    "subaccounts": 15_000_000,
    "box_snapshots": 16_000_000,
    "actions": 100_000_000,
    "clients": 0,
}
KINDS = tuple(ID_BASE)


def nics_in(state: "State", nid: int) -> list[tuple[Server, Nic]]:
    """Return (server, NIC) for every NIC in the cloud network with this ID."""
    with state.lock:
        return [
            (server, nic)
            for server in state["servers"].values()
            for nic in server["_lab"]["nics"]
            if nic["network"] == nid
        ]


def nic_addresses(nic: Nic) -> list[str]:
    """Return a NIC's addresses: its private IP and its alias IPs."""
    return [nic["ip"], *nic["alias_ips"]]


def client_by_name(state: "State", name: str) -> Client | None:
    """Return the Internet client with this name, or None."""
    with state.lock:
        return next((c for c in state["clients"].values() if c["name"] == name), None)


def server_by_id(state: "State", sid: int | None) -> Server | None:
    """Return the server with this ID, or None (also without an ID)."""
    with state.lock:
        return state["servers"].get(sid) if sid is not None else None


def server_by_name(state: "State", name: str) -> Server | None:
    """Return the server with this name, or None."""
    with state.lock:
        return next((s for s in state["servers"].values() if s["name"] == name), None)


def primary_ip_field(ip_type: str) -> Literal["ipv4_id", "ipv6_id"]:
    """Return the field of a server that holds its primary IP of a type."""
    return "ipv4_id" if ip_type == "ipv4" else "ipv6_id"


def primary_ip_of(state: "State", server: Server, ip_type: str) -> PrimaryIP | None:
    """Return the server's primary IP of a type ("ipv4", "ipv6"), or None."""
    with state.lock:
        pid = server.get(primary_ip_field(ip_type))
        return state["primary_ips"].get(pid) if pid is not None else None


def primary_address_of(state: "State", server: Server, ip_type: str) -> str | None:
    """Return the address of the server's primary IP of a type (IPv6: its /64)."""
    with state.lock:
        primary_ip = primary_ip_of(state, server, ip_type)
        return primary_ip["ip"] if primary_ip else None


def lab_records() -> dict:
    """Return the lab's own records, empty (see the module docstring)."""
    return {
        "faults": [],
        "netem": {},
        "net_routes": [],
        "box_users": {},
        "box_uids": {},
    }


def lock_holder() -> tuple[int, asyncio.Task | None]:
    """Return who runs this code: the thread, and the asyncio task it runs (or None)."""
    try:
        task = asyncio.current_task()
    except RuntimeError:  # no event loop runs in this thread
        task = None
    return threading.get_ident(), task


class StateLock:
    """The state's re-entrant lock, which knows who holds it.

    `threading.RLock` cannot tell its holder (only through a private method), so this
    lock records its owner and how often it entered, and `assert_held()` turns the
    locking rule into a check. The owner is the thread and, on the event loop, the
    asyncio task: the coroutines there share one thread, and a coroutine that does not
    hold the lock must fail the check while another one holds it. It costs two cheap
    lookups, so it is always on. The rule keeps awaits outside the lock; a task that
    takes the lock while another task of the same thread holds it (an await inside the
    lock) raises.
    """

    def __init__(self) -> None:
        """Start unlocked."""
        self.rlock = threading.RLock()
        # the holder (`lock_holder()`): its thread's ident and its task, or None
        self.owner: tuple[int, asyncio.Task | None] | None = None
        self.depth = 0  # how often the owner entered

    def acquire(self) -> None:
        """Wait for the lock and take it (again, if the caller holds it already)."""
        self.rlock.acquire()
        holder = lock_holder()
        if self.depth and self.owner != holder:
            # the RLock let in another task of the owner's thread
            self.rlock.release()
            raise RuntimeError("state.lock is held across an await by another task")
        self.owner = holder
        self.depth += 1

    def release(self) -> None:
        """Give the lock back once; it is free when every acquire is undone."""
        if not self.held_by_me():
            raise RuntimeError("state.lock released by a caller that does not hold it")
        self.depth -= 1
        if self.depth == 0:
            self.owner = None
        self.rlock.release()

    def __enter__(self) -> "StateLock":
        """Take the lock for a `with` block."""
        self.acquire()
        return self

    def __exit__(self, *exc_info: object) -> None:
        """Give the lock back at the end of a `with` block."""
        self.release()

    def held_by_me(self) -> bool:
        """Return whether the caller (its thread and asyncio task) holds the lock."""
        return self.owner == lock_holder()

    def assert_held(self) -> None:
        """Raise AssertionError unless the caller holds the lock."""
        if not self.held_by_me():
            raise AssertionError("the state is changed without holding state.lock")


class State:
    """The lab's state: kept in memory, saved as JSON."""

    def __init__(self, path: Path | None):
        """Load the state from `path` if it exists; without a path, nothing is saved."""
        self.path = path
        self.lock = StateLock()
        self.data: dict[str, Any] = {k: {} for k in KINDS}
        self.data["next_id"] = dict(ID_BASE)
        self.data.update(lab_records())
        if path and path.is_file():
            loaded = json.loads(path.read_text())
            self.data.update(loaded)
            # kinds added since the file was written start at their base
            self.data["next_id"] = {
                **ID_BASE,
                **loaded.get("next_id", {}),
            }
            for k in KINDS:  # JSON keys are strings
                self.data[k] = {int(i): v for i, v in self.data.get(k, {}).items()}

    def new_id(self, kind: str) -> int:
        """Return the next free ID for a kind."""
        self.lock.assert_held()  # the new ID belongs to a record the caller adds
        self.data["next_id"][kind] += 1
        return self.data["next_id"][kind]

    # each kind with its record type (records.py), so the type checker knows the fields
    @overload
    def __getitem__(self, kind: Literal["servers"]) -> dict[int, Server]: ...
    @overload
    def __getitem__(self, kind: Literal["networks"]) -> dict[int, CloudNetwork]: ...
    @overload
    def __getitem__(self, kind: Literal["ssh_keys"]) -> dict[int, SSHKey]: ...
    @overload
    def __getitem__(self, kind: Literal["firewalls"]) -> dict[int, Firewall]: ...
    @overload
    def __getitem__(self, kind: Literal["floating_ips"]) -> dict[int, FloatingIP]: ...
    @overload
    def __getitem__(self, kind: Literal["primary_ips"]) -> dict[int, PrimaryIP]: ...
    @overload
    def __getitem__(self, kind: Literal["actions"]) -> dict[int, Action]: ...
    @overload
    def __getitem__(self, kind: Literal["volumes"]) -> dict[int, Volume]: ...
    @overload
    def __getitem__(self, kind: Literal["storage_boxes"]) -> dict[int, StorageBox]: ...
    @overload
    def __getitem__(self, kind: Literal["subaccounts"]) -> dict[int, Subaccount]: ...
    @overload
    def __getitem__(self, kind: Literal["box_snapshots"]) -> dict[int, BoxSnapshot]: ...
    @overload
    def __getitem__(self, kind: Literal["clients"]) -> dict[int, Client]: ...
    @overload
    def __getitem__(self, kind: str) -> dict[int, Any]: ...

    def __getitem__(self, kind: str) -> dict[int, Any]:
        """Return the resources of a kind, by ID."""
        return self.data[kind]

    def clear(self) -> None:
        """Remove every resource and the lab's records (a lab reset).

        IDs keep counting up (`next_id` stays), as on Hetzner, so no old ID matches a
        new resource. The lab's own Storage Box accounts stay too.
        """
        with self.change():
            kept = {key: self.data[key] for key in ("box_users", "box_uids")}
            self.data.update({kind: {} for kind in KINDS})
            self.data.update(lab_records())
            self.data.update(kept)

    @contextmanager
    def change(self) -> Iterator[None]:
        """Hold the lock for a change, and save the state if the block succeeds.

        A block that raises leaves the state unsaved, as a refused request should.
        Check first, then change: an exception after a change leaves memory and file
        apart until the next save (nothing is rolled back), so a block makes no change
        before its checks have passed, or undoes it before it raises. It saves: on the
        event loop, run the block in a function through `lab.blocking`.
        """
        with self.lock:
            yield
            self.save()

    def save(self) -> None:
        """Write the state to its file atomically (no-op without a path).

        The caller holds the lock (usually through `change()`), so the state it saves is
        the one its change left, not one half changed by another thread.
        """
        self.lock.assert_held()
        if not self.path:
            return
        tmp = self.path.with_suffix(".tmp")
        tmp.write_text(json.dumps(self.data, indent=1, sort_keys=True))
        tmp.chmod(0o600)  # passwords of Storage Boxes are in it
        with open(tmp, "rb+") as file:  # on disk before it replaces the old state
            os.fsync(file.fileno())
        os.replace(tmp, self.path)

    def free_index(self) -> int:
        """Return the lab index of a new server (tap pub<index>, MACs, IPv6 link-local).

        Returns 0 when the lab is full.
        """
        with self.lock:
            used = {s["_lab"]["index"] for s in self.data["servers"].values()}
            return next((i for i in range(1, 240) if i not in used), 0)
