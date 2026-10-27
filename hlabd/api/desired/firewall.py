"""The rules of the firewalls, per server."""

from ...fabric.firewall import Rule, ServerRules
from ...labels import firewall_servers
from ...records import (
    FirewallRule,
)
from ...state import (
    FIREWALLS,
    SERVERS,
    State,
)


def firewall_rule(rule: FirewallRule) -> Rule:
    """Return a firewall rule as the fabric builds it into nft rules."""
    return Rule(
        rule["direction"],
        rule["protocol"],
        rule["port"],
        tuple(rule["source_ips"]),
        tuple(rule["destination_ips"]),
    )


def firewall_rules(state: State) -> list[ServerRules]:
    """Return the rules of every server that a firewall applies to, by server ID.

    A server's rules are the rules of all its firewalls.
    """
    out = []
    for sid, server in sorted(state[SERVERS].items()):
        firewalls = [
            f for f in state[FIREWALLS].values() if sid in firewall_servers(state, f)
        ]
        if firewalls:
            rules = [firewall_rule(r) for f in firewalls for r in f["rules"]]
            out.append(ServerRules(sid, server["_lab"]["tap"], rules))
    return out
