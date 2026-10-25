"""Hetzner label selectors, and the servers a firewall applies to through them. Used by the API and by the
fabric (firewall rules)."""


def firewall_servers(f: dict, state) -> set[int]:
    """Servers a firewall applies to, directly or by label selector."""
    out = set()
    for a in f["applied_to"]:
        if a["type"] == "server":
            out.add(a["server"]["id"])
        elif a["type"] == "label_selector":
            out |= {s["id"] for s in state["servers"].values() if match_labels(s["labels"], a["label_selector"]["selector"])}
    return out


def match_labels(labels: dict, selector: str) -> bool:
    """Hetzner label selectors: k=v, k==v, k!=v, k, !k, k in (a,b), k notin (a,b); comma means AND."""
    if not selector:
        return True
    terms, depth, cur = [], 0, ""
    for ch in selector:  # split on commas outside parentheses
        depth += ch == "("
        depth -= ch == ")"
        if ch == "," and depth == 0:
            terms.append(cur)
            cur = ""
        else:
            cur += ch
    terms.append(cur)
    for t in (t.strip() for t in terms):
        if " notin " in t:
            k, vs = t.split(" notin ", 1)
            if labels.get(k.strip()) in [v.strip() for v in vs.strip("() ").split(",")]:
                return False
        elif " in " in t:
            k, vs = t.split(" in ", 1)
            if labels.get(k.strip()) not in [v.strip() for v in vs.strip("() ").split(",")]:
                return False
        elif "!=" in t:
            k, v = t.split("!=", 1)
            if labels.get(k.strip()) == v.strip():
                return False
        elif "=" in t:
            k, v = t.replace("==", "=").split("=", 1)
            if labels.get(k.strip()) != v.strip():
                return False
        elif t.startswith("!"):
            if t[1:].strip() in labels:
                return False
        elif t not in labels:
            return False
    return True
