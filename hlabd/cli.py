"""The lab command inside the container.

`serve` runs the lab (container main process); every other command talks to it through
the control API on http://198.18.0.1/_lab/.
"""

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

from . import config
from .api import storagebox

ROOT = Path("/hlab")
CONTROL = f"http://{config.SERVICES}/_lab"


def call(method: str, path: str, body: dict | None = None) -> dict:
    """Send a request to the control API and return its JSON answer.

    Exits with the error message if the request fails.
    """
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(
        CONTROL + path,
        data=data,
        method=method,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            return json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as error:
        try:
            message = json.loads(error.read())["error"]["message"]
        except Exception:  # noqa: BLE001
            message = str(error)
        sys.exit(f"hlab: {message}")
    except urllib.error.URLError as error:
        sys.exit(f"hlab: control API not reachable ({error.reason}) – is the lab up?")


def serve(cfg_path: str | None) -> None:
    """Run the lab: the fake APIs, the metadata service and the VM watcher.

    On exit (docker stop), the guests are powered down and the Storage Box stops.
    """
    import uvicorn

    from .api.app import create_app
    from .api.catalogue import load_images
    from .api.metadata import create_metadata_app
    from .api.restart import restart
    from .fabric import Fabric, Shell
    from .lab import Lab
    from .state import State

    # one line per event on stdout (docker logs): time, level, part of the lab, message
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        stream=sys.stdout,
        force=True,
    )
    cfg = config.load(cfg_path)
    (ROOT / "vm").mkdir(parents=True, exist_ok=True)
    # this process starts with the container: no VM or dnsmasq of a former run lives,
    # and their PIDs may belong to new processes now
    for pid in [*ROOT.glob("vm/*/qemu.pid"), *ROOT.glob("dhcp/*.pid")]:
        pid.unlink(missing_ok=True)
    key = ROOT / "id_ed25519"
    if not key.is_file():
        subprocess.run(
            [
                "ssh-keygen",
                "-q",
                "-t",
                "ed25519",
                "-N",
                "",
                "-C",
                "hlab",
                "-f",
                str(key),
            ],
            check=True,
        )
    sh = Shell()
    sh.run("net-init")
    state = State(ROOT / "state.json")
    fabric = Fabric(cfg, state, ROOT, sh)
    fabric.write_dns()
    fabric.write_dhcp()
    lab = Lab(cfg, state, fabric)
    load_images(lab)
    api, meta, hetzner = (
        create_app(lab),
        create_metadata_app(lab),
        storagebox.create_app(lab),
    )

    # one server for all addresses, dispatched by address
    async def dispatch(scope, receive, send):
        host = (scope.get("server") or ("",))[0]
        # the metadata service: on its own addresses (fe80::a9fe:a9fe is DNATed to
        # METADATA6), nothing else there
        if host in (config.METADATA, config.METADATA6):
            return await meta(scope, receive, send)
        if host in (config.HETZNER_API, config.HETZNER_API6):
            return await hetzner(scope, receive, send)
        return await api(scope, receive, send)

    requests = logging.getLogger("hlabd.http")

    # every request in the log: who, what, answer, how long
    async def app(scope, receive, send):
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
            requests.info(
                "%s %s %s %s %.0f ms",
                (scope.get("client") or ("-",))[0],
                scope["method"],
                scope["path"],
                status.get("code", "-"),
                (time.monotonic() - start) * 1000,
            )

    async def main():
        # actions started from worker threads run here (schedulers too)
        lab.loop = asyncio.get_running_loop()
        # blocking work runs in threads; downloads waiting for each other must not
        # starve the rest
        lab.loop.set_default_executor(concurrent.futures.ThreadPoolExecutor(64))
        await restart(lab)
        socks = []
        for addr in (
            config.SERVICES,
            config.METADATA,
            config.SERVICES6,
            config.METADATA6,
            config.HETZNER_API,
            config.HETZNER_API6,
        ):
            listener = socket.socket(
                socket.AF_INET6 if ":" in addr else socket.AF_INET, socket.SOCK_STREAM
            )
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            listener.bind((addr, 80))
            socks.append(listener)
        server = uvicorn.Server(
            uvicorn.Config(app, log_level="warning", access_log=False, lifespan="off")
        )

        async def ready():
            while not server.started:
                await asyncio.sleep(0.1)
            print(
                f"hlab ready: {cfg['name']}, API http://{config.SERVICES}/v1",
                flush=True,
            )

        asyncio.get_running_loop().create_task(ready())
        asyncio.get_running_loop().create_task(storagebox.snapshots.plan_scheduler(lab))
        asyncio.get_running_loop().create_task(lab.vm_watcher())
        await server.serve(sockets=socks)

    try:
        asyncio.run(main())
    finally:
        # docker stop: the guests shut down like on a power button (docker gives the lab
        # 20 s in all)
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
        fabric.box.stop()


