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
| DNS resolver (lab names, the DNS zones) | `198.18.0.1` |
| ACME server (Pebble) and the certificates to trust | `https://acme.hlab:14000/dir`, `http://198.18.0.1/_lab/acme/ca.pem` |
| Lab control (faults, clients, ...) | `http://198.18.0.1/_lab/...` |
| Network namespace to reach VMs | `docker run --network container:hlab-<name> ...` |

`hlab env` prints these as shell variables (`HCLOUD_ENDPOINT`, `HCLOUD_TOKEN`, `HETZNER_ENDPOINT`, ...).

## Requirements

Docker with `/dev/kvm` (Linux, or Docker Desktop on Windows with WSL2). Check:
`docker run --rm --device /dev/kvm alpine ls -l /dev/kvm`.

## Use

```bash
bin/hlab -c examples/hlab.yaml up        # build the image (cached), load the OS images, start hlab-demo
bin/hlab -c examples/hlab.yaml status    # servers, Floating IPs, clients, faults
bin/hlab -c examples/hlab.yaml logs      # what the lab does: requests, actions, VMs
bin/hlab -c examples/hlab.yaml down      # stop; disks and state stay in the volume hlab-demo
bin/hlab -c examples/hlab.yaml purge     # stop and delete the volume
bin/hlab test                           # style checks (STYLE.md) and unit tests (no VMs)
bin/hlab format                         # apply what the formatters can fix
bin/hlab -c examples/hlab.yaml itest     # live tests with VMs (resets the lab, ~15 min)
tests/tofu-smoke/run.sh                # OpenTofu with the hcloud provider against a running lab
```

On Windows run `bin/hlab` from Git Bash or WSL.

The OS images are those listed in `hlab.yaml` (URL and checksum file); the lab loads them when it starts. Servers
are created through the API, for example with OpenTofu:

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
bin/hlab ssh a uptime                     # root on a server with the lab's SSH key
bin/hlab reset                            # delete everything the APIs created
```

## What it does

- **Servers** boot through cloud-init's Hetzner datasource (SMBIOS, metadata, DHCP on `eth0`, IPv6 from the
  metadata), with Hetzner's timings for power actions and `locked` (423) while an action runs. Rebuild,
  `change_type`, `reset_password` (guest agent), reverse DNS, primary IPs, SSH keys. Placement groups (`spread`,
  Hetzner's limits; the lab has one host, so they place nothing).
- **Images**: snapshots and backups of a server's disk (`create_image`, also of a running server: shut it down for a
  consistent image, as on Hetzner), servers created or rebuilt from them, daily backups at 22:00 UTC with seven
  slots while enabled, backups converted to snapshots, delete protection.
- **Metrics**: CPU, disk and public network of running servers (`/servers/{id}/metrics`), sampled every 15 seconds.
  The lab keeps them for a day and in memory only: they start anew when the lab restarts (Hetzner keeps 30 days).
- **DNS zones**: zones and RRSets of the DNS API (all record types, zone file export and import, protection, Hetzner's
  SOA and nameservers, the serial counted on). The lab's resolver answers for them inside the lab, so servers,
  clients and tools see a change at once.
- **ACME**: Pebble, Let's Encrypt's test server, issues certificates inside the lab. It checks DNS-01 against the DNS
  zones and HTTP-01 (port 80) and TLS-ALPN-01 (port 443) on the servers; those checks come from `192.0.2.2`, so a
  Cloud firewall must let them in. `HLAB_ACME_DIRECTORY` and `HLAB_ACME_CA` (`hlab env`) tell clients where it is
  and what to trust: the lab CA, which signs the directory's HTTPS and stays, and Pebble's root, which is new after
  every start of the lab.
- **Certificates and load balancers**: uploaded certificates, and managed ones that the lab issues at its ACME
  server through DNS-01 in the DNS zones (as on Hetzner, each domain needs a zone). Load balancers (lb11, lb21, lb31)
  with TCP, HTTP and HTTPS services, servers, label selectors and IPs as targets, health checks, sticky sessions and
  the redirect to HTTPS: HAProxy runs each one in a network namespace of its own with public IPv4 and IPv6. The
  targets see the load balancer's address; the client's address reaches them through the PROXY protocol or in
  `X-Forwarded-For` (with `-Proto` and `-Port`).
- **Networks**: cloud networks with subnets and routes (pushed by DHCP, forwarded to the gateway server), alias IPs,
  hot-plugged NICs, overlapping networks; Floating IPs routed to their server; firewalls with Hetzner's semantics;
  latency between locations. A server sends only from its own public addresses, so the source a server sees is the
  real client IP.
- **Volumes**: SCSI disks as `/dev/disk/by-id/scsi-0HC_Volume_<id>`, hot-plugged, formatted on request, resized,
  automounted.
- **Storage Box**: boxes, sub-accounts, snapshots and plans, rollback and access settings through the API; SFTP on
  port 22 (always on, keys in RFC4716 format), SFTP and commands (`rsync`, `scp`, `borg`, ...) on port 23 (with
  `ssh_enabled`, keys in OpenSSH format); snapshots under `/home/.zfs/snapshot`. A plain SFTP rename never overwrites.
- **Catalogue and prices**: Hetzner's x86 server types of `eu-central` (CX, CPX, CCX) and Storage Box types with
  their prices from `hlab.yaml`, and `/v1/pricing`.
- **Compatibility** is checked by the tests: every API response against Hetzner's OpenAPI specification, and the
  OpenTofu smoke test with Hetzner's own provider (an empty second plan, a clean destroy).

### Not like Hetzner

- Not offered: rescue, ISOs, console. Samba and WebDAV of Storage Boxes are only reported.
- Load balancers in private networks (`use_private_ip`, attach, detach), without a public interface, and their
  metrics are not there yet. Managed certificates are not renewed (Pebble's last five years).
- Only the network zone `eu-central` and x86: no locations in the US or Asia, no Arm types (CAX), not even in the
  catalogue. Every server type is offered in every location.
- DNS zones in primary mode only (secondary zones are refused). As on Hetzner, a zone is a domain right below a
  well-known public suffix (`example.com`, not `example.test`); inside the lab it hides the real domain. Nothing
  delegates to the lab, so `delegation_status` stays `unknown`.
- No IPv6 to the real Internet, no vSwitch. All Storage Boxes share `storage_box.size_gb`; SCP on port 22 only
  through SFTP (OpenSSH's default).
- A server's `locked` is true while one of its actions runs; on Hetzner it marks an administrative lock. Wait for the
  action instead.
- A test lab, not a hosting platform: the guests run your own code. The lab keeps the Docker host and its network
  out of reach, but it does not defend servers and users against each other (`/_lab` has no authentication, QEMU
  runs as root in the container, cloud networks have no source checks).

## Developing

How the code fits together is in [DEVELOPING.md](DEVELOPING.md), the standards it follows in [STYLE.md](STYLE.md).
