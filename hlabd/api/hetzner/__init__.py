"""JSON shapes of the Hetzner Cloud API, for the catalogue and the resources.

One module per area; everything is used as `hetzner.<name>`. See
https://docs.hetzner.cloud/.
"""

from .balancers import (
    HEALTH,
    certificate_json,
    health_status,
    load_balancer_json,
    target_json,
)
from .catalogue import (
    COORDS,
    LOCATION_IDS,
    image_id,
    image_json,
    image_os,
    load_balancer_type_id,
    load_balancer_type_json,
    location_id,
    location_json,
    pricing_json,
    server_type_id,
    server_type_json,
    server_type_prices,
    storage_box_type_id,
    type_prices,
)
from .common import (
    CREATED,
    HOURS_PER_MONTH,
    TB,
    default_ptr,
    dns_ptrs,
    error,
    hourly,
    price,
)
from .dns import NAMESERVERS, rrset_json, zone_json
from .networks import firewall_json, network_json
from .servers import (
    floating_ip_json,
    own_image_json,
    placement_group_json,
    primary_ip_json,
    server_image_json,
    server_json,
    ssh_key_json,
    volume_json,
)

__all__ = [
    "COORDS",
    "CREATED",
    "HEALTH",
    "HOURS_PER_MONTH",
    "LOCATION_IDS",
    "NAMESERVERS",
    "TB",
    "certificate_json",
    "default_ptr",
    "dns_ptrs",
    "error",
    "firewall_json",
    "floating_ip_json",
    "health_status",
    "hourly",
    "image_id",
    "image_json",
    "image_os",
    "load_balancer_json",
    "load_balancer_type_id",
    "load_balancer_type_json",
    "location_id",
    "location_json",
    "network_json",
    "own_image_json",
    "placement_group_json",
    "price",
    "pricing_json",
    "primary_ip_json",
    "rrset_json",
    "server_image_json",
    "server_json",
    "server_type_id",
    "server_type_json",
    "server_type_prices",
    "ssh_key_json",
    "storage_box_type_id",
    "target_json",
    "type_prices",
    "volume_json",
    "zone_json",
]
