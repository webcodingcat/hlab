# Known deviations from Hetzner

HLab speaks Hetzner's Cloud API, so hcloud, OpenTofu and Ansible work
against it as against Hetzner. Because everything runs as Hyper-V VMs on
your own machine, some things behave differently or are missing. This
page says what you will notice, by topic. Contributors find every
difference with its exact API route in
[API coverage](development/api-coverage.md).

The API offers actions, locations, server types, prices, images, ISOs,
SSH keys, servers, placement groups, volumes, networks, primary and
floating IPs, firewalls, DNS zones, load balancers and certificates.
Anything else answers `not_found`.

## Servers

Servers are VMs on your machine and run only x86 server types; Arm types
are refused. A server type may have no more vCPUs than your host has
logical CPUs, and its RAM and disk must fit into what is free beyond the
reserves in the [configuration](configuration.md#settingstoml); otherwise
creating, powering on or changing the type answers
`resource_unavailable`. Creating a server
finishes when the VM runs; SSH answers a few seconds later.

Public IPv4 addresses come from a local range (`198.19.0.0/16` by
default), not from Hetzner's. The IPv6 network of a server is assigned
on paper only: it carries no traffic and the guest has no IPv6 address.
A root password returned for a server without SSH keys does not expire,
so you are not asked to change it at the first login.

A shutdown counts as done once the guest has been asked; the server shows
`stopping` until the guest turns off. Resetting the root password works
through a small HLab helper in the guest and only while the server runs.
The remote console is not available. Metrics are sampled every 10 seconds
and kept for 24 hours in memory, so they are lost when the cloud stops.
The metadata service offers only Hetzner's paths, not the EC2-style ones.
Placement groups only record their members, since every server runs on
the same machine; a spread group still takes at most 10 servers.

## Images, snapshots, backups, rescue and ISOs

The system images are Ubuntu 22.04, 24.04 and 26.04 and Debian 12 and 13;
each is downloaded the first time a server uses it, which takes a while.
A snapshot is taken while the server keeps running and is
crash-consistent, like the disk after a power cut. Its size is the size
of the uncompressed disk file.

Backups ignore the shown backup window: HLab makes one backup per server
and day at its first hourly check of that day, but only while the cloud
runs, and days on which it did not run get no backup later. Daily and
manual backups share seven slots.

The rescue system is Debian 13 booting from a disk of its own; the
server's disk stays `/dev/sda`, and what you write to the rescue disk is
lost. The first rescue takes longer because the rescue disk is prepared.
Rescue ends with the next boot that HLab starts, or after 60 minutes; a
reboot from inside the guest keeps it enabled.

The ISOs are five Ubuntu and Debian installer images of HLab's own list,
with HLab's own IDs, plus the ISOs you add yourself. Your own ISOs appear
in every project as private ISOs without a name, shown by their
description, and are attached by their ID only. The first attach of an
ISO downloads it, which takes minutes. The server sees a second DVD drive
with its cloud-init data.

## Volumes

The volume device link, automatic mounting and noticing a grown volume
come from small HLab helpers that every new server installs at its first
boot. A server without them, for example because its user data turns off
cloud-init's vendor data, sees a volume as a plain SCSI disk. A server
holds at most 16 volumes, an attached volume cannot be deleted, and a
volume that does not fit into the free disk space is refused. As at
Hetzner, the filesystem is not grown for you.

Where Hetzner answers `locked` because the server is busy, attaching or
detaching a volume waits instead: the action shows as `running` and is
carried out after the earlier actions on that server, in the order they
were sent. An action on the volume itself still answers `locked`.

## Networks, primary and floating IPs

There is no DHCP server: a server's private network is set up by
cloud-init at boot, and a network attached while the server runs by the
HLab helper in the guest. The MTU is 1500 (Hetzner: 1450), and private
adapters have other MAC addresses than at Hetzner, so tools that look for
Hetzner's MACs, such as hc-utils, do not find them. vSwitch subnets are
not supported. The gateway is the first address of the network range.

Attaching a server or load balancer to a network, detaching it, changing
alias IPs and adding or deleting subnets and routes do not answer
`locked` while another of these actions runs on the same network, as
Hetzner does: the action shows as `running` and is carried out after the
earlier ones, in the order they were sent. Assigning and unassigning
primary and floating IPs likewise wait for other actions on the server.
Changing the network's range or protection still answers `locked`.

Primary and floating IPv4 addresses come from the local public range. A
floating IPv4 reaches its server through the server's public IPv4; as at
Hetzner, you configure it in the guest yourself. IPv6 primary and
floating IPs are assigned on paper only and carry no traffic. A primary
IP held by a load balancer shows the load balancer as its assignee.

## Firewalls

Firewalls filter the public IPv4 of a server and its floating IPs;
private networks are never filtered. IPv6 rules are stored but have no
effect. A firewall shows as applied at once, never as pending. Hetzner's
limits of 500 effective rules per firewall and 50 firewalls per project
are not enforced, while the limits per rule list and per server are.
Addresses in rules must be exact network prefixes such as `10.0.0.0/24`.

Applying a firewall to servers or removing it waits for other actions on
those servers instead of answering `locked`, and the firewall actions of
a newly created server wait for other actions on the same firewall, so
servers created at the same time with one firewall all succeed.

## DNS and reverse DNS

Your zones are answered by a DNS server inside HLab, which the host, WSL2
and the servers ask while the cloud runs; nothing is delegated and no
registrar is involved. Besides real domain names, a zone may be named
one label below `internal`, `test` or `home.arpa`, such as `lab.internal`;
subdomains and reverse zones are refused. A zone name is unique across
all projects.

A zone named like a real domain hides that domain on your machine;
[`hlab cloud status`](commands.md#hlab-cloud-status) warns about it.
Secondary zones are stored but never transferred: their names are looked
up in the public DNS. There is no DNSSEC. The SOA record is managed by
HLab; only its TTL can be changed.

Changes to the records of a zone wait for other changes to the same
zone instead of answering `locked`; they show as `running` until their
turn comes, in the order they were sent, and appear when they succeed.
Changing the zone's TTL, protection or primary nameservers, importing a
zone file and deleting the zone still answer `locked` while another
change to it runs.

Reverse DNS names of IPv4 addresses resolve through HLab's DNS server.
IPv6 entries are stored and shown but do not resolve.

## Load balancers and certificates

A load balancer is an HAProxy instance inside HLab. Its IPv6 address
carries no traffic. IP targets may be almost any IPv4 address, servers
of the cloud included, not only your dedicated servers. The PROXY protocol is sent in
version 2, HTTPS health checks do not verify the target's certificate,
and a load balancer joins at most one network.

Adding and removing targets and adding, changing and deleting services
wait for other actions on the load balancer and its target servers
instead of answering `locked`; they show as `running` until their turn
comes, in the order they were sent.

Managed certificates are issued by the HLab CA instead of Let's Encrypt,
only for domains that lie in a DNS zone of the project. They are valid
for 90 days and renewed 30 days before they expire. Clients trust them
only after [`hlab ca trust on`](commands.md#hlab-ca-trust) or with the
exported CA certificate. Uploaded certificates are not checked for
expiry or chain order.

## Projects, server types and prices

There is no Hetzner account: projects and their API tokens are managed
with [`hlab`](commands.md) and in the tray program. Deleting a project
deletes all its resources, protected servers included.

Server types and prices come from HLab's catalog, which
[`hlab catalog update`](commands.md#hlab-catalog-update) refreshes from
Hetzner with your token. Your own server types, defined in the
[configuration](configuration.md#custom-server-types) or in the tray
program, appear after the catalog types; without prices they cost
nothing.

## WSL2

Servers and the API are reachable from WSL2 only in NAT networking mode
with DNS tunneling turned on.
