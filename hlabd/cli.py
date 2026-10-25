"""The lab command inside the container. `serve` runs the lab (container main process); every other command talks
to it through the control API on http://198.18.0.1/_lab/."""

import argparse
import asyncio
import concurrent.futures
import json
import logging
import os
import shlex
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

from . import config as C
from .api import storagebox

ROOT = Path("/hlab")
CONTROL = f"http://{C.SERVICES}/_lab"


def call(method: str, path: str, body: dict | None = None) -> dict:
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(CONTROL + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            return json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            msg = json.loads(e.read())["error"]["message"]
        except Exception:  # noqa: BLE001
            msg = str(e)
        sys.exit(f"hlab: {msg}")
    except urllib.error.URLError as e:
        sys.exit(f"hlab: control API not reachable ({e.reason}) – is the lab up?")


def serve(cfg_path: str | None) -> None:
    import uvicorn

    from .api.app import create_app
    from .api.catalogue import load_images
    from .api.metadata import create_metadata_app
    from .api.restart import reconcile
    from .fabric import Fabric, Shell
    from .lab import Lab
    from .state import State

    # one line per event on stdout (docker logs): time, level, part of the lab, message
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s",
                        stream=sys.stdout, force=True)
    cfg = C.load(cfg_path)
    (ROOT / "vm").mkdir(parents=True, exist_ok=True)
    # this process starts with the container: no VM or dnsmasq of a former run lives, and their PIDs may belong to
    # new processes now
    for pid in [*ROOT.glob("vm/*/qemu.pid"), *ROOT.glob("dhcp/*.pid")]:
        pid.unlink(missing_ok=True)
    key = ROOT / "id_ed25519"
    if not key.is_file():
        subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "hlab", "-f", str(key)], check=True)
    sh = Shell()
    sh.run("net-init")
    state = State(ROOT / "state.json")
    fabric = Fabric(cfg, state, ROOT, sh)
    fabric.write_dns()
    fabric.write_dhcp()
    lab = Lab(cfg, state, fabric)
    load_images(lab)
    box = cfg["storage_box"]
    if box.get("enabled"):
        sh.run("box-start", str(int(box.get("size_gb") or 0)))
        for user, pub in state.data.get("box_users", {}).items():
            p = ROOT / f"{user}.restore.pub"
            p.write_text(pub + "\n")
            fabric.box.lab_account(state.data.setdefault("box_uids", {}), user, str(p))
            p.unlink()
        storagebox.restore(lab)
    api, meta, hetzner = create_app(lab), create_metadata_app(lab), storagebox.create_app(lab)

    async def dispatch(scope, receive, send):  # one server for all addresses, dispatched by address
        host = (scope.get("server") or ("",))[0]
        # the metadata service: on its own addresses (fe80::a9fe:a9fe is DNATed to METADATA6), nothing else there
        if host in (C.METADATA, C.METADATA6):
            return await meta(scope, receive, send)
        if host in (C.HETZNER_API, C.HETZNER_API6):
            return await hetzner(scope, receive, send)
        return await api(scope, receive, send)

    requests = logging.getLogger("hlabd.http")

    async def app(scope, receive, send):  # every request in the log: who, what, answer, how long
        if scope["type"] != "http":
            return await dispatch(scope, receive, send)
        start, status = time.monotonic(), {}

        async def sent(message):
            if message["type"] == "http.response.start":
                status["code"] = message["status"]
            await send(message)
        try:
            await dispatch(scope, receive, sent)
        finally:
            requests.info("%s %s %s %s %.0f ms", (scope.get("client") or ("-",))[0], scope["method"], scope["path"],
                          status.get("code", "-"), (time.monotonic() - start) * 1000)

    async def main():
        lab.loop = asyncio.get_running_loop()   # actions started from worker threads run here (schedulers too)
        # blocking work runs in threads; downloads waiting for each other must not starve the rest
        lab.loop.set_default_executor(concurrent.futures.ThreadPoolExecutor(64))
        await reconcile(lab)
        socks = []
        for addr in (C.SERVICES, C.METADATA, C.SERVICES6, C.METADATA6, C.HETZNER_API, C.HETZNER_API6):
            s = socket.socket(socket.AF_INET6 if ":" in addr else socket.AF_INET, socket.SOCK_STREAM)
            s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            s.bind((addr, 80))
            socks.append(s)
        server = uvicorn.Server(uvicorn.Config(app, log_level="warning", access_log=False, lifespan="off"))

        async def ready():
            while not server.started:
                await asyncio.sleep(0.1)
            print(f"hlab ready: {cfg['name']}, API http://{C.SERVICES}/v1", flush=True)
        asyncio.get_running_loop().create_task(ready())
        asyncio.get_running_loop().create_task(storagebox.snapshots.plan_scheduler(lab))
        asyncio.get_running_loop().create_task(lab.vm_watcher())
        await server.serve(sockets=socks)

    try:
        asyncio.run(main())
    finally:
        # docker stop: the guests shut down like on a power button (docker gives the lab 20 s in all)
        running = [s for s in state["servers"].values() if fabric.vm_alive(s)]
        for s in running:
            try:
                fabric.qmp(s, "system_powerdown")
            except RuntimeError:
                pass
        for _ in range(15):
            if not any(fabric.vm_alive(s) for s in running):
                break
            time.sleep(1)
        sh.run("box-stop", check=False)


