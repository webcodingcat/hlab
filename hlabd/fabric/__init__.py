"""How things happen in the lab: VMs, taps, routes, DHCP, latency and faults (here), firewall rules
(firewall.py) and the Storage Box (storagebox.py), all through scripts/hlab.sh. The API decides, the fabric carries it
out and knows nothing of HTTP. A fake shell replaces hlab.sh in the tests."""

import ipaddress
import json
import logging
import secrets
import socket
import subprocess
import threading
import time
from pathlib import Path

from .. import config as C
from .firewall import ruleset
from .storagebox import Box

HLAB_SH = "/opt/hlab/scripts/hlab.sh"
log = logging.getLogger("hlabd.vm")


class Shell:
    def run(self, *args: str, check: bool = True, input: str | None = None) -> str:
        p = subprocess.run([HLAB_SH, *args], capture_output=True, text=True, input=input)
        if check and p.returncode != 0:
            raise RuntimeError(f"hlab.sh {' '.join(args)}: {p.stderr.strip() or p.returncode}")
        return p.stdout

    def ok(self, *args: str) -> bool:
        return subprocess.run([HLAB_SH, *args], capture_output=True).returncode == 0

    def qga(self, sock: Path, command: dict, timeout: float = 10) -> dict | None:
        """One command to a QEMU guest agent. Its channel buffers answers a client left behind, so guest-sync with
        a fresh ID first discards them; None when the agent does not answer (within the timeout in all)."""
        token = secrets.randbelow(2**31)
        deadline = time.monotonic() + timeout
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as c:
                c.settimeout(timeout)
                c.connect(str(sock))
                f = c.makefile("rwb")

                def send(cmd: dict) -> None:
                    f.write(json.dumps(cmd).encode() + b"\n")
                    f.flush()

                def line() -> bytes:   # a guest that keeps talking does not hold the lab up
                    left = deadline - time.monotonic()
                    if left <= 0:
                        raise TimeoutError
                    c.settimeout(left)
                    return f.readline(1 << 20)

                send({"execute": "guest-sync", "arguments": {"id": token}})
                for _ in range(1000):
                    got = line()
                    if not got:
                        return None
                    if json.loads(got) == {"return": token}:
                        break
                else:
                    return None
                send(command)
                return json.loads(line())
        except (OSError, ValueError):
            return None


def public_mac(index: int) -> str:
    return f"96:00:00:00:00:{index:02x}"


def link_local(index: int) -> str:
    """The IPv6 link-local address a guest derives from its public MAC (EUI-64), the only one its tap lets in."""
    return f"fe80::9400:ff:fe00:{index:x}"


def private_mac(index: int, n: int) -> str:
    return f"86:00:00:00:{index:02x}:{n:02x}"


def network_gateway(ip_range: str) -> str:
    return str(next(ipaddress.ip_network(ip_range).hosts()))


def route_table(network_id: int) -> str:
    """The routing table of a cloud network: its ID (networks start at 2001, clear of the kernel's tables)."""
    return str(network_id)


def prefix(ip: str) -> str:
    """An address as routed prefix: IPv4 → /32, IPv6 networks stay as they are."""
    return ip if "/" in ip else f"{ip}/32" if ":" not in ip else f"{ip}/128"


