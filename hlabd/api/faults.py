"""The bookkeeping of faults and netem in the state.

Faults and netem are set through the lab's control API (api/control.py). They refer to
servers, clients and their links. Code that removes one of those calls the functions
here to drop its faults. The fabric puts the faults in place (fabric/faults.py).

A fault points at a server by ID and at a client by name (`TargetRef` in records.py).
So a renamed server keeps its faults, and a new server with an old name does not
inherit them. The API shows a reference as s:<id> or c:<name> (`fault_json`).
"""

from typing import cast

from ..lab import Lab
from ..records import Fault, TargetRef

# the field of a fault → the field with the reference to what it points at
REF_FIELDS = {"first": "first_ref", "second": "second_ref", "vm": "target_ref"}


def ref_text(ref: TargetRef) -> str:
    """Return a reference as the API shows it: s:<id> or c:<name>."""
    if "server" in ref:
        return f"s:{ref['server']}"
    return f"c:{ref['client']}"


def fault_json(fault: Fault) -> dict:
    """Return a fault as the API shows it: its fields, references as text."""
    shown: dict[str, object] = dict(fault)
    for ref_field in REF_FIELDS.values():
        if ref_field in shown:
            # the reference fields hold a TargetRef in every fault type
            shown[ref_field] = ref_text(cast(TargetRef, shown[ref_field]))
    return shown


def drop_netem(lab: Lab, links: list[str]) -> None:
    """Drop the netem faults of links that are gone (a new link may reuse the name)."""
    for link in links:
        lab.state.netem.pop(link, None)


def drop_faults(lab: Lab, target: TargetRef, links: list[str]) -> None:
    """Drop the faults of a deleted server or client, and the netem of its links.

    A new server or client may get the same links. system.apply_faults() then updates
    the fault sets.
    """
    faults = lab.state.faults
    lab.state.faults = [
        fault
        for fault in faults
        if target not in [fault.get(ref_field) for ref_field in REF_FIELDS.values()]
    ]
    drop_netem(lab, links)
