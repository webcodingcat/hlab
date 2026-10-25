"""Hetzner's metadata service (169.254.169.254 and fe80::a9fe:a9fe).

It answers by the caller's address and carries what cloud-init's Hetzner datasource
reads at boot: hostname, instance ID, SSH keys, network configuration, vendor data,
user data and the cloud networks.
"""

import ipaddress
import json

import yaml
from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse, Response

from ..fabric.network import link_local, network_gateway, public_mac
from . import hetzner

# Hetzner's images let root log in with the project's SSH keys, configure cloud network
# NICs (MAC 86:…) by DHCP – also hot-plugged ones – with hc-utils, and ship the QEMU
# guest agent (reset_password needs it). The stock cloud images lack both; vendor data
# adds the equivalent: a networkd rule for the private NICs and, in the background, the
# guest agent.
VENDOR_DATA = """Content-Type: multipart/mixed; boundary="==LAB=="
MIME-Version: 1.0

--==LAB==
Content-Type: text/cloud-config; charset="us-ascii"

#cloud-config
disable_root: false
ssh_pwauth: false

--==LAB==
Content-Type: text/x-shellscript; charset="us-ascii"

#!/bin/sh
cat > /etc/systemd/network/60-hc-private.network <<'EOF'
[Match]
Name=en*
[Link]
MTUBytes=1450
RequiredForOnline=no
[Network]
DHCP=ipv4
LinkLocalAddressing=no
[DHCPv4]
UseDNS=no
UseHostname=no
EOF
networkctl reload
# volumes attached with automount: mounted at /mnt/HC_Volume_<id>, also when
# hot-plugged later
cat > /usr/local/sbin/hc-volume-automount <<'EOF'
#!/bin/sh
for id in $(curl -sf http://169.254.169.254/hetzner/v1/metadata/hlab-automount); do
    dev=/dev/disk/by-id/scsi-0HC_Volume_$id
    [ -e "$dev" ] && blkid "$dev" >/dev/null 2>&1 || continue
    mountpoint -q /mnt/HC_Volume_$id && continue
    mkdir -p /mnt/HC_Volume_$id
    grep -q "HC_Volume_$id " /etc/fstab || \
        echo "$dev /mnt/HC_Volume_$id auto discard,nofail,defaults 0 0" >> /etc/fstab
    mount /mnt/HC_Volume_$id
done
EOF
chmod 755 /usr/local/sbin/hc-volume-automount
echo 'ACTION=="add", SUBSYSTEM=="block", ENV{ID_SERIAL}=="0HC_Volume_*",' \
    'RUN+="/usr/bin/systemd-run --no-block /usr/local/sbin/hc-volume-automount"' \
    > /etc/udev/rules.d/99-hc-volume-automount.rules
udevadm control --reload
/usr/local/sbin/hc-volume-automount
command -v qemu-ga >/dev/null 2>&1 && exit 0
# its own unit: a background job of this script would die with cloud-init's service
systemd-run --unit=hlab-qemu-guest-agent --no-block \
    sh -c 'export DEBIAN_FRONTEND=noninteractive;
  apt-get update -qq && apt-get install -y -qq qemu-guest-agent &&
  systemctl start qemu-guest-agent'

--==LAB==--
"""


