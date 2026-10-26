"""Cloud firewalls as nftables rules, with Hetzner's semantics.

- They filter only the public interface, not cloud networks. In the lab that is a
  server's public tap, so Floating IPs routed to it are filtered too.
- Stateful: answers to allowed connections pass.
- Inbound: once a firewall applies, everything not allowed by a rule is dropped (also
  without any rules).
- Outbound: everything is allowed until a firewall of the server has an outbound rule;
  then only what rules allow.
- Several firewalls on a server: their rules add up.
- Protocols tcp, udp, icmp, esp, gre (gre IPv4 only); anything else is dropped inbound.
- Always allowed: ARP (not IP), DHCP, the ICMPv6 types of neighbour discovery, the
  metadata service.

The lab's admin access (`hlab ssh` from 198.18.0.2 / 2001:db8:fffe::3) always passes,
like Hetzner's console.

The API has already checked the rules (api/firewalls.py) and collected the rules of
each server (api/desired.py). This module is a part of the fabric (see __init__.py).
"""

import ipaddress
import threading
from dataclasses import dataclass
from pathlib import Path

from .. import config
from .shell import Shell


@dataclass(frozen=True)
class Rule:
    """A firewall rule as the API checked it (Hetzner's fields, no description)."""

    direction: str  # in, out
    protocol: str  # tcp, udp, icmp, esp, gre
    port: str | None  # a port, a range like 80-85 or any; tcp and udp only
    source_ips: tuple[str, ...]  # inbound rules
    destination_ips: tuple[str, ...]  # outbound rules


@dataclass
class ServerRules:
    """The rules of all firewalls that apply to one server."""

    sid: int
    tap: str  # its public tap
    rules: list[Rule]  # maybe empty: then everything inbound is dropped


def nft_port(port: str | None) -> str:
    """Return a Hetzner port (a port, a range or "any") in nft syntax."""
    return "1-65535" if port == "any" else str(port)


def _rule_lines(rule: Rule, verdict: str) -> list[str]:
    """Turn one Hetzner rule into nft rules (one per address family present)."""
    addrs = rule.source_ips if rule.direction == "in" else rule.destination_ips
    side = "saddr" if rule.direction == "in" else "daddr"
    networks = [ipaddress.ip_network(a) for a in addrs]
    lines = []
    for fam, ver, any_net in (("ip", 4, "0.0.0.0/0"), ("ip6", 6, "::/0")):
        fam_nets = [n for n in networks if n.version == ver]
        if not fam_nets:
            continue
        if rule.protocol == "gre" and ver == 6:
            continue  # GRE over IPv6 is dropped by Hetzner's firewalls
        match = (
            f"meta nfproto {'ipv4' if ver == 4 else 'ipv6'}"
            if any(str(n) == any_net for n in fam_nets)
            else f"{fam} {side} {{ {', '.join(str(n) for n in fam_nets)} }}"
        )
        proto = rule.protocol
        if proto in ("tcp", "udp"):
            layer4 = f"{proto} dport {nft_port(rule.port)}"
        elif proto == "icmp":
            layer4 = "meta l4proto icmp" if ver == 4 else "meta l4proto ipv6-icmp"
        else:
            layer4 = f"meta l4proto {proto}"
        lines.append(f"{match} {layer4} {verdict}")
    return lines


def chain(name: str, lines: list[str]) -> str:
    """Return an nft chain of the table with these lines, indented in the table."""
    return f"    chain {name} {{\n" + "".join(f"        {x}\n" for x in lines) + "    }"


def ruleset(servers: list[ServerRules]) -> str:
    """Return the complete table inet hlabfw for the servers that have firewalls."""
    chains, forward, input_rules, output = [], [], [], []
    for server in servers:
        sid, tap, rules = server.sid, server.tap, server.rules
        inbound = [r for r in rules if r.direction == "in"]
        outbound = [r for r in rules if r.direction == "out"]
        body = [f"ip saddr {config.ADMIN} accept", f"ip6 saddr {config.ADMIN6} accept"]
        for r in inbound:
            body += _rule_lines(r, "accept")
        chains.append(chain(f"in_{sid}", [*body, "drop"]))
        forward.append(f'oifname "{tap}" jump in_{sid}')
        output.append(f'oifname "{tap}" jump in_{sid}')
        # outbound rules allow only what they list; `return` lets the receiver's
        # inbound rules decide next
        if outbound:
            body = [
                f"ip daddr {config.ADMIN} return",
                f"ip6 daddr {config.ADMIN6} return",
            ]
            for r in outbound:
                body += _rule_lines(r, "return")
            chains.append(chain(f"out_{sid}", [*body, "drop"]))
            forward.insert(0, f'iifname "{tap}" jump out_{sid}')
            input_rules.append(f'iifname "{tap}" jump out_{sid}')
    always = [
        "ct state established,related accept",
        "ct state invalid drop",
        "icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-solicit, "
        "nd-router-advert, nd-redirect } accept",
    ]
    # DHCP and the metadata service pass only between a server and the lab itself
    # (never server to server); metadata over IPv6 arrives DNATed from fe80::a9fe:a9fe
    to_lab = [
        "udp sport 68 udp dport 67 accept",
        f"ip daddr {config.METADATA} tcp dport 80 accept",
        f"ct status dnat ip6 daddr {config.METADATA6} tcp dport 80 accept",
    ]
    from_lab = ["udp sport 67 udp dport 68 accept"]

    def base(name: str, hook: str, rules: list[str], extra: list[str]) -> str:
        lines = [f"type filter hook {hook} priority 10; policy accept;"] + (
            always + extra + rules if rules else []
        )
        return chain(name, lines)

    parts = [
        base("forward", "forward", forward, []),
        base("input", "input", input_rules, to_lab),
        base("output", "output", output, from_lab),
    ] + chains
    return (
        "table inet hlabfw\ndelete table inet hlabfw\ntable inet hlabfw {\n"
        + "\n".join(parts)
        + "\n}\n"
    )


class Firewall:
    """The firewall table inet hlabfw, built by `ruleset`."""

    def __init__(self, root: Path, shell: Shell, net_lock: threading.RLock):
        """Store what the table uses; `net_lock` is the fabric's lock (see Fabric)."""
        self.root = root
        self.shell = shell
        self.net_lock = net_lock

    def apply(self, servers: list[ServerRules]) -> None:
        """Replace the firewall table (inet hlabfw) atomically.

        The new table holds the rules of all firewalls, per server (sorted by ID).
        """
        with self.net_lock:
            path = self.root / "firewall.nft"
            path.write_text(ruleset(servers))
            self.shell.run("nft", "-f", str(path))
