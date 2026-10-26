"""The lab's state: everything the fake API knows.

It is saved as JSON in the state volume. Resources are kept by kind (KINDS) and ID;
their fields are the TypedDicts in records.py. Next to them and `next_id`, the state
keeps the lab's own records:

- `faults`: the faults set through /_lab/faults (see fabric/faults.py)
- `netem`: netem options per tap or client link (/_lab/netem)
- `net_routes`: the routes the fabric put in for cloud networks, to remove them again
- `box_users`, `box_uids`: the lab's own Storage Box accounts, with fixed UIDs

The event loop owns the state: it is read and changed only on the loop's thread, which
`check_owner()` checks. A saved change is `with state.change():`, a transaction that
is undone if its block raises. See DEVELOPING.md, State.
"""

import asyncio
import json
import logging
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

log = logging.getLogger(__name__)

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
    return next((c for c in state["clients"].values() if c["name"] == name), None)


def server_by_id(state: "State", sid: int | None) -> Server | None:
    """Return the server with this ID, or None (also without an ID)."""
    return state["servers"].get(sid) if sid is not None else None


def server_by_name(state: "State", name: str) -> Server | None:
    """Return the server with this name, or None."""
    return next((s for s in state["servers"].values() if s["name"] == name), None)


def primary_ip_field(ip_type: str) -> Literal["ipv4_id", "ipv6_id"]:
    """Return the field of a server that holds its primary IP of a type."""
    return "ipv4_id" if ip_type == "ipv4" else "ipv6_id"


def primary_ip_of(state: "State", server: Server, ip_type: str) -> PrimaryIP | None:
    """Return the server's primary IP of a type ("ipv4", "ipv6"), or None."""
    pid = server.get(primary_ip_field(ip_type))
    return state["primary_ips"].get(pid) if pid is not None else None


def primary_address_of(state: "State", server: Server, ip_type: str) -> str | None:
    """Return the address of the server's primary IP of a type (IPv6: its /64)."""
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


def current_task() -> asyncio.Task | None:
    """Return the asyncio task that runs this code, or None (no task, no loop)."""
    try:
        return asyncio.current_task()
    except RuntimeError:  # no event loop runs in this thread
        return None


# what `snapshot()` keeps: every dict and list of the state with a shallow copy of it
type Snapshot = list[tuple[dict | list, dict | list]]


def snapshot(data: dict) -> Snapshot:
    """Return a snapshot of the state's data that `restore()` puts back in place.

    Every dict and list keeps its identity: code holds records (a server in an action's
    steps) and compares them by identity, so a restore must refill the very objects,
    not replace them with copies.
    """
    saved: Snapshot = []
    seen: set[int] = set()
    pending: list[Any] = [data]
    while pending:
        node = pending.pop()
        if id(node) in seen:
            continue
        seen.add(id(node))
        if isinstance(node, dict):
            saved.append((node, dict(node)))
            pending.extend(v for v in node.values() if isinstance(v, (dict, list)))
        else:
            saved.append((node, list(node)))
            pending.extend(v for v in node if isinstance(v, (dict, list)))
    return saved


def restore(saved: Snapshot) -> None:
    """Put the state's data back as `snapshot()` saw it."""
    for node, content in saved:
        if isinstance(node, dict):
            node.clear()
            node.update(content)
        else:
            node[:] = content


class Writer:
    """Writes the saved state to its file in a thread of its own.

    `State.save()` hands it the serialized state; it writes atomically and fsyncs. If
    several saves come in while it writes, only the newest is written next. The thread
    ends when nothing is left to write and starts again with the next save.
    """

    def __init__(self, path: Path):
        """Write to `path`; nothing is waiting yet."""
        self.path = path
        self.condition = threading.Condition()
        self.pending: bytes | None = None  # the newest state not yet written
        self.running = False  # a thread writes, or is about to

    def put(self, data: bytes) -> None:
        """Have `data` written soon, instead of any older state not yet written."""
        with self.condition:
            self.pending = data
            if not self.running:
                self.running = True
                threading.Thread(
                    target=self.run, name="state-writer", daemon=True
                ).start()

    def run(self) -> None:
        """Write the pending states until none is left."""
        while True:
            with self.condition:
                data, self.pending = self.pending, None
                if data is None:
                    self.running = False
                    self.condition.notify_all()
                    return
            try:
                self.write(data)
            except Exception:  # the next save tries again; the state stays in memory
                log.exception("saving the state to %s failed", self.path)

    def write(self, data: bytes) -> None:
        """Write the state to its file atomically, on disk before it replaces it."""
        tmp = self.path.with_suffix(".tmp")
        with open(tmp, "wb") as file:
            os.fchmod(file.fileno(), 0o600)  # passwords of Storage Boxes are in it
            file.write(data)
            file.flush()
            os.fsync(file.fileno())
        os.replace(tmp, self.path)

    def flush(self) -> None:
        """Wait until everything handed in so far is on disk."""
        with self.condition:
            self.condition.wait_for(lambda: not self.running)