def create_metadata_app(lab) -> FastAPI:
    """Build the app of the metadata service."""
    app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None)
    cfg, state, fabric = lab.cfg, lab.state, lab.fabric

    def caller(request: Request) -> dict | None:
        ip = request.client.host if request.client else ""
        servers = list(state["servers"].values())
        if ip.startswith(
            "fe80:"
        ):  # the link-local address follows from the MAC: one per server
            return next(
                (s for s in servers if link_local(s["_lab"]["index"]) == ip), None
            )
        holders = {
            nic["tap"]: s
            for s in servers
            for nic in s["_lab"]["nics"]
            if ip in [nic["ip"], *(nic.get("alias_ips") or [])]
        }
        if holders:
            # a private IP (networks may overlap): the tap the neighbour sits on tells
            # the server – only taps that hold the address count (any guest can put
            # entries into the neighbour table), and only a single one
            taps = fabric.neighbour_taps(ip) & set(holders)
            return holders[taps.pop()] if len(taps) == 1 else None
        for s in servers:
            ipv6 = fabric.ipv6_net(s)
            if ip == fabric.ipv4(s) or (
                ipv6
                and ":" in ip
                and ipaddress.ip_address(ip) in ipaddress.ip_network(ipv6)
            ):
                return s
        return None

    def private_networks(server: dict) -> list[dict]:
        out = []
        for i, n in enumerate(server["_lab"]["nics"], start=1):
            net = state["networks"][n["network"]]
            subnet = next(
                (
                    x["ip_range"]
                    for x in net["subnets"]
                    if ipaddress.ip_address(n["ip"])
                    in ipaddress.ip_network(x["ip_range"])
                ),
                net["ip_range"],
            )
            out.append(
                {
                    "ip": n["ip"],
                    "alias_ips": n.get("alias_ips", []),
                    "interface_num": i,
                    "mac_address": n["mac"],
                    "network_id": net["id"],
                    "network_name": net["name"],
                    "network": net["ip_range"],
                    "subnet": subnet,
                    "gateway": network_gateway(net["ip_range"]),
                }
            )
        return out

    def network_config(server: dict) -> dict:
        subnets = []
        if fabric.ipv4(server):
            subnets.append({"type": "dhcp", "ipv4": True})
        ipv6 = fabric.ipv6_net(server)
        if ipv6:
            subnets.append(
                {
                    "type": "static",
                    "ipv6": True,
                    "address": ipv6.split("/")[0] + "1/64",
                    "gateway": "fe80::1",
                }
            )
        return {
            "version": 1,
            "config": [
                {
                    "type": "physical",
                    "name": "eth0",
                    "mac_address": public_mac(server["_lab"]["index"]),
                    "subnets": subnets,
                }
            ],
        }

    def values(server: dict) -> dict:
        keys = list(server.get("public_keys", []))
        lab_key = fabric.lab_key.with_suffix(".pub")
        if lab_key.is_file():  # the lab's own access (hlab ssh)
            keys.append(lab_key.read_text().strip())
        vendor = VENDOR_DATA
        if server["_lab"].get(
            "root_password"
        ):  # no SSH keys: Hetzner's root password and password login
            password = json.dumps(server["_lab"]["root_password"])
            vendor = vendor.replace(
                "ssh_pwauth: false",
                "ssh_pwauth: true\nchpasswd:\n  expire: false\n  users:\n"
                f"  - {{name: root, password: {password}, type: text}}",
            )
        return {
            "hostname": server["name"],
            "instance-id": server["id"],
            "public-ipv4": fabric.ipv4(server) or "",
            "local-ipv4": "",
            "availability-zone": hetzner.DC_NAMES.get(
                server["location"], f"{server['location']}-dc1"
            ),
            "region": cfg["locations"][server["location"]].get(
                "network_zone", "eu-central"
            ),
            "public-keys": keys,
            "vendor_data": vendor,
            "network-config": network_config(server),
            # older cloud-init (Debian 13) reads the cloud networks from here, newer
            # from their own path
            "private-networks": private_networks(server),
        }

    def text(value) -> PlainTextResponse:
        return PlainTextResponse(
            str(value)
            if isinstance(value, str | int)
            else yaml.safe_dump(value, sort_keys=False)
        )

    @app.get("/hetzner/v1/metadata")
    def metadata(request: Request):
        server = caller(request)
        if not server:
            return PlainTextResponse("not found", status_code=404)
        return text(values(server))

    @app.get("/hetzner/v1/metadata/private-networks")
    def metadata_private(request: Request):
        server = caller(request)
        if not server:
            return PlainTextResponse("not found", status_code=404)
        return text(private_networks(server))

    @app.get("/hetzner/v1/metadata/hlab-automount")
    def metadata_automount(request: Request):
        """Return the IDs of the caller's volumes to mount.

        A lab extension for the automount helper of the vendor data.
        """
        server = caller(request)
        ids = sorted(
            v["id"]
            for v in state["volumes"].values()
            if server and v.get("server") == server["id"] and v.get("automount")
        )
        return PlainTextResponse(" ".join(map(str, ids)))

    @app.get("/hetzner/v1/metadata/{key}")
    def metadata_key(key: str, request: Request):
        server = caller(request)
        value = values(server).get(key) if server else None
        if value is None:
            return PlainTextResponse("not found", status_code=404)
        return text(value)

    @app.get("/hetzner/v1/userdata")
    def userdata(request: Request):
        server = caller(request)
        if not server:
            return PlainTextResponse("not found", status_code=404)
        if not server.get("user_data"):
            return Response(status_code=204)  # cloud-init waits for content otherwise
        return PlainTextResponse(server["user_data"])

    return app
