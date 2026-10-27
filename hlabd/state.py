"""The lab's state: everything the fake API knows.

The state is saved as JSON in the state volume. Resources are stored by kind and ID;
`ALL_KINDS` lists the kinds (`Kind`: name, record type in records.py, ID range), and
`state[SERVERS]` returns the servers by ID. Besides the resources, `next_id` and the
`version` of the saved shape (migrations.py), the state holds records that only the
lab uses:

- `faults`: the faults set through /_lab/faults (`Fault` in records.py, api/faults.py)
- `netem`: netem options per tap or client link (/_lab/netem)
- `net_routes`: the routes the fabric added for cloud networks, so they can be removed
- `box_users`, `box_uids`: Storage Box accounts created outside the API, with fixed UIDs

Who may use the state and how to change it (`check_owner()`, `change()`,
`check_committed()`): DEVELOPING.md, The state.
"""

import asyncio
import difflib
import json
import logging
import os
import threading
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Literal

from .migrations import STATE_VERSION, migrate
from .records import (
    Action,
    BoxSnapshot,
    Client,
    CloudNetwork,
    Firewall,
    FloatingIP,
    Nic,
    PrimaryIP,
    Record,
    Server,
    SSHKey,
    StorageBox,
    Subaccount,
    Volume,
)

log = logging.getLogger(__name__)

# how many servers the lab runs at most: a server's index (1 to MAX_SERVERS) names
# its tap and is part of its MACs and IPv6 link-local address (`State.free_index`)
MAX_SERVERS = 239


@dataclass(frozen=True)
class Kind[R]:
    """A kind of resource: its key in the state, its record type and its IDs.

    The module constants below (`SERVERS`, ...) are the typed keys of the state:
    `state[SERVERS]` is a `dict[int, Server]`, so mypy knows the fields.
    """

    name: str  # its key in the state and in the saved file ("servers")
    record: type[R]  # its TypedDict in records.py
    # Its first ID, so that an ID in a log shows its kind (server 1000001, network
    # 2000001, ...). Each kind has a million IDs before the next kind's range starts.
    # Actions grow fastest, so their range starts far above the others.
    id_base: int
    noun: str  # how messages name one of them ("server", "SSH key")


SERVERS: Kind[Server] = Kind("servers", Server, 1_000_000, "server")
NETWORKS: Kind[CloudNetwork] = Kind("networks", CloudNetwork, 2_000_000, "network")
SSH_KEYS: Kind[SSHKey] = Kind("ssh_keys", SSHKey, 3_000_000, "SSH key")
FIREWALLS: Kind[Firewall] = Kind("firewalls", Firewall, 4_000_000, "firewall")
FLOATING_IPS: Kind[FloatingIP] = Kind(
    "floating_ips", FloatingIP, 5_000_000, "Floating IP"
)
PRIMARY_IPS: Kind[PrimaryIP] = Kind("primary_ips", PrimaryIP, 6_000_000, "primary IP")
VOLUMES: Kind[Volume] = Kind("volumes", Volume, 10_000_000, "volume")
STORAGE_BOXES: Kind[StorageBox] = Kind(
    "storage_boxes", StorageBox, 14_000_000, "Storage Box"
)
SUBACCOUNTS: Kind[Subaccount] = Kind(
    "subaccounts", Subaccount, 15_000_000, "sub-account"
)
BOX_SNAPSHOTS: Kind[BoxSnapshot] = Kind(
    "box_snapshots", BoxSnapshot, 16_000_000, "snapshot"
)
ACTIONS: Kind[Action] = Kind("actions", Action, 100_000_000, "action")
CLIENTS: Kind[Client] = Kind("clients", Client, 0, "client")

# Every kind of resource the state keeps: the one registry. A new kind starts here;
# the recipe in DEVELOPING.md lists the other places, and tests/test_state.py finds
# those missed. The kinds of `Record` (records.py), then the actions.
RECORD_KINDS: tuple[Kind[Record], ...] = (
    SERVERS,
    NETWORKS,
    SSH_KEYS,
    FIREWALLS,
    FLOATING_IPS,
    PRIMARY_IPS,
    VOLUMES,
    STORAGE_BOXES,
    SUBACCOUNTS,
    BOX_SNAPSHOTS,
    CLIENTS,
)
ALL_KINDS: tuple[Kind[Record | Action], ...] = (*RECORD_KINDS, ACTIONS)
# the kinds by name, for code that gets a kind's name at runtime (an API path)
KIND_TYPES: dict[str, Kind[Record | Action]] = {kind.name: kind for kind in ALL_KINDS}
KINDS = tuple(KIND_TYPES)
ID_BASE = {kind.name: kind.id_base for kind in ALL_KINDS}


