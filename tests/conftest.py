import base64
import re
import time

import pytest
import yaml
from fastapi.testclient import TestClient

import spec_check
from hlabd import config as C
from hlabd.api import storagebox
from hlabd.api.app import create_app
from hlabd.lab import Lab
from hlabd.fabric import Fabric
from hlabd.api.metadata import create_metadata_app
from hlabd.state import State


class FakeShell:
    """Stands in for scripts/hlab.sh: records the calls and keeps track of which VMs run."""

    def __init__(self):
        self.calls: list[tuple] = []
        self.running: set[str] = set()
        self.neigh: dict[str, str] = {}   # address → taps it was seen on (neigh-dev)
        self.fail: set[str] = set()        # commands that fail (error paths)
        self.stdin: list[tuple] = []       # (command, what it got on stdin): passwords

    def run(self, *args, check=True, input=None):
        self.calls.append(args)
        if input is not None:
            self.stdin.append((args[0], input))
        if args[0] in self.fail:
            raise RuntimeError(f"{args[0]} failed")
        if args[0] == "vm-start":
            self.running.add(args[1])
        elif args[0] == "neigh-dev":
            return self.neigh.get(args[1], "")
        elif args[0] == "vm-kill":
            self.running.discard(args[1])
        return ""

    def ok(self, *args):
        return (args[0] == "vm-alive" and args[1] in self.running) or args[0] == "link-exists"

    def qga(self, sock, command):
        self.calls.append(("qga", str(sock), command["execute"]))
        return None  # no guest agent in the fake

    def commands(self, name):
        return [c for c in self.calls if c[0] == name]


@pytest.fixture
def lab(tmp_path):
    cfg = C.load(None)
    cfg["api"]["time_scale"] = 0
    cfg["api"]["token"] = "secret"
    state = State(tmp_path / "state.json")
    sh = FakeShell()
    lab = Lab(cfg, state, Fabric(cfg, state, tmp_path, sh))
    lab.images = {n: str(tmp_path / f"{n}.qcow2") for n in cfg["images"]}   # as if loaded at the lab's start
    return lab


class SpecClient(TestClient):
    """Every /v1 response must match Hetzner's OpenAPI specification."""

    spec = "cloud"

    def request(self, method, url, *args, **kwargs):
        response = super().request(method, url, *args, **kwargs)
        path = str(url).split("?")[0]
        if path.startswith("/v1/") and response.status_code not in (401, 503, 429) and spec_check.available():
            body = response.json() if response.headers.get("content-type", "").startswith("application/json") else None
            errors = spec_check.check(self.spec, method.upper(), path[3:], response.status_code, body)
            assert not errors, "response does not match the Hetzner spec:\n" + "\n".join(errors)
        return response


@pytest.fixture
def api(lab):
    with SpecClient(create_app(lab), headers={"Authorization": "Bearer secret"}) as client:
        client.lab = lab
        client.sh = lab.fabric.sh
        yield client


def wait_action(api, action, timeout=5.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        current = api.get(f"/v1/actions/{action['id']}").json()["action"]
        if current["status"] != "running":
            return current
        time.sleep(0.02)
    raise AssertionError(f"action {action['id']} still running")


def dhcp(root) -> str:
    """All DHCP configurations the lab wrote (one dnsmasq per tap)."""
    return "".join(f.read_text() for f in sorted((root / "dhcp").glob("*.conf")))


def nft_set(api, name: str) -> str:
    """The elements of an nft set as the lab last wrote them (one transaction, faults.nft)."""
    text = (api.lab.fabric.root / "faults.nft").read_text()
    match = re.search(rf"add element inet hlab {name} {{ (.*) }}", text)
    return match.group(1) if match else ""


# --- helpers of several test files ----------------------------------------------------------------------------

def make_key(index: int) -> str:
    blob = b"\x00\x00\x00\x0bssh-ed25519\x00\x00\x00 " + bytes([index]) * 32
    return "ssh-ed25519 " + base64.b64encode(blob).decode()


KEY = make_key(99) + " test@lab"


def create_server(api, name="s1", **extra):
    body = {"name": name, "server_type": "cx23", "image": "debian-13", "location": "fsn1", **extra}
    response = api.post("/v1/servers", json=body)
    assert response.status_code == 201, response.text
    out = response.json()
    wait_action(api, out["action"])
    return out


def metadata(api, ip: str, path: str = "") -> dict:
    """The metadata a server with that address sees (yaml), or the raw response for a path."""
    with TestClient(create_metadata_app(api.lab), client=(ip, 40000)) as client:
        response = client.get(f"/hetzner/v1/metadata{path}")
    assert response.status_code == 200, response.text
    return yaml.safe_load(response.text)


def off(api, sid):
    wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweroff").json()["action"])


ANY = ["0.0.0.0/0", "::/0"]


def ruleset(api) -> str:
    return (api.lab.fabric.root / "firewall.nft").read_text()


def create_network(api, *subnets, name="n"):
    return api.post("/v1/networks", json={"name": name, "ip_range": "10.0.0.0/16", "subnets": [
        {"type": "cloud", "ip_range": s, "network_zone": "eu-central"} for s in subnets]}).json()["network"]


@pytest.fixture
def box_api(lab):
    """The api.hetzner.com stand-in, every /v1 response checked against Hetzner's specification."""
    class Client(SpecClient):
        spec = "hetzner"
    with Client(storagebox.create_app(lab), headers={"Authorization": "Bearer secret"}) as client:
        client.lab, client.sh = lab, lab.fabric.sh
        yield client


def box_wait(api, action, timeout=5.0):
    """Actions of the Storage Box API live under /v1/storage_boxes/actions."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        current = api.get(f"/v1/storage_boxes/actions/{action['id']}").json()["action"]
        if current["status"] != "running":
            return current
        time.sleep(0.02)
    raise AssertionError(f"action {action['id']} still running")


def new_box(api, name="backup", **extra):
    response = api.post("/v1/storage_boxes", json={"name": name, "location": "fsn1", "storage_box_type": "bx11",
                                                   "password": "Secret-Pass-123", **extra})
    assert response.status_code == 201, response.text
    box_wait(api, response.json()["action"])
    return api.get(f"/v1/storage_boxes/{response.json()['storage_box']['id']}").json()["storage_box"]
