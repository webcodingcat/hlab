"""Hetzner's metadata service (169.254.169.254 and fe80::a9fe:a9fe), answering by the caller's address. It carries
what cloud-init's Hetzner datasource reads at boot: hostname, instance ID, SSH keys, network configuration, vendor
data, user data and the cloud networks."""

import json
import ipaddress

import yaml
from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse, Response

from ..fabric.network import link_local, network_gateway, public_mac
from . import hetzner as H

# Hetzner's images let root log in with the project's SSH keys, configure cloud network NICs (MAC 86:…) by DHCP –
# also hot-plugged ones – with hc-utils, and ship the QEMU guest agent (reset_password needs it). The stock cloud
# images lack both; vendor data adds the equivalent: a networkd rule for the private NICs and, in the background,
# the guest agent.
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
# volumes attached with automount: mounted at /mnt/HC_Volume_<id>, also when hot-plugged later
cat > /usr/local/sbin/hc-volume-automount <<'EOF'
#!/bin/sh
for id in $(curl -sf http://169.254.169.254/hetzner/v1/metadata/hlab-automount); do
    dev=/dev/disk/by-id/scsi-0HC_Volume_$id
    [ -e "$dev" ] && blkid "$dev" >/dev/null 2>&1 || continue
    mountpoint -q /mnt/HC_Volume_$id && continue
    mkdir -p /mnt/HC_Volume_$id
    grep -q "HC_Volume_$id " /etc/fstab || echo "$dev /mnt/HC_Volume_$id auto discard,nofail,defaults 0 0" >> /etc/fstab
    mount /mnt/HC_Volume_$id
done
EOF
chmod 755 /usr/local/sbin/hc-volume-automount
echo 'ACTION=="add", SUBSYSTEM=="block", ENV{ID_SERIAL}=="0HC_Volume_*", RUN+="/usr/bin/systemd-run --no-block /usr/local/sbin/hc-volume-automount"' \
    > /etc/udev/rules.d/99-hc-volume-automount.rules
udevadm control --reload
/usr/local/sbin/hc-volume-automount
command -v qemu-ga >/dev/null 2>&1 && exit 0
# its own unit: a background job of this script would die with cloud-init's service
systemd-run --unit=hlab-qemu-guest-agent --no-block sh -c 'export DEBIAN_FRONTEND=noninteractive;
  apt-get update -qq && apt-get install -y -qq qemu-guest-agent && systemctl start qemu-guest-agent'

--==LAB==--
"""



def create_metadata_app(lab) -> FastAPI:
    app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None)
    cfg, state, fabric = lab.cfg, lab.state, lab.fabric

    def caller(request: Request) -> dict | None:
        ip = request.client.host if request.client else ""
        servers = list(state["servers"].values())
        if ip.startswith("fe80:"):   # the link-local address follows from the MAC: one per server
            return next((s for s in servers if link_local(s["_lab"]["index"]) == ip), None)
        holders = {n["tap"]: s for s in servers for n in s["_lab"]["nics"] if ip in [n["ip"], *(n.get("alias_ips") or [])]}
        if holders:
            # a private IP (networks may overlap): the tap the neighbour sits on tells the server – only taps that
            # hold the address count (any guest can put entries into the neighbour table), and only a single one
            taps = set(fabric.sh.run("neigh-dev", ip, check=False).split()) & set(holders)
            return holders[taps.pop()] if len(taps) == 1 else None
        for s in servers:
            v6 = fabric.ipv6_net(s)
            if ip == fabric.ipv4(s) or (v6 and ":" in ip and ipaddress.ip_address(ip) in ipaddress.ip_network(v6)):
                return s
        return None

    def private_networks(s: dict) -> list[dict]:
        out = []
        for i, n in enumerate(s["_lab"]["nics"], start=1):
            net = state["networks"][n["network"]]
            sub = next((x["ip_range"] for x in net["subnets"]
                        if ipaddress.ip_address(n["ip"]) in ipaddress.ip_network(x["ip_range"])), net["ip_range"])
            out.append({"ip": n["ip"], "alias_ips": n.get("alias_ips", []), "interface_num": i,
                        "mac_address": n["mac"], "network_id": net["id"], "network_name": net["name"],
                        "network": net["ip_range"], "subnet": sub, "gateway": network_gateway(net["ip_range"])})
        return out

    def network_config(s: dict) -> dict:
        subnets = []
        if fabric.ipv4(s):
            subnets.append({"type": "dhcp", "ipv4": True})
        v6 = fabric.ipv6_net(s)
        if v6:
            subnets.append({"type": "static", "ipv6": True, "address": v6.split("/")[0] + "1/64",
                            "gateway": "fe80::1"})
        return {"version": 1, "config": [{"type": "physical", "name": "eth0",
                                          "mac_address": public_mac(s["_lab"]["index"]), "subnets": subnets}]}

    def values(s: dict) -> dict:
        keys = list(s.get("public_keys", []))
        lab_key = fabric.lab_key.with_suffix(".pub")
        if lab_key.is_file():  # the lab's own access (hlab ssh)
            keys.append(lab_key.read_text().strip())
        vendor = VENDOR_DATA
        if s["_lab"].get("root_password"):   # no SSH keys: Hetzner's root password and password login
            vendor = vendor.replace("ssh_pwauth: false", "ssh_pwauth: true\nchpasswd:\n  expire: false\n  users:\n"
                                    f"  - {{name: root, password: {json.dumps(s['_lab']['root_password'])}, type: text}}")
        return {"hostname": s["name"], "instance-id": s["id"],
                "public-ipv4": fabric.ipv4(s) or "", "local-ipv4": "",
                "availability-zone": H.DC_NAMES.get(s["location"], f"{s['location']}-dc1"),
                "region": cfg["locations"][s["location"]].get("network_zone", "eu-central"),
                "public-keys": keys, "vendor_data": vendor, "network-config": network_config(s),
                # older cloud-init (Debian 13) reads the cloud networks from here, newer from their own path
                "private-networks": private_networks(s)}

    def text(v) -> PlainTextResponse:
        return PlainTextResponse(str(v) if isinstance(v, str | int) else yaml.safe_dump(v, sort_keys=False))

    @app.get("/hetzner/v1/metadata")
    def metadata(request: Request):
        s = caller(request)
        if not s:
            return PlainTextResponse("not found", status_code=404)
        return text(values(s))

    @app.get("/hetzner/v1/metadata/private-networks")
    def metadata_private(request: Request):
        s = caller(request)
        if not s:
            return PlainTextResponse("not found", status_code=404)
        return text(private_networks(s))

    @app.get("/hetzner/v1/metadata/hlab-automount")
    def metadata_automount(request: Request):
        """Lab extension for the automount helper of the vendor data: IDs of volumes to mount."""
        s = caller(request)
        ids = sorted(v["id"] for v in state["volumes"].values() if s and v.get("server") == s["id"]
                     and v.get("automount"))
        return PlainTextResponse(" ".join(map(str, ids)))

    @app.get("/hetzner/v1/metadata/{key}")
    def metadata_key(key: str, request: Request):
        s = caller(request)
        v = values(s).get(key) if s else None
        if v is None:
            return PlainTextResponse("not found", status_code=404)
        return text(v)

    @app.get("/hetzner/v1/userdata")
    def userdata(request: Request):
        s = caller(request)
        if not s:
            return PlainTextResponse("not found", status_code=404)
        if not s.get("user_data"):
            return Response(status_code=204)  # cloud-init waits for content otherwise
        return PlainTextResponse(s["user_data"])

    return app
