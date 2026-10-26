"""SSH keys: the public keys new servers get for root."""

import base64
import hashlib

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import check_unique_name, invalid, not_unique
from ..lab import Lab, now
from ..records import SSHKey
from . import hetzner
from .web import filtered, paginate, read_body, update_resource


def ssh_fingerprint(public_key: str) -> str:
    """Return the MD5 fingerprint of an OpenSSH public key.

    An invalid key raises ValueError: its blob does not name its type, or it has
    leading whitespace (Hetzner refuses that since 2026-04).
    """
    parts = public_key.split()
    if len(parts) < 2 or public_key != public_key.lstrip():
        raise ValueError("invalid public key")
    blob = base64.b64decode(parts[1], validate=True)
    length = int.from_bytes(blob[:4], "big")
    if len(blob) < 4 + length or blob[4 : 4 + length] != parts[0].encode():
        raise ValueError("invalid public key")
    digest = hashlib.md5(blob).hexdigest()
    return ":".join(digest[i : i + 2] for i in range(0, 32, 2))


def create_ssh_key(lab: Lab, body: dict) -> dict:
    """Create an SSH key; like on Hetzner, names and fingerprints must be unique."""
    state = lab.state
    if not body.get("name") or not body.get("public_key"):
        raise invalid("name and public_key are required")
    try:
        fingerprint = ssh_fingerprint(body["public_key"])
    # binascii.Error, ValueError, ...: whatever a broken key raises, it is invalid
    except Exception:  # noqa: BLE001
        raise invalid("invalid public key") from None
    with state.change():
        ssh_keys = state["ssh_keys"].values()
        check_unique_name(
            ssh_keys, body["name"], message="SSH key with the same name already exists"
        )
        if any(k["fingerprint"] == fingerprint for k in ssh_keys):
            raise not_unique(
                "SSH key with the same fingerprint already exists", "public_key"
            )
        kid = state.new_id("ssh_keys")
        ssh_key: SSHKey = {
            "id": kid,
            "name": body["name"],
            "fingerprint": fingerprint,
            "public_key": body["public_key"].strip(),
            "labels": body.get("labels") or {},
            "created": now(),
        }
        state["ssh_keys"][kid] = ssh_key
    return {"ssh_key": hetzner.ssh_key_json(lab, ssh_key)}


def update_ssh_key(lab: Lab, kid: int, body: dict) -> dict:
    """Change an SSH key's name or labels."""
    ssh_key = lab.get_or_404("ssh_keys", kid, "SSH key")
    update_resource(lab, "ssh_keys", ssh_key, body, ("name", "labels"), "SSH key")
    return {"ssh_key": hetzner.ssh_key_json(lab, ssh_key)}


def delete_ssh_key(lab: Lab, kid: int) -> None:
    """Delete an SSH key (servers keep the keys they were made with)."""
    with lab.state.change():
        lab.get_or_404("ssh_keys", kid, "SSH key")
        del lab.state["ssh_keys"][kid]


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the SSH key routes to the app."""
    state = lab.state

    @app.get("/v1/ssh_keys")
    async def ssh_key_list(request: Request):
        items = filtered(
            request,
            list(state["ssh_keys"].values()),
            {"fingerprint": lambda ssh_key, value: ssh_key["fingerprint"] == value},
        )
        return paginate(
            request, [hetzner.ssh_key_json(lab, k) for k in items], "ssh_keys"
        )

    @app.post("/v1/ssh_keys", status_code=201)
    async def ssh_key_create(request: Request):
        return create_ssh_key(lab, await read_body(request))

    @app.get("/v1/ssh_keys/{kid}")
    async def ssh_key_get(kid: int):
        ssh_key = lab.get_or_404("ssh_keys", kid, "SSH key")
        return {"ssh_key": hetzner.ssh_key_json(lab, ssh_key)}

    @app.put("/v1/ssh_keys/{kid}")
    async def ssh_key_update(kid: int, request: Request):
        return update_ssh_key(lab, kid, await read_body(request))

    @app.delete("/v1/ssh_keys/{kid}", status_code=204)
    async def ssh_key_delete(kid: int):
        delete_ssh_key(lab, kid)
        return Response(status_code=204)
