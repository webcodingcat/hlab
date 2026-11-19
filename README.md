# hlab

hlab is a local fake Hetzner Cloud on Hyper-V under Windows. Unmodified
Hetzner clients (hcloud CLI, OpenTofu provider hetznercloud/hcloud, Ansible
collection hetzner.hcloud) talk to it as if it were the real API, and the
servers they create are real Hyper-V VMs you can reach with SSH from
Windows and from WSL2.

The project is in development; there is no release and no installer yet.
You run hlab from a working copy as described below. The current version
covers projects, tokens, SSH keys, servers with snapshots, backups,
rescue system and ISOs, volumes, placement groups, private networks,
primary IPs and floating IPs; see
[What does not work yet](#what-does-not-work-yet).

## Prerequisites and setup

For developing hlab you need:

- Windows 11 Pro, Enterprise or Education (or Windows Server 2022/2025 with
  Desktop Experience), x64, with hardware virtualization on.
- Hyper-V and its PowerShell module enabled, and your account in the group
  "Hyper-V Administrators". For the end-to-end tests at least 16 GB RAM and
  100 GB free disk.
- On the host: Go 1.27.4, Node.js 24 LTS or newer, Task v3.54.0,
  golangci-lint v2.15.0, WiX Toolset v7.0.1 (`dotnet tool install --global wix`),
  Wails v3.1.0 (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`),
  hcloud CLI v1.70.2 and OpenTofu 1.13.9.
- WSL2 with an Ubuntu 24.04 or newer distribution in NAT mode with
  `dnsTunneling` on (the defaults), and in it:
  - `uv`
  - ansible-core 2.22.0 with hcloud-python 2.27.0:
    `uv tool install ansible-core==2.22.0 --with hcloud==2.27.0`
  - the collection: `ansible-galaxy collection install hetzner.hcloud:==7.2.0`
- Internet access for image, ISO, package and OpenAPI downloads.

Setup:

1. Clone the repository and run `task build`.
2. Once per machine, in an administrator PowerShell in the repository:
   `.\scripts\dev-setup.ps1`. It registers the development service
   `hlab-dev` that runs `bin\hlabd.exe` of your working copy as LocalSystem
   and lets interactive users start and stop it, and it creates the data
   folder `%ProgramData%\hlab-dev`. Building, starting and testing then
   need no elevated rights. This setup is for development machines only:
   any process of your account can act with system rights through it.
3. Optional: create an hcloud context for the real Hetzner API, named
   `HLab` (or set `HLAB_HCLOUD_CONTEXT` to its name), with a read-only
   token: `hcloud context create HLab`. `task catalog` uses it, and asks for
   a token instead when hcloud or the context is missing.

The developer guide in [docs/dev/](docs/dev/setup.md) explains the build,
the tests and the code.

## First steps

Start the cloud. The first start creates the virtual switch and the
appliance VM and takes a few minutes; wait until the status says
`running`:

```
> bin\hlab.exe cloud start
the cloud is starting
> bin\hlab.exe cloud status
state: running
version: dev
```

Create a project and a token for it. The token is printed once and cannot
be shown again:

```
> bin\hlab.exe project create demo
project demo created
> bin\hlab.exe token create --project demo --name ci
<64-character token>
```

Point hcloud at hlab with two environment variables (PowerShell):

```powershell
$env:HCLOUD_ENDPOINT = "http://api.hetzner.internal/v1"
$env:HCLOUD_TOKEN = "<the token>"
```

Then use hcloud as with the real cloud:

```
> hcloud ssh-key create --name demo --public-key-from-file $HOME\.ssh\id_ed25519.pub
SSH Key 2 created
> hcloud server create --name web1 --type cx23 --location fsn1 --image ubuntu-24.04 --ssh-key demo
Waiting for create_server (server: 4) ...
Waiting for create_server (server: 4) ... done
Server 4 created
IPv4: 198.19.0.7
IPv6: 2001:db8:0:4::1
IPv6 Network: 2001:db8:0:4::/64
> ssh root@198.19.0.7
```

The server gets the CPUs, memory and disk of its server type. The first
server with an image downloads that image from its official source, so it
takes longer; later servers reuse the cached copy. `server create` returns
when the VM runs; a fresh server needs about a minute more until
cloud-init has finished and SSH accepts the key.

Clean up with `bin\hlab.exe project delete demo`, which deletes the
server and the SSH key with the project, and `bin\hlab.exe cloud stop`.

All `hlab` commands are in the [command reference](docs/user/commands.md),
the settings in the [configuration reference](docs/user/configuration.md),
and the differences from the real API in
[Known deviations](docs/user/deviations.md).

## Managing servers

The server actions of hcloud work on hlab's VMs:

```
> hcloud server shutdown web1
> hcloud server poweron web1
> hcloud server reboot web1
> hcloud server poweroff web1
> hcloud server change-type web1 cx33
> hcloud server rebuild --image debian-13 web1
> hcloud server reset-password web1
> hcloud server metrics --type cpu web1
```

- `poweroff` and `reset` cut the power; `shutdown` and `reboot` ask the
  guest.
- `change-type` needs a stopped server and starts it again afterwards. It
  grows the disk to the new type's size unless you pass `--keep-disk`;
  with `--keep-disk` you can later change back to a smaller type.
- `rebuild` replaces the disk with a fresh system image and keeps the ID
  and the addresses, and the user data unless you pass new. A server
  without SSH keys gets a new root password.
- A server created without `--ssh-key` gets a root password, printed
  once; root can log in with it over SSH. `reset-password` sets a new one
  on a running server.
- `metrics` shows CPU, disk and network; hlab keeps them for 24 hours
  while the cloud runs.

Inside a server, the metadata is at `http://169.254.169.254/hetzner/v1/metadata`,
as at Hetzner:

```
root@web1:~# curl http://169.254.169.254/hetzner/v1/metadata/hostname
```

A cloud with too little free RAM or disk, beyond the reserve set in the
[configuration](docs/user/configuration.md#settingstoml), refuses to
create or start a server with `resource_unavailable`.

## Snapshots, backups and rescue

```
> hcloud server create-image --type snapshot --description web1-base web1
Waiting for create_image (server: 88) ...
Waiting for create_image (server: 88) ... done
Image 2 created from Server 88
> hcloud server create --name web2 --type cx23 --location fsn1 --image 2 --ssh-key demo
> hcloud server enable-backup web1
> hcloud server create-image --type backup web1
> hcloud image list --type snapshot --type backup
```

- A snapshot copies the server's disk while it runs; servers created or
  rebuilt from it start with that disk.
- With backups enabled, hlab makes one backup per server and day while
  the cloud runs and keeps the newest 7. Deleting the server deletes its
  backups; snapshots stay.

The rescue system is a Debian 13 system with your SSH keys and a new root
password. `enable-rescue` prepares it; the next boot runs it:

```
> hcloud server enable-rescue --ssh-key demo web2
Waiting for enable_rescue (server: 89) ...
Waiting for enable_rescue (server: 89) ... done
Rescue enabled for Server 89 with root password: <password>
> hcloud server reboot web2
> ssh root@198.19.0.92
root@web2:~# findmnt -n -o SOURCE /
/dev/sdb1
```

The server's own disk is `/dev/sda`. The next `reboot` starts the
installed system again. The first `enable-rescue` downloads the debian-13
image if no server used it yet.

## ISOs

```
> hcloud iso list
ID       NAME                                   DESCRIPTION                          TYPE     ARCHITECTURE
130001   ubuntu-22.04.5-live-server-amd64.iso   Ubuntu 22.04.5 Live Server (amd64)   public   x86
...
> hcloud server attach-iso web2 ubuntu-22.04.5-live-server-amd64.iso
> hcloud server reset web2
> hcloud server detach-iso web2
```

An attached ISO boots before the server's disk until you detach it. The
first attach of an ISO downloads it, which takes minutes; later attaches
use the cached copy.

## Volumes

```
> hcloud volume create --name data1 --size 10 --server web1 --automount --format ext4
Waiting for create_volume (volume: 10) ...
Waiting for attach_volume (server: 88) ...
Waiting for create_volume (volume: 10) ... done
Waiting for attach_volume (server: 88) ... done
Volume 10 created
> ssh root@198.19.0.91 df -h /mnt/HC_Volume_10
Filesystem      Size  Used Avail Use% Mounted on
/dev/sdb        9.8G  2.1M  9.3G   1% /mnt/HC_Volume_10
```

- In the server the volume is `/dev/disk/by-id/scsi-0HC_Volume_<id>`; with
  `--automount` it is mounted at `/mnt/HC_Volume_<id>`, also after a later
  `hcloud volume attach --automount`.
- `hcloud volume resize --size 20 data1` grows the disk; grow the
  filesystem in the server, for example with
  `resize2fs /dev/disk/by-id/scsi-0HC_Volume_10`.
- `hcloud volume detach` and `attach` move a volume between servers of
  its location; its data stays. Deleting a server detaches its volumes.
- `--format xfs` needs packages that hlab downloads for the appliance at
  `hlab cloud start`.

## Networks

```
> hcloud network create --name net1 --ip-range 10.0.0.0/16
> hcloud network add-subnet net1 --type cloud --network-zone eu-central --ip-range 10.0.1.0/24
> hcloud server create --name app1 --type cx23 --location fsn1 --image ubuntu-24.04 --ssh-key demo --network net1
> hcloud server attach-to-network --network net1 web1
> hcloud server describe web1
```

- A server gets the lowest free address of a subnet in its location's
  network zone, or the one you pass with `--ip`; `describe` shows it under
  `Private Net`. Servers of one network reach each other on these
  addresses; servers of different networks do not.
- In the server, the network appears as an adapter with the private IP as
  `/32` and a route to the network range via the network's gateway, the
  first host address of the range (`10.0.0.1` here). A server attached while
  it runs gets the adapter and its configuration within a few seconds;
  `detach-from-network` removes them.
- A server holds at most 3 networks and 5 alias IPs per network.
- Deleting a network detaches its servers.

Alias IPs and routes to destinations outside the network range are not
configured in the guest, as at Hetzner. Add them yourself; the adapter is
the one whose MAC is the `mac_address` of the network in the server's
`private_net`:

```
> hcloud server change-alias-ips --network net1 --alias-ips 10.0.1.10 web1
root@web1:~# ip -br link
root@web1:~# ip addr add 10.0.1.10/32 dev eth1
```

A network route sends traffic for its destination to a server of the
network. Add the route on the network, then on each server that uses it:

```
> hcloud network add-route net1 --destination 10.99.0.0/24 --gateway 10.0.1.2
root@app1:~# ip route add 10.99.0.0/24 via 10.0.0.1 dev eth1 onlink
```

## Primary and floating IPs

A server's public addresses are primary IPs. Those made with the server
are deleted with it; one you create yourself stays and can move between
powered-off servers of its location:

```
> hcloud primary-ip create --type ipv4 --location fsn1 --name ip1
> hcloud server create --name web3 --type cx23 --location fsn1 --image ubuntu-24.04 --ssh-key demo --primary-ipv4 ip1
> hcloud server poweroff web3
> hcloud primary-ip unassign ip1
> hcloud server create --name web4 --type cx23 --location fsn1 --image ubuntu-24.04 --ssh-key demo --without-ipv4 --start-after-create=false
> hcloud primary-ip assign --server web4 ip1
> hcloud server poweron web4
```

The server uses its new address from its next start. Primary IPv6
addresses are formal and carry no traffic.

A floating IP can move between running servers of a network zone. hlab
routes it to the server's public IPv4; configure the address in the
server, as at Hetzner:

```
> hcloud floating-ip create --type ipv4 --home-location fsn1 --name fip1
> hcloud floating-ip describe fip1
> hcloud floating-ip assign fip1 web1
root@web1:~# ip addr add <floating IP>/32 dev eth0
```

After `hcloud floating-ip assign fip1 web2` and the same command in web2,
the address reaches web2; web1 needs no change.

## OpenTofu and Ansible

The OpenTofu provider and the Ansible collection read the same two
variables as hcloud, so the provider block and the module calls stay as
they are for the real cloud. For OpenTofu on Windows:

```powershell
$env:HCLOUD_ENDPOINT = "http://api.hetzner.internal/v1"
$env:HCLOUD_TOKEN = "<the token>"
```

```hcl
terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "1.70.2"
    }
  }
}

provider "hcloud" {}
```

For Ansible in WSL2 (see [WSL2](#wsl2)), set them in the shell that runs
`ansible-playbook`:

```bash
export HCLOUD_ENDPOINT=http://api.hetzner.internal/v1
export HCLOUD_TOKEN=<the token>
```

When ansible-core and hcloud-python are installed with `uv` into their own
environment, set `ansible_python_interpreter: "{{ ansible_playbook_python }}"`
in plays for `localhost`, so the modules find hcloud-python:

```yaml
- hosts: localhost
  connection: local
  gather_facts: false
  vars:
    ansible_python_interpreter: "{{ ansible_playbook_python }}"
  tasks:
    - hetzner.hcloud.server_info:
      register: servers
```

## WSL2

hcloud, OpenTofu and Ansible in a WSL2 distribution reach
`api.hetzner.internal` and the servers' addresses as on Windows, with the
same endpoint and token. This needs WSL2 in NAT networking mode with
`dnsTunneling` on, which are the WSL defaults. In
`%UserProfile%\.wslconfig`, section `[wsl2]`, leave `networkingMode` unset
or set it to `nat`, and leave `dnsTunneling` unset or set it to `true`.

At every `hlab cloud start`, hlab reads the `.wslconfig` of the user
logged on at the console. When it finds `networkingMode` set to
`mirrored`, `virtioproxy`, `bridged` or `none`, or `dnsTunneling=false`,
the cloud starts anyway and `hlab cloud status` shows a `problem:` line
saying what to change. Change `.wslconfig`, run `wsl --shutdown`, and stop
and start the cloud. hlab only reads the file; it does not notice when WSL
falls back to another mode on its own.

hlab enables IPv4 forwarding for the WSL adapter when the cloud starts and
restores the previous setting when it stops. It does not yet notice when
WSL recreates its adapter (after `wsl --shutdown`); stop and start the
cloud again in that case.

## The release and the development service

Every build belongs to a profile. `task build` builds the `dev` profile:
service `hlab-dev`, data in `%ProgramData%\hlab-dev`, Hyper-V objects named
`hlab-dev...`. A future installed hlab uses the `release` profile with the
name `hlab`. Both may be installed on one machine, but only one cloud runs
at a time: a cloud refuses to start while the other one runs, and
`hlab cloud status` names the reason.

## Removing the development service

In an administrator terminal in the repository:

```
bin\hlabd.exe uninstall-cleanup
```

It stops and removes the service `hlab-dev`, removes the host network
settings it made (route, NAT, DNS rules, firewall rule) and deletes
`%ProgramData%\hlab-dev`. With `--keep-data` the data folder stays. The
Hyper-V VMs and the virtual switch `hlab-dev` stay; remove them in Hyper-V
Manager if you want them gone.

## What does not work yet

- The API offers actions, locations, server types, pricing, images
  (with snapshots and backups), ISOs, SSH keys, servers, server metrics,
  placement groups, volumes, networks, primary IPs and floating IPs.
  Everything else, for example firewalls, certificates and load
  balancers, answers `not_found`, and the server action `request_console` answers
  `unsupported_error`. The details are in
  [Known deviations](docs/user/deviations.md).
- `hlab cloud stop` stops the appliance and the host networking, but does
  not save or shut down the server VMs; they stay as they are in Hyper-V.
- There is no tray program and no installer.
