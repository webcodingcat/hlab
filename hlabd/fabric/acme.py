"""ACME: Pebble, Let's Encrypt's test server, as the lab's certificate authority.

A part of the fabric (see __init__.py). Pebble serves its directory at
https://acme.hlab:14000/dir and checks DNS-01 and HTTP-01 challenges through the lab
resolver, so it sees the DNS zones of the API. Its HTTPS certificate comes from the
lab's own CA, made once and kept in the lab's volume. Pebble makes a new root for the
certificates it issues on every start; `ca_bundle` hands out both.
"""

import datetime
import ipaddress
import json
import ssl
import urllib.request
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID

from .. import config
from .shell import Shell

# Pebble's management API (its current roots); only the lab itself asks it
MANAGEMENT = "127.0.0.1:15000"
CERT_DAYS = 3650


def new_key() -> ec.EllipticCurvePrivateKey:
    """Return a new P-256 key."""
    return ec.generate_private_key(ec.SECP256R1())


def pem_key(key: ec.EllipticCurvePrivateKey) -> bytes:
    """Return a private key as unencrypted PEM."""
    return key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    )


def lab_ca(name: str) -> tuple[ec.EllipticCurvePrivateKey, x509.Certificate]:
    """Return a new CA: its key and its self-signed certificate."""
    key = new_key()
    subject = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, name)])
    start = datetime.datetime.now(datetime.UTC)
    cert = (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(subject)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(start)
        .not_valid_after(start + datetime.timedelta(days=CERT_DAYS))
        .add_extension(x509.BasicConstraints(ca=True, path_length=0), critical=True)
        .add_extension(
            x509.SubjectKeyIdentifier.from_public_key(key.public_key()), critical=False
        )
        .add_extension(
            x509.KeyUsage(
                digital_signature=True,
                key_cert_sign=True,
                crl_sign=True,
                content_commitment=False,
                key_encipherment=False,
                data_encipherment=False,
                key_agreement=False,
                encipher_only=False,
                decipher_only=False,
            ),
            critical=True,
        )
        .sign(key, hashes.SHA256())
    )
    return key, cert


def server_cert(
    ca_key: ec.EllipticCurvePrivateKey, ca_cert: x509.Certificate
) -> tuple[ec.EllipticCurvePrivateKey, x509.Certificate]:
    """Return a key and certificate for Pebble's HTTPS: acme.hlab and its address."""
    key = new_key()
    start = datetime.datetime.now(datetime.UTC)
    names = [
        x509.DNSName(config.ACME_NAME),
        x509.IPAddress(ipaddress.ip_address(config.SERVICES)),
    ]
    cert = (
        x509.CertificateBuilder()
        .subject_name(
            x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, config.ACME_NAME)])
        )
        .issuer_name(ca_cert.subject)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(start)
        .not_valid_after(start + datetime.timedelta(days=CERT_DAYS))
        .add_extension(x509.SubjectAlternativeName(names), critical=False)
        # what strict clients (Python 3.13 and newer) want of a server certificate
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(
            x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]), critical=False
        )
        .add_extension(
            x509.AuthorityKeyIdentifier.from_issuer_public_key(ca_key.public_key()),
            critical=False,
        )
        .add_extension(
            x509.SubjectKeyIdentifier.from_public_key(key.public_key()), critical=False
        )
        .sign(ca_key, hashes.SHA256())
    )
    return key, cert


def pebble_config(directory: Path) -> dict:
    """Return Pebble's configuration: real challenge ports, no account binding."""
    return {
        "pebble": {
            "listenAddress": f"{config.SERVICES}:{config.ACME_PORT}",
            "managementListenAddress": MANAGEMENT,
            "certificate": str(directory / "acme.pem"),
            "privateKey": str(directory / "acme.key"),
            # the ports Let's Encrypt checks
            "httpPort": 80,
            "tlsPort": 443,
            "ocspResponderURL": "",
            "externalAccountBindingRequired": False,
            "retryAfter": {"authz": 3, "order": 5},
        }
    }


class Acme:
    """Pebble with its configuration and the lab CA, under the lab's directory."""

    def __init__(self, root: Path, shell: Shell):
        """Keep the directory for Pebble's files and the shell."""
        self.directory = root / "acme"
        self.shell = shell

    def start(self) -> None:
        """Start Pebble; make the lab CA and Pebble's certificate the first time."""
        self.directory.mkdir(parents=True, exist_ok=True)
        ca_file = self.directory / "lab-ca.pem"
        if not ca_file.is_file():
            ca_key, ca_cert = lab_ca("HLab CA")
            key, cert = server_cert(ca_key, ca_cert)
            (self.directory / "acme.key").write_bytes(pem_key(key))
            (self.directory / "acme.pem").write_bytes(
                cert.public_bytes(serialization.Encoding.PEM)
            )
            ca_file.write_bytes(ca_cert.public_bytes(serialization.Encoding.PEM))
        config_file = self.directory / "pebble.json"
        config_file.write_text(json.dumps(pebble_config(self.directory), indent=2))
        # challenges are looked up through the lab resolver: it knows the zones
        self.shell.run("start-pebble", str(config_file), f"{config.SERVICES}:53")

    def ca_bundle(self) -> str:
        """Return the PEM certificates a client trusts for the lab's ACME.

        The lab CA signs Pebble's HTTPS; Pebble's root and intermediate sign what it
        issues, and they are new after every start of Pebble.
        """
        lab_ca_pem = (self.directory / "lab-ca.pem").read_text()
        context = ssl.create_default_context(cafile=str(self.directory / "lab-ca.pem"))
        context.check_hostname = False  # the management API listens on 127.0.0.1
        pems = [lab_ca_pem]
        for path in ("roots/0", "intermediates/0"):
            url = f"https://{MANAGEMENT}/{path}"
            with urllib.request.urlopen(url, context=context, timeout=5) as answer:
                pems.append(answer.read().decode())
        return "".join(pem if pem.endswith("\n") else pem + "\n" for pem in pems)
