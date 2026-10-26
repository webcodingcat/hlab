"""Fixtures and helpers of the unit tests.

The lab runs against FakeShell instead of scripts/hlab.sh, and every /v1 response of
the test clients is checked against Hetzner's OpenAPI specification.
"""

import base64
import re
import time

import pytest
import spec_check
import yaml
from fastapi.testclient import TestClient

from hlabd import config
from hlabd.api import spec, storagebox
from hlabd.api.app import create_app
from hlabd.api.metadata import create_metadata_app
from hlabd.fabric import Fabric
from hlabd.lab import Lab
from hlabd.state import State


class FakeShell:
    """Stand-in for scripts/hlab.sh.

    It records the calls and keeps track of which VMs run.
    """

    def __init__(self):
        """Start with no calls, no running VMs and no failing commands."""
        self.calls: list[tuple] = []
        self.running: set[str] = set()
        self.neigh: dict[str, str] = {}  # address → taps it was seen on (neigh-dev)
        self.fail: set[str] = set()  # commands that fail (error paths)
        self.stdin: list[tuple] = []  # (command, what it got on stdin): passwords

    def run(self, *args, check=True, stdin_text=None):
        """Record a command and play its effect on the fake VMs.

        Takes the arguments of hlabd.fabric.shell.Shell.run; a command in self.fail
        raises.
        """
        self.calls.append(args)
        if stdin_text is not None:
            self.stdin.append((args[0], stdin_text))
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
        """Answer a yes/no command: a VM is alive while it runs, every link exists."""
        command = args[0]
        return (command == "vm-alive" and args[1] in self.running) or (
            command == "link-exists"
        )

    def qga(self, sock, command):
        """Record a guest agent command; the fake has no guest agent to answer."""
        self.calls.append(("qga", str(sock), command["execute"]))
        return None

    def commands(self, name):
        """Return the recorded calls of one command."""
        return [call for call in self.calls if call[0] == name]


@pytest.fixture
def lab(tmp_path):
    """Provide a lab on a FakeShell with instant actions and the token "secret"."""
    cfg = config.load(None)
    cfg["api"]["time_scale"] = 0
    cfg["api"]["token"] = "secret"
    state = State(tmp_path / "state.json")
    lab = Lab(cfg, state, Fabric(cfg, state, tmp_path, FakeShell()))
    # as if loaded at the lab's start
    lab.images = {name: str(tmp_path / f"{name}.qcow2") for name in cfg["images"]}
    return lab


def pytest_sessionstart(session):
    """Stop at once without the specifications: no answer could be checked."""
    absent = spec_check.missing()
    if absent:
        pytest.exit(
            f"Hetzner's OpenAPI specifications are missing ({', '.join(absent)} in "
            f"{spec.SPEC_DIR}): run the tests through bin/hlab test; the specs are in "
            "the image",
            returncode=1,
        )


class SpecClient(TestClient):
    """Every /v1 response must match Hetzner's OpenAPI specification."""

    spec = "cloud"

    def request(self, method, url, *args, **kwargs):
        """Send a request and assert that a /v1 response matches the spec."""
        response = super().request(method, url, *args, **kwargs)
        path = str(url).split("?")[0]
        if path.startswith("/v1/") and response.status_code not in (401, 503, 429):
            body = (
                response.json()
                if response.headers.get("content-type", "").startswith(
                    "application/json"
                )
                else None
            )
            errors = spec_check.check(
                self.spec, method.upper(), path, response.status_code, body
            )
            assert not errors, (
                "response does not match the Hetzner spec:\n" + "\n".join(errors)
            )
        return response


@pytest.fixture
def api(lab):
    """Provide a client of the Cloud API, with .lab and .sh (the FakeShell)."""
    with SpecClient(
        create_app(lab), headers={"Authorization": "Bearer secret"}
    ) as client:
        client.lab = lab
        client.sh = lab.fabric.sh
        yield client


def wait_action(api, action, prefix="", timeout=5.0):
    """Wait until an action has finished and return it.

    The Storage Box API keeps its actions under /v1/storage_boxes/actions; pass
    prefix="/storage_boxes" for those.
    """
    deadline = time.time() + timeout
    while time.time() < deadline:
        current = api.get(f"/v1{prefix}/actions/{action['id']}").json()["action"]
        if current["status"] != "running":
            return current
        time.sleep(0.02)
    raise AssertionError(f"action {action['id']} still running")


def dhcp(root) -> str:
    """Return all DHCP configurations the lab wrote (one dnsmasq per tap)."""
    return "".join(conf.read_text() for conf in sorted((root / "dhcp").glob("*.conf")))


def nft_set(api, name: str) -> str:
    """Return the elements of an nft set as the lab last wrote them.

    They are read from one transaction, faults.nft.
    """
    text = (api.lab.fabric.root / "faults.nft").read_text()
    match = re.search(rf"add element inet hlab {name} {{ (.*) }}", text)
    return match.group(1) if match else ""


# --- helpers of several test files ----------------------------------------------------


def make_key(index: int) -> str:
    """Return a well-formed ed25519 public key, a different one per index."""
    blob = b"\x00\x00\x00\x0bssh-ed25519\x00\x00\x00 " + bytes([index]) * 32
    return "ssh-ed25519 " + base64.b64encode(blob).decode()


KEY = make_key(99) + " test@lab"


def create_server(api, name="s1", **extra):
    """Create a cx23 Debian server in fsn1, wait for it and return the response."""
    body = {
        "name": name,
        "server_type": "cx23",
        "image": "debian-13",
        "location": "fsn1",
        **extra,
    }
    response = api.post("/v1/servers", json=body)
    assert response.status_code == 201, response.text
    out = response.json()
    wait_action(api, out["action"])
    return out


def metadata(api, ip: str, path: str = "") -> dict | list:
    """Return the metadata a server with that address sees (yaml).

    With a path, return that key's value (parsed YAML).
    """
    with TestClient(create_metadata_app(api.lab), client=(ip, 40000)) as client:
        response = client.get(f"/hetzner/v1/metadata{path}")
    assert response.status_code == 200, response.text
    return yaml.safe_load(response.text)


def power_off(api, sid):
    """Power a server off and wait until the action has finished."""
    wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweroff").json()["action"])


def create_network(api, *subnets, name="n"):
    """Create a network 10.0.0.0/16 with these cloud subnets and return it."""
    return api.post(
        "/v1/networks",
        json={
            "name": name,
            "ip_range": "10.0.0.0/16",
            "subnets": [
                {"type": "cloud", "ip_range": subnet, "network_zone": "eu-central"}
                for subnet in subnets
            ],
        },
    ).json()["network"]


@pytest.fixture
def box_api(lab):
    """Provide a client of the api.hetzner.com stand-in.

    Every /v1 response is checked against Hetzner's specification.
    """

    class StorageBoxClient(SpecClient):
        """A SpecClient that checks against the api.hetzner.com specification."""

        spec = "hetzner"

    with StorageBoxClient(
        storagebox.create_app(lab), headers={"Authorization": "Bearer secret"}
    ) as client:
        client.lab, client.sh = lab, lab.fabric.sh
        yield client
