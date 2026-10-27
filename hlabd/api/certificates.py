"""TLS certificates for load balancers: uploaded ones, and managed ones.

The lab issues a managed certificate at its own ACME server (Pebble, fabric/acme.py)
with DNS-01. As on Hetzner, every domain must belong to a DNS zone of the API: the
lab publishes the challenges there while the order runs, then removes them. Pebble's
certificates last five years, so the lab renews none (`renewal` stays "scheduled").
"""

import hashlib
from typing import TypedDict

from cryptography import x509
from cryptography.hazmat.primitives import serialization
from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import check_unique_name, invalid, resource_in_use
from ..lab import ActionSpec, ActionTable, Lab, Plan, now
from ..records import CertificateError, TlsCertificate
from ..state import CERTIFICATES, LOAD_BALANCERS
from . import hetzner, system, zones
from .web import filtered, paginate, read_body, update_resource


class IssuanceError(Exception):
    """A managed certificate cannot be issued; Hetzner reports the code."""

    def __init__(self, code: str, message: str):
        """Keep Hetzner's error code with the message."""
        super().__init__(message)
        self.code = code


class ChainFields(TypedDict):
    """What a certificate shows of its PEM chain."""

    certificate: str | None
    domain_names: list[str]
    not_valid_before: str | None
    not_valid_after: str | None
    fingerprint: str | None


def chain_fields(chain_pem: str) -> ChainFields:
    """Return the fields a certificate shows of its PEM chain (the leaf first)."""
    try:
        leaf = x509.load_pem_x509_certificates(chain_pem.encode())[0]
    except ValueError:
        raise invalid("certificate is not a PEM certificate", "certificate") from None
    try:
        names = leaf.extensions.get_extension_for_class(x509.SubjectAlternativeName)
        domains = names.value.get_values_for_type(x509.DNSName)
    except x509.ExtensionNotFound:
        domains = []
    digest = hashlib.sha256(leaf.public_bytes(serialization.Encoding.DER)).hexdigest()
    return {
        "certificate": chain_pem,
        "domain_names": domains,
        "not_valid_before": leaf.not_valid_before_utc.isoformat(),
        "not_valid_after": leaf.not_valid_after_utc.isoformat(),
        "fingerprint": ":".join(digest[i : i + 2] for i in range(0, len(digest), 2)),
    }


def check_key(chain_pem: str, key_pem: str) -> None:
    """Refuse a private key that does not belong to the chain's leaf."""
    try:
        key = serialization.load_pem_private_key(key_pem.encode(), password=None)
    except (ValueError, TypeError):
        raise invalid(
            "private_key is not an unencrypted PEM key", "private_key"
        ) from None
    leaf = x509.load_pem_x509_certificates(chain_pem.encode())[0]
    public = serialization.PublicFormat.SubjectPublicKeyInfo
    encoding = serialization.Encoding.DER
    if key.public_key().public_bytes(
        encoding, public
    ) != leaf.public_key().public_bytes(encoding, public):
        raise invalid("private_key does not match the certificate", "private_key")


def create_certificate(lab: Lab, body: dict) -> dict:
    """Create an uploaded certificate, or a managed one and start its issuance."""
    managed = body.get("type") == "managed"
    if managed:
        domains = body.get("domain_names") or []
        if not domains:
            raise invalid("domain_names is required", "domain_names")
        fields: ChainFields = {
            "certificate": None,
            "domain_names": domains,
            "not_valid_before": None,
            "not_valid_after": None,
            "fingerprint": None,
        }
    else:
        if not body.get("certificate") or not body.get("private_key"):
            raise invalid("certificate and private_key are required", "certificate")
        fields = chain_fields(body["certificate"])
        check_key(body["certificate"], body["private_key"])
    state = lab.state
    with state.change():
        check_unique_name(
            state[CERTIFICATES].values(), body.get("name"), None, "certificate"
        )
        cid = state.new_id(CERTIFICATES)
        certificate: TlsCertificate = {
            "id": cid,
            "name": body.get("name") or "",
            "labels": body.get("labels") or {},
            "type": "managed" if managed else "uploaded",
            "created": now(),
            **fields,
            "status": {"issuance": "pending", "renewal": "unavailable", "error": None}
            if managed
            else None,
            "_key": None if managed else body["private_key"],
        }
        state[CERTIFICATES][cid] = certificate
        action = (
            lab.new_action("create_certificate", [(cid, "certificate")])
            if managed
            else None
        )
    if action is not None:
        lab.run_action(action, issue_steps(lab, cid))
    return {"certificate": hetzner.certificate_json(lab, certificate), "action": action}