class Fabric:
    def __init__(self, cfg: dict, state, root: Path, shell: Shell | None = None):
        self.cfg = cfg
        self.state = state
        self.root = root
        self.sh = shell or Shell()
        self.box = Box(self.sh)   # the Storage Box
        self.lab_key = root / "id_ed25519"
        # tc and nft changes of concurrent actions (two servers starting) must not interleave
        self.net_lock = threading.RLock()
        self._local: tuple[float, set[str]] | None = None

    # --- VMs -------------------------------------------------------------------------------------------------

    def vm_dir(self, server: dict) -> Path:
        return self.root / "vm" / str(server["id"])

    def create_vm(self, server: dict, base: str) -> None:
        """The disk of a new server: an overlay on a system image. Everything else comes from the metadata service
        at boot, as on Hetzner (cloud-init's Hetzner datasource)."""
        d = self.vm_dir(server)
        d.mkdir(parents=True, exist_ok=True)
        # the server's disk size: a type change without upgrade_disk kept the smaller one
        size = server["_lab"].get("disk_gb") or self.cfg["server_types"][server["server_type"]]["disk_gb"]
        self.sh.run("vm-disk", str(d), base, str(size))
        server["_lab"]["disk_gb"] = size

    def start_vm(self, server: dict) -> None:
        lab = server["_lab"]
        d = self.vm_dir(server)
        if self.sh.ok("vm-alive", str(d)):
            return
        self.sh.run("public-tap-up", lab["tap"], *([self.ipv4(server)] if self.ipv4(server) else []))
        nics = [f"{lab['tap']}={public_mac(lab['index'])}"]
        for nic in lab["nics"]:
            self._private_tap_up(nic)
            nic.pop("port", None)  # at boot every NIC sits on the root bus; the hot-plug ports are free again
            nics.append(f"{nic['tap']}={nic['mac']}")
        v6 = self.ipv6_net(server)
        if v6:   # the /64 goes to the guest's link-local address, as Hetzner routes it
            self.sh.run("route6", v6, lab["tap"], link_local(lab["index"]))
        self.write_dhcp()
        vols = [v for v in list(self.state["volumes"].values())   # only those whose disk exists
                if v.get("server") == server["id"] and v["status"] == "available"]
        (d / "volumes").write_text("".join(f"{v['id']} {self.root / 'volumes' / (str(v['id']) + '.raw')}\n"
                                           for v in vols))
        stype = self.cfg["server_types"][server["server_type"]]
        ram = max(512, int(stype["memory_gb"] * 1024 * self.cfg["memory_scale"]))
        log.info("VM %s (server %s) starts: %s MB, %s cores", server["name"], server["id"], ram, stype["cores"])
        self.sh.run("vm-start", str(d), server["name"], str(ram), str(stype["cores"]), str(server["id"]), *nics)
        self.route_floating_ips(server)
        self.apply_faults()
        self.apply_latency()
        self.apply_routes()

    def ipv4(self, server: dict) -> str | None:
        p = self.state["primary_ips"].get(server.get("ipv4_id"))
        return p["ip"] if p else None

    def ipv6_net(self, server: dict) -> str | None:
        p = self.state["primary_ips"].get(server.get("ipv6_id"))
        return p["ip"] if p else None

    def kill_vm(self, server: dict) -> None:
        log.info("VM %s (server %s) stops", server["name"], server["id"])
        lab = server["_lab"]
        self.sh.run("vm-kill", str(self.vm_dir(server)))
        self.sh.run("tap-down", lab["tap"])
        for nic in lab["nics"]:
            self.sh.run("tap-down", nic["tap"])
        self.write_dhcp()   # the taps' dnsmasq instances go too

    def vm_alive(self, server: dict) -> bool:
        return self.sh.ok("vm-alive", str(self.vm_dir(server)))

    def delete_vm(self, server: dict) -> None:
        self.kill_vm(server)
        d = self.vm_dir(server)
        if d.is_dir():
            for f in d.iterdir():
                f.unlink()
            d.rmdir()

    def qmp(self, server: dict, command: str) -> None:
        self.sh.run("qmp", str(self.vm_dir(server)), f'{{"execute":"{command}"}}')

    def hmp(self, server: dict, command: str) -> None:
        self.sh.run("hmp", str(self.vm_dir(server)), command)

    def qga(self, server: dict, command: dict) -> dict | None:
        """A command to the QEMU guest agent; None when no agent answers."""
        if not self.sh.ok("vm-alive", str(self.vm_dir(server))):
            return None
        return self.sh.qga(self.vm_dir(server) / "qga.sock", command)

    # --- cloud networks ----------------------------------------------------------------------------------------

    def _private_tap_up(self, nic: dict) -> None:
        net = self.state["networks"][nic["network"]]
        rng = ipaddress.ip_network(net["ip_range"])
        self.sh.run("private-tap-up", nic["tap"], f"{network_gateway(net['ip_range'])}/{rng.prefixlen}", nic["ip"],
                    route_table(nic["network"]), str(rng), *(nic.get("alias_ips") or []))

    def local_addresses(self, fresh: bool = False) -> set[str]:
        """The IPv4 addresses the lab container itself has (never handed to a server); read at most every 10 s
        (fresh: now)."""
        now = time.monotonic()
        if fresh or self._local is None or now - self._local[0] > 10:
            out = self.sh.run("local-addrs", check=False)
            self._local = (now, {a for a in out.split() if a})
        return self._local[1]

    def refresh_network(self, nid: int) -> None:
        """A network's range changed: its table and the gateway address on its members' taps are set anew."""
        with self.net_lock:
            self.sh.run("route-flush", route_table(nid))
            for s in list(self.state["servers"].values()):
                if self.vm_alive(s):
                    for nic in list(s["_lab"]["nics"]):
                        if nic["network"] == nid:
                            self._private_tap_up(nic)

    def attach_nic(self, server: dict, nic: dict) -> None:
        if self.vm_alive(server):
            self._private_tap_up(nic)
            used = {n.get("port") for n in server["_lab"]["nics"]}
            nic["port"] = next((p for p in ("hp1", "hp2", "hp3", "hp4") if p not in used), None)
            if nic["port"] is None:
                raise RuntimeError("no free hot-plug port (the lab hot-plugs 4 NICs per boot)")
            self.write_dhcp()  # the tap exists now: its dnsmasq binds to it
            self.sh.run("nic-add", str(self.vm_dir(server)), nic["tap"], nic["tap"], nic["mac"], nic["port"])
        else:
            self.write_dhcp()
        self.apply_faults()
        self.apply_routes()   # a gateway server attached now gets the network's routes
        self.apply_latency()

    def detach_nic(self, server: dict, nic: dict) -> None:
        if self.vm_alive(server):
            self.sh.run("nic-del", str(self.vm_dir(server)), nic["tap"], check=False)
        self.sh.run("tap-down", nic["tap"])
        self.state.data.get("netem", {}).pop(nic["tap"], None)   # the next NIC gets the tap name again

    def write_dhcp(self) -> None:
        with self.net_lock:  # servers starting side by side must not restart dnsmasq concurrently
            self._write_dhcp()

    def _write_dhcp(self) -> None:
        """DHCP like Hetzner's: a fixed address per NIC, a /32 with routes – on public interfaces to 172.31.1.1, on
        cloud networks to the network's gateway and range. One dnsmasq per tap (only taps that exist: dnsmasq binds
        to its interface at start); only instances whose configuration changed are restarted."""
        d = self.root / "dhcp"
        d.mkdir(exist_ok=True)
        confs: dict[str, str] = {}
        for s in list(self.state["servers"].values()):
            lab = s["_lab"]
            if self.ipv4(s):
                # the primary IPv4 as /32, on-link gateway 172.31.1.1, the lab's resolver
                confs[lab["tap"]] = self._dhcp_conf(lab["tap"], public_mac(lab["index"]), self.ipv4(s), [
                    f"dhcp-range={C.PUBLIC_NET}.0,static,255.255.255.0,infinite",
                    "dhcp-option=option:netmask,255.255.255.255",
                    f"dhcp-option=option:router,{C.GATEWAY}",
                    f"dhcp-option=121,{C.GATEWAY}/32,0.0.0.0,0.0.0.0/0,{C.GATEWAY}",
                    f"dhcp-option=option:dns-server,{C.SERVICES}"])
            for nic in list(lab["nics"]):
                net = self.state["networks"].get(nic["network"])
                if not net:   # its network went meanwhile
                    continue
                rng = ipaddress.ip_network(net["ip_range"])
                gw = network_gateway(net["ip_range"])
                # the network's routes go to its gateway too; the lab forwards them to the route's server
                extra = "".join(f",{r['destination']},{gw}" for r in net["routes"])
                confs[nic["tap"]] = self._dhcp_conf(nic["tap"], nic["mac"], nic["ip"], [
                    f"dhcp-range={rng.network_address},static,{rng.netmask},infinite",
                    "dhcp-option=option:netmask,255.255.255.255",
                    "dhcp-option=option:router",
                    f"dhcp-option=121,{gw}/32,0.0.0.0,{rng},{gw}{extra}"])
        confs = {tap: c for tap, c in confs.items() if self.sh.ok("link-exists", tap)}
        for f in d.glob("*.conf"):
            if f.stem not in confs:
                self.sh.run("dhcp-stop", str(f))
                f.unlink()
        for tap, conf in confs.items():
            f = d / f"{tap}.conf"
            if not f.is_file() or f.read_text() != conf:
                f.write_text(conf)
                self.sh.run("dhcp-restart", str(f))

    def _dhcp_conf(self, tap: str, mac: str, ip: str, options: list[str]) -> str:
        return "\n".join(["port=0", "bind-dynamic", f"interface={tap}", "dhcp-authoritative", "leasefile-ro",
                          f"pid-file={self.root / 'dhcp' / (tap + '.pid')}", f"dhcp-host={mac},{ip}",
                          *options]) + "\n"

    def apply_routes(self) -> None:
        """Routes of cloud networks: the destination goes to the tap of the server that holds the gateway IP, in
        the network's routing table."""
        with self.net_lock:
            for dest, table in (r for r in self.state.data.get("net_routes", []) if isinstance(r, list)):
                self.sh.run("route-del", dest, table, check=False)
            applied = []
            for net in list(self.state["networks"].values()):
                for r in net["routes"]:
                    nic = next((n for s in list(self.state["servers"].values()) for n in s["_lab"]["nics"]
                                if n["network"] == net["id"] and r["gateway"] in [n["ip"], *(n.get("alias_ips") or [])]
                                and self.vm_alive(s)), None)
                    if nic:
                        table = route_table(net["id"])
                        self.sh.run("route-via", r["destination"], r["gateway"], nic["tap"], table, check=False)
                        applied.append([r["destination"], table])
            self.state.data["net_routes"] = applied

    def write_dns(self) -> None:
        """The lab resolver: its own names and the hlab.yaml records."""
        lines = [f"address=/{n}/{a}" for a in (C.SERVICES, C.SERVICES6)
                 for n in ("api.hlab", "box.hlab", "your-storagebox.de")]
        lines += [f"address=/hetzner-api.hlab/{a}" for a in (C.HETZNER_API, C.HETZNER_API6)]
        lines += [f"address=/{name}/{ip}" for name, ip in (self.cfg.get("dns") or {}).items()]
        with self.net_lock:
            (self.root / "dns.conf").write_text("\n".join(lines) + "\n")
            self.sh.run("dns-start", str(self.root / "dns.conf"))

    # --- Floating IPs ------------------------------------------------------------------------------------------

    def _route_to(self, ip: str, server: dict) -> None:
        """An IPv4 (/32) to the server's tap; an IPv6 /64 to its link-local address there."""
        lab = server["_lab"]
        if ":" in ip:
            self.sh.run("route6", prefix(ip), lab["tap"], link_local(lab["index"]))
        else:
            self.sh.run("route", prefix(ip), lab["tap"])

    def route_floating_ips(self, server: dict) -> None:
        """Hetzner keeps routing an assigned IP across a power cycle; the route went with the old tap."""
        with self.net_lock:
            for f in list(self.state["floating_ips"].values()):
                if f.get("server") == server["id"]:
                    self._route_to(f["ip"], server)

    def route_floating_ip(self, ip: str, server: dict | None) -> None:
        """Assigned to a running server, the IP goes to it; to a server that is off (or to none), it goes nowhere
        – the old server does not keep it."""
        with self.net_lock:
            if server is not None and self.vm_alive(server):
                self._route_to(ip, server)
            else:
                self.sh.run("route", prefix(ip))

    def route_floating(self, f: dict) -> None:
        """A Floating IP routed as the state says now: of two moves in a row, the later one wins."""
        with self.net_lock:
            self.route_floating_ip(f["ip"], self.state["servers"].get(f.get("server")) if f.get("server") else None)

    def trim_console(self, server: dict, limit: int = 8 << 20) -> None:
        """A console log above the limit keeps its last MiB (QEMU appends, so it writes on at the new end)."""
        p = self.vm_dir(server) / "console.log"
        try:
            if p.stat().st_size > limit:
                with p.open("r+b") as f:
                    f.seek(-(1 << 20), 2)
                    tail = f.read()
                    f.seek(0)
                    f.truncate()
                    f.write(tail)
        except OSError:
            pass

    # --- faults ------------------------------------------------------------------------------------------------

    def fault_ref(self, name: str) -> str:
        """What a fault points at, by ID (s:<id>) or client name (c:<name>): a renamed server keeps its faults, a
        new server under an old name does not inherit them."""
        for s in self.state["servers"].values():
            if s["name"] == name:
                return f"s:{s['id']}"
        for c in self.state["clients"].values():
            if c["name"] == name:
                return f"c:{name}"
        raise KeyError(f"no server or client named {name!r}")

    def server_ifaces(self, ref: str, which: str = "all") -> list[str]:
        """Interfaces of a server ('public', 'private', 'all') or of a client, by reference (fault_ref) or name."""
        if not ref.startswith(("s:", "c:")):
            ref = self.fault_ref(ref)
        kind, key = ref.split(":", 1)
        if kind == "s":
            s = self.state["servers"].get(int(key))
            if s:
                pub = [s["_lab"]["tap"]]
                prv = [n["tap"] for n in s["_lab"]["nics"]]
                return {"public": pub, "private": prv, "all": pub + prv}[which]
        for c in self.state["clients"].values():
            if kind == "c" and c["name"] == key:
                return [c["veth"]] if which in ("public", "all") else []
        raise KeyError(f"{ref} is gone")

    def apply_faults(self) -> None:
        """Re-create the nftables set elements from the fault list (interfaces come and go with power cycles)."""
        with self.net_lock:
            self._apply_faults()

    def _apply_faults(self) -> None:
        """All sets in one nft transaction: no moment without anti-spoofing sources (which would drop every packet)
        or with faults lifted."""
        faults = self.state.data.setdefault("faults", [])
        sets: dict[str, list[str]] = {s: [] for s in ("cuts", "isolated", "blocked_svc", "blocked_inet",
                                                       "private_pairs", "src4", "src6")}
        servers = list(self.state["servers"].values())
        members: dict[int, list[str]] = {}  # cloud network → private taps of its servers
        for srv in servers:
            for nic in srv["_lab"]["nics"]:
                members.setdefault(nic["network"], []).append(nic["tap"])
        sets["private_pairs"] = [f'"{a}" . "{b}"' for taps in members.values() for a in taps for b in taps if a != b]
        # anti-spoofing: the sources each public tap and client may send from
        for srv in servers:
            tap = srv["_lab"]["tap"]
            sets["src6"].append(f'"{tap}" . {link_local(srv["_lab"]["index"])}/128')   # its own link-local only
            for ip in self._server_ips(srv, None):
                if ":" in ip:
                    sets["src6"].append(f'"{tap}" . {ip}')
                else:
                    sets["src4"].append(f'"{tap}" . {ip.split("/")[0]}')
        for c in list(self.state["clients"].values()):
            sets["src4"].append(f'"{c["veth"]}" . {c["ip"]}')
            sets["src6"].append(f'"{c["veth"]}" . {c["ip6"]}/128')
        for flt in faults:
            try:
                if flt["type"] == "cut":
                    a = self.server_ifaces(flt.get("a_ref") or flt["a"], flt["net"])
                    b = self.server_ifaces(flt.get("b_ref") or flt["b"], flt["net"])
                    sets["cuts"] += [f'"{x}" . "{y}"' for x in a for y in b] + [f'"{y}" . "{x}"' for x in a for y in b]
                elif flt["type"] == "isolate":
                    sets["isolated"] += [f'"{x}"' for x in self.server_ifaces(flt.get("vm_ref") or flt["vm"])]
                elif flt["type"] == "block":
                    ifs = self.server_ifaces(flt.get("vm_ref") or flt["vm"])
                    if flt["what"] == "internet":
                        sets["blocked_inet"] += [f'"{x}"' for x in ifs]
                    else:
                        ports = {"api": [80], "box": [22, 23]}[flt["what"]]   # the box: SFTP and SSH
                        sets["blocked_svc"] += [f'"{x}" . {p}' for x in ifs for p in ports]
            except KeyError:
                continue  # the server is gone; the fault stays listed until cleared
        tables = [f'"{nic["tap"]}" : {route_table(nic["network"])}' for srv in servers for nic in srv["_lab"]["nics"]]
        lines = [f"flush set inet hlab {name}" for name in sets] + ["flush map inet hlab tap_tables"]
        if tables:
            lines.append(f"add element inet hlab tap_tables {{ {', '.join(tables)} }}")
        lines += [f"add element inet hlab {name} {{ {', '.join(elems)} }}" for name, elems in sets.items() if elems]
        script = self.root / "faults.nft"
        script.write_text("\n".join(lines) + "\n")
        self.sh.run("nft", "-f", str(script))

    # --- latency between locations, netem faults ----------------------------------------------------------------

    def _server_ips(self, s: dict, network: int | None) -> list[str]:
        """Source prefixes of a server's packets on a public tap (network None: public IPv4, its IPv6 /64, its
        Floating IPs) or on a tap of a cloud network (its private IP there – networks may overlap)."""
        if network is not None:
            return [prefix(n["ip"]) for n in s["_lab"]["nics"] if n["network"] == network]
        ips = [prefix(self.ipv4(s))] if self.ipv4(s) else []
        ips += [prefix(f["ip"]) for f in list(self.state["floating_ips"].values()) if f.get("server") == s["id"]]
        if self.ipv6_net(s):
            ips.append(self.ipv6_net(s))
        return ips

    def apply_latency(self) -> None:
        """Delay packets towards a server by half the round-trip time to the sender's location, per tap; a netem
        fault on a tap replaces this until cleared."""
        with self.net_lock:
            self._apply_latency()

    def _apply_latency(self) -> None:
        netem = self.state.data.setdefault("netem", {})
        servers = list(self.state["servers"].values())
        for s in servers:
            if not self.vm_alive(s):
                continue
            taps = [(s["_lab"]["tap"], None)] + [(n["tap"], n["network"]) for n in s["_lab"]["nics"]]
            for tap, network in taps:
                if not self.sh.ok("link-exists", tap):
                    continue  # a NIC being attached right now: its tap gets the rules when it comes up
                self.sh.run("tc", "qdisc", "del", "dev", tap, "root", check=False)
                if tap in netem:   # checked when it was set; a failure here must not stop a server's start
                    self.sh.run("tc", "qdisc", "replace", "dev", tap, "root", "netem", *netem[tap].split(),
                                check=False)
                    continue
                by_rtt: dict[int, list[str]] = {}
                for o in servers:
                    if o is s or o["location"] == s["location"]:
                        continue
                    rtt = self.cfg["latency_ms"].get(C.location_pair(o["location"], s["location"]), 0)
                    if rtt:
                        by_rtt.setdefault(rtt, []).extend(self._server_ips(o, network))
                if not by_rtt:
                    continue
                self.sh.run("tc", "qdisc", "replace", "dev", tap, "root", "handle", "1:", "prio", "bands",
                            str(3 + len(by_rtt)), "priomap", *["1", "2", "2", "2", "1", "2", "0", "0"] + ["1"] * 8)
                for band, (rtt, ips) in enumerate(sorted(by_rtt.items()), start=4):
                    self.sh.run("tc", "qdisc", "replace", "dev", tap, "parent", f"1:{band}", "handle", f"{band}0:",
                                "netem", "delay", f"{rtt / 2}ms")
                    for ip in ips:
                        v6 = ":" in ip
                        self.sh.run("tc", "filter", "add", "dev", tap, "parent", "1:", "protocol",
                                    "ipv6" if v6 else "ip", "prio", "2" if v6 else "1", "u32", "match",
                                    "ip6" if v6 else "ip", "src", ip, "flowid", f"1:{band}")
        for c in self.state["clients"].values():   # an Internet client's link: only a netem fault
            if not self.sh.ok("link-exists", c["veth"]):
                continue
            self.sh.run("tc", "qdisc", "del", "dev", c["veth"], "root", check=False)
            if c["veth"] in netem:
                self.sh.run("tc", "qdisc", "replace", "dev", c["veth"], "root", "netem", *netem[c["veth"]].split(),
                            check=False)

    # --- Cloud firewalls ---------------------------------------------------------------------------------------

    def apply_firewalls(self) -> None:
        """Replace the firewall table (inet hlabfw) atomically with the rules of all firewalls."""
        with self.net_lock:
            path = self.root / "firewall.nft"
            path.write_text(ruleset(self.state, {s["id"]: s["_lab"]["tap"] for s in self.state["servers"].values()}))
            self.sh.run("nft", "-f", str(path))
