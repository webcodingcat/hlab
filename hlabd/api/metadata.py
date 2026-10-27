"""Hetzner's metadata service (169.254.169.254 and fe80::a9fe:a9fe).

It finds the calling server by its address. It serves what cloud-init's Hetzner
datasource reads at boot: hostname, instance ID, SSH keys, network configuration,
vendor data, user data and the cloud networks.
"""

import json
from pathlib import Path

import yaml
from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse, Response

from .. import config
from ..fabric.addresses import link_local, network_gateway, public_mac
from ..lab import Lab
from ..records import Server
from ..state import NETWORKS, SERVERS, VOLUMES, nic_addresses, primary_address_of
from .networks import subnet_of
from .public_addresses import contains

# the availability-zone of a location (others: <location>-dc1)
DC_NAMES = {
    "fsn1": "fsn1-dc14",
    "nbg1": "nbg1-dc3",
    "hel1": "hel1-dc2",
}

# Hetzner's images let root log in with the project's SSH keys, configure cloud network
# NICs (MAC 86:…) by DHCP with hc-utils (hot-plugged ones too), and ship the QEMU
# guest agent (reset_password needs it). The stock cloud images have neither hc-utils
# nor the agent, so the vendor data installs the same things: a networkd rule for the
# private NICs and, in the background, the guest agent.
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
# a separate unit: a background job of this script would die with cloud-init's service
systemd-run --unit=hlab-qemu-guest-agent --no-block \
    sh -c 'export DEBIAN_FRONTEND=noninteractive;
  apt-get update -qq && apt-get install -y -qq qemu-guest-agent &&
  systemctl start qemu-guest-agent'

--==LAB==--
"""


# --- the caller -----------------------------------------------------------------------


async def calling_server(lab: Lab, request: Request) -> Server | None:
    """Return the server a request comes from, or None."""
    state, fabric = lab.state, lab.fabric
    ip = request.client.host if request.client else ""
    servers = list(state[SERVERS].values())
    holders = {
        nic["tap"]: server["id"]
        for server in servers
        for nic in server["_lab"]["nics"]
        if ip in nic_addresses(nic)
    }
    # the link-local address follows from the MAC: one per server
    if ip.startswith("fe80:"):
        return next((s for s in servers if link_local(s["_lab"]["index"]) == ip), None)
    if holders:
        # a private IP (networks may overlap): the tap on which the address is a
        # neighbour tells which server it is. Only taps that hold the address count,
        # because any guest can add entries to the neighbour table. And it counts only
        # if there is exactly one such tap.
        taps = await lab.blocking(fabric.network.neighbour_taps, ip) & set(holders)
        # looked up again after the await: the server may be gone
        return state.get(SERVERS, holders[taps.pop()]) if len(taps) == 1 else None
    for server in servers:
        ipv6 = primary_address_of(state, server, "ipv6")
        if ip == primary_address_of(state, server, "ipv4") or (
            ipv6 and contains(ipv6, ip)
        ):
            return server
    return None


# --- what a server gets ---------------------------------------------------------------


def private_networks(lab: Lab, server: Server) -> list[dict]:
    """Return the cloud networks of a server as the metadata lists them."""
    state = lab.state
    out = []
    for number, nic in enumerate(server["_lab"]["nics"], start=1):
        network = state[NETWORKS][nic["network"]]
        out.append(
            {
                "ip": nic["ip"],
                "alias_ips": nic["alias_ips"],
                "interface_num": number,
                "mac_address": nic["mac"],
                "network_id": network["id"],
                "network_name": network["name"],
                "network": network["ip_range"],
                "subnet": subnet_of(network, nic["ip"]),
                "gateway": network_gateway(network["ip_range"]),
            }
        )
    return out


def network_config(lab: Lab, server: Server) -> dict:
    """Return the network configuration (version 1) of a server for cloud-init."""
    state = lab.state
    subnets = []
    if primary_address_of(state, server, "ipv4"):
        subnets.append({"type": "dhcp", "ipv4": True})
    ipv6 = primary_address_of(state, server, "ipv6")
    if ipv6:
        subnets.append(
            {
                "type": "static",
                "ipv6": True,
                "address": ipv6.split("/")[0] + "1/64",
                "gateway": config.GATEWAY6,
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


def read_lab_key(lab_key: Path) -> str | None:
    """Return the lab's public SSH key (for `hlab ssh`), or None without one."""
    public_key = lab_key.with_suffix(".pub")
    return public_key.read_text().strip() if public_key.is_file() else None


