"""The lab daemon: the container's main process (`hlab serve`).

It sets the lab up (state, fabric, images), brings back what ran before (api/restart.py)
and serves three apps on one HTTP server, chosen by the address a request arrives at:
the Cloud API (and lab control under /_lab/), the Storage Box API (api.hetzner.com
stand-in) and the metadata service. On `docker stop` the guests are powered down.
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
from .api import storagebox
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
    """Prepare the container and return the lab: state, fabric, network, images."""
    (config.ROOT / "vm").mkdir(parents=True, exist_ok=True)
    # this process starts with the container: no VM of a former run lives, and their
    # PIDs may belong to new processes now
    for pid_file in config.ROOT.glob("vm/*/qemu.pid"):
        pid_file.unlink(missing_ok=True)
    key = config.ROOT / "id_ed25519"
    if not key.is_file():
        command = ["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "hlab"]
        subprocess.run([*command, "-f", str(key)], check=True)
    shell = Shell()
    shell.run("net-init")
    state = State(config.ROOT / "state.json")
    fabric = Fabric(cfg, state, config.ROOT, shell)
    fabric.write_dns()
    fabric.write_dhcp()
    lab = Lab(cfg, state, fabric)
    load_images(lab)
    return lab


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
    """Bring the lab back, then serve until stopped (with the background jobs)."""
    # actions started from worker threads run on this loop (the schedulers too);
    # blocking work runs in threads, and downloads waiting for each other must not
    # starve the rest
    lab.loop = asyncio.get_running_loop()
    lab.loop.set_default_executor(concurrent.futures.ThreadPoolExecutor(64))
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

    lab.loop.create_task(announce_ready())
    lab.loop.create_task(storagebox.snapshots.snapshot_plan_scheduler(lab))
    lab.loop.create_task(lab.vm_watcher())
    await server.serve(sockets=listeners())


def shut_down(lab: Lab) -> None:
    """Power the guests down like the power button, then stop the Storage Box.

    `docker stop` gives the lab 20 seconds in all.
    """
    fabric = lab.fabric
    with lab.state.lock:
        servers = list(lab.state["servers"].values())
    running = [s for s in servers if fabric.vm_alive(s)]
    for server in running:
        try:
            fabric.press_power_button(server)
        except RuntimeError:  # its QEMU does not answer: it is killed with the rest
            pass
    for _ in range(15):
        if not any(fabric.vm_alive(s) for s in running):
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