def env() -> None:
    """Print shell lines for `eval "$(hlab env)"`.

    The variables are exported, so tools started afterwards (docker run -e ...) get
    them.
    """
    cfg = config.load(os.environ.get("HLAB_CONFIG"))
    name = os.environ.get("HLAB_NAME", cfg["name"])
    for k, v in (
        ("HLAB_NAME", name),
        ("HLAB_NETWORK", f"container:hlab-{name}"),
        ("HCLOUD_ENDPOINT", f"http://{config.SERVICES}/v1"),
        ("HCLOUD_TOKEN", cfg["api"]["token"] or config.ANY_TOKEN),
        ("HLAB_CONTROL", CONTROL),
        ("HLAB_BOX_HOST", config.SERVICES),
        ("HLAB_BOX_PORT", "23"),
        ("HLAB_METADATA", f"http://{config.METADATA}/hetzner/v1/metadata"),
        ("HLAB_SERVICES6", config.SERVICES6),
        ("HETZNER_ENDPOINT", f"http://{config.HETZNER_API}/v1"),
    ):
        print(f"export {k}={shlex.quote(str(v))}")


def status(as_json: bool) -> None:
    """Print the lab's servers, Floating IPs, clients and faults (or all as JSON)."""
    status = call("GET", "/status")
    if as_json:
        print(json.dumps(status, indent=2))
        return
    print(
        f"{'ID':<6}{'NAME':<16}{'STATUS':<14}{'VM':<5}{'LOC':<6}{'TYPE':<7}"
        f"{'PUBLIC':<16}{'IPV6':<20}PRIVATE"
    )
    for s in status["servers"]:
        private = ", ".join(p["ip"] for p in s["private"])
        public = (s["public_ip"] or {}).get("ip", "-")
        print(
            f"{s['id']:<6}{s['name']:<16}"
            f"{s['status'] + (' (dead)' if s['dead'] else ''):<14}{s['vm']:<5}"
            f"{s['location']:<6}{s['server_type']:<7}{public:<16}"
            f"{s.get('ipv6') or '-':<20}{private}"
        )
    for f in status["floating_ips"]:
        print(f"floating IP {f['ip']} → server {f['server'] or '-'}")
    for c in status["clients"]:
        print(f"client {c['name']} {c['ip']} {c.get('ip6', '')}")
    for f in status["faults"]:
        print("fault " + " ".join(f"{k}={v}" for k, v in f.items()))
    for tap, args in status["netem"].items():
        print(f"netem {tap}: {args}")
    if status["api_fault"]["count"] or status["api_fault"]["delay_ms"]:
        print(f"API fault: {status['api_fault']}")


def server_ip(name: str) -> str:
    """Return the public IPv4 of a server, or its IPv6 (::1 of its /64) without one."""
    for s in call("GET", "/status")["servers"]:
        if s["name"] == name:
            if s["public_ip"]:
                return s["public_ip"]["ip"]
            if s.get("ipv6"):
                return s["ipv6"].split("/")[0] + "1"
            sys.exit(f"hlab: server {name!r} has no public IP")
    sys.exit(f"hlab: no server named {name!r}")


NOBODY = 65534
# the unprivileged client's home with its copy of the lab key
SSH_HOME = Path("/run/hlab-ssh")


