# HLab: Hetzner Cloud stand-in with QEMU/KVM virtual machines, a fake Cloud API, a Storage Box stand-in and
# "Internet" clients, all inside one privileged container with its own network namespace. bin/hlab drives it.
# The newest Debian 13 (trixie-slim, not pinned); apt upgrades it to the current security fixes.
FROM debian:trixie-slim AS lab

# Only Debian packages, so apt keeps everything up to date. QEMU (runs the untrusted guests) and OpenSSH (the Storage
# Box's sshd, the client that logs into guests) come from Debian's backports, whose releases fix issues the trixie
# ones still have. Nothing that is not needed is installed (fewer packages, fewer vulnerabilities). Known findings
# without a Debian fix are listed with their reasons in .trivyignore.
RUN set -eux; \
    echo "deb http://deb.debian.org/debian trixie-backports main" > /etc/apt/sources.list.d/backports.list; \
    apt-get update; \
    apt-get upgrade -y; \
    apt-get install -y --no-install-recommends -t trixie-backports qemu-system-x86 qemu-utils \
        openssh-client openssh-server openssh-sftp-server; \
    apt-get install -y --no-install-recommends dnsmasq iproute2 nftables \
        socat curl ca-certificates jq procps e2fsprogs util-linux \
        python3 python3-fastapi python3-uvicorn python3-yaml python3-httpx python3-pytest \
        python3-paramiko python3-jsonschema iputils-ping rsync \
        borgbackup xfsprogs; \
    rm -rf /var/lib/apt/lists/*

# Hetzner's OpenAPI specifications, for the conformance tests. Not pinned on purpose: a rebuild with a new
# HETZNER_SPEC date picks up API changes, and the tests show where the lab falls behind.
ARG HETZNER_SPEC=2026-10-24
RUN set -eux; \
    mkdir -p /opt/hlab/spec; \
    curl -fsSLo /opt/hlab/spec/cloud.json https://docs.hetzner.cloud/cloud.spec.json; \
    curl -fsSLo /opt/hlab/spec/hetzner.json https://docs.hetzner.cloud/hetzner.spec.json; \
    echo "${HETZNER_SPEC}" > /opt/hlab/spec/fetched

COPY scripts/ /opt/hlab/scripts/
COPY hlabd/ /opt/hlab/hlabd/
COPY tests/ /opt/hlab/tests/
COPY itests/ /opt/hlab/itests/
RUN set -eux; \
    chmod 0755 /opt/hlab/scripts/*.sh; \
    printf '#!/bin/sh\nPYTHONPATH=/opt/hlab exec python3 -m hlabd "$@"\n' > /usr/local/bin/hlab; \
    chmod 0755 /usr/local/bin/hlab

ENTRYPOINT ["/usr/local/bin/hlab"]
CMD ["serve"]

# Development tools for `bin/hlab test`, `lint` and `format` (not part of the lab image):
# ruff checks PEP 8 and PEP 257, shellcheck and shfmt the Google Shell Style Guide,
# yamllint YAML. ruff comes from PyPI (newest), the others from Debian.
FROM lab AS dev
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends shellcheck shfmt yamllint python3-venv; \
    rm -rf /var/lib/apt/lists/*; \
    python3 -m venv /opt/dev; \
    /opt/dev/bin/pip install --no-cache-dir ruff
ENV PATH=/opt/dev/bin:$PATH
WORKDIR /src
ENTRYPOINT []