def nics_in(state: "State", nid: int) -> list[tuple[Server, Nic]]:
    """Return (server, NIC) for every NIC in the cloud network with this ID."""
    return [
        (server, nic)
        for server in state[SERVERS].values()
        for nic in server["_lab"]["nics"]
        if nic["network"] == nid
    ]


def nic_addresses(nic: Nic) -> list[str]:
    """Return a NIC's addresses: its private IP and its alias IPs."""
    return [nic["ip"], *nic["alias_ips"]]


def client_by_name(state: "State", name: str) -> Client | None:
    """Return the Internet client with this name, or None."""
    return next((c for c in state[CLIENTS].values() if c["name"] == name), None)


def server_by_id(state: "State", sid: int | None) -> Server | None:
    """Return the server with this ID, or None (also without an ID)."""
    return state.get(SERVERS, sid)


def server_by_name(state: "State", name: str) -> Server | None:
    """Return the server with this name, or None."""
    return next((s for s in state[SERVERS].values() if s["name"] == name), None)


def primary_ip_field(ip_type: str) -> Literal["ipv4_id", "ipv6_id"]:
    """Return the field of a server that holds its primary IP of a type."""
    return "ipv4_id" if ip_type == "ipv4" else "ipv6_id"


def primary_ip_of(state: "State", server: Server, ip_type: str) -> PrimaryIP | None:
    """Return the server's primary IP of a type ("ipv4", "ipv6"), or None."""
    return state.get(PRIMARY_IPS, server.get(primary_ip_field(ip_type)))


def primary_address_of(state: "State", server: Server, ip_type: str) -> str | None:
    """Return the address of the server's primary IP of a type (IPv6: its /64)."""
    primary_ip = primary_ip_of(state, server, ip_type)
    return primary_ip["ip"] if primary_ip else None


