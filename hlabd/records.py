"""The record types of the state, for the type checker.

There is one TypedDict per kind of resource, with the meaning of each field; state.py
keeps the records by kind and ID. Fields that start with `_` are used only by the lab,
and the API never shows them. lab.py describes the action flags among them.
`NotRequired` fields are missing until something sets them.

The faults (/_lab/faults) are typed here too. state.py describes the other records
that only the lab uses (netem, routes of networks, Storage Box accounts created outside
the API).
"""

from typing import Literal, NotRequired, TypedDict, final


class Protection(TypedDict):
    """The delete protection of a resource."""

    delete: bool


class ServerProtection(TypedDict):
    """A server's protection (Hetzner sets both together)."""

    delete: bool
    rebuild: bool


class DnsPtr(TypedDict):
    """A reverse DNS entry of one address of a primary or Floating IP."""

    ip: str
    dns_ptr: str


class Nic(TypedDict):
    """A server's NIC in a cloud network (one per network)."""

    network: int  # the network's ID
    ip: str  # its private IP
    alias_ips: list[str]  # maybe empty
    number: int  # the lowest free on its server, 1-255
    tap: str  # prv<index>x<number>
    mac: str
    # while hot-plugged into a running VM: the PCIe root port it sits on (hp1-hp4)
    port: NotRequired[str | None]


class ServerLab(TypedDict):
    """The lab-only fields of a server (`server["_lab"]`, set by `create_server`)."""

    index: int  # its number in the lab (1-239): tap pub<index>, MACs, IPv6 link-local
    tap: str  # its public tap
    nics: list[Nic]  # its NICs in cloud networks, in the order they were attached
    disk_gb: int  # the size of its disk (a type change without upgrade_disk keeps it)
    # "running" or "off": the status that its create or rebuild ends in; a lab restart
    # uses it to finish either one
    target: str
    root_password: NotRequired[str]  # only without SSH keys (Hetzner then sets one)


class Server(TypedDict):
    """A server."""

    id: int
    name: str
    status: str  # Hetzner's: initializing, starting, running, stopping, off, ...
    created: str
    labels: dict[str, str]
    server_type: str  # names of catalogue entries (hlab.yaml)
    image: str
    location: str
    protection: ServerProtection
    user_data: str | None
    public_keys: list[str]  # the SSH keys it was made with
    _lab: ServerLab
    # its primary IPs (IDs); set right after it is made, None when it has none
    ipv4_id: NotRequired[int | None]
    ipv6_id: NotRequired[int | None]
    _dead: NotRequired[bool]  # the fault "dead": a power on leaves it off
    _locked: NotRequired[bool]  # an action runs (the API shows it as `locked`)


class ActionResource(TypedDict):
    """A resource that an action is about."""

    id: int
    type: str  # "server", "volume", ...


class Action(TypedDict):
    """An action, as the API shows it."""

    id: int
    command: str  # Hetzner's name of the action
    status: str  # running, success, error
    progress: int
    started: str
    finished: str | None
    error: dict | None  # {"code", "message"} when it failed
    resources: list[ActionResource]


class SSHKey(TypedDict):
    """An SSH key of the project."""

    id: int
    name: str
    fingerprint: str  # MD5, unique in the project
    public_key: str
    labels: dict[str, str]
    created: str


class Volume(TypedDict):
    """A volume: a raw disk file (fabric/volumes.py)."""

    id: int
    name: str
    size: int  # GB
    location: str
    labels: dict[str, str]
    protection: Protection
    status: str  # creating, available
    format: str | None  # ext4, xfs or none
    created: str
    server: int | None  # the server it is attached to
    automount: NotRequired[bool]
    _busy: NotRequired[bool]


class PrimaryIP(TypedDict):
    """A primary IP: a server's own IPv4, or its IPv6 /64."""

    id: int
    name: str
    type: str  # ipv4, ipv6
    location: str
    ip: str  # an address, or a /64 network
    assignee_id: int | None  # the server it belongs to
    auto_delete: bool  # deleted with its server
    labels: dict[str, str]
    protection: Protection
    dns_ptr: list[DnsPtr]
    created: str


class FloatingIP(TypedDict):
    """A Floating IP, routed to the server it is assigned to."""

    id: int
    name: str
    description: str | None
    ip: str  # an address, or a /64 network
    type: str  # ipv4, ipv6
    server: int | None  # the server it is assigned to
    home_location: str
    labels: dict[str, str]
    protection: Protection
    dns_ptr: list[DnsPtr]
    created: str
    _moving: NotRequired[bool]


class Subnet(TypedDict):
    """A subnet of a cloud network."""

    type: str  # cloud, server, vswitch
    ip_range: str
    network_zone: str
    gateway: str  # the network's first address, which the lab holds
    vswitch_id: int | None


class Route(TypedDict):
    """A route of a cloud network: a destination behind a server's address."""

    destination: str
    gateway: str