def env() -> None:
    """Shell lines for `eval "$(hlab env)"`: exported, so tools started afterwards (docker run -e ...) get them."""
    cfg = C.load(os.environ.get("HLAB_CONFIG"))
    name = os.environ.get("HLAB_NAME", cfg["name"])
    for k, v in (("HLAB_NAME", name), ("HLAB_NETWORK", f"container:hlab-{name}"),
                 ("HCLOUD_ENDPOINT", f"http://{C.SERVICES}/v1"),
                 ("HCLOUD_TOKEN", cfg["api"]["token"] or C.ANY_TOKEN), ("HLAB_CONTROL", CONTROL),
                 ("HLAB_BOX_HOST", C.SERVICES), ("HLAB_BOX_PORT", "23"),
                 ("HLAB_METADATA", f"http://{C.METADATA}/hetzner/v1/metadata"), ("HLAB_SERVICES6", C.SERVICES6),
                 ("HETZNER_ENDPOINT", f"http://{C.HETZNER_API}/v1")):
        print(f"export {k}={shlex.quote(str(v))}")


def status(as_json: bool) -> None:
    st = call("GET", "/status")
    if as_json:
        print(json.dumps(st, indent=2))
        return
    print(f"{'ID':<6}{'NAME':<16}{'STATUS':<14}{'VM':<5}{'LOC':<6}{'TYPE':<7}{'PUBLIC':<16}{'IPV6':<20}PRIVATE")
    for s in st["servers"]:
        priv = ", ".join(p["ip"] for p in s["private"])
        pub = (s["public_ip"] or {}).get("ip", "-")
        print(f"{s['id']:<6}{s['name']:<16}{s['status'] + (' (dead)' if s['dead'] else ''):<14}{s['vm']:<5}"
              f"{s['location']:<6}{s['server_type']:<7}{pub:<16}{s.get('ipv6') or '-':<20}{priv}")
    for f in st["floating_ips"]:
        print(f"floating IP {f['ip']} → server {f['server'] or '-'}")
    for c in st["clients"]:
        print(f"client {c['name']} {c['ip']} {c.get('ip6', '')}")
    for f in st["faults"]:
        print("fault " + " ".join(f"{k}={v}" for k, v in f.items()))
    for tap, args in st["netem"].items():
        print(f"netem {tap}: {args}")
    if st["api_fault"]["count"] or st["api_fault"]["delay_ms"]:
        print(f"API fault: {st['api_fault']}")