def ssh(name: str, cmd: list[str]) -> None:
    """SSH into a server as root, from the admin address.

    The lab's own access passes Cloud firewalls, like the console would. The client
    runs as nobody, not as root: it talks to guests, which are not trusted, and the
    container is privileged.
    """
    ip = server_ip(name)
    key = SSH_HOME / "id_ed25519"
    if not key.is_file():
        SSH_HOME.mkdir(mode=0o700, exist_ok=True)
        shutil.copyfile(ROOT / "id_ed25519", key)
        for p in (SSH_HOME, key):
            os.chown(p, NOBODY, NOBODY)
        key.chmod(0o600)
    args = [
        "setpriv",
        f"--reuid={NOBODY}",
        f"--regid={NOBODY}",
        "--clear-groups",
        "--",
        "ssh",
        "-q",
        "-b",
        config.ADMIN6 if ":" in ip else config.ADMIN,
        "-i",
        str(key),
        "-o",
        "StrictHostKeyChecking=no",
        "-o",
        "UserKnownHostsFile=/dev/null",
        "-o",
        "ConnectTimeout=5",
        f"root@{ip}",
        *cmd,
    ]
    os.execvpe("setpriv", args, {**os.environ, "HOME": str(SSH_HOME)})


def console(name: str) -> None:
    """Follow a server's serial console log."""
    status = call("GET", "/status")
    server = next((s for s in status["servers"] if s["name"] == name), None)
    if not server:
        sys.exit(f"hlab: no server named {name!r}")
    os.execvp(
        "tail",
        [
            "tail",
            "-n",
            "100",
            "-f",
            str(ROOT / "vm" / str(server["id"]) / "console.log"),
        ],
    )


def main(argv: list[str] | None = None) -> None:
    """Parse the command line and run the command."""
    parser = argparse.ArgumentParser(
        prog="hlab", description="Hetzner Cloud stand-in with QEMU/KVM VMs"
    )
    parser.add_argument("-c", "--config", default=os.environ.get("HLAB_CONFIG"))
    subparsers = parser.add_subparsers(dest="cmd", required=True)
    subparsers.add_parser("serve", help="run the lab (container main process)")
    status_parser = subparsers.add_parser(
        "status", help="servers, Floating IPs, clients and faults"
    )
    status_parser.add_argument("--json", action="store_true")
    subparsers.add_parser(
        "env",
        help="what consumers need: API endpoint, token, network namespace, Storage Box",
    )
    command_parser = subparsers.add_parser(
        "ssh", help="SSH into a server as root (lab key)"
    )
    command_parser.add_argument("server")
    command_parser.add_argument("command", nargs=argparse.REMAINDER)
    command_parser = subparsers.add_parser(
        "console", help="follow a server's serial console"
    )
    command_parser.add_argument("server")
    subparsers.add_parser(
        "reset", help="delete everything the API created (servers, networks, ...)"
    )

    fault_parser = subparsers.add_parser("fault", help="network and machine faults")
    fault_parser = fault_parser.add_subparsers(dest="fault", required=True)
    for name, help_text in (
        ("cut", "drop traffic between two servers/clients"),
        ("heal", "undo a cut"),
    ):
        command_parser = fault_parser.add_parser(name, help=help_text)
        command_parser.add_argument("a")
        command_parser.add_argument("b")
        command_parser.add_argument(
            "--net", choices=("public", "private", "all"), default="all"
        )
    for name in ("isolate", "unisolate"):
        help_text = (
            "cut a server off from everything" if name == "isolate" else "undo isolate"
        )
        fault_parser.add_parser(name, help=help_text).add_argument("vm")
    for name in ("block", "unblock"):
        command_parser = fault_parser.add_parser(
            name, help="keep a server from the API, the Storage Box or the Internet"
        )
        command_parser.add_argument("vm")
        command_parser.add_argument("what", choices=("api", "box", "internet"))
    for name, help_text in (
        ("pause", "freeze a VM (clock stops)"),
        ("cont", "resume a paused VM"),
        ("dead", "broken hardware: power-on succeeds, the VM stays off"),
        ("alive", "repair"),
    ):
        fault_parser.add_parser(name, help=help_text).add_argument("vm")
    command_parser = fault_parser.add_parser(
        "netem",
        help="tc netem on a server's taps (towards the server), "
        "e.g. delay 100ms loss 5%%",
    )
    command_parser.add_argument("vm")
    command_parser.add_argument(
        "--net", choices=("public", "private", "all"), default="all"
    )
    command_parser.add_argument("args", nargs=argparse.REMAINDER)
    command_parser = fault_parser.add_parser(
        "api",
        help="answer the next COUNT API requests with STATUS, "
        "or delay all by --delay-ms",
    )
    command_parser.add_argument("status", type=int, nargs="?", default=0)
    command_parser.add_argument("count", type=int, nargs="?", default=0)
    command_parser.add_argument("--delay-ms", type=int, default=0)
    fault_parser.add_parser("clear", help="remove all faults")

    client_parser = subparsers.add_parser(
        "client", help="'Internet' clients (own addresses, no NAT)"
    )
    client_parser = client_parser.add_subparsers(dest="client", required=True)
    client_parser.add_parser("add").add_argument("name")
    client_parser.add_parser("rm").add_argument("name")
    client_parser.add_parser("list")
    command_parser = client_parser.add_parser(
        "exec", help="run a command in the client, e.g. curl"
    )
    command_parser.add_argument("name")
    command_parser.add_argument("command", nargs=argparse.REMAINDER)

    box_parser = subparsers.add_parser(
        "box", help="Storage Box stand-in"
    ).add_subparsers(dest="box", required=True)
    command_parser = box_parser.add_parser(
        "user", help="add an SFTP account (public key from a file or '-' for stdin)"
    )
    command_parser.add_argument("name")
    command_parser.add_argument("key_file")

    args = parser.parse_args(argv)
    if args.config:
        os.environ["HLAB_CONFIG"] = args.config
    match args.cmd:
        case "serve":
            serve(args.config)
        case "status":
            status(args.json)
        case "env":
            env()
        case "ssh":
            ssh(args.server, args.command)
        case "console":
            console(args.server)
        case "reset":
            call("POST", "/reset")
            print("hlab reset")
        case "fault":
            fault(args)
        case "client":
            client(args)
        case "box":
            key = (
                sys.stdin.read()
                if args.key_file == "-"
                else Path(args.key_file).read_text()
            )
            response = call(
                "POST", "/box/users", {"name": args.name, "public_key": key.strip()}
            )
            print(f"sftp -P {response['port']} {args.name}@{response['host']}")


