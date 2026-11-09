# Servers, Primary and Floating IPs, firewalls, networks and the lab's own resources

How servers, Primary IPs, Floating IPs and firewalls behave, and the lab's own operations on
projects, tokens and faults. The rules they keep are in DEVELOPING.md
(Actions, Behaviour that changes must keep); section names in parentheses
point to this file unless they name another.

## Servers and Primary IPs

The handlers in `internal/api/cloud/servers.go` and `primary_ips.go` decide;
`internal/lab/servers.go` does the slow part with IDs, through `system`.

- **Create**: the handler records the server with its Primary IPs (status
  `initializing`), puts it into the firewalls its request names and runs
  the table's rows `create` (`create_server`), an `attach_at_create`
  (`attach_to_network`) per network, an `apply_at_create` of the
  firewalls' table (`apply_firewall`) per firewall and, unless
  `start_after_create` is false, `poweron` (`start_server`), all in one
  `Lab.Create`; the answer's `next_actions` lists them in this order, as
  Hetzner's does, and its server shows the networks and the firewalls. Hetzner checks the networks after the SSH keys: each must be
  the project's and have a subnet, at most three, their ranges apart
  (the network ID 0 fails Hetzner with a 500, and the lab's too); then the
  firewalls: each the project's, at most five; a label selector's
  firewall takes a place only where those leave one. A field
  `floating_ips` is ignored, as Hetzner has none. The attach and the
  apply run beside the create and have no steps of their own: the create's
  VM gets the NICs, and its pictures the firewalls, before the first
  start, so they end at once. The create's steps are
  `Machine.CreateServer`; its `Machine.BuildServer` takes the system image from the
  cache, makes the differencing disk, the
  seed and the VM, and sends the appliance the pictures with the server. If
  it fails, the steps remove the VM, the disk and the seed, and the
  action's undo removes the record and its new Primary IPs. A create that
  HLab's end cut short is built anew at the next start.
- **Delete** is the row `delete` (`delete_server`): the record goes at once,
  as on Hetzner, with its Primary IPs that have `auto_delete`; the others
  stay, unassigned. The steps remove the VM, the disk and the seed.
- **Power.** `poweron`, `poweroff`, `shutdown`, `reboot` and `reset` set the
  statuses Hetzner shows (`starting`, `stopping`, …) and wait its times.
  - A reboot asks the guest to shut down (Hyper-V has no ACPI reboot), turns
    it off after a minute and starts it.
  - A reboot or reset of an off server starts it, as on Hetzner.
  - Each sets the status before its first call that changes the VM, because
    Hetzner shows the new status as soon as the action runs; a `shutdown`
    leaves the status to the watchdog, which sees the guest go off. If the
    call fails, the status stays as the steps left it; only a start of an
    off server sets it back to `off` (Room).
  - `want_running` changes only in the plan, when the action is asked for:
    a start sets it, a `poweroff` clears it (Room). The steps leave it, so
    queued actions leave it in the order they were sent: a `poweroff` sent
    during the create ends with the server off and not wanting to run.
- **The watchdog** looks every 2 s at the VMs of the servers that no action
  works on and that are `running`, `starting` or `stopping`. A running
  server whose guest shut itself down is `off` and no longer wants to run.
  A server that a failed action left `starting` or `stopping` gets its VM's
  status, `running` or `off`; `want_running` stays.
- **Who sets a server's status:** the steps of its actions while they run
  (flagged); the watchdog for a server no action works on; the start of the
  lab, which makes every status follow its VM (`resumeServers`). Who sets
  `want_running`: the create, the plans of the power actions, the watchdog
  for a guest that shut itself down.
- **`want_running`** is what a start of the lab follows: a server whose VM
  runs is taken over (hlabd had crashed); one whose VM is off gets its NICs on
  `HLab-Public` and `HLab-Private` again (detached and attached, as a stop of
  the lab removed the switches) and starts if it wants to run. A server without a VM gets a new one
  first (`Machine.restoreServer`): with its disk as it is, if the disk is there
  (an uninstall kept the data folder but removed the VMs); built anew if
  there is no disk (hlabd's end cut its create short). `system.CreateServer`
  keeps a disk that is there and never removes it on a failure. A stop shuts
  every server down, all at once, and turns it off after `shutdown_timeout`
  (`hlab.yaml`, 60 s); `want_running` stays.
- **Room.** A create or a start (`poweron`, and `reboot` or `reset` of an off
  server) beyond the host's room is Hetzner's 412 `resource_unavailable`.
  - Memory (`Machine.CheckRoom`, from `Hypervisor.HostResources`): memory is
    static, so the servers that want to run may take all of the host's memory
    except a reserve (25 %, at least 6 GB) and the appliance's 1 GB.
  - Cores: four virtual cores per thread of the host.
  - Disk, for a create (`Machine.CheckDisk`, from `Hypervisor.DiskSpace`): a
    tenth of the data folder's drive stays free, counted as it is now.
    Dynamic disks take only what the guests wrote, so the servers' disks may
    be larger together than the drive.
  - The room counts the servers whose `want_running` is set. A start sets it
    in its plan and a `poweroff` clears it there, so actions sent at once, or
    queued behind each other, count each other before their steps run. If a
    start's steps fail, the server is off again. An example on a host with
    room for two servers, `a` and `b` running, `c` off: a `poweroff` of `a`
    clears its `want_running` at once, so a `poweron` of `c` right after it
    fits and sets `c`'s; a `poweron` of `a`, queued behind its `poweroff`,
    then finds `a` not wanting to run, checks the room and gets 412.
  - Without the machine started (the API's unit tests) there is always room
    for memory, and the fake reports a drive of 1 TB, half free.
- **Addresses** of Primary and Floating IPs come from one pool,
  `198.19.0.0/16` (IPv4, without `.0` and `.255`) and `2001:db8:1::/48`
  (one `/64` per IPv6), in order: `state.Lab.Pools` holds
  where the next search starts, so a released address comes back only once the
  pool has gone round (`lab.NewAddress`).
- **Names and MACs.** A server's VM is `hlab-server-<id>`, its public NIC has
  the MAC `96:00:` and its ID in 32 bits; its disk is
  `disks/servers/<id>.vhdx`, its seed `disks/seeds/<id>.iso`.
- **The lab's key.** `hlabd` keeps an SSH key in the data folder, `lab-key`
  (Ed25519 in OpenSSH's format, `internal/sshkey`, made at the first start,
  readable like the control token). Every server's root takes its public key
  besides the server's own keys, from the seed and the metadata service
  (`picture.ServerKeys`); `hlab ssh` logs in with it. A server created
  before the key existed does not have it.
- **The seed** (`internal/seed`) is what cloud-init reads on Hyper-V, where the
  SMBIOS vendor cannot say "Hetzner": `meta-data` (instance ID, host name,
  keys), `user-data`, `vendor-data` (root's login, the same text the metadata
  service answers) and `network-config` version 2: IPv4 by DHCP and IPv6 as
  `::1` of the `/64` with `fe80::1`, as on Hetzner (IPv4 by DHCP also for a
  server with IPv6 only, below), always with the lab's resolvers; none for a server without public addresses, as Hetzner's
  metadata has none for it. Its files are JSON, which is YAML too: cloud-init's YAML 1.1
  parser reads an unquoted MAC of digits as a number.

**The image cache.** The catalog names each system image's source: the
official qcow2 file (Debian's genericcloud, Ubuntu's cloud image) and its
list of checksums. The first server of an image loads it
(`system.LoadImage`): download, checksum, `internal/imagefmt` reads the
qcow2 in Go and writes the disk as a fixed VHD (the raw disk and a 512-byte
footer), and `FlattenDisk` (`Convert-VHD`) makes it a dynamic VHDX,
`images/cache/<sha256>.vhdx`. `state.Lab.Images` records it by the image's
name; one load per image runs at a time. Converting through the appliance
(`qemu-img`) is for imports and exports (3b). A cached image whose source in
the catalog changed is loaded anew. Each start of the lab removes the files
that neither a cache entry nor a server's disk needs, what a load cut short
left, and the disks and seeds of servers that are gone (a delete that
HLab's end cut short).

## Floating IPs

The handlers in `internal/api/cloud/floating_ips.go` and
`floating_ip_actions.go` decide; `Machine.ChangeFloatingIP`
(`internal/lab/floating_ips.go`) sends the pictures.

- **Create** takes a `home_location` (by name only; Hetzner refuses an ID)
  or a `server`, whose location is then the home, and runs the row
  `assign` in the same `Lab.Create` for the second. An empty name is none:
  the IP is named `floating_ip-<id>`. Hetzner takes labels whose keys its
  rules refuse elsewhere, so the lab does not check them.
- **Assign** (`assign_floating_ip`, resources the IP and the server) goes
  to any server of the project in the IP's network zone, running or not;
  another zone is Hetzner's 422 `service_error`, a server whose create
  runs 423. On an assigned IP it is a move: as on Hetzner, an
  `unassign_floating_ip` of the old server comes first, runs as long as the
  assign and is not in the answer (row `unassign_for_move`). To the IP's
  own server it ends at once. It flags the IP: a second assign meanwhile is 423 with
  Hetzner's own text, but it does not lock the server.
- **Unassign** (`unassign_floating_ip`) takes no flag, as Hetzner runs it
  beside an assign; of an unassigned IP it is 422 `service_error`.
- Both change the record in the plan, so the IP names its new server at
  once, as on Hetzner, and the steps send the pictures: the route moves.
  If the steps fail, the undo puts the IP back on its server, and the
  pictures follow (DEVELOPING.md, Actions).
- **In the data plane** an assigned IP is a route on the appliance's
  `public` NIC through the server's Primary IP of its family
  (docs/appliance.md). The guest configures the address itself, as on
  Hetzner: `ip address add <ip>/32 dev eth0`, or an address of the `/64`.
  A connection open during a move breaks, as the new server knows nothing
  of it; the next one reaches the new server.
- **Delete** refuses an assigned IP (422 `must_be_unassigned`) and a
  protected one (423 `protected`). A server's delete leaves its Floating
  IPs, unassigned.
- **Lists** filter by `name` and `label_selector` and sort by `id` and
  `created`; Hetzner ignores an unknown sort value and the filter `ip`.
- **`dns_ptr`** is Hetzner's default name for an IPv4 and empty for an
  IPv6 until `change_dns_ptr` comes with DNS (phase 2b).

## Firewalls

The handlers in `internal/api/cloud/firewalls.go` and
`firewall_actions.go` decide; `Machine.ChangeFirewall` and
`Machine.ChangeFirewallSelector` (`internal/lab/firewalls.go`) send the
pictures and wait Hetzner's 3 s and 1 s. What Hetzner answers is measured
(`compat/requirements.md`, Firewalls).

- **Rules** are checked as Hetzner checks them: first the fields with
  their index (direction, description, at most 100 addresses), then each
  rule's protocol (tcp, udp, icmp, esp, gre), addresses (CIDRs without
  host bits) and port (`80` or `80-85`). The firewall keeps them in
  Hetzner's form: tcp and udp without a port take `any`, the other
  protocols drop a port, a rule keeps only the addresses of its
  direction, IPv6 in its canonical form. There is no limit of 50 rules,
  only of 500 effective rules: each address of a direction counts once.
- **Actions** answer as a list, from one transaction (`Lab.ActAll`, or
  `Lab.Create` for a create): `set_firewall_rules` ends at once; each entry
  of `apply_to` and `remove_from` gets an `apply_firewall` or
  `remove_firewall` that names the firewall and the server, or the label
  selector by an ID of its own; after `set_rules`, an `apply_firewall`
  names the servers. They take no flag. The steps send the `ruleset`
  picture; a server's `public_net.firewalls` shows a firewall `pending`
  while an `apply_firewall` or `remove_firewall` that names both runs,
  as on Hetzner, also one that takes it away, then `applied` or nothing.
  `remove_from_resources` of a server the firewall does not reach is 404
  `"resource not found"`.
- **Label selectors** follow the servers: after a server's create, its
  relabel, a remove or a firewall's delete, `refreshFirewallSelectors`
  brings the servers a selector holds (`applied_to_resources`) up to date
  in the same transaction; the data plane has the change at once. A
  server that comes or leaves gets an `apply_firewall` (a
  `remove_firewall`) naming the firewall and the server, as on Hetzner
  (`recordSelectorChange`, `Lab.RecordAction`); the change is made, so the
  action ends at once and the server never shows the firewall `pending`
  for it. A server applied directly is not held
  by the firewall's selector: an apply to a selected server moves it, with
  a `remove_firewall` naming the selector first.
- **Limits:** 5 firewalls per server. An apply or create beyond it is 403;
  a selector skips a server that has five, and takes it once one goes.
  50 firewalls per project: **a deviation from Hetzner**, which allows 50
  per account across its projects (its documentation); in the lab each
  project is a fresh account, so that projects run side by side. The
  51st is 403 `resource_limit_exceeded`, `"firewall limit exceeded"`,
  limit `firewalls` (Hetzner's error is not measured).
- **Delete** refuses a firewall applied to a server directly (422
  `resource_in_use`) and deletes one applied only through selectors. A
  server's delete takes it out of every firewall, without an action.
- **Lists** filter by `name` and `label_selector` and sort by `id`, `name`
  and `created`; Hetzner ignores an unknown sort value.
- **In the data plane** (docs/appliance.md, the ruleset) a firewall works
  on a server's public addresses, its Floating IPs among them, also
  between servers of the project: `in` is closed but for the rules, `out`
  is open until a rule names it, answers pass.
- **A deviation from Hetzner:** the lab's endpoints (DHCP, metadata, DNS)
  and the host's APIs (`198.18.0.1` and `2001:db8:0:ffff::1`, TCP 8080 and
  8081) pass any firewall. Hetzner too lets DHCP, the metadata service and
  its DNS resolvers through; its API, though, is on the Internet on port
  443, where an `out` rule such as "only TCP 443" lets it through. In the
  lab the API is on 8080 and 8081 of the host, and such a rule must not
  cut a server off from it.

## Private networks

The handlers in `internal/api/cloud/networks.go` and
`network_actions.go` decide, the server's side in `server_actions.go`;
the steps are in `internal/lab/networks.go`. What Hetzner answers is
measured (`compat/requirements.md`, Networks).

- **Create** checks as Hetzner does: first the fields (name, `ip_range`
  as a prefix, each subnet's type, zone and range, each route's
  destination and gateway, labels), then the range (RFC 1918, at least a
  `/24`, no host bits), the subnets (inside the range, at most a `/30`,
  one zone, no overlap), the routes (RFC 1918 or `0.0.0.0/0`, never
  `172.31.1.1`; a gateway in the range other than its first address and
  `172.31.1.1`; no overlap but the default route), then the limits (50
  subnets, 100 routes) and the name. It answers no action. Two networks
  may have the same range, also in one project. Each network gets a VLAN
  of the lab's own (2 to 4094, from a pool that goes round).
- **Subnets** are listed in the order of their ranges; each one's gateway
  is the network's first address. A subnet of the type `vswitch` is 422
  `vswitch not found`, as Hetzner answers a vSwitch it does not know: the
  lab has no Robot. `expose_routes_to_vswitch` is kept and has no effect.
- **Actions** (`add_subnet`, `delete_subnet`, `add_route`,
  `delete_route`, `change_ip_range`, `change_protection`) are queued per
  network: Hetzner takes a second one while the first runs and runs it
  after. `add_subnet` without a range takes the first free `/24`, else
  the first free block of the next size down (Hetzner gave the free `/25`
  of a full `/24`), else 422 `no_subnet_available`;
  `delete_subnet` refuses a subnet that holds a server's address;
  `change_ip_range` only extends (the same first address, a shorter
  prefix) a network without servers. Subnet and range actions end at
  once; route actions take Hetzner's 5 s when servers are in the network.
- **A server's actions** in a network are rows of the servers' table,
  queued with its other actions. Hetzner lists them only among the
  network's actions (`/networks/{id}/actions`), not the server's.
  - `attach_to_network` takes the address asked for, or the first free
    one of the subnet named by `ip_range` or of the last subnet with room,
    and up to five alias IPs. It refuses, in Hetzner's order: the fields,
    the network, more than five alias IPs (403), a network the server is
    in already, a network whose range overlaps one of the server's, a
    fourth network (403), then the addresses: one in no subnet (the
    network's gateway among them) has no subnet, a subnet's own and last
    address and one another server holds are not available. It does not
    check the network zone, as Hetzner does not. The plan records the
    NIC with a MAC `86:00:` and 32 bits of a counter; the steps put the
    NIC into the VM (also one that runs) on the private switch, in the
    network's VLAN, and send the pictures.
  - `detach_from_network` takes the NIC out of the record at once; the
    steps send the pictures, then remove the NIC from the VM.
  - `change_alias_ips` replaces the alias IPs (at most five, 422 here);
    an empty list for a network the server is not in changes nothing and
    succeeds, as on Hetzner.
- **Members** (`GET /networks/{id}/members`) are the servers in the
  network with their subnet; a server whose attach runs is `attaching`,
  one whose alias IPs change `updating`. As on Hetzner, a server that
  leaves (a detach, its delete) is no member any more at once. Load
  balancers come with phase 4.
- **Delete** refuses a protected network (423) and deletes one with
  servers in it, as Hetzner does: they leave it at once, without an
  action of theirs, and their NICs leave the VMs in the background
  (`Machine.DetachNICsLater`). An action that runs on the network, a
  server's attach among them, ends with Hetzner's error `aborted`
  (`Lab.ChangeAborting`).
- **In the data plane** (docs/appliance.md) each network is a VLAN on
  the private switch and a VRF in the appliance with the gateway; a
  guest gets its address by DHCP as a `/32`, the gateway on-link and the
  network's range through it, MTU 1450, as on Hetzner. Only the addresses
  and alias IPs handed out, and the network's routes, are routed; an
  address nobody holds answers nothing. Private networks pass no Cloud
  Firewall, as on Hetzner. Two networks with the same range stay apart.
- **In the guest** the seed's vendor-data does what Hetzner's images do
  with hc-utils: a udev rule starts dhcpcd on each NIC with a MAC
  `86:00:…`, also one hot-plugged into a running server. Three deviations
  from Hetzner: the seed's vendor-data has this script, where Hetzner's
  images bring hc-utils (the metadata service's `vendor_data` stays
  Hetzner's); under Hyper-V the private NICs are `eth1` to `eth3`, where
  Hetzner's KVM names them `enp7s0` and up; and two guests in one VLAN
  could reach each other directly if one set that up by hand, which
  Hetzner's network does not allow (the lab's DHCP routes everything
  through the gateway). The script waits up to 30 s for the leases of the
  NICs a server boots with, so that the user-data's commands find the
  networks up, as hc-utils has them on Hetzner.
- **The metadata** (`picture.Metadata`): `/hetzner/v1/metadata/private-networks`
  lists the server's networks with Hetzner's keys in Hetzner's order (`ip`,
  `alias_ips`, `interface_num`, `mac_address`, `network_id`,
  `network_name`, `network`, `subnet`, `gateway`), `[]` without one; the
  summary `/hetzner/v1/metadata` leaves it out, as Hetzner's does. Each NIC
  keeps its `interface_num` while others come and go, and a new one takes
  the lowest free number, as on Hetzner (`ServerPrivateNet.Interface`).
  Two deviations: the lab sorts the list by that number, where Hetzner's
  order changes from one read to the next; and a MAC of digits only is
  quoted in the YAML, as in `network-config`, because YAML 1.1 (cloud-init
  among its readers) reads it as a number.
- **A server without public addresses** reaches the metadata service
  through its networks, as on Hetzner: its lease has the route
  `169.254.169.254/32` through the gateway, the service listens in each
  network's VRF and knows the server by the VRF and its address, and
  `network-config` has no NIC. The lab gives the route only to a server
  without IPv4 and IPv6, as measured: Hetzner's server with IPv6 only
  has none in its private lease either.
- **A server with IPv6 only** reaches the metadata service through its
  public NIC, as on Hetzner (measured 2026-11-09): DHCP gives it a `/32`
  of `100.64.0.0/10`, its service address, with the classless routes
  (options 121 and 249) `172.31.1.1/32` on-link, `169.254.169.254/32` and
  `188.40.24.200/32` (Hetzner's DHCP server) through `172.31.1.1`, no
  default route and no DNS server; `network-config` names `dhcp` for IPv4
  beside the static IPv6, and the service knows the server by that
  address. The address is the number of the server's `/64` in
  `2001:db8:1::/48` counted into `100.64.0.0/10` (`lab.serviceAddress`):
  no record, and it follows the `/64`. It reaches only DHCP and the
  metadata service (docs/appliance.md, the ruleset).

## Projects, tokens, faults

These are the lab's own operations (`internal/lab/projects.go`, `tokens.go`,
`faults.go`). The lab control (`/_lab/...`) and the CLI call them. They report
errors as `lab.ErrInvalid`, `lab.ErrExists` and `state.ErrNotFound`.

- **Projects** have a name, limits and Internet access.
  - The name must be a DNS label, because it appears in URLs of the lab control and
    in log file names.
  - A limit of zero means no limit; a negative limit is refused.
  - Internet access is on by default.
  - `Lab.DeleteProject` first removes the records. Its tokens are gone then, so no
    new request is admitted. Then it cancels the running actions, which find their
    actions gone, and last removes its servers' VMs, disks and seeds and sends
    the pictures without them, as a server's delete does. What it fails to
    remove, the next start removes.
  - A request admitted just before the delete may still try to create something.
    So every create calls `lab.CheckProject` inside its `Change` (`Lab.Act` does
    so itself); the API answers `lab.ErrProjectGone` as a deleted token
    (`web.TokenInvalid`).
- **Tokens** are 64 letters and digits, like Hetzner's, with the permission `read`
  or `read_write`. Names are unique per project, so the CLI can refer to a token by
  name. `Lab.CreateToken` returns the token only once. The state keeps its SHA-256
  (`state.HashToken`) and its first four characters, to tell tokens apart.
- **The env token.** `Lab.EnvToken` returns a project's `read_write` token named
  `hlab-env`. It returns the same token on every call and after a restart, so
  `hlab env` gives every shell the same token, in WSL2 and Windows alike.
  - The state cannot show a token a second time, so the clear text is kept in
    `env-tokens.json` in the data folder (`lab.EnvTokenFile`).
  - That file is written like the control token and readable by the same users.
    This adds no risk: whoever can read the control token can create tokens anyway.
  - If the token is gone (deleted, or the file was lost), a new one is made. The
    file keeps only projects that still exist.
- **API faults.** `Lab.SetAPIFault` makes a project's next n API calls fail with a
  given status, each after a delay. `Lab.TakeAPIFault` counts one call against the
  fault. The counter is in the state (`state.Lab.Faults`), so it survives a restart.
  A call that finds no fault saves nothing.
