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
| Web UI (from the host's browser) | `http://localhost:8480` (`ui_port`) |
| SOCKS5 proxy into the lab (from the host) | `socks5h://localhost:1080` (`socks_port`) |
| Network namespace to reach VMs | `docker run --network container:hlab-<name> ...` |

`hlab env` prints these as shell variables (`HCLOUD_ENDPOINT`, `HCLOUD_TOKEN`, `HETZNER_ENDPOINT`,
`HLAB_ACME_DIRECTORY`, `HLAB_ACME_CA`, `HLAB_UI`, `HLAB_SOCKS`, ...). The web UI shows everything the lab holds, refreshed every
two seconds, and the log as it grows; it sets faults, adds clients and resets the lab like `bin/hlab fault`,
`client` and `reset`. Docker publishes it on the host's localhost only.

## Requirements

Docker with `/dev/kvm` (Linux, or Docker Desktop on Windows with WSL2). Check:
`docker run --rm --device /dev/kvm alpine ls -l /dev/kvm`. On Windows run `bin/hlab` from Git Bash or WSL; the unit
tests run there through Docker (`bin/hlab test`), not from a local venv.

## Use

```bash
bin/hlab -c examples/hlab.yaml up        # build the image (cached), load the OS images, start hlab-demo
bin/hlab -c examples/hlab.yaml status    # servers, Floating IPs, clients, faults
bin/hlab -c examples/hlab.yaml logs      # what the lab does: requests, actions, VMs
bin/hlab -c examples/hlab.yaml down      # stop; disks and state stay in the volume hlab-demo
bin/hlab -c examples/hlab.yaml purge     # stop and delete the volume
bin/hlab lint                           # style checks (STYLE.md)
bin/hlab test                           # style checks and unit tests (no VMs)
bin/hlab format                         # apply what the formatters can fix
bin/hlab -c examples/hlab.yaml itest     # live tests with VMs (resets the lab, ~15 min)
tests/tofu-smoke/run.sh                # OpenTofu with the hcloud provider against the running lab
```

`examples/hlab.yaml` shows every setting: the catalogue (types, locations, OS images, prices), timings, the Storage
Box. Servers are created through the API, for example with OpenTofu:

```bash
eval "$(bin/hlab env)"
tofu() { docker run --rm -i --network "$HLAB_NETWORK" -e HCLOUD_ENDPOINT -e HCLOUD_TOKEN -e HETZNER_ENDPOINT \
    -v "$(pwd -W 2>/dev/null || pwd):/work" -w /work ghcr.io/opentofu/opentofu:1 "$@"; }
tofu init && tofu apply
```

### From the host

The lab's addresses exist only in the lab's network. Two ways lead there from this host; in the lab, both come
from `192.0.2.2`, like any tool in the lab's namespace, so a Cloud firewall must let them in.

SSH, and with it scp, rsync and Ansible, through `bin/hlab proxy`:

```bash
ssh -o ProxyCommand="bin/hlab proxy %h %p" -i ~/.ssh/id_ed25519 root@198.51.100.11
```

```ini
# Ansible inventory: the addresses from `tofu output`, the key the servers got through OpenTofu
[lab]
198.51.100.11
198.51.100.12

[lab:vars]
ansible_user=root
ansible_ssh_common_args='-o ProxyCommand="/path/to/hlab/bin/hlab -c /path/to/hlab.yaml proxy %h %p"'
```

Everything else (a browser, curl) through the SOCKS5 proxy. With `socks5h` the lab resolves the names, so the
names of its DNS zones work as inside the lab; for HTTPS with managed certificates, trust `HLAB_ACME_CA`.

```bash
curl --proxy socks5h://localhost:1080 http://198.51.100.11/
```

Ansible does not run on Windows itself; there it runs in WSL, where `docker` and `bin/hlab` work alike.

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
bin/hlab image export 7 base.qcow2        # an image of the project (snapshot or backup) to a file here
bin/hlab image import base.qcow2 --label role=base   # a qcow2 file as a new snapshot; prints its ID
bin/hlab ssh a uptime                     # root on a server with the lab's SSH key
bin/hlab reset                            # delete everything the APIs created
```

## What it does

- **Servers**: boot through cloud-init's Hetzner datasource, Hetzner's timings and `locked` (423), rebuild, type
  change, password reset, reverse DNS, primary IPs, SSH keys, placement groups.
- **Images**: snapshots and backups (daily, seven slots), servers created or rebuilt from them. `image export`
  and `image import` move an image between labs as a qcow2 file, so a prepared image outlives its lab.
- **Networks**: cloud networks with subnets, routes and alias IPs, hot-plugged NICs, Floating IPs, firewalls with
  Hetzner's semantics, latency between locations. Servers see the real client IP.
- **Volumes**: hot-plugged SCSI disks, formatted, resized, automounted.
- **Load balancers**: TCP, HTTP and HTTPS on HAProxy, servers, label selectors and IPs as targets, health checks,
  private networks; the real client IP through the PROXY protocol or `X-Forwarded-For`.
- **Certificates**: uploaded, or managed: issued by the lab's ACME server (Pebble) through DNS-01 in the DNS zones.
- **DNS zones**: zones and RRSets, zone files, served by the lab's resolver inside the lab.
- **Storage Box**: boxes, sub-accounts, snapshots and plans through the API; SFTP on port 22, SSH commands (`rsync`,
  `borg`, ...) on port 23.
- **Metrics** of servers and load balancers, **prices** and the **catalogue** of Hetzner's x86 types in
  `eu-central`.
- **Compatibility** is checked by the tests: every API response against Hetzner's OpenAPI specification, and the
  OpenTofu smoke test with Hetzner's own provider.

### Not like Hetzner

- Not offered: rescue, ISOs, console, vSwitch, IPv6 to the real Internet. Samba and WebDAV of Storage Boxes are only
  reported. IPv6 packets to the Internet are refused at once (ICMPv6 no route), so clients fall back to IPv4; a
  client that waits on UDP, such as systemd-timesyncd with the Debian NTP pool, takes about a minute to get there.
- Only the network zone `eu-central` and x86 (no CAX), not even in the catalogue; every server type is offered in
  every location.
- DNS zones in primary mode only. A zone is a domain right below a well-known public suffix (`example.com`, not
  `example.test`) and hides the real domain inside the lab.
- ACME: checks come from `192.0.2.2`, so a Cloud firewall must let them in; Pebble's root is new after every start
  of the lab (`HLAB_ACME_CA` has the current one). Managed certificates are not renewed (they last five years).
- An imported image is a snapshot without `created_from` and with `os_flavor` `unknown`; Hetzner has no import.
- A load balancer is in one cloud network at most.
- Metrics reach back one day and start anew when the lab restarts (Hetzner keeps 30 days).
- All Storage Boxes share `storage_box.size_gb`; SCP on port 22 only through SFTP (OpenSSH's default).
- A server's `locked` is true while one of its actions runs; on Hetzner it marks an administrative lock. Wait for the
  action instead.
- A test lab, not a hosting platform: the guests run your own code. The lab keeps the Docker host and its network
  out of reach, but it does not defend servers and users against each other (`/_lab` has no authentication, QEMU
  runs as root in the container, cloud networks have no source checks).

## Developing

How the code fits together is in [DEVELOPING.md](DEVELOPING.md), the standards it follows in [STYLE.md](STYLE.md).