def issue_steps(lab: Lab, cid: int):
    """Return the steps that issue a managed certificate at the lab's ACME server."""

    async def steps():
        domains = list(lab.current(CERTIFICATES, cid)["domain_names"])
        try:
            missing = [d for d in domains if zones.zone_of(lab, d) is None]
            if missing:
                raise IssuanceError(
                    "dns_zone_not_found", f"no DNS zone for {', '.join(missing)}"
                )
            chain, key_pem = await order(lab, domains)
        except IssuanceError as error:
            fail(lab, cid, {"code": error.code, "message": str(error)})
            raise
        except Exception as error:
            fail(lab, cid, {"code": "unknown_error", "message": str(error)})
            raise
        with lab.state.change():
            certificate = lab.current(CERTIFICATES, cid)
            certificate.update(chain_fields(chain))
            certificate["domain_names"] = domains  # as ordered, in their order
            certificate["_key"] = key_pem
            certificate["status"] = {
                "issuance": "completed",
                "renewal": "scheduled",
                "error": None,
            }
        await system.apply_load_balancers(lab)

    return steps


async def order(lab: Lab, domains: list[str]) -> tuple[str, str]:
    """Order a certificate at Pebble with DNS-01; return its PEM chain and key."""
    started = await lab.blocking(lab.fabric.acme.start_order, domains)
    with lab.state.change():
        for name, value in started.records:
            zones.publish_txt(lab, name, value)
    await system.apply_dns(lab)
    try:
        chain = await lab.blocking(lab.fabric.acme.finish_order, started)
    finally:
        with lab.state.change():
            for name, value in started.records:
                zones.withdraw_txt(lab, name, value)
        await system.apply_dns(lab)
    return chain, started.key_pem


def fail(lab: Lab, cid: int, error: CertificateError) -> None:
    """Mark a managed certificate's issuance as failed, if it is still there."""
    certificate = lab.state.get(CERTIFICATES, cid)
    if certificate is not None:
        with lab.state.change():
            certificate["status"] = {
                "issuance": "failed",
                "renewal": "unavailable",
                "error": error,
            }


def used_by(lab: Lab, cid: int) -> list[int]:
    """Return the IDs of the load balancers whose services use the certificate."""
    return [
        lid
        for lid, balancer in lab.state[LOAD_BALANCERS].items()
        if any(
            cid in s.get("http", {}).get("certificates", [])
            for s in balancer["services"]
        )
    ]


async def retry(lab: Lab, certificate: TlsCertificate, body: dict) -> Plan:
    """Issue a managed certificate again whose issuance failed."""
    status = certificate["status"]
    if status is None or status["issuance"] != "failed":
        raise invalid("only a failed managed certificate can be retried", "type")
    with lab.state.change():
        certificate["status"] = {
            "issuance": "pending",
            "renewal": "unavailable",
            "error": None,
        }
    return Plan(issue_steps(lab, certificate["id"]))


# the certificate actions: Hetzner's name of the action and the function that plans it
CERTIFICATE_ACTIONS: ActionTable[TlsCertificate] = {
    "retry": ActionSpec("issue_certificate", retry, flag=False),
}


def delete_certificate(lab: Lab, cid: int) -> None:
    """Delete a certificate that no load balancer uses."""
    with lab.state.change():
        lab.get_or_404(CERTIFICATES, cid, "certificate")
        if used_by(lab, cid):
            # not checked against Hetzner; firewalls and placement groups behave so
            raise resource_in_use("the certificate is used by a load balancer")
        del lab.state[CERTIFICATES][cid]


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the certificate routes to the app."""
    state = lab.state

    @app.get("/v1/certificates")
    async def certificate_list(request: Request):
        items = filtered(
            request,
            list(state[CERTIFICATES].values()),
            {"type": lambda c, value: c["type"] in value.split(",")},
        )
        return paginate(
            request,
            [hetzner.certificate_json(lab, c) for c in items],
            "certificates",
        )

    @app.post("/v1/certificates", status_code=201)
    async def certificate_create(request: Request):
        return create_certificate(lab, await read_body(request))

    @app.get("/v1/certificates/{cid}")
    async def certificate_get(cid: int):
        certificate = lab.get_or_404(CERTIFICATES, cid, "certificate")
        return {"certificate": hetzner.certificate_json(lab, certificate)}

    @app.put("/v1/certificates/{cid}")
    async def certificate_update(cid: int, request: Request):
        body = await read_body(request)
        certificate = lab.get_or_404(CERTIFICATES, cid, "certificate")
        keys = ("name", "labels")
        update_resource(lab, CERTIFICATES, certificate, body, keys, "certificate")
        return {"certificate": hetzner.certificate_json(lab, certificate)}

    @app.delete("/v1/certificates/{cid}", status_code=204)
    async def certificate_delete(cid: int):
        delete_certificate(lab, cid)
        return Response(status_code=204)

    @app.post("/v1/certificates/{cid}/actions/{cmd}", status_code=201)
    async def certificate_action(cid: int, cmd: str, request: Request):
        body = await read_body(request)
        certificate = lab.get_or_404(CERTIFICATES, cid, "certificate")
        return await lab.act(CERTIFICATE_ACTIONS, cmd, certificate, "certificate", body)
