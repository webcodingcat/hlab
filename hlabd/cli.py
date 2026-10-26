"""The lab command inside the container.

`serve` runs the lab (container main process, see daemon.py); every other command talks
to it through the control API on http://198.18.0.1/_lab/.
"""

import argparse
import json
import os
import shlex
import shutil
import sys
import urllib.error
import urllib.request
from pathlib import Path

from . import config

CONTROL = f"http://{config.SERVICES}/_lab"
NOBODY = 65534
# the unprivileged client's home with its copy of the lab key
SSH_HOME = Path("/run/hlab-ssh")


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
        # not the lab's JSON (a proxy's page, a cut-off answer): report the HTTP error
        except Exception:  # noqa: BLE001
            message = str(error)
        sys.exit(f"hlab: {message}")
    except urllib.error.URLError as error:
        sys.exit(f"hlab: control API not reachable ({error.reason}) – is the lab up?")


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
    report = call("GET", "/status")
    if as_json:
        print(json.dumps(report, indent=2))
        return
    print(
        f"{'ID':<6}{'NAME':<16}{'STATUS':<14}{'VM':<5}{'LOC':<6}{'TYPE':<7}"
        f"{'PUBLIC':<16}{'IPV6':<20}PRIVATE"
    )
    for s in report["servers"]:
        private = ", ".join(p["ip"] for p in s["private"])
        public = s["public_ip"] or "-"
        print(
            f"{s['id']:<6}{s['name']:<16}"
            f"{s['status'] + (' (dead)' if s['dead'] else ''):<14}{s['vm']:<5}"
            f"{s['location']:<6}{s['server_type']:<7}{public:<16}"
            f"{s.get('ipv6') or '-':<20}{private}"
        )
    for f in report["floating_ips"]:
        print(f"Floating IP {f['ip']} → server {f['server'] or '-'}")
    for c in report["clients"]:
        print(f"client {c['name']} {c['ip']} {c.get('ip6', '')}")
    for f in report["faults"]:
        print("fault " + " ".join(f"{k}={v}" for k, v in f.items()))
    for tap, args in report["netem"].items():
        print(f"netem {tap}: {args}")
    if report["api_fault"]["count"] or report["api_fault"]["delay_ms"]:
        print(f"API fault: {report['api_fault']}")


def server_ip(name: str) -> str:
    """Return a server's public IPv4, or its IPv6 (::1 of its /64) if it has none."""
    for s in call("GET", "/status")["servers"]:
        if s["name"] == name:
            if s["public_ip"]:
                return s["public_ip"]
            if s.get("ipv6"):
                return s["ipv6"].split("/")[0] + "1"
            sys.exit(f"hlab: server {name!r} has no public IP")
    sys.exit(f"hlab: no server named {name!r}")


def ssh(name: str, cmd: list[str]) -> None:
    """SSH into a server as root, from the admin address.

    Like Hetzner's console, the lab's own access passes Cloud firewalls. The client
    runs as nobody, not as root: it talks to guests, which are not trusted, and the
    container is privileged.
    """
    ip = server_ip(name)
    key = SSH_HOME / "id_ed25519"
    if not key.is_file():
        SSH_HOME.mkdir(mode=0o700, exist_ok=True)
        shutil.copyfile(config.ROOT / "id_ed25519", key)
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
    report = call("GET", "/status")
    server = next((s for s in report["servers"] if s["name"] == name), None)
    if not server:
        sys.exit(f"hlab: no server named {name!r}")
    os.execvp(
        "tail",
        [
            "tail",
            "-n",
            "100",
            "-f",
            str(config.ROOT / "vm" / str(server["id"]) / "console.log"),
        ],
    )


