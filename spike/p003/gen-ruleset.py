"""Generate a spike nftables ruleset like the data plane would: one table, replaced
atomically, with per-server firewall chains (Hetzner Cloud Firewall style)."""
import sys

servers = int(sys.argv[1])
firewalls = int(sys.argv[2])
lines = ["table inet hlab", "delete table inet hlab", "table inet hlab {"]
for fw in range(firewalls):
    lines.append(f"  chain fw{fw}_in {{")
    lines.append("    ct state established,related accept")
    lines.append("    icmp type echo-request accept")
    lines.append("    meta l4proto ipv6-icmp accept")
    for port in (22, 80, 443, 8000 + fw):
        lines.append(f"    tcp dport {port} ip saddr {{ 0.0.0.0/0 }} accept")
    lines.append("    udp dport 53 accept")
    lines.append("    counter drop")
    lines.append("  }")
lines.append("  chain forward {")
lines.append("    type filter hook forward priority filter; policy accept;")
# Guests may open connections to the host only on the API ports; replies pass.
lines.append('    iifname "eth1" ip daddr 198.18.0.1 ct state new tcp dport != { 8080, 8081 } counter drop')
for n in range(servers):
    address = f"198.18.16.{10 + n}"
    if n < firewalls * 2:
        lines.append(f"    ip daddr {address} counter jump fw{n % firewalls}_in")
    else:
        lines.append(f"    ip daddr {address} counter accept")
lines.append("  }")
lines.append("}")
print("\n".join(lines))
