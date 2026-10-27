"""Certificates: uploaded ones are checked, managed ones are issued through DNS-01."""

import datetime

from conftest import state_of, wait_action
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import NameOID

from hlabd.fabric.acme import AcmeOrder
from hlabd.state import RRSETS


def self_signed(*names: str) -> tuple[str, str]:
    """Return a self-signed PEM certificate for the names and its PEM key."""
    key = ec.generate_private_key(ec.SECP256R1())
    subject = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, names[0])])
    start = datetime.datetime.now(datetime.UTC)
    cert = (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(subject)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(start)
        .not_valid_after(start + datetime.timedelta(days=90))
        .add_extension(
            x509.SubjectAlternativeName([x509.DNSName(n) for n in names]),
            critical=False,
        )
        .sign(key, hashes.SHA256())
    )
    key_pem = key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    )
    return cert.public_bytes(serialization.Encoding.PEM).decode(), key_pem.decode()


def upload(api, name="web", names=("example.com",)):
    """Upload a self-signed certificate and return it."""
    cert, key = self_signed(*names)
    response = api.post(
        "/v1/certificates",
        json={"name": name, "certificate": cert, "private_key": key},
    )
    assert response.status_code == 201, response.text
    assert response.json()["action"] is None  # Hetzner: no action for an upload
    return response.json()["certificate"]


def test_uploaded_certificate_shows_its_names_and_dates_never_its_key(api):
    certificate = upload(api, names=("example.com", "www.example.com"))
    assert certificate["type"] == "uploaded"
    assert certificate["domain_names"] == ["example.com", "www.example.com"]
    assert certificate["not_valid_after"] and len(certificate["fingerprint"]) == 95
    assert "private_key" not in certificate and certificate["status"] is None


def test_uploads_with_a_foreign_key_or_no_certificate_are_invalid(api):
    cert, _ = self_signed("example.com")
    _, other_key = self_signed("example.org")
    for body in (
        {"name": "a", "certificate": cert, "private_key": other_key},
        {"name": "b", "certificate": "no PEM", "private_key": other_key},
    ):
        response = api.post("/v1/certificates", json=body)
        assert response.json()["error"]["code"] == "invalid_input"


def test_managed_certificate_without_a_zone_fails_as_on_hetzner(api):
    response = api.post(
        "/v1/certificates",
        json={"name": "m", "type": "managed", "domain_names": ["example.com"]},
    )
    action = wait_action(api, response.json()["action"])
    assert action["status"] == "error"
    certificate = api.get(f"/v1/certificates/{response.json()['certificate']['id']}")
    status = certificate.json()["certificate"]["status"]
    assert status["issuance"] == "failed"
    assert status["error"]["code"] == "dns_zone_not_found"


def test_managed_certificate_is_issued_through_the_zone(api, monkeypatch):
    api.post("/v1/zones", json={"name": "example.com", "mode": "primary"})
    cert, key = self_signed("example.com", "*.example.com")
    seen = []

    def start_order(domains):
        return AcmeOrder(
            [("_acme-challenge.example.com", "token")], key, None, None, []
        )

    def finish_order(order):
        # the challenge is published while the order is checked
        seen.extend(
            r["records"] for r in state_of(api, "rrsets").values() if r["type"] == "TXT"
        )
        return cert

    monkeypatch.setattr(api.lab.fabric.acme, "start_order", start_order)
    monkeypatch.setattr(api.lab.fabric.acme, "finish_order", finish_order)
    response = api.post(
        "/v1/certificates",
        json={
            "name": "m",
            "type": "managed",
            "domain_names": ["example.com", "*.example.com"],
        },
    )
    assert response.json()["certificate"]["status"]["issuance"] == "pending"
    assert wait_action(api, response.json()["action"])["status"] == "success"
    assert seen == [[{"value": '"token"', "comment": ""}]]
    certificate = api.get(f"/v1/certificates/{response.json()['certificate']['id']}")
    issued = certificate.json()["certificate"]
    assert issued["status"]["issuance"] == "completed"
    assert issued["certificate"] == cert
    # the challenge is gone again
    assert not [r for r in state_of(api, "rrsets").values() if r["type"] == "TXT"]
    assert RRSETS.name == "rrsets"


def test_only_a_failed_certificate_is_retried(api):
    certificate = upload(api)
    response = api.post(f"/v1/certificates/{certificate['id']}/actions/retry")
    assert response.json()["error"]["code"] == "invalid_input"