def server_ip(name: str) -> str:
    """The public IPv4 of a server, or its IPv6 (::1 of its /64) when it has none."""
    for s in call("GET", "/status")["servers"]:
        if s["name"] == name:
            if s["public_ip"]:
                return s["public_ip"]["ip"]
            if s.get("ipv6"):
                return s["ipv6"].split("/")[0] + "1"
            sys.exit(f"hlab: server {name!r} has no public IP")
    sys.exit(f"hlab: no server named {name!r}")


NOBODY = 65534
SSH_HOME = Path("/run/hlab-ssh")   # the unprivileged client's home with its copy of the lab key


def ssh(name: str, cmd: list[str]) -> None:
    """From the admin address (the lab's own access passes Cloud firewalls, like the console would). The client runs
    as nobody, not as root: it talks to guests, which are not trusted, and the container is privileged."""
    ip = server_ip(name)
    key = SSH_HOME / "id_ed25519"
    if not key.is_file():
        SSH_HOME.mkdir(mode=0o700, exist_ok=True)
        shutil.copyfile(ROOT / "id_ed25519", key)
        for p in (SSH_HOME, key):
            os.chown(p, NOBODY, NOBODY)
        key.chmod(0o600)
    args = ["setpriv", f"--reuid={NOBODY}", f"--regid={NOBODY}", "--clear-groups", "--",
            "ssh", "-q", "-b", C.ADMIN6 if ":" in ip else C.ADMIN, "-i", str(key), "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=5", f"root@{ip}", *cmd]
    os.execvpe("setpriv", args, {**os.environ, "HOME": str(SSH_HOME)})


def console(name: str) -> None:
    st = call("GET", "/status")
    s = next((s for s in st["servers"] if s["name"] == name), None)
    if not s:
        sys.exit(f"hlab: no server named {name!r}")
    os.execvp("tail", ["tail", "-n", "100", "-f", str(ROOT / "vm" / str(s["id"]) / "console.log")])


