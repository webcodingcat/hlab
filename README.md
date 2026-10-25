# HLab – a local Hetzner Cloud lab

HLab (Hetzner Lab) is a local stand-in for Hetzner Cloud: QEMU/KVM virtual machines behind a fake Cloud API, a
Storage Box with its API, "Internet" clients and fault injection – all inside one privileged Docker container. Tools
that manage Hetzner Cloud (OpenTofu with the hcloud provider, the hcloud CLI, Ansible) run against it unchanged; only
the API endpoints differ.

| What | Where |
|---|---|
| Cloud API (`/v1/...`, Bearer token) | `http://198.18.0.1/v1` |
| Storage Box API (api.hetzner.com) | `http://198.18.0.3/v1` |
| Metadata service (from a VM) | `http://169.254.169.254/hetzner/v1/metadata` |
| Storage Boxes (SFTP 22, SSH 23) | `u<id>.your-storagebox.de` → `198.18.0.1` |
| DNS resolver (lab names) | `198.18.0.1` |
| Lab control (faults, clients, ...) | `http://198.18.0.1/_lab/...` |
| Network namespace to reach VMs | `docker run --network container:hlab-<name> ...` |

`hlab env` prints these as shell variables (`HCLOUD_ENDPOINT`, `HCLOUD_TOKEN`, `HETZNER_ENDPOINT`, ...).

## Requirements

Docker with `/dev/kvm` (Linux, or Docker Desktop on Windows with WSL2). Check:
`docker run --rm --device /dev/kvm alpine ls -l /dev/kvm`.

## Use

```bash
bin/hlab -c examples/hlab.yaml up        # build the image if needed, load the OS images, start hlab-demo
bin/hlab -c examples/hlab.yaml status    # servers, Floating IPs, clients, faults
bin/hlab -c examples/hlab.yaml logs      # what the lab does: requests, actions, VMs
bin/hlab -c examples/hlab.yaml down      # stop; disks and state stay in the volume hlab-demo
bin/hlab -c examples/hlab.yaml purge     # stop and delete the volume
bin/hlab test                           # style checks (STYLE.md) and unit tests (no VMs)
bin/hlab format                         # apply what the formatters can fix
bin/hlab -c examples/hlab.yaml itest     # live tests with VMs (resets the lab, ~15 min)
tests/tofu-smoke/run.sh                # OpenTofu with the hcloud provider against a running lab
```

On Windows PowerShell use `.\bin\hlab.ps1` with the same arguments (allow scripts once with
`Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`).

The OS images are those listed in `hlab.yaml` (URL and checksum file); the lab loads them when it starts. Servers
come into being through the API, for example with OpenTofu:

```bash
eval "$(bin/hlab env)"
tofu() { docker run --rm -i --network "$HLAB_NETWORK" -e HCLOUD_ENDPOINT -e HCLOUD_TOKEN -e HETZNER_ENDPOINT \
    -v "$(pwd -W 2>/dev/null || pwd):/work" -w /work ghcr.io/opentofu/opentofu:1 "$@"; }
tofu init && tofu apply
```

### Faults and clients

```bash
bin/hlab fault cut a b [--net private]   # drop traffic between two servers (or a server and a client)
bin/hlab fault heal a b [--net private]
bin/hlab fault isolate a                  # a server loses every network
bin/hlab fault block a api|box|internet   # a server cannot reach the API, the Storage Box or the Internet
bin/hlab fault pause a / cont a           # freeze the VM (its clock stops) / resume
bin/hlab fault dead a / alive a           # broken hardware: power-on succeeds, the VM stays off
bin/hlab fault netem a delay 100ms loss 5%   # tc netem towards a server (empty = back to normal)
bin/hlab fault api 503 3                  # the next 3 API requests fail
bin/hlab fault clear
bin/hlab client add alice                 # an Internet client with its own addresses, no NAT
bin/hlab client exec alice curl -s http://203.0.113.11/
bin/hlab box user lab1 key.pub            # a Storage Box account outside the API
bin/hlab ssh a uptime                     # root on a server with the lab's own key
bin/hlab reset                            # delete everything the APIs created
```

## What it does

- **Servers** boot through cloud-init's Hetzner datasource (SMBIOS, metadata, DHCP on `eth0`, IPv6 from the
  metadata), with the cloud's timings for power actions and `locked` (423) while an action runs. Rebuild,
  `change_type`, `reset_password` (guest agent), reverse DNS, primary IPs, SSH keys.
- **Networks**: cloud networks with subnets and routes (pushed by DHCP, forwarded to the gateway server), alias IPs,
  hot-plugged NICs, overlapping networks; Floating IPs routed to their server; firewalls with Hetzner's semantics;
  latency between locations. A server sends only from its own public addresses, so the source a server sees is the
  real client IP.
- **Volumes**: SCSI disks as `/dev/disk/by-id/scsi-0HC_Volume_<id>`, hot-plugged, formatted on request, resized,
  automounted.
- **Storage Box**: boxes, sub-accounts, snapshots and plans, rollback and access settings through the API; SFTP on
  port 22 (always on, keys in RFC4716 format), SFTP and commands (`rsync`, `scp`, `borg`, ...) on port 23 (with
  `ssh_enabled`, keys in OpenSSH format); snapshots under `/home/.zfs/snapshot`. A plain SFTP rename never overwrites.
- **Compatibility** is checked by the tests: every API response against Hetzner's OpenAPI specification, and the
  OpenTofu smoke test with Hetzner's own provider (an empty second plan, a clean destroy).

### Not like Hetzner

- Not offered: load balancers, certificates, DNS zones, images of your own (snapshots, backups), rescue, ISOs,
  console, placement groups, pricing, metrics. Samba and WebDAV of Storage Boxes are only reported.
- No IPv6 to the real Internet, no vSwitch. All Storage Boxes share `storage_box.size_gb`; SCP on port 22 only
  through SFTP (OpenSSH's default), and the automatic snapshots share the limit of the manual ones.
- A server's `locked` is true while one of its actions runs; on Hetzner it marks an administrative lock. Wait for the
  action instead.
- A test lab, not a hosting platform: the guests run your own code. The lab keeps the Docker host and its network
  out of reach, but it does not defend servers and users against each other (`/_lab` has no authentication, QEMU
  runs as root in the container, cloud networks have no source checks).

## Layout

| Path | What |
|---|---|
| `Dockerfile` | the lab image (Debian 13 packages: QEMU, dnsmasq, nftables, OpenSSH, Python/FastAPI) |
| `bin/hlab`, `bin/hlab.ps1` | host side: build, start, stop, pass commands into the container |
| `scripts/hlab.sh` | low-level primitives: taps, QEMU, nftables, tc, DHCP, DNS, volumes, images |
| `scripts/box.sh` | the Storage Box: sshd, chrooted accounts, snapshots |
| `hlabd/lab.py` | the core: state, actions, locks (how resources are kept apart while actions run) |
| `hlabd/api/` | what happens: Cloud API, Storage Box API, metadata service, lab control (one module per resource) |
| `hlabd/fabric/` | how it happens: VMs, taps, nftables firewall rules, Storage Box accounts (through the scripts) |
| `hlabd/cli.py` | the `hlab` command inside the container |
| `tests/` | unit tests by feature, the OpenTofu smoke test |
| `itests/` | live tests with VMs |
| `tools/lint.sh`, `STYLE.md` | style checks and the standards they follow |
