"""Helpers of the live tests (see conftest.py)."""

import subprocess
import time

import httpx

API = "http://198.18.0.1/v1"
TOKEN = "hlab" * 16

# The echo service on every server: "<host> <proto> <peer address>" on TCP/UDP 7000 and TCP 7001, IPv4 and IPv6.
ECHO = r'''
import socket, socketserver, sys, threading
host = socket.gethostname()
def peer(a): return a[0].removeprefix("::ffff:")
class Tcp(socketserver.BaseRequestHandler):
    def handle(self): self.request.sendall(f"{host} tcp {peer(self.client_address)}\n".encode())
class Udp(socketserver.BaseRequestHandler):
    def handle(self): self.request[1].sendto(f"{host} udp {peer(self.client_address)}\n".encode(), self.client_address)
class T6(socketserver.ThreadingTCPServer):
    address_family = socket.AF_INET6
    allow_reuse_address = True
    def server_bind(self):
        self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        super().server_bind()
class U6(socketserver.UDPServer):
    address_family = socket.AF_INET6
    def server_bind(self):
        self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        super().server_bind()
for port in (7000, 7001):
    threading.Thread(target=T6(("::", port), Tcp).serve_forever, daemon=True).start()
U6(("::", 7000), Udp).serve_forever()
'''


class Lab:
    def __init__(self):
        self.http = httpx.Client(base_url=API, headers={"Authorization": f"Bearer {TOKEN}"}, timeout=30)

    def api(self, method: str, path: str, body: dict | None = None, ok: bool = True) -> dict:
        r = self.http.request(method, path, json=body)
        if ok:
            assert r.status_code < 300, f"{method} {path}: {r.status_code} {r.text}"
        return r.json() if r.content else {}

    def control(self, method: str, path: str, body: dict | None = None) -> dict:
        r = httpx.request(method, f"http://198.18.0.1/_lab{path}", json=body, timeout=120)
        assert r.status_code < 300, r.text
        return r.json()

    def wait_action(self, a: dict, timeout: float = 120) -> dict:
        deadline = time.time() + timeout
        while time.time() < deadline:
            a = self.api("GET", f"/actions/{a['id']}")["action"]
            if a["status"] != "running":
                assert a["status"] == "success", a
                return a
            time.sleep(0.5)
        raise AssertionError(f"action {a} still running")

    def server(self, name: str) -> dict:
        return self.api("GET", f"/servers?name={name}")["servers"][0]

    def action(self, name: str, cmd: str, body: dict | None = None) -> dict:
        return self.wait_action(self.api("POST", f"/servers/{self.server(name)['id']}/actions/{cmd}", body or {})["action"])

    def ssh(self, name: str, cmd: str, timeout: float = 30) -> subprocess.CompletedProcess:
        return subprocess.run(["hlab", "ssh", name, cmd], capture_output=True, text=True, timeout=timeout)

    def sh(self, name: str, cmd: str, timeout: float = 30) -> str:
        p = self.ssh(name, cmd, timeout)
        assert p.returncode == 0, f"{name}: {cmd}: {p.stderr}"
        return p.stdout.strip()

    def wait_ssh(self, name: str, timeout: float = 180) -> None:
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                if self.ssh(name, "true", timeout=8).returncode == 0:
                    return
            except subprocess.TimeoutExpired:
                pass
            time.sleep(1)
        raise AssertionError(f"{name}: no SSH after {timeout} s")

    def create(self, name: str, **body) -> dict:
        """A server, booted and through cloud-init."""
        a = self.api("POST", "/servers", {"name": name, "server_type": "cx23", "image": "debian-13",
                                          "location": "fsn1", "labels": {"lab": "live"}, **body})
        self.wait_action(a["action"], timeout=600)
        if body.get("start_after_create", True):
            self.wait_ssh(name)
            self.sh(name, "cloud-init status --wait >/dev/null; sync", timeout=300)
        return self.server(name)

    def delete(self, name: str) -> None:
        self.wait_action(self.api("DELETE", f"/servers/{self.server(name)['id']}")["action"])

    def client(self, name: str, cmd: str, timeout: float = 15) -> subprocess.CompletedProcess:
        return subprocess.run(["ip", "netns", "exec", name, "sh", "-c", cmd], capture_output=True, text=True,
                              timeout=timeout)

    def start_echo(self, name: str) -> None:
        p = subprocess.run(["hlab", "ssh", name, "cat > /root/echo.py && { [ -f /run/echo.pid ] && kill $(cat /run/echo.pid); "
                            "setsid python3 /root/echo.py >/dev/null 2>&1 < /dev/null & echo $! > /run/echo.pid; "
                            "sleep 1; }"],
                           input=ECHO, capture_output=True, text=True, timeout=30)
        assert p.returncode == 0, p.stderr


def probe(lab: Lab, where: str, proto: str, addr: str, port: int = 7000) -> str:
    """Talk to an echo service from a client ('client:<name>') or a server; the echo line, '' when nothing came back."""
    if where.startswith("client:"):  # clients have socat
        target = f"[{addr}]:{port}" if ":" in addr else f"{addr}:{port}"
        cmd = (f"timeout 5 socat -T3 - TCP:{target},connect-timeout=3 </dev/null" if proto == "tcp"
               else f"echo hi | timeout 4 socat -T3 - UDP-DATAGRAM:{target}{',pf=ip6' if ':' in addr else ''}")
        return lab.client(where[7:], cmd).stdout.strip()
    fam = "socket.AF_INET6" if ":" in addr else "socket.AF_INET"  # servers: python
    kind, send = (("socket.SOCK_DGRAM", "s.sendto(b'hi', a)") if proto == "udp"
                  else ("socket.SOCK_STREAM", "s.connect(a)"))
    cmd = (f"python3 -c \"import socket; s=socket.socket({fam},{kind}); s.settimeout(3); a=('{addr}',{port}); "
           f"{send}; print(s.recv(200).decode().strip())\" 2>/dev/null")
    return lab.ssh(where, cmd).stdout.strip()


def ping(lab: Lab, where: str, addr: str, count: int = 3) -> float | None:
    """Average round-trip time in ms, None when nothing answered."""
    cmd = f"ping -c{count} -i0.2 -W2 -q {addr} | tail -1"
    out = lab.client(where[7:], cmd).stdout if where.startswith("client:") else lab.ssh(where, cmd).stdout
    return float(out.split("=")[1].split("/")[1]) if "rtt" in out else None