def main(argv: list[str] | None = None) -> None:
    p = argparse.ArgumentParser(prog="hlab", description="Hetzner Cloud stand-in with QEMU/KVM VMs")
    p.add_argument("-c", "--config", default=os.environ.get("HLAB_CONFIG"))
    sub = p.add_subparsers(dest="cmd", required=True)
    sub.add_parser("serve", help="run the lab (container main process)")
    st = sub.add_parser("status", help="servers, Floating IPs, clients and faults")
    st.add_argument("--json", action="store_true")
    sub.add_parser("env", help="what consumers need: API endpoint, token, network namespace, Storage Box")
    x = sub.add_parser("ssh", help="SSH into a server as root (lab key)")
    x.add_argument("server")
    x.add_argument("command", nargs=argparse.REMAINDER)
    x = sub.add_parser("console", help="follow a server's serial console")
    x.add_argument("server")
    sub.add_parser("reset", help="delete everything the API created (servers, networks, ...)")

    f = sub.add_parser("fault", help="network and machine faults").add_subparsers(dest="fault", required=True)
    for name, hlp in (("cut", "drop traffic between two servers/clients"), ("heal", "undo a cut")):
        x = f.add_parser(name, help=hlp)
        x.add_argument("a")
        x.add_argument("b")
        x.add_argument("--net", choices=("public", "private", "all"), default="all")
    for name in ("isolate", "unisolate"):
        f.add_parser(name, help="cut a server off from everything" if name == "isolate" else "undo isolate").add_argument("vm")
    for name in ("block", "unblock"):
        x = f.add_parser(name, help="keep a server from the API, the Storage Box or the Internet")
        x.add_argument("vm")
        x.add_argument("what", choices=("api", "box", "internet"))
    for name, hlp in (("pause", "freeze a VM (clock stops)"), ("cont", "resume a paused VM"),
                      ("dead", "broken hardware: power-on succeeds, the VM stays off"), ("alive", "repair")):
        f.add_parser(name, help=hlp).add_argument("vm")
    x = f.add_parser("netem", help="tc netem on a server's taps (towards the server), e.g. delay 100ms loss 5%%")
    x.add_argument("vm")
    x.add_argument("--net", choices=("public", "private", "all"), default="all")
    x.add_argument("args", nargs=argparse.REMAINDER)
    x = f.add_parser("api", help="answer the next COUNT API requests with STATUS, or delay all by --delay-ms")
    x.add_argument("status", type=int, nargs="?", default=0)
    x.add_argument("count", type=int, nargs="?", default=0)
    x.add_argument("--delay-ms", type=int, default=0)
    f.add_parser("clear", help="remove all faults")

    c = sub.add_parser("client", help="'Internet' clients (own addresses, no NAT)").add_subparsers(dest="client", required=True)
    c.add_parser("add").add_argument("name")
    c.add_parser("rm").add_argument("name")
    c.add_parser("list")
    x = c.add_parser("exec", help="run a command in the client, e.g. curl")
    x.add_argument("name")
    x.add_argument("command", nargs=argparse.REMAINDER)

    b = sub.add_parser("box", help="Storage Box stand-in").add_subparsers(dest="box", required=True)
    x = b.add_parser("user", help="add an SFTP account (public key from a file or '-' for stdin)")
    x.add_argument("name")
    x.add_argument("key_file")

    a = p.parse_args(argv)
    if a.config:
        os.environ["HLAB_CONFIG"] = a.config
    match a.cmd:
        case "serve":
            serve(a.config)
        case "status":
            status(a.json)
        case "env":
            env()
        case "ssh":
            ssh(a.server, a.command)
        case "console":
            console(a.server)
        case "reset":
            call("POST", "/reset")
            print("hlab reset")
        case "fault":
            fault(a)
        case "client":
            client(a)
        case "box":
            key = sys.stdin.read() if a.key_file == "-" else Path(a.key_file).read_text()
            r = call("POST", "/box/users", {"name": a.name, "public_key": key.strip()})
            print(f"sftp -P {r['port']} {a.name}@{r['host']}")


def fault(a) -> None:
    match a.fault:
        case "cut":
            call("POST", "/faults", {"type": "cut", "a": a.a, "b": a.b, "net": a.net})
        case "heal":
            call("POST", "/faults/remove", {"type": "cut", "a": a.a, "b": a.b, "net": a.net})
            call("POST", "/faults/remove", {"type": "cut", "a": a.b, "b": a.a, "net": a.net})
        case "isolate":
            call("POST", "/faults", {"type": "isolate", "vm": a.vm})
        case "unisolate":
            call("POST", "/faults/remove", {"type": "isolate", "vm": a.vm})
        case "block":
            call("POST", "/faults", {"type": "block", "vm": a.vm, "what": a.what})
        case "unblock":
            call("POST", "/faults/remove", {"type": "block", "vm": a.vm, "what": a.what})
        case "pause" | "cont" | "dead" | "alive":
            call("POST", f"/vm/{a.vm}/{a.fault}")
        case "netem":
            call("POST", "/netem", {"vm": a.vm, "net": a.net, "args": " ".join(a.args)})
        case "api":
            call("POST", "/api-fault", {"status": a.status, "count": a.count, "delay_ms": a.delay_ms})
        case "clear":
            call("POST", "/faults/clear")


def client(a) -> None:
    match a.client:
        case "add":
            c = call("POST", "/clients", {"name": a.name})["client"]
            print(f"client {c['name']} at {c['ip']} and {c['ip6']}")
        case "rm":
            call("DELETE", f"/clients/{a.name}")
        case "list":
            for c in call("GET", "/status")["clients"]:
                print(f"{c['name']} {c['ip']} {c.get('ip6', '')}")
        case "exec":
            os.execvp("ip", ["ip", "netns", "exec", a.name, *a.command])
