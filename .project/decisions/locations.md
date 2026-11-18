# Decision: Locations and network zones

Status: decided by the user, 2026-11-18

## Question

How are Hetzner locations, datacenters and network zones represented in
hlab, and which locations exist in v1? Many Hetzner rules depend on them:
volumes attach only to servers in the same location, floating IPs have a home
location, primary IPs belong to a datacenter, private networks and subnets
belong to a network zone, load balancers reach private targets only in their
network zone.

## Decision

Option (a): all locations share one network segment on Hyper-V. A location
is a label on the resources. The API enforces Hetzner's location, datacenter
and network zone rules.

- The catalog contains all six real Hetzner locations with their
  datacenters and network zones as fixed entries: fsn1, nbg1 and hel1
  (eu-central), ash (us-east), hil (us-west), sin (ap-southeast). The
  locations are not configurable and cannot be switched off in v1.
- Guests get the same addressing as on Hetzner. The public IPv4 is a /32
  with gateway 172.31.1.1 (onlink). Private network addresses are /32 and
  route via the network's gateway (`.1`). Guests never talk to each other
  directly on layer 2. All traffic, east-west included, goes through the
  appliance.
- Port isolation on the Hyper-V switch (port ACLs or an equivalent) lets
  VMs talk only to the appliance. A route that a guest adds by hand
  therefore cannot bypass it.
- The appliance knows the location of every IP address.
- Exactly one place in the code decides which segment a VM's network
  adapter is connected to. In v1 it always returns the shared segment.

## Reasons

- This option is the least work for v1: one switch for the public network
  and few appliance interfaces. The location rules are plain API validation
  and easy to test.
- With /32 routing all traffic passes the appliance. Latency injection
  between locations, which is part of the target vision, therefore stays
  possible later as rules on the appliance (`tc`/`netem` by source and
  destination location), without separate segments.
- Separate segments give no advantage for private networks. Hetzner's
  private networks are routed, so a network that spans several locations of
  a zone would need routing through the appliance in any case.
- A location that is only a catalog entry costs no resources, so all real
  locations can exist. Scripts that use `hel1`, `ash` or `sin` run
  unchanged.

## Cost of change

Switching to separate segments per location or per network zone (option b)
later is moderate, an estimated few days of work, provided v1 is built as
described above:

- Unchanged: the API and its rules, the guest configuration (/32, same
  gateway address on every segment interface), and the floating IP routes
  and firewalls on the appliance.
- Changed: the Hyper-V network setup (one VLAN ID per location on each VM
  adapter, via `Set-VMNetworkAdapterVlan`, also possible on running VMs),
  and the appliance (a trunk with one subinterface per segment, routes and
  interface configuration generated per segment).
- Existing installations need a one-time migration that tags the adapters
  of existing VMs. There is no data schema change, because every VM's
  location is already stored.

Without /32 routing through the appliance (a plain shared subnet), the same
switch would be expensive. The guest configuration, the floating IP
mechanism and the firewalls would have to be rebuilt.

Update 2026-11-18 (spec): the public segment is isolated by a private VLAN
(E1 in research/EXPERIMENTS.md) instead of port ACLs. Separate segments per
location later become one PVLAN pair per location (primary/secondary IDs
per location) chosen by `SegmentFor`; the appliance gets one promiscuous
port per pair. The cost estimate above stays.

## Rejected alternatives

- (b) One segment per location or per network zone, routed by the
  appliance (VLANs or several vSwitches). It would give physically enforced
  separation and simple per-interface latency injection. It was rejected
  because it means more work in v1 (trunk, subinterfaces, routing per
  segment, floating IPs across segments) and offers no functional gain,
  since /32 routing already sends all traffic through the appliance.
- (c) A configurable subset of locations. It saves resources only with
  separate segments. With labels a location costs nothing, so all real
  locations are always present.
- Switching off single locations (for example to make server types report
  unavailable) is not part of v1.
