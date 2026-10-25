"""The lab's state: everything the fake API knows, persisted as JSON in the state volume."""

import json
import os
import threading
from pathlib import Path

# First ID per kind, so IDs in logs tell what they are (server 1001, network 2001, ...).
ID_BASE = {"servers": 1000, "networks": 2000, "ssh_keys": 3000, "firewalls": 4000, "floating_ips": 5000,
           "primary_ips": 6000, "actions": 9000, "volumes": 10000, "storage_boxes": 14000, "subaccounts": 15000,
           "box_snapshots": 16000, "clients": 0}
KINDS = tuple(ID_BASE)


class State:
    def __init__(self, path: Path | None):
        self.path = path
        self.lock = threading.RLock()
        self.data: dict = {k: {} for k in KINDS}
        self.data["next_id"] = dict(ID_BASE)
        if path and path.is_file():
            loaded = json.loads(path.read_text())
            self.data.update(loaded)
            self.data["next_id"] = {**ID_BASE, **loaded.get("next_id", {})}  # kinds added later
            for k in KINDS:  # JSON keys are strings
                self.data[k] = {int(i): v for i, v in self.data.get(k, {}).items()}

    def new_id(self, kind: str) -> int:
        with self.lock:
            self.data["next_id"][kind] += 1
            return self.data["next_id"][kind]

    def __getitem__(self, kind: str) -> dict:
        return self.data[kind]

    def save(self) -> None:
        if not self.path:
            return
        with self.lock:
            tmp = self.path.with_suffix(".tmp")
            for attempt in range(5):  # a handler in another thread may change a dict while it is written
                try:
                    text = json.dumps(self.data, indent=1, sort_keys=True)
                    break
                except RuntimeError:
                    if attempt == 4:
                        raise
            tmp.write_text(text)
            tmp.chmod(0o600)   # passwords of Storage Boxes are in it
            with open(tmp, "rb+") as f:   # on disk before it replaces the old state
                os.fsync(f.fileno())
            os.replace(tmp, self.path)

    def free_index(self) -> int:
        """Lab index of a new server (tap pub<index>, MAC, console port)."""
        with self.lock:
            used = {s["_lab"]["index"] for s in self.data["servers"].values()}
            return next((i for i in range(1, 240) if i not in used), 0)   # 0: the lab is full
