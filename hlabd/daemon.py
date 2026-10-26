"""The lab daemon: the container's main process (`hlab serve`).

It sets up the lab (state, fabric, images) and brings back what ran before
(api/restart.py). Then it serves three apps on one HTTP server. The address that a
request arrives at picks the app: the Cloud API (with lab control under /_lab/), the
Storage Box API (the stand-in for api.hetzner.com), or the metadata service. On
`docker stop` the guests are powered down.
"""

import asyncio
import concurrent.futures
import logging
import socket
import subprocess
import sys
import time

import uvicorn

from . import config
from .api import storagebox, system
from .api.app import create_app
from .api.catalogue import load_images
from .api.metadata import create_metadata_app
from .api.restart import restore_lab
from .fabric import Fabric, Shell
from .lab import Lab
from .state import State

# the addresses the HTTP server listens on (port 80)
LISTEN = (
    config.SERVICES,
    config.METADATA,
    config.SERVICES6,
    config.METADATA6,
    config.HETZNER_API,
    config.HETZNER_API6,
)

log = logging.getLogger(__name__)


def set_up(cfg: dict) -> Lab:
    """Prepare the container and return the lab: state, fabric, network."""
    (config.ROOT / "vm").mkdir(parents=True, exist_ok=True)
    # this process starts with the container: no VM of an earlier run is alive, and
    # their PIDs may belong to other processes now
    for pid_file in config.ROOT.glob("vm/*/qemu.pid"):
        pid_file.unlink(missing_ok=True)
    key = config.ROOT / "id_ed25519"
    if not key.is_file():
        command = ["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "hlab"]
        subprocess.run([*command, "-f", str(key)], check=True)
    shell = Shell()
    shell.run("init-net")
    state = State(config.ROOT / "state.json")
    fabric = Fabric(cfg, config.ROOT, shell)
    fabric.network.write_dns()
    return Lab(cfg, state, fabric)


def http_app(lab: Lab):
    """Return the ASGI app of the lab: each request goes to the app of its address.

    Every request is logged: who, what, answer, how long.
    """
    cloud_api = create_app(lab)
    box_api = storagebox.create_app(lab)
    metadata = create_metadata_app(lab)

    async def dispatch(scope, receive, send):
        host = (scope.get("server") or ("",))[0]
        # the metadata service: on its own addresses (fe80::a9fe:a9fe is DNATed to
        # METADATA6), nothing else there
        if host in (config.METADATA, config.METADATA6):
            return await metadata(scope, receive, send)
        if host in (config.HETZNER_API, config.HETZNER_API6):
            return await box_api(scope, receive, send)
        return await cloud_api(scope, receive, send)

    async def logged(scope, receive, send):
        if scope["type"] != "http":
            return await dispatch(scope, receive, send)
        start, answer = time.monotonic(), {}

        async def send_and_note(message):
            if message["type"] == "http.response.start":
                answer["status"] = message["status"]
            await send(message)

        try:
            await dispatch(scope, receive, send_and_note)
        finally:
            log.info(
                "%s %s %s %s %.0f ms",
                (scope.get("client") or ("-",))[0],
                scope["method"],
                scope["path"],
                answer.get("status", "-"),
                (time.monotonic() - start) * 1000,
            )

    return logged


def listeners() -> list[socket.socket]:
    """Return sockets bound to the lab's service addresses (port 80)."""
    sockets = []
    for address in LISTEN:
        family = socket.AF_INET6 if ":" in address else socket.AF_INET
        listener = socket.socket(family, socket.SOCK_STREAM)
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind((address, 80))
        sockets.append(listener)
    return sockets


async def run(lab: Lab) -> None:
    """Bring the lab back (DHCP, images, state), then serve until stopped.

    The background jobs run as long as the server does.
    """
    # this loop owns the state. Blocking work runs in threads; the pool is large, so
    # downloads that wait for each other do not use up every thread
    lab.own_state()
    loop = asyncio.get_running_loop()
    loop.set_default_executor(concurrent.futures.ThreadPoolExecutor(64))
    await system.write_dhcp(lab)
    await lab.blocking(load_images, lab)  # downloads; only `lab.images` changes
    await restore_lab(lab)
    server = uvicorn.Server(
        uvicorn.Config(
            http_app(lab), log_level="warning", access_log=False, lifespan="off"
        )
    )

    async def announce_ready():
        while not server.started:
            await asyncio.sleep(0.1)
        print(
            f"hlab ready: {lab.cfg['name']}, API http://{config.SERVICES}/v1",
            flush=True,
        )

    lab.jobs.update(
        loop.create_task(job)
        for job in (
            announce_ready(),
            storagebox.snapshots.snapshot_plan_scheduler(lab),
            lab.vm_watcher(),
        )
    )
    await server.serve(sockets=listeners())


def shut_down(lab: Lab) -> None:
    """Power the guests down like the power button, then stop the Storage Box.

    `docker stop` gives the lab 20 seconds in total. First the last save is written to
    disk. The loop has ended, but this runs on its thread, which still owns the state.
    """
    fabric = lab.fabric
    lab.state.flush()
    servers = list(lab.state["servers"].values())
    running = [s for s in servers if fabric.vms.is_alive(s)]
    for server in running:
        try:
            fabric.vms.press_power_button(server)
        except RuntimeError:  # its QEMU does not answer: it is killed with the rest
            pass
    for _ in range(15):
        if not any(fabric.vms.is_alive(s) for s in running):
            break
        time.sleep(1)
    fabric.box.stop_box()


def serve(cfg_path: str | None) -> None:
    """Run the lab until the container stops."""
    # one line per event on stdout (docker logs): time, level, part of the lab, message
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        stream=sys.stdout,
        force=True,
    )
    lab = set_up(config.load(cfg_path))
    try:
        asyncio.run(run(lab))
    finally:
        shut_down(lab)
