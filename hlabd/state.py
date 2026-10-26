"""The lab's state: everything the fake API knows.

It is saved as JSON in the state volume. The resources are kept by kind (KINDS) and ID.
Their fields, the lab's own included (a server's `_lab`, its NICs), are the TypedDicts
in records.py. Next to the resources and `next_id`, the state keeps the lab's own
records:

- `faults`: the faults set through /_lab/faults (cuts, isolation, blocks), see
  fabric/faults.py
- `netem`: netem options per tap or client link (/_lab/netem)
- `net_routes`: the routes of cloud networks the fabric has put in place (to remove them
  again)
- `box_users`, `box_uids`: the lab's own Storage Box accounts (/_lab/box/users) with
  their keys and fixed UIDs

Every change to the state (any dict or list reachable from `data`) happens under `lock`,
and so does every loop over it. The lock is re-entrant. The API's worker threads and the
event loop share the state, and `save()` writes it under the lock. Slow work stays
outside the lock (lab.py has the rule). The lookups below take the lock themselves.
"""

import json
import os
import threading
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


class State:
    """The lab's state: kept in memory, saved as JSON."""

    def __init__(self, path: Path | None):
        """Load the state from `path` if it exists; without a path, nothing is saved."""
        self.path = path
        self.lock = threading.RLock()
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
        with self.lock:
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

        IDs go on counting (`next_id` stays), as in the cloud, so nothing old matches a
        new one; the lab's own Storage Box accounts stay too.
        """
        with self.lock:
            kept = {key: self.data[key] for key in ("box_users", "box_uids")}
            self.data.update({kind: {} for kind in KINDS})
            self.data.update(lab_records())
            self.data.update(kept)
            self.save()

    def save(self) -> None:
        """Write the state to its file atomically (no-op without a path)."""
        if not self.path:
            return
        with self.lock:
            tmp = self.path.with_suffix(".tmp")
            # every change happens under the lock: nothing changes while it is written
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
