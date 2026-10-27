# HLab: Hetzner Cloud stand-in with QEMU/KVM virtual machines, a fake Cloud API, a Storage Box stand-in and
# "Internet" clients, all inside one privileged container with its own network namespace. bin/hlab drives it.
# The newest Debian 13 (trixie-slim, not pinned); apt upgrades it to the current security fixes.
FROM debian:trixie-slim AS lab
# Marks every hlab image (the dev stage inherits it), so bin/hlab can prune the untagged
# ones that rebuilds leave behind.
LABEL hlab=1

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
        borgbackup xfsprogs nsd publicsuffix python3-dnspython pebble \
        haproxy python3-acme; \
    rm -rf /var/lib/apt/lists/*

# Hetzner's OpenAPI specifications, for the conformance tests. Not pinned on purpose: a rebuild with a new
# HETZNER_SPEC date picks up API changes, and the tests show where the lab falls behind.
# tools/fetch_specs.py has the URLs; the unit tests outside Docker use it too.
ARG HETZNER_SPEC=2026-10-24
COPY tools/fetch_specs.py /opt/hlab/tools/
RUN set -eux; \
    python3 /opt/hlab/tools/fetch_specs.py /opt/hlab/spec; \
    echo "${HETZNER_SPEC}" > /opt/hlab/spec/fetched

COPY scripts/ /opt/hlab/scripts/
COPY hlabd/ /opt/hlab/hlabd/
# The live tests stay in the lab image: `bin/hlab itest` runs them inside the running lab container.
COPY itests/ /opt/hlab/itests/
RUN set -eux; \
    chmod 0755 /opt/hlab/scripts/*.sh; \
    printf '#!/bin/sh\nPYTHONPATH=/opt/hlab exec python3 -m hlabd "$@"\n' > /usr/local/bin/hlab; \
    chmod 0755 /usr/local/bin/hlab

ENTRYPOINT ["/usr/local/bin/hlab"]
CMD ["serve"]

# Development tools for `bin/hlab test`, `lint` and `format` (not part of the lab image):
# ruff checks PEP 8 and PEP 257, mypy the types, shellcheck and shfmt the Google Shell
# Style Guide, yamllint YAML. ruff and mypy (pinned: a new release may report more)
# come from PyPI (the same pins as pyproject.toml's dev extra), the others from Debian.
# The venv sees Debian's packages, so mypy knows FastAPI.
FROM lab AS dev
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends shellcheck shfmt yamllint python3-venv; \
    rm -rf /var/lib/apt/lists/*; \
    python3 -m venv --system-site-packages /opt/dev; \
    /opt/dev/bin/pip install --no-cache-dir ruff==0.17.2 mypy==2.4.0
ENV PATH=/opt/dev/bin:$PATH
# The mounted repository stays free of caches: ruff and mypy keep theirs in /tmp,
# and Python writes no __pycache__.
ENV RUFF_CACHE_DIR=/tmp/ruff-cache MYPY_CACHE_DIR=/tmp/mypy-cache PYTHONDONTWRITEBYTECODE=1
# The unit tests run here (`bin/hlab test`), next to the lab's code and specs; they also read
# the wrappers in bin/ and the sample configuration.
COPY tests/ /opt/hlab/tests/
COPY bin/ /opt/hlab/bin/
COPY examples/ /opt/hlab/examples/
WORKDIR /src
ENTRYPOINT []