def fault(args) -> None:
    """Run an `hlab fault` subcommand."""
    match args.fault:
        case "cut":
            call(
                "POST",
                "/faults",
                {"type": "cut", "a": args.a, "b": args.b, "net": args.net},
            )
        case "heal":
            call(
                "POST",
                "/faults/remove",
                {"type": "cut", "a": args.a, "b": args.b, "net": args.net},
            )
            call(
                "POST",
                "/faults/remove",
                {"type": "cut", "a": args.b, "b": args.a, "net": args.net},
            )
        case "isolate":
            call("POST", "/faults", {"type": "isolate", "vm": args.vm})
        case "unisolate":
            call("POST", "/faults/remove", {"type": "isolate", "vm": args.vm})
        case "block":
            call("POST", "/faults", {"type": "block", "vm": args.vm, "what": args.what})
        case "unblock":
            call(
                "POST",
                "/faults/remove",
                {"type": "block", "vm": args.vm, "what": args.what},
            )
        case "pause" | "cont" | "dead" | "alive":
            call("POST", f"/vm/{args.vm}/{args.fault}")
        case "netem":
            call(
                "POST",
                "/netem",
                {"vm": args.vm, "net": args.net, "args": " ".join(args.args)},
            )
        case "api":
            call(
                "POST",
                "/api-fault",
                {"status": args.status, "count": args.count, "delay_ms": args.delay_ms},
            )
        case "clear":
            call("POST", "/faults/clear")


def client(args) -> None:
    """Run an `hlab client` subcommand."""
    match args.client:
        case "add":
            client_info = call("POST", "/clients", {"name": args.name})["client"]
            print(
                f"client {client_info['name']} at {client_info['ip']} "
                f"and {client_info['ip6']}"
            )
        case "rm":
            call("DELETE", f"/clients/{args.name}")
        case "list":
            for client_info in call("GET", "/status")["clients"]:
                print(
                    f"{client_info['name']} {client_info['ip']} "
                    f"{client_info.get('ip6', '')}"
                )
        case "exec":
            os.execvp("ip", ["ip", "netns", "exec", args.name, *args.command])
