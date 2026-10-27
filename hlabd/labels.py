"""Hetzner label selectors, and the servers a firewall applies to through them.

Used by the API (and by api/desired/ for the firewall rules).
"""

from .records import Firewall, Server
from .state import SERVERS, State


def firewall_servers(state: State, firewall: Firewall) -> set[int]:
    """Return the servers a firewall applies to, directly or by label selector."""
    out = set()
    for entry in firewall["applied_to"]:
        if entry["type"] == "server":
            out.add(entry["server"]["id"])
        elif entry["type"] == "label_selector":
            selector = entry["label_selector"]["selector"]
            out |= {s["id"] for s in selector_servers(state, selector)}
    return out


def selector_servers(state: State, selector: str) -> list[Server]:
    """Return the servers whose labels match a label selector, in the state's order."""
    return [s for s in state[SERVERS].values() if match_labels(s["labels"], selector)]


def match_labels(labels: dict, selector: str) -> bool:
    """Check whether labels match a Hetzner label selector.

    Terms: k=v, k==v, k!=v, k, !k, k in (a,b), k notin (a,b); a comma means AND.
    """
    if not selector:
        return True
    terms, depth, current = [], 0, ""
    for ch in selector:  # split on commas outside parentheses
        depth += ch == "("
        depth -= ch == ")"
        if ch == "," and depth == 0:
            terms.append(current)
            current = ""
        else:
            current += ch
    terms.append(current)
    for t in (t.strip() for t in terms):
        if " notin " in t:
            key, values = t.split(" notin ", 1)
            if labels.get(key.strip()) in [
                v.strip() for v in values.strip("() ").split(",")
            ]:
                return False
        elif " in " in t:
            key, values = t.split(" in ", 1)
            if labels.get(key.strip()) not in [
                v.strip() for v in values.strip("() ").split(",")
            ]:
                return False
        elif "!=" in t:
            key, value = t.split("!=", 1)
            if labels.get(key.strip()) == value.strip():
                return False
        elif "=" in t:
            key, value = t.replace("==", "=").split("=", 1)
            if labels.get(key.strip()) != value.strip():
                return False
        elif t.startswith("!"):
            if t[1:].strip() in labels:
                return False
        elif t not in labels:
            return False
    return True
