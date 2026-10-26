"""Access to scripts/hlab.sh and to a VM's QEMU guest agent.

A fake shell replaces it in the tests.
"""

import json
import os
import secrets
import socket
import subprocess
import time
from pathlib import Path

from .. import config

HLAB_SH = "/opt/hlab/scripts/hlab.sh"

# How long a command may run, in seconds. A hung script would hold its thread and a
# lab reset that waits for it (`Lab.stop_actions`). Most commands end in a second.
TIMEOUT = 120
# for commands that copy data: image downloads, Storage Box snapshots and rollbacks,
# formatting or removing big files
LONG_TIMEOUT = 3600


class Shell:
    """Runs scripts/hlab.sh commands and talks to QEMU guest agents.

    The scripts get the lab's addresses from config.py as HLAB_* environment variables,
    so config.py is their only source.
    """

    def __init__(self):
        """Prepare the environment the scripts run with."""
        self.env = {**os.environ, **config.script_env()}

    def run(
        self,
        *args: str,
        check: bool = True,
        stdin_text: str | None = None,
        timeout: float = TIMEOUT,
    ) -> str:
        """Run an hlab.sh command and return its output; raise on failure if checked.

        `stdin_text` is written to the command's standard input (e.g. a password). A
        command that runs longer than `timeout` seconds is killed, and this raises
        RuntimeError, whether checked or not.
        """
        try:
            process = subprocess.run(
                [HLAB_SH, *args],
                capture_output=True,
                text=True,
                input=stdin_text,
                env=self.env,
                timeout=timeout,
            )
        except subprocess.TimeoutExpired:
            raise RuntimeError(
                f"hlab.sh {' '.join(args)}: no end after {timeout:g} s (killed)"
            ) from None
        if check and process.returncode != 0:
            raise RuntimeError(
                f"hlab.sh {' '.join(args)}: "
                f"{process.stderr.strip() or process.returncode}"
            )
        return process.stdout

    def ok(self, *args: str, timeout: float = TIMEOUT) -> bool:
        """Run an hlab.sh command and tell whether it succeeded.

        A command that runs longer than `timeout` seconds is killed, and this raises
        RuntimeError.
        """
        try:
            process = subprocess.run(
                [HLAB_SH, *args], capture_output=True, env=self.env, timeout=timeout
            )
        except subprocess.TimeoutExpired:
            raise RuntimeError(
                f"hlab.sh {' '.join(args)}: no end after {timeout:g} s (killed)"
            ) from None
        return process.returncode == 0

    def qga(self, sock: Path, command: dict, timeout: float = 10) -> dict | None:
        """Send one command to a QEMU guest agent and return its answer.

        The agent's channel keeps answers that an earlier client did not read. So a
        guest-sync with a fresh ID comes first and skips them. Return None when the
        agent does not answer within the timeout.
        """
        token = secrets.randbelow(2**31)
        deadline = time.monotonic() + timeout
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
                connection.settimeout(timeout)
                connection.connect(str(sock))
                stream = connection.makefile("rwb")

                def send(cmd: dict) -> None:
                    stream.write(json.dumps(cmd).encode() + b"\n")
                    stream.flush()

                # one deadline for all reads: a chatty guest cannot hold up the lab
                def line() -> bytes:
                    left = deadline - time.monotonic()
                    if left <= 0:
                        raise TimeoutError
                    connection.settimeout(left)
                    return stream.readline(1 << 20)

                send({"execute": "guest-sync", "arguments": {"id": token}})
                for _ in range(1000):
                    got = line()
                    if not got:
                        return None
                    if json.loads(got) == {"return": token}:
                        break
                else:
                    return None
                send(command)
                return json.loads(line())
        except (OSError, ValueError):
            return None
