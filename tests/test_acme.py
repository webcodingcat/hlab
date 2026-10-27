"""The lab's ACME server: Pebble, its configuration, the lab CA and the CA bundle."""

import ipaddress
import json

from conftest import FakeShell
from cryptography import x509
from cryptography.hazmat.primitives.asymmetric import ec

from hlabd import cli, config
from hlabd.fabric.acme import Acme


def test_pebble_starts_with_real_challenge_ports_and_the_lab_resolver(tmp_path):
    shell = FakeShell()
    Acme(tmp_path, shell).start()
    directory = tmp_path / "acme"
    pebble = json.loads((directory / "pebble.json").read_text())["pebble"]
    assert pebble["listenAddress"] == f"{config.SERVICES}:{config.ACME_PORT}"
    # Let's Encrypt checks HTTP-01 on 80 and TLS-ALPN-01 on 443
    assert (pebble["httpPort"], pebble["tlsPort"]) == (80, 443)
    assert shell.commands("start-pebble") == [
        ("start-pebble", str(directory / "pebble.json"), f"{config.SERVICES}:53")
    ]


def test_the_lab_ca_is_made_once_and_signs_pebbles_certificate(tmp_path):
    acme = Acme(tmp_path, FakeShell())
    acme.start()
    ca_pem = (tmp_path / "acme" / "lab-ca.pem").read_bytes()
    acme.start()  # a restart keeps the lab CA, so clients keep trusting it
    assert (tmp_path / "acme" / "lab-ca.pem").read_bytes() == ca_pem
    ca = x509.load_pem_x509_certificate(ca_pem)
    cert = x509.load_pem_x509_certificate((tmp_path / "acme" / "acme.pem").read_bytes())
    names = cert.extensions.get_extension_for_class(x509.SubjectAlternativeName).value
    assert names.get_values_for_type(x509.DNSName) == [config.ACME_NAME]
    assert names.get_values_for_type(x509.IPAddress) == [
        ipaddress.ip_address(config.SERVICES)
    ]
    public_key = ca.public_key()
    assert isinstance(public_key, ec.EllipticCurvePublicKey)
    public_key.verify(
        cert.signature,
        cert.tbs_certificate_bytes,
        ec.ECDSA(cert.signature_hash_algorithm),
    )


def test_the_ca_bundle_is_served_to_clients(api, monkeypatch):
    bundle = "-----BEGIN CERTIFICATE-----\nlab\n-----END CERTIFICATE-----\n"
    monkeypatch.setattr(api.lab.fabric.acme, "ca_bundle", lambda: bundle)
    response = api.get("/_lab/acme/ca.pem")
    assert response.status_code == 200 and response.text == bundle


def test_env_names_the_acme_directory_and_its_ca(capsys, monkeypatch):
    monkeypatch.setattr(
        cli.config,
        "load",
        lambda _: {"name": "demo", "ui_port": 8480, "api": {"token": None}},
    )
    cli.env()
    out = capsys.readouterr().out
    assert "export HLAB_ACME_DIRECTORY=https://acme.hlab:14000/dir" in out
    assert f"export HLAB_ACME_CA=http://{config.SERVICES}/_lab/acme/ca.pem" in out