def lab_records() -> dict:
    """Return the records that only the lab uses, empty (see the module docstring)."""
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
    """Return a snapshot of the state's data, for `restore()` to put back.

    Every dict and list keeps its identity. Code holds records (such as a server in an
    action's steps) and compares them by identity. So a restore must refill the same
    objects, not replace them with copies.
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
    """Write the saved state to its file, in a separate thread.

    `State.save()` hands it the serialized state. The writer writes the file
    atomically and fsyncs it. If several saves come in while it writes, only the newest
    is written next. The thread ends when nothing is left to write, and a new one
    starts with the next save.
    """

    def __init__(self, path: Path):
        """Write to `path`; nothing is waiting yet."""
        self.path = path
        self.condition = threading.Condition()
        self.pending: bytes | None = None  # the newest state not yet written
        self.running = False  # a thread writes, or is about to

    def put(self, data: bytes) -> None:
        """Have `data` written soon; it replaces an older state not yet written."""
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
        """Write the state to its file atomically, fsynced before the rename."""
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

    # Check at every `change()` and save that the state is still what the last block
    # committed, so a change outside a block raises AssertionError. It serializes the
    # whole state once more per block, so the unit tests turn it on (conftest.py) and
    # the daemon leaves it off.
    verify_changes = False

    def __init__(self, path: Path | None):
        """Load the state from `path` if it exists; without a path, nothing is saved."""
        self.path = path
        self.writer = Writer(path) if path else None
        # the thread of the event loop that owns the state (`own`); while it is None,
        # the first thread that uses the state becomes the owner
        self.owner: int | None = None
        # the tasks whose `change()` blocks are open, innermost last
        self.changing: list[asyncio.Task | None] = []
        # a nested block of the open transaction raised (`change`)
        self.nested_failed = False
        self._data: dict[str, Any] = {k: {} for k in KINDS}
        self._data["next_id"] = dict(ID_BASE)
        self._data.update(lab_records())
        self._data["version"] = STATE_VERSION
        if path and path.is_file():
            loaded = json.loads(path.read_text())
            # older shapes are brought up to date before anything reads them; a
            # newer file refuses to load (StateVersionError)
            migrate(loaded, path)
            # a kind or lab-only record newer than the file starts empty: adding
            # one needs no migration
            self._data.update(loaded)
            # kinds that are newer than the file start at their base ID
            self._data["next_id"] = {
                **ID_BASE,
                **loaded.get("next_id", {}),
            }
            for k in KINDS:  # JSON keys are strings
                self._data[k] = {int(i): v for i, v in self._data.get(k, {}).items()}
        # the state as the last `change()` block left it (`check_committed`)
        self.committed = self.serialized()

    # --- the owner ----------------------------------------------------------------

    def own(self) -> None:
        """Make the calling thread (the event loop's) the state's owner."""
        self.owner = threading.get_ident()

    def check_owner(self) -> None:
        """Raise AssertionError unless the caller runs on the owner's thread.

        It also raises while a `change()` block of another task is open. That block
        has awaited, and this task would see its change half done. It runs on every
        access, so it must stay cheap.
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
        """Return the whole state: resources by kind, `next_id`, lab-only records."""
        self.check_owner()
        return self._data

    def new_id(self, kind: Kind[object]) -> int:
        """Return the next free ID for a kind."""
        self.check_owner()
        self._data["next_id"][kind.name] += 1
        return self._data["next_id"][kind.name]

    def __getitem__[R](self, kind: Kind[R]) -> dict[int, R]:
        """Return the resources of a kind, by ID (`state[SERVERS]`)."""
        self.check_owner()
        records: dict[int, R] = self._data[kind.name]
        return records

    def get[R](self, kind: Kind[R], item_id: int | None) -> R | None:
        """Return the resource of a kind with this ID, or None (gone, or no ID)."""
        return self[kind].get(item_id) if item_id is not None else None

    def clear(self) -> None:
        """Remove every resource and the lab's records (a lab reset).

        IDs keep counting up (`next_id` stays), as on Hetzner, so no old ID matches a
        new resource. The Storage Box accounts created outside the API stay too.
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
        (`snapshot`). So a refused request leaves nothing behind, in memory or in the
        file. The block must not await. It is a plain `with`, and another task would
        see the change half done (`check_owner` refuses that).

        Blocks may nest. Only the outermost one takes a snapshot, undoes and saves: a
        nested block is part of its transaction. So an error in a nested block must
        reach the outermost one; caught in between, it would leave the nested block's
        change half done. The unit tests (`verify_changes`) fail such a transaction.
        """
        self.check_owner()
        outermost = not self.changing
        if outermost:
            self.check_committed()
            saved = snapshot(self._data)
            self.nested_failed = False
        self.changing.append(current_task())
        try:
            yield
        except BaseException:
            if outermost:
                restore(saved)
            else:
                self.nested_failed = True
            raise
        finally:
            self.changing.pop()
        if outermost:
            if self.nested_failed and self.verify_changes:
                raise AssertionError(
                    "a nested state.change() block failed, and its error was caught "
                    "inside the outer block: the nested change was not undone"
                )
            self.save()

    def save(self) -> None:
        """Commit the state and hand it to the writer thread; only `change()` calls it.

        The state is serialized here, on the loop, so the file gets the state as it is
        now. The writer's thread writes and fsyncs the file (none without a path).
        """
        self.check_owner()
        self.committed = self.serialized()
        if self.writer:
            self.writer.put(self.committed)

    def serialized(self) -> bytes:
        """Return the state as it is saved: JSON, keys sorted."""
        return json.dumps(self._data, indent=1, sort_keys=True).encode()

    def check_committed(self) -> None:
        """Raise AssertionError if the state changed since the last `change()` block.

        Such a change was made outside a block: it was neither undone on an error nor
        saved. It runs only with `verify_changes` (the unit tests), because it
        serializes the whole state.
        """
        self.check_owner()
        if not self.verify_changes:
            return
        now = self.serialized()
        if now != self.committed:
            # what changed, so the change can be found
            diff = difflib.unified_diff(
                self.committed.decode().splitlines(), now.decode().splitlines(), n=4
            )
            raise AssertionError(
                "the state was changed outside a `with state.change():` block:\n"
                + "\n".join(list(diff)[2:40])
            )

    def flush(self) -> None:
        """Wait until every save so far is on disk; any thread may call this."""
        if self.writer:
            self.writer.flush()

    def free_index(self) -> int:
        """Return the lab index of a new server (tap pub<index>, MACs, IPv6 link-local).

        Returns 0 when the lab is full.
        """
        used = {s["_lab"]["index"] for s in self[SERVERS].values()}
        return next((i for i in range(1, MAX_SERVERS + 1) if i not in used), 0)
