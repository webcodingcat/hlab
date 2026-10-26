"""Helpers of the live tests (see conftest.py).

LiveLab talks to a running lab: its APIs over HTTP, its servers over `hlab ssh` and
its Internet clients through their network namespaces.
"""

import subprocess
import time
from pathlib import Path

import httpx

API = "http://198.18.0.1/v1"
CONTROL = "http://198.18.0.1/_lab"
TOKEN = "hlab" * 16

# The echo service on every server (see echo.py).
ECHO = Path(__file__).with_name("echo.py").read_text()

# The addresses the lab hands out to the servers a, b, c and the client alice.
A4, B4, C4 = "198.51.100.11", "198.51.100.12", "198.51.100.13"
A6, B6, C6 = "2001:db8:1::1", "2001:db8:2::1", "2001:db8:3::1"
A_PRIV, B_PRIV, C_PRIV = "10.0.1.1", "10.0.1.2", "10.1.1.1"
ALICE4, ALICE6 = "192.0.2.11", "2001:db8:ffff::b"


class LiveLab:
    """A running lab, reached through one of its APIs (the Cloud API by default)."""

    def __init__(self, base_url: str = API):
        """Connect to the API at base_url with the lab's token."""
        self.http = httpx.Client(
            base_url=base_url, headers={"Authorization": f"Bearer {TOKEN}"}, timeout=60
        )

    def api(
        self, method: str, path: str, body: dict | None = None, ok: bool = True
    ) -> dict:
        """Send an API request and return the JSON answer.

        With ok, assert that the request succeeded.
        """
        response = self.http.request(method, path, json=body)
        if ok:
            assert response.status_code < 300, (
                f"{method} {path}: {response.status_code} {response.text}"
            )
        return response.json() if response.content else {}

    def control(self, method: str, path: str, body: dict | None = None) -> dict:
        """Send a request to the lab's control API (/_lab) and return the answer."""
        response = httpx.request(method, f"{CONTROL}{path}", json=body, timeout=120)
        assert response.status_code < 300, response.text
        return response.json()

    def wait_action(self, action: dict, prefix: str = "", timeout: float = 120) -> dict:
        """Wait until an action has succeeded and return it.

        The Storage Box API keeps its actions under /storage_boxes/actions; pass
        prefix="/storage_boxes" for those.
        """
        deadline = time.time() + timeout
        while time.time() < deadline:
            action = self.api("GET", f"{prefix}/actions/{action['id']}")["action"]
            if action["status"] != "running":
                assert action["status"] == "success", action
                return action
            time.sleep(0.5)
        raise AssertionError(f"action {action} still running")

    def server(self, name: str) -> dict:
        """Return the server with this name."""
        return self.api("GET", f"/servers?name={name}")["servers"][0]

    def server_action(self, name: str, cmd: str, body: dict | None = None) -> dict:
        """Run a server action (poweroff, reset, ...) and wait until it succeeded."""
        return self.wait_action(
            self.api(
                "POST", f"/servers/{self.server(name)['id']}/actions/{cmd}", body or {}
            )["action"]
        )

    def run(
        self, name: str, cmd: str, timeout: float = 30, stdin_text: str | None = None
    ) -> subprocess.CompletedProcess:
        """Run a shell command on a server over SSH and return the result."""
        return subprocess.run(
            ["hlab", "ssh", name, cmd],
            input=stdin_text,
            capture_output=True,
            text=True,
            timeout=timeout,
        )

    def check_output(self, name: str, cmd: str, timeout: float = 30) -> str:
        """Run a shell command on a server, assert success and return its output."""
        result = self.run(name, cmd, timeout)
        assert result.returncode == 0, f"{name}: {cmd}: {result.stderr}"
        return result.stdout.strip()

    def wait_ssh(self, name: str, timeout: float = 180) -> None:
        """Wait until a server takes SSH logins."""
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                if self.run(name, "true", timeout=8).returncode == 0:
                    return
            except subprocess.TimeoutExpired:
                pass
            time.sleep(1)
        raise AssertionError(f"{name}: no SSH after {timeout} s")

    def create(self, name: str, **body) -> dict:
        """Create a server and wait until it has booted and run cloud-init."""
        created = self.api(
            "POST",
            "/servers",
            {
                "name": name,
                "server_type": "cx23",
                "image": "debian-13",
                "location": "fsn1",
                "labels": {"lab": "live"},
                **body,
            },
        )
        self.wait_action(created["action"], timeout=600)
        if body.get("start_after_create", True):
            self.wait_ssh(name)
            self.check_output(
                name, "cloud-init status --wait >/dev/null; sync", timeout=300
            )
        return self.server(name)

    def delete(self, name: str) -> None:
        """Delete a server and wait until it is gone."""
        self.wait_action(
            self.api("DELETE", f"/servers/{self.server(name)['id']}")["action"]
        )

    def client(
        self, name: str, cmd: str, timeout: float = 15
    ) -> subprocess.CompletedProcess:
        """Run a shell command on an Internet client and return the result."""
        return subprocess.run(
            ["ip", "netns", "exec", name, "sh", "-c", cmd],
            capture_output=True,
            text=True,
            timeout=timeout,
        )

    def start_echo(self, name: str) -> None:
        """Copy the echo service to a server and (re)start it there."""
        result = self.run(
            name,
            "cat > /root/echo.py && "
            "{ [ -f /run/echo.pid ] && kill $(cat /run/echo.pid); "
            "setsid python3 /root/echo.py >/dev/null 2>&1 < /dev/null & "
            "echo $! > /run/echo.pid; "
            "sleep 1; }",
            stdin_text=ECHO,
        )
        assert result.returncode == 0, result.stderr


def probe(lab: LiveLab, where: str, proto: str, addr: str, port: int = 7000) -> str:
    """Talk to an echo service from a client ('client:<name>') or a server.

    Return the echo line, or '' when nothing came back.
    """
    if where.startswith("client:"):  # clients have socat
        target = f"[{addr}]:{port}" if ":" in addr else f"{addr}:{port}"
        cmd = (
            f"timeout 5 socat -T3 - TCP:{target},connect-timeout=3 </dev/null"
            if proto == "tcp"
            else "echo hi | timeout 4 socat -T3 - "
            f"UDP-DATAGRAM:{target}{',pf=ip6' if ':' in addr else ''}"
        )
        return lab.client(where.removeprefix("client:"), cmd).stdout.strip()
    family = "socket.AF_INET6" if ":" in addr else "socket.AF_INET"  # servers: python
    kind, send = (
        ("socket.SOCK_DGRAM", "s.sendto(b'hi', a)")
        if proto == "udp"
        else ("socket.SOCK_STREAM", "s.connect(a)")
    )
    cmd = (
        f'python3 -c "import socket; s=socket.socket({family},{kind}); '
        f"s.settimeout(3); a=('{addr}',{port}); "
        f'{send}; print(s.recv(200).decode().strip())" 2>/dev/null'
    )
    return lab.run(where, cmd).stdout.strip()


def ping(lab: LiveLab, where: str, addr: str, count: int = 3) -> float | None:
    """Return the average round-trip time in ms, None when nothing answered.

    The ping runs on a client ('client:<name>') or a server.
    """
    cmd = f"ping -c{count} -i0.2 -W2 -q {addr} | tail -1"
    out = (
        lab.client(where.removeprefix("client:"), cmd).stdout
        if where.startswith("client:")
        else lab.run(where, cmd).stdout
    )
    return float(out.split("=")[1].split("/")[1]) if "rtt" in out else None
