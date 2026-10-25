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
        p = subprocess.run([HLAB_SH, *args], capture_output=True, text=True, input=input)
        if check and p.returncode != 0:
            raise RuntimeError(f"hlab.sh {' '.join(args)}: {p.stderr.strip() or p.returncode}")
        return p.stdout

    def ok(self, *args: str) -> bool:
        return subprocess.run([HLAB_SH, *args], capture_output=True).returncode == 0

    def qga(self, sock: Path, command: dict, timeout: float = 10) -> dict | None:
        """One command to a QEMU guest agent. Its channel buffers answers a client left behind, so guest-sync with
        a fresh ID first discards them; None when the agent does not answer (within the timeout in all)."""
        token = secrets.randbelow(2**31)
        deadline = time.monotonic() + timeout
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as c:
                c.settimeout(timeout)
                c.connect(str(sock))
                f = c.makefile("rwb")

                def send(cmd: dict) -> None:
                    f.write(json.dumps(cmd).encode() + b"\n")
                    f.flush()

                def line() -> bytes:   # a guest that keeps talking does not hold the lab up
                    left = deadline - time.monotonic()
                    if left <= 0:
                        raise TimeoutError
                    c.settimeout(left)
                    return f.readline(1 << 20)

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