class State:
    """The lab's state: kept in memory, owned by the event loop, saved as JSON."""

    def __init__(self, path: Path | None):
        """Load the state from `path` if it exists; without a path, nothing is saved."""
        self.path = path
        self.writer = Writer(path) if path else None
        # the thread of the event loop that owns the state (`own`); None until then,
        # and the first thread that uses the state owns it
        self.owner: int | None = None
        # the tasks whose `change()` blocks are open, innermost last
        self.changing: list[asyncio.Task | None] = []
        self._data: dict[str, Any] = {k: {} for k in KINDS}
        self._data["next_id"] = dict(ID_BASE)
        self._data.update(lab_records())
        if path and path.is_file():
            loaded = json.loads(path.read_text())
            self._data.update(loaded)
            # kinds added since the file was written start at their base
            self._data["next_id"] = {
                **ID_BASE,
                **loaded.get("next_id", {}),
            }
            for k in KINDS:  # JSON keys are strings
                self._data[k] = {int(i): v for i, v in self._data.get(k, {}).items()}

    # --- the owner ----------------------------------------------------------------

    def own(self) -> None:
        """Make the calling thread (the event loop's) the state's owner."""
        self.owner = threading.get_ident()

    def check_owner(self) -> None:
        """Raise AssertionError unless the caller runs on the owner's thread.

        Also when a `change()` block of another task is open: that block awaited, so
        another task sees its change half done. Called on every access, so it is cheap.
        """
        thread = threading.get_ident()
        if self.owner is None:
            self.owner = thread
        elif thread != self.owner:
            raise AssertionError("the state is used outside the event loop's thread")
        if self.changing and self.changing[-1] is not current_task():
            raise AssertionError("a state.change() block of another task is open")

    @property
    def data(self) -> dict[str, Any]:
        """Return the whole state: resources by kind, `next_id`, the lab's records."""
        self.check_owner()
        return self._data

    def new_id(self, kind: str) -> int:
        """Return the next free ID for a kind."""
        self.check_owner()
        self._data["next_id"][kind] += 1
        return self._data["next_id"][kind]

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
        self.check_owner()
        return self._data[kind]

    def clear(self) -> None:
        """Remove every resource and the lab's records (a lab reset).

        IDs keep counting up (`next_id` stays), as on Hetzner, so no old ID matches a
        new resource. The lab's own Storage Box accounts stay too.
        """
        with self.change():
            kept = {key: self._data[key] for key in ("box_users", "box_uids")}
            self._data.update({kind: {} for kind in KINDS})
            self._data.update(lab_records())
            self._data.update(kept)

    # --- changing and saving ------------------------------------------------------

    @contextmanager
    def change(self) -> Iterator[None]:
        """Change the state as one transaction: saved if the block succeeds.

        A block that raises is undone: the state is put back as it was before the block
        (`snapshot`), so a refused request leaves nothing behind, in memory or in the
        file. The block must not await: it is a plain `with`, and another task would
        see the change half done (`check_owner` refuses that). Blocks may nest; the
        outermost one saves.
        """
        self.check_owner()
        saved = snapshot(self._data)
        self.changing.append(current_task())
        try:
            yield
        except BaseException:
            restore(saved)
            raise
        finally:
            self.changing.pop()
        if not self.changing:
            self.save()

    def save(self) -> None:
        """Hand the state to the writer thread (no-op without a path).

        The state is serialized here, on the loop, so the file gets the state as it is
        now; writing and fsyncing happen in the writer's thread.
        """
        self.check_owner()
        if self.writer:
            self.writer.put(json.dumps(self._data, indent=1, sort_keys=True).encode())

    def flush(self) -> None:
        """Wait until every save so far is on disk (any thread; it reads no state)."""
        if self.writer:
            self.writer.flush()

    def free_index(self) -> int:
        """Return the lab index of a new server (tap pub<index>, MACs, IPv6 link-local).

        Returns 0 when the lab is full.
        """
        used = {s["_lab"]["index"] for s in self["servers"].values()}
        return next((i for i in range(1, 240) if i not in used), 0)
