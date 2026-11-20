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
primary IPs, floating IPs, firewalls, DNS zones, reverse DNS, load
balancers and certificates, and a [tray program](#the-tray-program) for
the cloud, projects, settings, server types and logs; see
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

The developer guide in [docs/development/](docs/development/setup.md) explains the build,
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

All `hlab` commands are in the [command reference](docs/commands.md),
the settings in the [configuration reference](docs/configuration.md),
and the differences from the real API in
[Known deviations](docs/development/api-coverage.md).

## The tray program

```
> bin\hlab-tray.exe
```

opens the hlab window and puts the hlab icon in the notification area.
The icon and its tooltip show the state of the cloud: running, stopped,
starting or stopping, or an error (start refused, start failed,
connection to the service lost). Right-click the icon for its menu:

- the state line, for example `Cloud running`;
- **Open hlab** shows the window, as a click on the icon does;
- **Start cloud** and **Stop cloud**, each enabled when it applies;
- **Quit** ends the tray program; the cloud keeps running.

Closing the window only hides it. Starting `hlab-tray.exe` again shows
the window of the tray that already runs. The tray program does not start
with Windows.

The top bar of the window shows the cloud state with **Start cloud** or
**Stop cloud**. A banner reports a lost connection to the service or a
failed start. When the cloud refused to start because of its state, the
banner offers **Discard state and start**, which does what
`hlab cloud start --discard-state` does (see
[Discarding the state](#discarding-the-state)) after you confirm.

The sidebar leads to four views; the `?` button in each opens its help:

- **Projects** (needs the running cloud): **Create project**; click a
  project to see its API tokens. **Create token** asks for a name and
  whether the token is read only, and shows the token once with a copy
  button. **Revoke** and **Delete** are in the row menu (`…`); deleting a
  project deletes all its resources and needs its name typed.
- **Server Types**: the catalog and your own types, with their source,
  also while the cloud is stopped. **Add server type** asks for name,
  description, cores, memory, disk, CPU type and locations; **Remove** in
  the row menu removes an own type. **Update from Hetzner** does what
  `hlab catalog update` does with the Hetzner token you enter; the token
  is not stored. See
  [Custom server types](docs/configuration.md#custom-server-types).
- **Settings**: every key of `settings.toml`; each field says when a
  change applies. **Save settings** checks all fields first. The switch
  **Trust the hlab CA** and **Save CA certificate** do what
  `hlab ca trust` and `hlab ca export` do (see
  [Trusting the hlab CA](#trusting-the-hlab-ca)) and need the running
  cloud.
- **Logs**: the last 1000 lines of the service log and new ones as they
  are written, also while the cloud is stopped. **Level** hides lines
  below a level. Turning **Follow** off stops new lines; turning it on
  again reloads the last 1000 lines and follows on.

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
[configuration](docs/configuration.md#settingstoml), refuses to
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

## Firewalls

```
> hcloud firewall create --name fw1
> hcloud firewall add-rule fw1 --direction in --protocol tcp --port 22 --source-ips 0.0.0.0/0
> hcloud firewall add-rule fw1 --direction in --protocol icmp --source-ips 0.0.0.0/0
> hcloud firewall apply-to-resource fw1 --type server --server web1
> hcloud firewall apply-to-resource fw1 --type label_selector --label-selector role=web
> hcloud server add-label web2 role=web
```

A firewall filters a server's public IPv4 and the floating IPs assigned
to it, as at Hetzner:

- A server without firewall accepts all traffic. Once a firewall is
  applied, inbound traffic is dropped unless an `in` rule allows it, ICMP
  included.
- Outbound traffic is allowed until one of the server's firewalls has an
  `out` rule; from then on only what the `out` rules allow leaves the
  server.
- The firewalls of a server are combined: what any of them allows passes.
  A server has at most 5 firewalls.
- Firewalls are stateful: replies to an allowed connection pass, and a
  rule change does not cut connections that are already open.
- A label selector reaches every server of the project with a public
  IPv4 whose labels match, also when the labels change later.
- Private networks are never filtered, and neither are the cloud's own
  services: DNS, the metadata service and the API.
- Rules for IPv6 addresses are stored but have no effect.

## DNS zones

Zones you create in a project resolve on the host, in WSL2 and in the
servers while the cloud runs:

```
> hcloud zone create --name lab.internal
> hcloud zone add-records lab.internal www A --record 198.19.0.7
> hcloud zone add-records lab.internal '*' A --record 198.19.0.7
> Resolve-DnsName www.lab.internal
```

A zone name is a registrable domain such as `example.com`, or one label
below `internal`, `test` or `home.arpa` such as `lab.internal`. The
private-use names can never collide with a real domain; prefer them.
RRSets, TTLs, protection and the zone file import and export
(`hcloud zone export-zonefile`, `hcloud zone import-zonefile`) work as at
Hetzner.

The appliance's DNS server answers for every zone. On the host, hlab adds
one NRPT rule per zone that sends its names to the appliance, and removes
it when the zone is deleted or the cloud stops; WSL2 uses the host's
resolver through `dnsTunneling`. Names outside your zones resolve as
before.

A zone named like a real domain hides that domain on your machine while
the cloud runs. hlab checks this and `hlab cloud status` then shows a
warning line for the zone; see the
[command reference](docs/commands.md#hlab-cloud-status).

A secondary zone is accepted but not transferred: its names are looked up
in the public DNS as before. The details are in
[Known deviations](docs/development/api-coverage.md#zones).

### Reverse DNS

The PTR records of servers, primary IPs and floating IPs resolve through
the same DNS server. A public IPv4 starts with Hetzner's default name
`static.<d>.<c>.<b>.<a>.clients.your-server.de`:

```
> hcloud server set-rdns web1 --ip 198.19.0.7 --hostname www.lab.internal
> Resolve-DnsName 198.19.0.7
> hcloud server set-rdns web1 --ip 198.19.0.7 --reset
```

`hcloud primary-ip set-rdns` and `hcloud floating-ip set-rdns` work the
same way. IPv6 entries are stored but do not resolve.

## Load balancers and certificates

A load balancer distributes connections to servers, label selectors or IP
addresses. The targets need a service on the destination port; here a web
server on port 80 in web1 and web2:

```
> hcloud server add-label web1 role=web
> hcloud server add-label web2 role=web
> hcloud load-balancer create --name lb1 --type lb11 --location fsn1
> hcloud load-balancer add-target lb1 --label-selector role=web
> hcloud load-balancer add-service lb1 --protocol http --listen-port 80 --destination-port 80
> hcloud load-balancer describe lb1
> curl.exe http://<load balancer IPv4>/
```

`describe` shows the public IPv4 and the health of each target; a new
target shows `unknown` until its first health check, a few seconds
later. TCP services, round robin and least connections, sticky
sessions, the redirect from HTTP to HTTPS, health checks, private
networks (`--network`, `--use-private-ip`), the PROXY protocol and
`hcloud load-balancer metrics` work as at Hetzner. Each load balancer is
an HAProxy instance in the appliance VM; the differences are in
[Known deviations](docs/development/api-coverage.md#load-balancers).

An HTTPS service takes uploaded or managed certificates. A managed
certificate is issued at once when its domains lie in a DNS zone of the
project, and renewed before it expires:

```
> hcloud zone add-records lab.internal www A --record <load balancer IPv4>
> hcloud certificate create --name www --type managed --domain www.lab.internal
> hcloud load-balancer add-service lb1 --protocol https --listen-port 443 --destination-port 80 --http-certificates www
```

Managed certificates come from the hlab CA, which hlab creates at its
first start, not from Let's Encrypt. No client trusts it until you
decide so; see the next section.

### Trusting the hlab CA

To reach the service with a plain `curl https://<name>`, add the hlab CA
to the trust stores of Windows and WSL2:

```
> bin\hlab.exe ca trust on
the hlab CA is trusted on this host
> curl.exe https://www.lab.internal/
```

and in WSL2:

```bash
curl https://www.lab.internal/
```

`bin\hlab.exe ca trust off` removes it again from both. The switch is
off until you turn it on, and stays on across cloud starts.

The risk: the hlab CA cannot be limited to your zones, because zones may
use any domain, real ones such as `example.com` included. While the CA is
trusted, anyone who obtains its private key can create certificates that
Windows and WSL2 accept for any domain. Turn trust on only on a
development machine, and off when you no longer need it. hlab never adds
the CA to servers.

Without trust, give the CA certificate to the client that needs it:

```
> bin\hlab.exe ca export hlab-ca.pem
hlab CA written to hlab-ca.pem
```

```bash
curl --cacert hlab-ca.pem https://www.lab.internal/
```

In a server, pass the file with cloud-init (`ca_certs`) or copy it
there yourself.

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

## Stopping and starting the cloud

The cloud never starts with Windows. Stop it when you need the memory
back and start it again later; servers, IPs, networks, firewalls, volumes,
load balancers and DNS zones are kept:

```
> bin\hlab.exe cloud stop
the cloud is stopping
> bin\hlab.exe cloud start
the cloud is starting
```

With `stop_behaviour = "save"` (the default) the stop saves the running
servers and the start resumes them where they were. With `"shutdown"` the
servers shut down cleanly and the start boots those that were running.
Servers that were off stay off. A Windows shutdown with a running cloud
does the same through Hyper-V; after the reboot, start the cloud again.
The details, also on changing the setting, are under
[Stop behaviour](docs/configuration.md#stop-behaviour).

When something goes wrong, `bin\hlab.exe logs` shows the end of the
service's log, also while the cloud is stopped; `--follow` keeps
watching it.

### Discarding the state

hlab refuses to start with a state it cannot use: one written by a newer
hlab version, a damaged state file, or a state that holds addresses
outside a changed `public_range`. `bin\hlab.exe cloud status` then shows
`state: refused` and the reason. Set the range back, or, if you can lose
everything in the cloud, confirm the discard:

```
> bin\hlab.exe cloud start --discard-state
the cloud is starting
```

This deletes all projects, servers with their disks, volumes, snapshots
and backups, keeps the old state file renamed to
`state.db.discarded-<time>`, and starts empty. A usable state is never
discarded by this flag.

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
Manager if you want them gone. The hlab CA stays in the trust stores of
Windows and WSL2: run `bin\hlab.exe ca trust off` while the cloud still
runs, before you remove the service.

## What does not work yet

- The API offers actions, locations, server types, pricing, images
  (with snapshots and backups), ISOs, SSH keys, servers, server metrics,
  placement groups, volumes, networks, primary IPs, floating IPs,
  firewalls, DNS zones, load balancer types, load balancers and
  certificates. Every other endpoint answers `not_found`, and the server
  action `request_console` answers `unsupported_error`. The details are
  in [Known deviations](docs/development/api-coverage.md).
- The tray program has views for projects and tokens, server types,
  settings and logs only; manage the resources of a project with hcloud,
  OpenTofu or Ansible.
- There is no installer.