def build_parser() -> argparse.ArgumentParser:
    """Return the parser of the command line."""
    parser = argparse.ArgumentParser(
        prog="hlab", description="Hetzner Cloud stand-in with QEMU/KVM VMs"
    )
    parser.add_argument("-c", "--config", default=os.environ.get("HLAB_CONFIG"))
    commands = parser.add_subparsers(dest="cmd", required=True)
    commands.add_parser("serve", help="run the lab (container main process)")
    status_parser = commands.add_parser(
        "status", help="servers, Floating IPs, clients and faults"
    )
    status_parser.add_argument("--json", action="store_true")
    commands.add_parser(
        "env",
        help="what consumers need: API endpoint, token, network namespace, Storage Box",
    )
    ssh_parser = commands.add_parser("ssh", help="SSH into a server as root (lab key)")
    ssh_parser.add_argument("server")
    ssh_parser.add_argument("command", nargs=argparse.REMAINDER)
    commands.add_parser(
        "console", help="follow a server's serial console"
    ).add_argument("server")
    commands.add_parser(
        "reset", help="delete everything the API created (servers, networks, ...)"
    )

    fault_parser = commands.add_parser("fault", help="network and machine faults")
    fault_commands = fault_parser.add_subparsers(dest="fault", required=True)
    for name, help_text in (
        ("cut", "drop traffic between two servers/clients"),
        ("heal", "undo a cut"),
    ):
        cut_parser = fault_commands.add_parser(name, help=help_text)
        cut_parser.add_argument("first")
        cut_parser.add_argument("second")
        cut_parser.add_argument(
            "--net", choices=("public", "private", "all"), default="all"
        )
    for name, help_text in (
        ("isolate", "cut a server off from everything"),
        ("unisolate", "undo isolate"),
    ):
        fault_commands.add_parser(name, help=help_text).add_argument("vm")
    for name in ("block", "unblock"):
        block_parser = fault_commands.add_parser(
            name, help="keep a server from the API, the Storage Box or the Internet"
        )
        block_parser.add_argument("vm")
        block_parser.add_argument("what", choices=("api", "box", "internet"))
    for name, help_text in (
        ("pause", "freeze a VM (clock stops)"),
        ("cont", "resume a paused VM"),
        ("dead", "broken hardware: power-on succeeds, the VM stays off"),
        ("alive", "repair"),
    ):
        fault_commands.add_parser(name, help=help_text).add_argument("vm")
    netem_parser = fault_commands.add_parser(
        "netem",
        help="tc netem on a server's taps (towards the server), "
        "e.g. delay 100ms loss 5%%",
    )
    netem_parser.add_argument("vm")
    netem_parser.add_argument(
        "--net", choices=("public", "private", "all"), default="all"
    )
    netem_parser.add_argument("args", nargs=argparse.REMAINDER)
    api_parser = fault_commands.add_parser(
        "api",
        help="answer the next COUNT API requests with STATUS, "
        "or delay all by --delay-ms",
    )
    api_parser.add_argument("status", type=int, nargs="?", default=0)
    api_parser.add_argument("count", type=int, nargs="?", default=0)
    api_parser.add_argument("--delay-ms", type=int, default=0)
    fault_commands.add_parser("clear", help="remove all faults")

    client_parser = commands.add_parser(
        "client", help="'Internet' clients (own addresses, no NAT)"
    )
    client_commands = client_parser.add_subparsers(dest="client", required=True)
    client_commands.add_parser("add").add_argument("name")
    client_commands.add_parser("rm").add_argument("name")
    client_commands.add_parser("list")
    exec_parser = client_commands.add_parser(
        "exec", help="run a command in the client, e.g. curl"
    )
    exec_parser.add_argument("name")
    exec_parser.add_argument("command", nargs=argparse.REMAINDER)

    box_parser = commands.add_parser("box", help="Storage Box stand-in")
    box_commands = box_parser.add_subparsers(dest="box", required=True)
    user_parser = box_commands.add_parser(
        "user", help="add an SFTP account (public key from a file or '-' for stdin)"
    )
    user_parser.add_argument("name")
    user_parser.add_argument("key_file")
    return parser


def main(argv: list[str] | None = None) -> None:
    """Parse the command line and run the command."""
    args = build_parser().parse_args(argv)
    if args.config:
        os.environ["HLAB_CONFIG"] = args.config
    match args.cmd:
        case "serve":
            # imported here: the other commands do not need the daemon's dependencies
            from .daemon import serve

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
            run_fault_command(args)
        case "client":
            run_client_command(args)
        case "box":
            add_box_user(args)


def add_box_user(args) -> None:
    """Run `hlab box user`: add an SFTP account and print how to reach it."""
    key = sys.stdin.read() if args.key_file == "-" else Path(args.key_file).read_text()
    response = call(
        "POST", "/box/users", {"name": args.name, "public_key": key.strip()}
    )
    print(f"sftp -P {response['port']} {args.name}@{response['host']}")


def cut_fault_body(first: str, second: str, net: str) -> dict:
    """Return the body of a cut fault between two servers (or clients)."""
    return {"type": "cut", "first": first, "second": second, "net": net}


def run_fault_command(args) -> None:
    """Run an `hlab fault` subcommand."""
    match args.fault:
        case "cut":
            call("POST", "/faults", cut_fault_body(args.first, args.second, args.net))
        case "heal":  # the lab removes a cut either way round
            call(
                "POST",
                "/faults/remove",
                cut_fault_body(args.first, args.second, args.net),
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


def run_client_command(args) -> None:
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
