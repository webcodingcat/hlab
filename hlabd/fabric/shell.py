"""The way to scripts/hlab.sh (a fake shell replaces it in the tests), and to a VM's QEMU guest agent."""

import json
import secrets
import socket
import subprocess
import time
from pathlib import Path

HLAB_SH = "/opt/hlab/scripts/hlab.sh"


class Shell:
    def run(self, *args: str, check: bool = True, input: str | None = None) -> str:
        process = subprocess.run([HLAB_SH, *args], capture_output=True, text=True, input=input)
        if check and process.returncode != 0:
            raise RuntimeError(f"hlab.sh {' '.join(args)}: {process.stderr.strip() or process.returncode}")
        return process.stdout

    def ok(self, *args: str) -> bool:
        return subprocess.run([HLAB_SH, *args], capture_output=True).returncode == 0

    def qga(self, sock: Path, command: dict, timeout: float = 10) -> dict | None:
        """One command to a QEMU guest agent. Its channel buffers answers a client left behind, so guest-sync with
        a fresh ID first discards them; None when the agent does not answer (within the timeout in all)."""
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

                def line() -> bytes:   # a guest that keeps talking does not hold the lab up
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