async def caller_metadata(lab: Lab, request: Request) -> dict | None:
    """Return the metadata of the calling server, by key; None for an unknown caller."""
    # read first: the server is looked up after the last await
    lab_key = await lab.blocking(read_lab_key, lab.fabric.lab_key)
    server = await calling_server(lab, request)
    return metadata_values(lab, server, lab_key) if server else None


def metadata_values(lab: Lab, server: Server, lab_key: str | None) -> dict:
    """Return the metadata of a server, by key, with the lab's public key if any."""
    cfg = lab.cfg
    keys = list(server["public_keys"])
    if lab_key:  # the lab's admin access (hlab ssh)
        keys.append(lab_key)
    vendor = VENDOR_DATA
    # no SSH keys: Hetzner's root password and password login
    if server["_lab"].get("root_password"):
        password = json.dumps(server["_lab"]["root_password"])
        vendor = vendor.replace(
            "ssh_pwauth: false",
            "ssh_pwauth: true\nchpasswd:\n  expire: false\n  users:\n"
            f"  - {{name: root, password: {password}, type: text}}",
        )
    return {
        "hostname": server["name"],
        "instance-id": server["id"],
        "public-ipv4": primary_address_of(lab.state, server, "ipv4") or "",
        "local-ipv4": "",
        "availability-zone": DC_NAMES.get(
            server["location"], f"{server['location']}-dc1"
        ),
        "region": cfg["locations"][server["location"]].get(
            "network_zone", "eu-central"
        ),
        "public-keys": keys,
        "vendor_data": vendor,
        "network-config": network_config(lab, server),
        # older cloud-init (Debian 13) reads the cloud networks from here; newer
        # versions read them from their own path
        "private-networks": private_networks(lab, server),
    }


def text_response(value) -> PlainTextResponse:
    """Return a value as text: plain for strings and numbers, else YAML."""
    return PlainTextResponse(
        str(value)
        if isinstance(value, str | int)
        else yaml.safe_dump(value, sort_keys=False)
    )


# --- the app --------------------------------------------------------------------------


def create_metadata_app(lab: Lab) -> FastAPI:
    """Build the app of the metadata service."""
    app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None)
    state = lab.state

    @app.get("/hetzner/v1/metadata")
    async def metadata_get(request: Request):
        values = await caller_metadata(lab, request)
        if values is None:
            return PlainTextResponse("not found", status_code=404)
        return text_response(values)

    @app.get("/hetzner/v1/metadata/hlab-automount")
    async def automount_get(request: Request):
        """Return the IDs of the caller's volumes to mount.

        Hetzner has no such key; the lab adds it for the automount helper of the vendor
        data.
        """
        server = await calling_server(lab, request)
        ids = sorted(
            v["id"]
            for v in state[VOLUMES].values()
            if server and v.get("server") == server["id"] and v.get("automount")
        )
        return PlainTextResponse(" ".join(map(str, ids)))

    # every key, also private-networks (which cloud-init reads on its own path)
    @app.get("/hetzner/v1/metadata/{key}")
    async def metadata_key_get(key: str, request: Request):
        values = await caller_metadata(lab, request)
        value = values.get(key) if values is not None else None
        if value is None:
            return PlainTextResponse("not found", status_code=404)
        return text_response(value)

    @app.get("/hetzner/v1/userdata")
    async def userdata_get(request: Request):
        server = await calling_server(lab, request)
        if not server:
            return PlainTextResponse("not found", status_code=404)
        if not server.get("user_data"):
            return Response(status_code=204)  # else cloud-init waits for content
        return PlainTextResponse(server["user_data"])

    return app