class CloudNetwork(TypedDict):
    """A cloud network; each routes in its own table (fabric/network.py)."""

    id: int
    name: str
    ip_range: str
    subnets: list[Subnet]
    routes: list[Route]
    labels: dict[str, str]
    protection: Protection
    created: str
    expose_routes_to_vswitch: bool
    _deleting: NotRequired[bool]


class FirewallRule(TypedDict):
    """A firewall rule, normalised to Hetzner's field set."""

    direction: str  # in, out
    protocol: str  # tcp, udp, icmp, esp, gre
    port: str | None  # a port, a range like 80-85 or any; tcp and udp only
    source_ips: list[str]  # inbound rules
    destination_ips: list[str]  # outbound rules
    description: str | None


class Firewall(TypedDict):
    """A cloud firewall (rules: fabric/firewall.py)."""

    id: int
    name: str
    labels: dict[str, str]
    created: str
    rules: list[FirewallRule]
    # {"type": "server", "server": {"id"}} or
    # {"type": "label_selector", "label_selector": {"selector"}}
    applied_to: list[dict]


class Client(TypedDict):
    """An "Internet" client: a network namespace behind a veth (/_lab/clients)."""

    id: int
    name: str
    number: int  # its number in the lab: veth, addresses
    veth: str  # cl<number>
    ip: str
    ip6: str


class StorageBox(TypedDict):
    """A Storage Box: an account on the lab's box (fabric/storagebox.py)."""

    id: int  # also the UID of its account
    name: str
    type: str  # a storage_box_types entry (hlab.yaml)
    location: str
    access: dict[str, bool]  # Hetzner's access_settings
    plan: dict | None  # the snapshot plan (Hetzner's fields), or none
    protection: Protection
    labels: dict[str, str]
    status: str  # initializing, active
    created: str
    _password: NotRequired[str]  # set again on every lab start
    _busy: NotRequired[bool]
    _deleting: NotRequired[bool]


class Subaccount(TypedDict):
    """A sub-account of a Storage Box."""

    id: int
    box: int  # the box's ID
    number: int  # u<box>-sub<number>
    username: str
    name: str | None
    home_directory: str  # relative to the box's root
    access: dict[str, bool]  # Hetzner's access_settings
    description: str
    labels: dict[str, str]
    created: str
    _password: NotRequired[str]  # set again on every lab start
    _creating: NotRequired[bool]


class BoxSnapshot(TypedDict):
    """A snapshot of a Storage Box: a copy of its data."""

    id: int
    box: int  # the box's ID
    name: str  # its time, unique per box
    description: str
    labels: dict[str, str]
    is_automatic: bool  # taken by the snapshot plan
    created: str
    size: int
    _copying: NotRequired[bool]  # while it is copied (a restart removes it)


# A record of any kind of resource, for code that treats every kind alike (flags, PUT
# fields, lookups). Code that knows the kind uses that kind's type.
Record = (
    Server
    | Volume
    | PrimaryIP
    | FloatingIP
    | CloudNetwork
    | Firewall
    | SSHKey
    | StorageBox
    | Subaccount
    | BoxSnapshot
    | Client
)

# The resources that have an action table, run by `Lab.act()`.
ActionRecord = (
    Server
    | Volume
    | PrimaryIP
    | FloatingIP
    | CloudNetwork
    | Firewall
    | StorageBox
    | Subaccount
)

# The resources with a plain delete protection (`api/web.py`: change_delete_protection).
ProtectedRecord = Volume | PrimaryIP | FloatingIP | CloudNetwork | StorageBox


# --- faults (/_lab/faults; state.py: `faults`) ---------------------------------------


# final: no other keys, so `"server" in ref` tells the two apart for mypy
@final
class ServerTarget(TypedDict):
    """A fault's server, by ID: a renamed server keeps its faults."""

    server: int


@final
class ClientTarget(TypedDict):
    """A fault's Internet client, by name (a client cannot be renamed)."""

    client: str


# what a fault points at; the API shows it as s:<id> or c:<name> (api/faults.py)
TargetRef = ServerTarget | ClientTarget

# which interfaces of a server a fault or netem takes; a client has only a public link
type Which = Literal["public", "private", "all"]


class CutFault(TypedDict):
    """A cut link between two servers or clients, in both directions."""

    type: Literal["cut"]
    first: str  # the names as given, sorted (a cut is stored in one order)
    second: str
    net: Which
    first_ref: TargetRef
    second_ref: TargetRef


class IsolateFault(TypedDict):
    """A server or client cut off from everything."""

    type: Literal["isolate"]
    vm: str  # the name as given
    target_ref: TargetRef


class BlockFault(TypedDict):
    """A server or client cut off from a service: the API, the box or the Internet."""

    type: Literal["block"]
    vm: str  # the name as given
    what: Literal["api", "box", "internet"]
    target_ref: TargetRef


Fault = CutFault | IsolateFault | BlockFault
