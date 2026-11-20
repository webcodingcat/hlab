# Phase 11 research: Hetzner Cloud Console create forms (non-server)

Applies to phase 11 (shared frame, servers, SSH keys) and phase 14 (remaining resources).

Source: public Hetzner docs (docs.hetzner.com), fetched 2026-11-20. Field order is as the docs page lists it; the docs describe the Console form in prose, so exact UI layout may differ slightly. Group names are only given for Load Balancers (and even there they are an interpretation, not Hetzner's headings).

## Volume
Source: https://docs.hetzner.com/cloud/volumes/getting-started/creating-a-volume/
1. Size (GB, text box or slider; price updates)
2. Name (unique per project)
3. Server (optional attach; one server at a time)
4. Mount option: automatic or manual
5. Filesystem: EXT4 or XFS (only with automatic mount)
Grouping: capacity, name, attachment, mounting.
Not on this page (unconfirmed for the form): location, labels.

## Load Balancer
Source: https://docs.hetzner.com/networking/load-balancers/getting-started/creating-a-load-balancer/
1. Location
2. Type (can be changed later)
3. Network (optional private network)
4. Targets (cloud servers, label selectors, Robot servers by IP)
5. Services (protocol, source port, destination port; advanced settings behind an edit icon)
6. Algorithm: Round Robin or Least Connections
7. Labels (optional)
8. Name (unique per project)
Grouping used: Placement (1-3), Backend (4-6), Identification (7-8). The page lists the fields in sequence only; the grouping is not Hetzner's wording.

## Network
Source: https://docs.hetzner.com/cloud/networks/getting-started/creating-a-network/
1. Name
2. IP range (RFC 1918: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
Subnets, routes, vSwitch are added after creation (not in this page's form).

## Firewall
Source: https://docs.hetzner.com/cloud/firewalls/getting-started/creating-a-firewall/
1. Rules (inbound, outbound)
2. Apply to (Servers or Label selectors)
3. Labels (optional)
4. Name (unique per project)
Note: name comes last.

## Floating IP
Source: https://docs.hetzner.com/cloud/floating-ips/getting-started/adding-a-floating-ip/
1. Location
2. Protocol (IPv4 or IPv6)
3. Name (unique per project)
Note: server assignment is a separate step (requires a Primary IP of the same type on the server).

## Primary IP
Source: https://docs.hetzner.com/cloud/servers/getting-started/creating-primary-ips/
1. Name
2. Location (only assignable to servers in the same location)
3. Protocol (IPv4 or IPv6)
Note: order differs from Floating IP (name first).

## DNS zone
Source: https://docs.hetzner.com/networking/dns/getting-started/creating-a-zone/
1. Domain name
2. Zone type: empty zone / import zone file / secondary
3. Primary name server IPs and port (only for secondary)
Submit button: "Add zone".

## Certificate
Source: https://docs.hetzner.com/networking/certificates/getting-started/adding-certificates/
Uploaded certificate:
1. Name
2. Certificate (PEM text)
3. Private Key (PEM text)
Managed certificate form (domain names, etc.): NOT documented on that page; unconfirmed.

## Placement Group
Source: https://docs.hetzner.com/cloud/placement-groups/overview/
No create-form description. Attributes: Name, Labels; only type: Spread. Field order of the Console form: unconfirmed (suggest Type, Name, Labels or Name only).

## Not confirmed
- Placement group form order; managed certificate form; labels fields on volume/network/IP forms (the docs omit them).
- Lead for later verification: the hcloud API reference (https://docs.hetzner.cloud/) for the full field set per resource.
