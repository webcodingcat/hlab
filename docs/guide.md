# Guide

How to use the resources of HLab with hcloud, OpenTofu, Ansible and the
HLab window. The examples assume a running cloud and hcloud pointed at a
project, as in [First steps](../README.md#first-steps): a server `web1`
with the SSH key `demo` in location `fsn1`. The differences from Hetzner are collected in
[Known deviations](deviations.md).

## Projects

A project has a display name, such as `Web Coding Cat`, and a name that
HLab derives from it, `web-coding-cat`. The window shows the display
name; `hlab` commands, the token commands and the window's addresses use
the name. Create a project with
[`hlab project create`](commands.md#hlab-project-create) or with
**+ New project** in the window, which shows the derived name below the
field as you type. A project whose name is taken cannot be created.

The window's start page shows the projects as cards with the display
name, the name and the number of resources. A click on the number opens
a menu with one entry per section, such as "1 SSH Key", that opens that
section; a project without resources offers **+ Create server** instead.
The toolbar searches by display name or name, sorts by display name or
creation time in either direction, and switches between a grid of cards
and a list. The window keeps the sort and the layout.

## Managing servers

The server actions of hcloud work on HLab's VMs:

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
- `metrics` shows CPU, disk and network; HLab keeps them for 24 hours
  while the cloud runs.

Inside a server, the metadata is at `http://169.254.169.254/hetzner/v1/metadata`,
as at Hetzner:

```
root@web1:~# curl http://169.254.169.254/hetzner/v1/metadata/hostname
```

A cloud with too little free RAM or disk, beyond the reserve set in the
[configuration](configuration.md#settingstoml), refuses to
create or start a server with `resource_unavailable`.

### Server console

`request-console` gives the URL and password of the server's screen, as
at Hetzner:

```
> hcloud server request-console web1
Waiting for request_console (server: 732) ...
Waiting for request_console (server: 732) ... done
Console for Server 732:
WebSocket URL: wss://console.hetzner.internal/?server_id=732&token=95be0e57-1b2b-420e-a9bf-7bdfbb0d375a
VNC Password: kRVjPAVG6tjB5TZ0jUdkEa6LZV0kIhFN
```

- The URL works for one connection, started within one minute; an open
  connection stays open until you close it. Request a new URL for every
  connection.
- A VNC client that speaks WebSocket (such as noVNC) shows the screen and
  sends keyboard and mouse input. The console's certificate comes from the HLab CA,
  so your own browser or VNC client accepts it only while the CA is
  trusted (see [Trusting the HLab CA](#trusting-the-hlab-ca)). The
  console window of the HLab window works either way.
- The console works in any server state. While the server is off,
  starting, stopping, saving or restoring, it shows the last picture or
  black and the live picture returns when there is one again.
- Several consoles may be open on one server; they share keyboard and
  mouse. They close when the server is deleted or the cloud stops.
- The picture refreshes up to 5 times a second. Each open console costs
  about 0.7 CPU cores, almost all in the Hyper-V service `vmms.exe`.

## Servers in the window

The HLab window manages the same resources as hcloud. Click a project's
card in the project list; its console opens on **Servers**. The sidebar has the
groups **Cloud** (Servers, Volumes, Firewalls) and **Networking** (Load
Balancers, Networks, IPs, DNS) and the entry **Security**; the tabs above
the page lead to the views of an entry: **Servers** to Servers,
Snapshots, Backups and Placement Groups, **IPs** to Primary IPs and
Floating IPs, **DNS** to DNS Zones, and **Security** to SSH Keys, API
tokens and Certificates. Every page needs the running cloud. The lists of
all sections work alike: the search field takes a name or a label
selector such as `env=dev`, the row menu (`…`) holds the quick actions, a
click on a row opens the detail page with its actions menu and tabs, and
each page's `?` button explains it. The paragraph "In the window" under
each resource below names what its section offers.

Every create form and every **Labels** tab takes labels as chips. Type
`env=dev` or `env` and press Enter, Space or a comma to turn it into a
chip; a label that breaks Hetzner's rules or repeats a key shows an error
under the box and stays in the input, and the form is not sent until
you correct or clear it. Click a chip and press Delete to
remove it, or double-click it to edit it.

- **Servers** lists the servers with status, public IPv4, type, location
  and labels. The search field takes a name or a label selector such as
  `env=dev`. The row menu (`…`) powers a server on, shuts it down,
  reboots, resets, powers it off, opens its console or deletes it.
- **Create server** opens a form in the order of the Hetzner Console:
  location, image (system images, snapshots and backups), type, public
  and private networking, SSH keys, volumes, firewalls, backups,
  placement group, labels, cloud config and name. A server without SSH
  keys gets a root password, shown once with a copy button.
- A click on a server opens its detail page. The `…` menu next to its name
  holds the power actions, **Reset root password**, **Rebuild**,
  **Change type**, **Rename**, the placement group actions, **Console**
  and **Delete**.
  The tabs Networking, Firewalls, Volumes, Backups, Snapshots, Rescue,
  ISO, Graphs, Labels and Protection cover the rest of the actions shown
  above.
- **Console** opens the window "Console: <server name>" with the server's
  screen, scaled to the window. A pill shows **Connecting**,
  **Connected** or **Disconnected** with the reason; **Send
  Ctrl+Alt+Del** sends that key combination, and **Reconnect** appears
  after the connection drops and connects with a new URL. Each server
  has at most one console window; **Console** again brings it to the
  front. Closing the window ends the connection, and quitting the tray
  program closes all console windows. The window works whether or not
  the HLab CA is trusted.
- Power off, reset, rebuild, change type, enable rescue, restoring and
  deleting a backup or snapshot ask for confirmation; deleting a server
  needs its name typed. A root password that **Reset root password**,
  a rebuild, a restore or enabling rescue returns shows once in the same
  dialog as at creation.
- **SSH Keys** lists the keys with their fingerprints. **Add SSH key**
  takes the public key in OpenSSH format, a name and labels; a key's
  detail page changes its name and labels, and **Delete** removes it.

Long actions such as creating, rebuilding or snapshotting show a toast
with their progress until they end.

## Placement groups

```
> hcloud placement-group create --name pg1 --type spread
Placement Group 16 created
> hcloud placement-group list
ID   NAME   SERVERS     TYPE     AGE
16   pg1    0 servers   spread   just now
```

A server joins a group with `hcloud server create --placement-group pg1`
or `hcloud server add-to-placement-group`. A spread group holds at most 10
servers. HLab runs every server on your one machine, so a group only
records its members.

In the window, **Placement Groups** is a tab of **Servers**; **Create
placement group** asks for name and labels (the type is always
`spread`); the
group's page lists its servers, and its actions menu offers **Rename** and
**Delete**. A server joins or leaves a group in the server's actions menu.

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
- With backups enabled, HLab makes one backup per server and day while
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

In the window, snapshots and backups are made in a server's **Snapshots**
and **Backups** tabs. The tabs **Snapshots** and **Backups** of the
sidebar entry **Servers** list the project's snapshots and backups (not
the system images) with size and the server they come from; their
actions menu offers **Edit description**, **Change protection**,
**Delete** and, for a backup, **Convert to snapshot**, which keeps it
when its server is deleted.

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

In the window, attach an ISO in the **ISO** tab of a server's page;
**Administration > ISOs** lists the ISOs.

### Own ISOs

Besides the catalog, HLab serves ISOs you add yourself, in every project.
Add them in **Administration > ISOs** with **Add ISO**: a **Name**, the
**Architecture** and a **Source**, either a **URL** (with an optional
**SHA-256** the download is checked against) or a **Local file**, whose
**Path** you type or choose with **Browse…**. **Edit** and **Remove** are
in an own ISO's row menu; catalog ISOs cannot be changed. The tray keeps
them in [`isos.custom.json`](configuration.md#own-isos).

The API lists an own ISO as a `private` ISO without a name; attach it by
its ID:

```
> hcloud iso list
ID       NAME                                   DESCRIPTION                          TYPE      ARCHITECTURE
130001   ubuntu-22.04.5-live-server-amd64.iso   Ubuntu 22.04.5 Live Server (amd64)   public    x86
...
100000   -                                      netboot.xyz                          private   x86
> hcloud server attach-iso web2 100000
Waiting for attach_iso (server: 383) ...
Waiting for attach_iso (server: 383) ... done
ISO  attached to Server 383
> hcloud server attach-iso web2 netboot.xyz
hcloud: ISO not found: netboot.xyz
```

- An ISO from a URL is downloaded at its first attach, like a catalog
  ISO.
- A local file is copied into HLab's cache when the cloud starts and,
  while the cloud runs, when you save the ISO in the tray. Keep the file
  where it is: at the next start a missing file leaves the ISO out.
- When an own ISO cannot be served, for example because its local file
  is missing, the others still are, and
  [`hlab cloud status`](commands.md#hlab-cloud-status) names the reason
  (the ISOs page names a broken file or a missing local file; a failed
  copy, for example a wrong SHA-256, shows only in the status):

  ```
  > hlab cloud status
  state: running
  version: dev
  problem: own ISO 100002 ("Gone") is left out: open D:/missing/gone.iso: <error message of Windows>
  ```

- A server that has an ISO attached keeps it after you change or remove
  the ISO, until you detach it.

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
- `--format xfs` needs packages that HLab downloads for the appliance at
  `hlab cloud start`.

In the window, **Create volume** takes the size (a number field with + and
− buttons and a slider from 10 to 10,240 GB), the name (proposed as the
first free `volume-<n>`), the **Server** the volume is attached to and
whose location it gets, the **Mount options** **Automatic** or
**Manual**, the **Filesystem** ext4 or xfs, and labels. The window always
creates a volume for a server; create one without a server with hcloud
(`--location` instead of `--server`). **Detach** and
**Delete** are in the row menu; a volume's page shows the Linux device
path with a copy button, and its actions menu offers **Rename**,
**Resize**, **Attach**, **Detach**, **Change protection** and **Delete**.

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

In the window, **Create network** asks for name, IP range and labels. A
network's page has the tabs **Subnets** and **Routes** to add and delete
them, **Resources** with the attached servers and load balancers, and
**Rename** and **Change IP range** in its actions menu; the latter only
grows the range and is refused while servers are attached. Subnets are always of type
`cloud`.

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

A floating IP can move between running servers of a network zone. HLab
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

In the window, **Create primary IP** and **Create floating IP** ask for
a location or a server to assign the address to at once, the protocol,
the name and labels; a primary IP also for **Auto delete**, which deletes
it with its server and which its page switches on and off. **Unassign**
and **Delete** are in the row menu. An IP's actions menu offers
**Rename**, **Assign**, **Unassign**, **Reverse DNS**, **Change protection** and
**Delete**; **Assign** offers only the servers the address can move to.

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

In the window, **Create firewall** takes the inbound and outbound rules,
the servers and label selectors to apply it to, labels and a name. A
firewall's **Rules** tab edits all rules and **Save rules** replaces
them at once; **Resources** applies it to a server or label selector,
shows the servers each selector matches, and removes them again. The
firewall's actions menu offers **Rename** and **Delete**.

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

The appliance's DNS server answers for every zone. On the host, HLab adds
one NRPT rule per zone that sends its names to the appliance, and removes
it when the zone is deleted or the cloud stops; WSL2 uses the host's
resolver through `dnsTunneling`. Names outside your zones resolve as
before.

A zone named like a real domain hides that domain on your machine while
the cloud runs. hlab checks this and `hlab cloud status` then shows a
warning line for the zone; see the
[command reference](commands.md#hlab-cloud-status).

A secondary zone is accepted but not transferred: its names are looked up
in the public DNS as before. The details are in
[Known deviations](deviations.md#dns-and-reverse-dns).

In the window, **DNS** lists the zones; **Add zone** creates an empty
zone, one from a pasted zone file, or a secondary zone with its primary
nameservers. A primary zone's page has the tabs **Records** (**Add record
set**, and per record set **Change TTL**, **Add records**, **Remove
records**, labels, protection and **Delete**; the SOA only takes a new
TTL) and **Zone file** (export with **Copy zone file**, import after a
confirmation, with the line of a bad zone file shown under the field). A
secondary zone has **Primary nameservers** instead. The zone's actions
menu offers **Change default TTL**, **Import zone file**, **Change
protection** and **Delete**.

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
same way. IPv6 entries are stored but do not resolve. In the window,
**Reverse DNS** in the actions menu of a primary IP, floating IP or load
balancer sets the name; an empty name resets it to the default.

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
[Known deviations](deviations.md#load-balancers-and-certificates).

In the window, **Create load balancer** takes location, type, network,
targets, services, algorithm, labels and name. A load balancer's page has
the tabs **Targets** (with the health per service port), **Services**,
**Networking**, **Graphs** (connections, requests and bandwidth over 1 hour,
24 hours or 7 days), **Labels** and **Protection**; its actions menu
offers **Rename**, **Change type**, **Change algorithm**, **Enable public interface**,
**Disable public interface**, **Reverse DNS**, **Change protection** and
**Delete**.

An HTTPS service takes uploaded or managed certificates. A managed
certificate is issued at once when its domains lie in a DNS zone of the
project, and renewed before it expires:

```
> hcloud zone add-records lab.internal www A --record <load balancer IPv4>
> hcloud certificate create --name www --type managed --domain www.lab.internal
> hcloud load-balancer add-service lb1 --protocol https --listen-port 443 --destination-port 80 --http-certificates www
```

Managed certificates come from the HLab CA, which HLab creates at its
first start, not from Let's Encrypt. No client trusts it until you
decide so; see the next section.

In the window, **Add certificate** uploads a certificate with its
private key or requests a managed one for a list of domain names. A
domain outside the project's zones fails with `dns_zone_not_found`; once
the zone exists, **Retry** in the certificate's actions menu issues it;
**Rename** in the same menu changes its name.
The certificate's page shows its status, domains, fingerprint, validity
and the load balancers that use it.

### Trusting the HLab CA

To reach the service with a plain `curl https://<name>`, add the HLab CA
to the trust stores of Windows and WSL2:

```
> hlab ca trust on
the HLab CA is trusted on this host
> curl.exe https://www.lab.internal/
```

and in WSL2:

```bash
curl https://www.lab.internal/
```

`hlab ca trust off` removes it again from both. The switch is
off until you turn it on, and stays on across cloud starts.

The risk: the HLab CA cannot be limited to your zones, because zones may
use any domain, real ones such as `example.com` included. While the CA is
trusted, anyone who obtains its private key can create certificates that
Windows and WSL2 accept for any domain. Turn trust on only on a
development machine, and off when you no longer need it. HLab never adds
the CA to servers.

Without trust, give the CA certificate to the client that needs it:

```
> hlab ca export hlab-ca.pem
HLab CA written to hlab-ca.pem
```

```bash
curl --cacert hlab-ca.pem https://www.lab.internal/
```

In a server, pass the file with cloud-init (`ca_certs`) or copy it
there yourself.

## Storage boxes

hcloud reaches storage boxes through `HETZNER_ENDPOINT` (see
[First steps](../README.md#first-steps)). A box stores files that you
reach over SSH: SFTP and scp on port 22, and with SSH enabled also
rsync, BorgBackup, SFTP and scp on port 23. With Samba enabled it serves
SMB on port 445, with WebDAV enabled WebDAV over HTTPS on port 443, and
FTP with explicit FTPS on port 21 is always on.

```
> hcloud storage-box-type list
ID     NAME   DESCRIPTION   SIZE      SNAPSHOT LIMIT   AUTOMATIC SNAPSHOT LIMIT   SUBACCOUNTS LIMIT
1333   bx11   BX11          1.0 TiB   10               10                         100
1334   bx21   BX21          5.0 TiB   20               20                         100
1335   bx31   BX31          10 TiB    30               30                         100
1336   bx41   BX41          20 TiB    40               40                         100
> hcloud storage-box create --name backup --type bx11 --location fsn1 --password 'Secret-Passw0rd' --enable-ssh --enable-samba --enable-webdav --reachable-externally --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"
Waiting for create_storage_box (storage_box: 12) ...
Waiting for create_storage_box (storage_box: 12) ... done
Storage Box 12 created
Server: u100011.storagebox.hetzner.internal
Username: u100011
> hcloud storage-box subaccount create backup --home-directory web1 --password 'Other-Passw0rd' --readonly --enable-ssh --enable-samba --enable-webdav --reachable-externally
Waiting for create_subaccount (storage_box: 12, storage_box_subaccount: 6) ...
Waiting for create_subaccount (storage_box: 12, storage_box_subaccount: 6) ... done
Storage Box Subaccount 6 created
> hcloud storage-box subaccount list backup
ID   NAME           HOME DIRECTORY   DESCRIPTION   USERNAME       SERVER                                     AGE
6    u100011-sub1   web1             -             u100011-sub1   u100011-sub1.storagebox.hetzner.internal   2s
```

The first box of the cloud creates a storage VM, so its create takes
longer; the data of all boxes stay on that VM's disk while the cloud is
stopped. The box logs in with its password or with the keys given at
`--ssh-key`. The subaccount `u100011-sub1` gets the home directory
`web1`, which is created for it.

### Files over SSH

From Windows, on port 22:

```
> sftp u100011@u100011.storagebox.hetzner.internal
sftp> ls
web1
sftp> put notes.txt web1/
sftp> ls web1
web1/notes.txt
sftp> exit
> scp u100011@u100011.storagebox.hetzner.internal:web1/notes.txt copy.txt
```

Port 22 serves SFTP only; scp works through its default SFTP mode, not
with `scp -O`. Port 23 also runs rsync and BorgBackup. From WSL2, with
the key in `~/.ssh` there:

```
$ rsync -av -e 'ssh -p 23' site/ u100011@u100011.storagebox.hetzner.internal:site/
sending incremental file list
created directory site
./
index.html

sent 138 bytes  received 65 bytes  406.00 bytes/sec
total size is 12  speedup is 0.06
$ borg init --encryption=repokey ssh://u100011@u100011.storagebox.hetzner.internal:23/./borg
$ borg create ssh://u100011@u100011.storagebox.hetzner.internal:23/./borg::monday site
$ borg list ssh://u100011@u100011.storagebox.hetzner.internal:23/./borg
monday                               Mon, 2026-11-23 08:21:55 [ed7c2f86f4f5fbc68744a95b3991c460f30311473ce80a854f9f8bc1220edcf3]
```

`borg init` asks for the passphrase of the repository. `--remote-path`
`borg-1.1`, `borg-1.2` or `borg-1.4` is accepted; all of them run
BorgBackup 1.4. Any other command on port 23
answers `Command not allowed`, and with SSH turned off for the account
port 23 refuses the login.

More keys go into `.ssh/authorized_keys` in the box, one OpenSSH key per
line or in RFC 4716 format; they apply to both ports. HLab writes the keys
of `--ssh-key` there once, at create.

### Reachability

Servers of the cloud reach every box. The host and WSL2 reach a box only
when it, or one of its subaccounts, is created with
`--reachable-externally`; otherwise the connection times out:

```
> hcloud storage-box create --name closed --type bx11 --location fsn1 --password 'Secret-Passw0rd' --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"
Waiting for create_storage_box (storage_box: 13) ...
Waiting for create_storage_box (storage_box: 13) ... done
Storage Box 13 created
Server: u100012.storagebox.hetzner.internal
Username: u100012
> sftp u100012@u100012.storagebox.hetzner.internal
ssh: connect to host u100012.storagebox.hetzner.internal port 22: Connection timed out
Connection closed
```

The flag also counts per account: an account created without it is
refused from the host and WSL2 on every protocol, even when another
account of the same box has it.

### Subaccounts

A subaccount logs in with its own name and password and sees only its
home directory; `--readonly` lets it read but not write:

```
> sftp u100011-sub1@u100011-sub1.storagebox.hetzner.internal
sftp> ls
notes.txt
sftp> put notes.txt other.txt
Uploading notes.txt to /home/other.txt
dest open "/home/other.txt": Permission denied
```

### Folders

`folders` lists the folders of a box, or those below `--path`:

```
> hcloud storage-box folders backup
Folders:
- .ssh
- borg
- site
- web1
> hcloud storage-box folders backup --path missing
hcloud: the requested resource was not found (not_found)
```

### SMB

With Samba enabled, a box serves the share `backup` with its data, and
each subaccount with Samba enabled a share named after it with its home
directory. Every share admits only its own account. From WSL2 with
smbclient:

```
$ smbclient //u100011.storagebox.hetzner.internal/backup -U u100011%Secret-Passw0rd -c 'put notes.txt; ls notes.txt'
putting file notes.txt as \notes.txt (0.7 kB/s) (average 0.7 kB/s)
  notes.txt                           A       12  Mon Nov 23 12:42:14 2026

		68719476736 blocks of size 1024. 68717362176 blocks available
$ smbclient //u100011-sub1.storagebox.hetzner.internal/u100011-sub1 -U u100011-sub1%Other-Passw0rd -c 'put notes.txt'
NT_STATUS_ACCESS_DENIED opening remote file \notes.txt
```

The share of the read-only subaccount refuses the write. The server
speaks SMB 2.1 and newer; `smbclient -L` does not list the shares.

From Windows, in PowerShell or cmd, `net use` maps a share without a
drive letter; the box (or subaccount) must be reachable externally:

```
> net use \\u100011.storagebox.hetzner.internal\backup Secret-Passw0rd /user:u100011 /persistent:no
The command completed successfully.
> Copy-Item report.txt \\u100011.storagebox.hetzner.internal\backup\web1\
> net use \\u100011-sub1.storagebox.hetzner.internal\u100011-sub1 Other-Passw0rd /user:u100011-sub1 /persistent:no
The command completed successfully.
> Get-Content \\u100011-sub1.storagebox.hetzner.internal\u100011-sub1\report.txt
via smb
> net use \\u100011.storagebox.hetzner.internal\backup /delete
\\u100011.storagebox.hetzner.internal\backup was deleted successfully.
> net use \\u100011-sub1.storagebox.hetzner.internal\u100011-sub1 /delete
\\u100011-sub1.storagebox.hetzner.internal\u100011-sub1 was deleted successfully.
```

Map each account under its own server name, the subaccount under
`u100011-sub1.storagebox.hetzner.internal`, not under the box's. Run
`net use` from PowerShell or cmd, not from Git Bash, which turns the
leading `\\` into `\` so that Windows answers `System error 67`.

### WebDAV

With WebDAV enabled, each account is served at
`https://<user>.storagebox.hetzner.internal/`, with its name and password.
The server certificate comes from the HLab CA, so give the client the CA
certificate (or trust it, see [Trusting the HLab CA](#trusting-the-hlab-ca)):

```
> hlab ca export hlab-ca.pem
HLab CA written to hlab-ca.pem
```

In WSL2, in the folder of that file (such as `/mnt/c/Users/<you>`):

```
$ curl -sS -o /dev/null -w '%{http_code}\n' --cacert hlab-ca.pem -u u100011:Secret-Passw0rd -T dav.txt https://u100011.storagebox.hetzner.internal/dav.txt
201
$ curl -sS --cacert hlab-ca.pem -u u100011:Secret-Passw0rd https://u100011.storagebox.hetzner.internal/dav.txt
via webdav
$ curl -sS -o /dev/null -w '%{http_code}\n' --cacert hlab-ca.pem -u u100011-sub1:Other-Passw0rd -T dav.txt https://u100011-sub1.storagebox.hetzner.internal/dav.txt
403
```

A read-only account may only read (`403` on the upload). Use the host
name: the bare IP address does not match the certificate and answers
`403`.

### FTP and FTPS

FTP on port 21 is always on, for the box and every subaccount; there is
no switch for it. It runs in passive mode only (data ports 50000 to
50099), and plain FTP and explicit FTPS (`AUTH TLS`) are both accepted.
From WSL2 with lftp, which uses FTPS when the server offers it:

```
$ lftp -u u100011,Secret-Passw0rd -e 'set ftp:ssl-force true; set ssl:ca-file hlab-ca.pem; put index.html; ls index.html; bye' u100011.storagebox.hetzner.internal
-rw-r--r--    1 100011   100011         14 Nov 23 11:42 index.html
```

Without `ssl:ca-file`, lftp stops with `Certificate verification: The
certificate is NOT trusted`. A read-only subaccount can read but not
write.

### Snapshots

A snapshot keeps the box's data as it is at that moment. Manual
snapshots are named after the UTC time of their creation:

```
> hcloud storage-box snapshot create backup --description 'before cleanup'
Waiting for create_snapshot (storage_box: 12, storage_box_snapshot: 3) ...
Waiting for create_snapshot (storage_box: 12, storage_box_snapshot: 3) ... done
Storage Box Snapshot 3 created
Name: 2026-11-23T11-42-17
Size: 16 KiB
> hcloud storage-box snapshot create backup
Waiting for create_snapshot (storage_box: 12, storage_box_snapshot: 4) ...
Waiting for create_snapshot (storage_box: 12, storage_box_snapshot: 4) ... done
Storage Box Snapshot 4 created
Name: 2026-11-23T11-42-18
Size: 16 KiB
> hcloud storage-box snapshot list backup
ID   NAME                  DESCRIPTION      SIZE     IS AUTOMATIC   AGE
3    2026-11-23T11-42-17   before cleanup   16 KiB   no             2s
4    2026-11-23T11-42-18   -                16 KiB   no             1s
```

A type allows a limited number of manual snapshots (`SNAPSHOT LIMIT` in
`hcloud storage-box-type list`); one more is refused with
`resource_limit_exceeded`. `hcloud storage-box snapshot update backup <name> --description <text>`
changes the description, and `hcloud storage-box snapshot delete backup
<name>` deletes a snapshot.

`rollback-snapshot` brings the box's data back to a snapshot and deletes
every snapshot taken after it. Here `notes.txt` was deleted after the
first snapshot:

```
$ lftp -u u100011,Secret-Passw0rd -e 'set ftp:ssl-force true; set ssl:ca-file hlab-ca.pem; rm notes.txt; bye' u100011.storagebox.hetzner.internal
> hcloud storage-box rollback-snapshot backup --snapshot 2026-11-23T11-42-17
Waiting for rollback_snapshot (storage_box: 12, storage_box_snapshot: 3) ...
Waiting for rollback_snapshot (storage_box: 12, storage_box_snapshot: 3) ... done
Rolled back Storage Box 12 to Snapshot 3
> hcloud storage-box snapshot list backup
ID   NAME                  DESCRIPTION      SIZE     IS AUTOMATIC   AGE
3    2026-11-23T11-42-17   before cleanup   16 KiB   no             1m
$ lftp -u u100011,Secret-Passw0rd -e 'set ftp:ssl-force true; set ssl:ca-file hlab-ca.pem; ls notes.txt; bye' u100011.storagebox.hetzner.internal
-rwxr--r--    1 100011   100011         12 Nov 23 11:42 notes.txt
```

A rollback ends the open sessions of the box and its subaccounts on
every protocol; connect again afterwards.

With ZFS enabled, the box account sees its snapshots as read-only
folders under `.zfs/snapshot`; subaccounts do not get it:

```
> hcloud storage-box update-access-settings backup --enable-zfs
Waiting for update_access_settings (storage_box: 12) ...
Waiting for update_access_settings (storage_box: 12) ... done
Access settings updated for Storage Box 12
$ lftp -u u100011,Secret-Passw0rd -e 'set ftp:ssl-force true; set ssl:ca-file hlab-ca.pem; ls .zfs/snapshot; ls .zfs/snapshot/2026-11-23T11-42-17; bye' u100011.storagebox.hetzner.internal
drwxr-x---    1 100011   100011         60 Nov 23 11:42 2026-11-23T11-42-17
-rw-r--r--    1 100011   100011         11 Nov 23 11:42 dav.txt
-rw-r--r--    1 100011   100011         14 Nov 23 11:42 index.html
-rwxr--r--    1 100011   100011         12 Nov 23 11:42 notes.txt
drwxr-x---    1 100011   100011          0 Nov 23 11:41 web1
```

SFTP, scp and SMB show the same folders.

### Snapshot plans

A snapshot plan takes an automatic snapshot at its time, given in UTC,
and keeps at most `--max-snapshots` of them, deleting the oldest; manual
snapshots are never deleted by it. Without `--day-of-week` and
`--day-of-month` it runs every day; with both set, a day must match
both:

```
> hcloud storage-box enable-snapshot-plan backup --max-snapshots 7 --hour 11 --minute 45
Waiting for enable_snapshot_plan (storage_box: 12) ...
Waiting for enable_snapshot_plan (storage_box: 12) ... done
Snapshot Plan enabled for Storage Box 12
> hcloud storage-box snapshot list backup
ID   NAME                                  DESCRIPTION      SIZE     IS AUTOMATIC   AGE
3    2026-11-23T11-42-17                   before cleanup   16 KiB   no             3m
5    snapshot-daily--2026-11-23T11-45-56   -                16 KiB   yes            1s
```

The automatic snapshot shows as a `create_snapshot` action of the box,
taken within a minute of the plan time. Plan times while the cloud is
stopped are skipped, not made up at the next start.
`hcloud storage-box disable-snapshot-plan backup` ends the plan; the
automatic snapshots stay.

### Passwords and protection

A password has 12 to 128 characters with at least one upper case letter,
one lower case letter, one digit and one special character; otherwise the
command fails at once:

```
> hcloud storage-box reset-password backup --password 'short'
hcloud: invalid input in field 'password' (invalid_input)
- password: password must be 12 to 128 characters long
```

`enable-protection backup delete` makes `delete` answer `protected`:

```
> hcloud storage-box enable-protection backup delete
Waiting for change_protection (storage_box: 12) ...
Waiting for change_protection (storage_box: 12) ... done
Resource protection enabled for Storage Box 12
> hcloud storage-box delete backup
hcloud: the resource is protected (protected)
> hcloud storage-box disable-protection backup delete
Waiting for change_protection (storage_box: 12) ...
Waiting for change_protection (storage_box: 12) ... done
Resource protection disabled for Storage Box 12
> hcloud storage-box delete backup
Waiting for delete_storage_box (storage_box: 12) ...
Waiting for delete_storage_box (storage_box: 12) ... done
Storage Box backup deleted
```

Deleting a box deletes its subaccounts, snapshots and data; deleting the
last box also removes the storage VM.

### Storage boxes in the window

**Storage Boxes** is an entry of the sidebar's **Cloud** group. Its list
shows each box's name, user name, type, location, usage and status and
refreshes every 5 seconds. **Create Storage Box** opens a form with the
**Location**, the **Type** (only the types offered there), the **Name**,
the **Password** (a refusal by the password rules appears under the
field), the **SSH keys** of the project as checkboxes, the access settings
(SSH, Samba, WebDAV, ZFS, reachable externally) and labels. To install a
key that is not an SSH key of the project, create the box with hcloud,
OpenTofu or Ansible.

A box's page has an actions menu with **Rename** and **Delete** and
these tabs:

- **Overview**: the host name and user name with copy buttons, type,
  location, the usage of files and snapshots, and the box's folders.
  Click a folder to open it, type a path and press **Open**, or go back
  with **Up**.
- **Subaccounts**: **Create subaccount** asks for name, description, home
  directory, password and access (SSH, Samba, WebDAV, read-only,
  reachable externally); **Edit**, **Reset password** and **Delete** are
  in each row's menu.
- **Snapshots**: the snapshot plan ("No plan" or the plan in UTC) with
  **Set plan**, which asks for the maximum number of snapshots, hour and
  minute in UTC, a day of the week and a day of the month (each "Every
  day" by default), and **Disable plan**. **Create snapshot** takes an
  optional description; each row's menu offers **Edit**, **Roll back**
  and **Delete**. The confirmation of a rollback lists the newer
  snapshots it deletes.
- **Settings**: the access settings, delete protection, **Change type**
  (only types offered in the box's location; a type too small for the
  box's data is refused with a message) and **Reset password** to a
  password you enter.
- **Labels**: the box's labels.

**Delete**, in the list's row menu and the actions menu, asks you to type
the box's name; while delete protection is on, it is disabled.

### Own storage box types

The types come from HLab's catalog; see [Catalogs](configuration.md#catalogs).
Small types of your own, defined in
[storage-box-types.custom.json](configuration.md#own-storage-box-types),
make it easy to try what happens when a box is full:

```
> hcloud storage-box-type list
ID       NAME   DESCRIPTION     SIZE      SNAPSHOT LIMIT   AUTOMATIC SNAPSHOT LIMIT   SUBACCOUNTS LIMIT
1333     bx11   BX11            1.0 TiB   10               10                         100
1334     bx21   BX21            5.0 TiB   20               20                         100
1335     bx31   BX31            10 TiB    30               30                         100
1336     bx41   BX41            20 TiB    40               40                         100
100001   tiny   Tiny test box   64 MiB    10               10                         10
> hcloud storage-box create --name small --type tiny --location fsn1 --password 'Secret-Passw0rd' --reachable-externally --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"
Waiting for create_storage_box (storage_box: 14) ...
Waiting for create_storage_box (storage_box: 14) ... done
Storage Box 14 created
Server: u100013.storagebox.hetzner.internal
Username: u100013
> sftp u100013@u100013.storagebox.hetzner.internal
sftp> put big.bin
write remote "/home/big.bin": Failure
```

`big.bin` has 70 MB, more than the 64 MiB of the type.

In the window, Administration > **Storage Box Types** lists the catalog
types and your own, also while the cloud is stopped; the column
**Source** tells them apart. **Add storage box type** asks for name,
description, size (a number in MiB, GiB or TiB), snapshot limit,
automatic snapshot limit and subaccounts limit (10, 10 and 100 unless
you change them) and the locations: **All locations** is ticked by
default; untick it and tick the locations the type is offered in.
**Edit** and **Remove** are in an own type's row menu. Changes apply at
once while the cloud runs. When the file of own types cannot be used, a
banner above the list says why.

## OpenTofu and Ansible

The OpenTofu provider and the Ansible collection read the same three
variables as hcloud (see [First steps](../README.md#first-steps)), so the provider block and the module calls stay as
they are for the real cloud. For OpenTofu on Windows:

```powershell
$env:HCLOUD_ENDPOINT = "http://api.hetzner.internal/v1"
$env:HETZNER_ENDPOINT = "http://api.hetzner.internal/hetzner/v1"
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

For Ansible in WSL2 (see [WSL2](../README.md#wsl2)), set them in the shell that runs
`ansible-playbook`:

```bash
export HCLOUD_ENDPOINT=http://api.hetzner.internal/v1
export HETZNER_ENDPOINT=http://api.hetzner.internal/hetzner/v1
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

### Storage boxes with OpenTofu and Ansible

OpenTofu creates a box with a subaccount; the provider reads the key
file with its trailing newline, which HLab takes like Hetzner does:

```hcl
resource "hcloud_storage_box" "backup" {
  name             = "backup"
  storage_box_type = "bx11"
  location         = "fsn1"
  password         = "Secret-Passw0rd"
  ssh_keys         = [file("id_ed25519.pub")]

  access_settings = {
    reachable_externally = true
    ssh_enabled          = true
  }

  snapshot_plan = {
    max_snapshots = 7
    hour          = 3
    minute        = 0
  }
}

resource "hcloud_storage_box_subaccount" "web1" {
  storage_box_id = hcloud_storage_box.backup.id
  name           = "web1"
  home_directory = "web1"
  password       = "Other-Passw0rd1"

  access_settings = {
    ssh_enabled = true
    readonly    = true
  }
}

output "server" {
  value = hcloud_storage_box.backup.server
}
```

```
> tofu apply
...
hcloud_storage_box.backup: Creation complete after 1m0s [name=backup]
hcloud_storage_box_subaccount.web1: Creating...
hcloud_storage_box_subaccount.web1: Creation complete after 2s [name=web1]

Apply complete! Resources: 2 added, 0 changed, 0 destroyed.

Outputs:

server = "u100131.storagebox.hetzner.internal"
```

A single `tofu destroy` removes the box together with its subaccounts
and `hcloud_storage_box_snapshot` resources: deletes of subaccounts and
snapshots wait for the box's running actions instead of failing with
`locked`.

The same with Ansible, plus a snapshot:

```yaml
- hosts: localhost
  connection: local
  gather_facts: false
  vars:
    ansible_python_interpreter: "{{ ansible_playbook_python }}"
  tasks:
    - name: Create the storage box
      hetzner.hcloud.storage_box:
        name: backup
        storage_box_type: bx11
        location: fsn1
        password: Secret-Passw0rd
        access_settings:
          ssh_enabled: true
          reachable_externally: true
        state: present
      register: box

    - name: Add a read-only subaccount
      hetzner.hcloud.storage_box_subaccount:
        storage_box: backup
        name: web1
        home_directory: web1
        password: Other-Passw0rd1
        access_settings:
          ssh_enabled: true
          readonly: true
        state: present

    - name: Take a snapshot
      hetzner.hcloud.storage_box_snapshot:
        storage_box: backup
        description: before cleanup
        state: present
      register: snap

    - name: Show the box address and the snapshot
      ansible.builtin.debug:
        msg: "{{ box.hcloud_storage_box.server }}, snapshot {{ snap.hcloud_storage_box_snapshot.name }}"
```

```
$ ansible-playbook box.yml
...
TASK [Show the box address and the snapshot] ***********************************
ok: [localhost] => {
    "msg": "u100130.storagebox.hetzner.internal, snapshot 2026-11-23T16-38-17"
}

PLAY RECAP *********************************************************************
localhost                  : ok=4    changed=3    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0
```

`hetzner.hcloud.storage_box` with `state: absent` deletes the box with
its subaccounts and snapshots.

## Stopping and starting the cloud

The cloud never starts with Windows. Stop it when you need the memory
back and start it again later; servers, IPs, networks, firewalls, volumes,
load balancers and DNS zones are kept:

```
> hlab cloud stop
the cloud is stopping
> hlab cloud start
the cloud is starting
```

With `stop_behaviour = "save"` (the default) the stop saves the running
servers and the start resumes them where they were. With `"shutdown"` the
servers shut down cleanly and the start boots those that were running.
Servers that were off stay off. A Windows shutdown with a running cloud
does the same through Hyper-V; after the reboot, start the cloud again.
The details, also on changing the setting, are under
[Stop behaviour](configuration.md#stop-behaviour).

When something goes wrong, `hlab logs` shows the end of the
service's log, also while the cloud is stopped; `--follow` keeps
watching it.

### Discarding the state

HLab refuses to start with a state it cannot use: one written by a newer
HLab version, a damaged state file, or a state that holds addresses
outside a changed `public_range`. `hlab cloud status` then shows
`state: refused` and the reason. Set the range back, or, if you can lose
everything in the cloud, confirm the discard:

```
> hlab cloud start --discard-state
the cloud is starting
```

This deletes all projects, servers with their disks, volumes, snapshots
and backups, keeps the old state file renamed to
`state.db.discarded-<time>`, and starts empty. A usable state is never
discarded by this flag.


## Profiles

Every build belongs to a profile. `task build` builds the `dev` profile:
service `hlab-dev`, data in `%ProgramData%\hlab-dev`, Hyper-V objects named
`hlab-dev...`. The installed HLab uses the `release` profile with the
name `hlab`. Both may be installed on one machine, but only one cloud runs
at a time: a cloud refuses to start while the other one runs, and
`hlab cloud status` names the reason.
