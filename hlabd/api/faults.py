"""The bookkeeping of faults and netem in the state.

Faults and netem are set through the lab's control API (api/control.py). They refer to
servers, clients and their links. Code that removes one of those calls the functions
here to drop its faults. The fabric puts the faults in place (fabric/faults.py).
"""

from ..lab import Lab

# the field of a fault → the field with the reference to what it points at
REF_FIELDS = {"first": "first_ref", "second": "second_ref", "vm": "target_ref"}


def drop_netem(lab: Lab, links: list[str]) -> None:
    """Drop the netem faults of links that are gone (a new link may reuse the name)."""
    for link in links:
        lab.state.data["netem"].pop(link, None)


def drop_faults(lab: Lab, ref: str, links: list[str]) -> None:
    """Drop the faults of a deleted server or client, and the netem of its links.

    A new server or client may get the same links. system.apply_faults() then updates
    the fault sets.
    """
    lab.state.data["faults"] = [
        fault
        for fault in lab.state.data["faults"]
        if ref not in [fault.get(ref_field) for ref_field in REF_FIELDS.values()]
    ]
    drop_netem(lab, links)
