package api

// Deviation is one known difference between HLab and the Hetzner Cloud API.
type Deviation struct {
	Endpoint    string
	Description string
}

// Deviations lists the known differences from the Hetzner Cloud API; the
// user documentation names each of them.
func Deviations() []Deviation {
	return []Deviation{
		{
			Endpoint:    "POST /servers",
			Description: "the IPv6 /64 is assigned formally; the server gets no IPv6 traffic",
		},
		{
			Endpoint:    "POST /servers",
			Description: "the returned root password does not expire; Hetzner forces a change at the first login",
		},
		{
			Endpoint:    "POST /servers",
			Description: "resource_unavailable when the host lacks CPUs, RAM or disk beyond the configured reserve",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/shutdown",
			Description: "the action succeeds once the request is sent to the guest, not when the server is off",
		},
		{
			Endpoint:    "POST /servers",
			Description: "a server type whose disk is smaller than the image's disk_size answers 422 invalid_input on the field server_type",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/rebuild",
			Description: "takes a system image or a snapshot or backup of the project; an image larger than the server type's disk answers 422 invalid_input on the field image",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/create_image",
			Description: "the copy is crash-consistent, taken through a Hyper-V checkpoint while the server runs; image_size is the uncompressed disk file size",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/create_image",
			Description: "type backup on a server without backups enabled answers 422 invalid_input on the field type",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/enable_backup",
			Description: "backup_window is shown as 22-02 but unused; one daily backup per UTC day at the first hourly check while the cloud runs, none for days it did not run",
		},
		{
			Endpoint:    "DELETE /images/{id}",
			Description: "an image that is still being created answers 423 locked",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/enable_rescue",
			Description: "the Debian 13 rescue system boots from its own disk, not from memory; the server's disk stays /dev/sda",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/enable_rescue",
			Description: "rescue is disabled only on boots HLab drives (poweron, reboot, reset) or after 60 minutes; a reboot inside the guest keeps it enabled",
		},
		{
			Endpoint:    "GET /isos",
			Description: "lists the five ISOs of HLab's catalog with HLab's own IDs from 130001; no private ISOs",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_iso",
			Description: "the first attach downloads the ISO inside the action; an unknown or deprecated ISO answers 422 invalid_input on the field iso; a second DVD drive holds the cloud-init seed",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/detach_iso",
			Description: "on a server without an ISO the action succeeds and changes nothing",
		},
		{
			Endpoint:    "POST /volumes",
			Description: "linux_device link, automount and pick-up of a grown volume work only through HLab guest hooks; without them a volume is a plain SCSI disk",
		},
		{
			Endpoint:    "POST /volumes",
			Description: "a location other than the server's answers 422 invalid_input on the field location; resource_unavailable when the volume does not fit beyond reserve_disk_gb",
		},
		{
			Endpoint:    "POST /volumes/{id}/actions/attach",
			Description: "at most 16 volumes per server, one more answers 422 invalid_input; another location answers 422 invalid_input on the field server or assignee",
		},
		{
			Endpoint:    "POST /volumes/{id}/actions/resize",
			Description: "the guest hook rescans the disk; a server without the hook sees the new size at the disk's next access",
		},
		{
			Endpoint:    "DELETE /volumes/{id}",
			Description: "an attached volume answers 422 invalid_input",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/reset_password",
			Description: "works through the HLab guest hook, not qemu-guest-agent; 409 conflict when the server is not running",
		},
		{
			Endpoint:    "GET /servers/{id}/metrics",
			Description: "samples every 10 s kept 24 h in memory, lost at cloud stop; disk.0 sums all disks of the VM",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/request_console",
			Description: "answers unsupported_error: server console is not supported by HLab yet",
		},
		{
			Endpoint:    "http://169.254.169.254/hetzner/v1/metadata",
			Description: "no EC2 routes; public-keys and userdata in cloud-init's shape",
		},
		{
			Endpoint:    "POST /zones",
			Description: "names may also be one label below internal, test or home.arpa (not hetzner.internal, never local); zones under arpa and subdomains are refused; a name used in another project answers 409 uniqueness_error",
		},
		{
			Endpoint:    "POST /zones",
			Description: "secondary zones are stored and their queries forwarded upstream; no zone transfer, no records of their own",
		},
		{
			Endpoint:    "GET /zones/{id}",
			Description: "authoritative_nameservers.delegated is [], delegation_status and registrar are unknown, status is always ok",
		},
		{
			Endpoint:    "POST /zones/{id}/rrsets",
			Description: "no DNSSEC, DS records are served as plain data; CNAME conflicts answer 422 invalid_input on type, an existing RRSet 409 uniqueness_error; the apex SOA is managed by HLab except its TTL",
		},
		{
			Endpoint:    "POST /zones/{id}/actions/import_zonefile",
			Description: "a bad zone file answers 422 invalid_input on zonefile naming the line, without an action; a change-protected RRSet in the zone answers protected",
		},
		{
			Endpoint:    "POST /zones/{id}/actions/change_primary_nameservers",
			Description: "addresses are checked for syntax only, never for reachability; private addresses are allowed",
		},
		{
			Endpoint:    "GET /zones/actions/{id}",
			Description: "zone and RRSet actions are also readable here and at /actions/{id}",
		},
		{
			Endpoint:    "POST /primary_ips/{id}/actions/change_dns_ptr",
			Description: "IPv6 PTR entries are stored and shown but not resolvable; only IPv4 PTRs are answered by DNS",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/change_dns_ptr",
			Description: "an IP the server does not hold answers 422 invalid_input on the field ip; the action names the server and its primary IP",
		},
		{
			Endpoint:    "POST /load_balancers",
			Description: "the IPv6 address is formal and carries no traffic; without public_net.ipv4 the IPv4 comes from the public range and is no primary IP, given primary IPs are held by the load balancer",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/add_target",
			Description: "ip targets take any unicast IPv4 except the first four addresses of the public range and the load balancer's own, servers of the cloud included; they are reached over the public interface",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/add_service",
			Description: "proxyprotocol sends PROXY protocol v2, Hetzner's version is not documented; an https health check is stored as http with http.tls true and does not verify the certificate; undocumented errors use HLab's chosen codes",
		},
		{
			Endpoint:    "POST /primary_ips/{id}/actions/assign",
			Description: "a primary IP held by a load balancer shows assignee_type load_balancer; assign answers 422 primary_ip_already_assigned, unassign 422 invalid_input",
		},
		{
			Endpoint:    "POST /certificates",
			Description: "managed certificates are issued by the HLab CA, not Let's Encrypt, and name the CRL http://api.hetzner.internal/hlab/v1/ca.crl",
		},
		{
			Endpoint:    "GET /load_balancers/{id}/metrics",
			Description: "HAProxy counters read every 10 s and kept 24 h in memory; the smallest step is 10 s",
		},
		{
			Endpoint:    "POST /networks/{id}/actions/add_subnet",
			Description: "a vswitch subnet answers 422 invalid_input; expose_routes_to_vswitch is stored and shown but has no effect",
		},
		{
			Endpoint:    "POST /networks",
			Description: "the gateway is the first host address of ip_range and is shown on every subnet, also one of type server",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_to_network",
			Description: "a server gets the lowest free host address of a subnet in its network zone; network, broadcast, gateway and 172.31.1.1 are never handed out",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_to_network",
			Description: "private adapters have MAC addresses 02:48:4c:2x:xx:xx, not Hetzner's 86:00:00:xx:xx:xx",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_to_network",
			Description: "no DHCP server; cloud-init's seed and the HLab guest hook set up private networks, a server without the hook needs the adapter configured by hand",
		},
		{
			Endpoint:    "POST /networks",
			Description: "the MTU on private networks is 1500; Hetzner uses 1450",
		},
		{
			Endpoint:    "DELETE /networks/{id}",
			Description: "detaches all servers of the network and removes their adapters before it answers",
		},
		{
			Endpoint:    "POST /networks/{id}/actions/delete_subnet",
			Description: "a subnet in which an attached server holds an IP or alias IP answers 422 service_error",
		},
		{
			Endpoint:    "POST /networks/{id}/actions/delete_route",
			Description: "a subnet or route the network does not have answers 422 invalid_input, for delete_subnet as well",
		},
		{
			Endpoint:    "POST /networks/{id}/actions/change_ip_range",
			Description: "another start address or a smaller range answers 422 invalid_input on the field ip_range",
		},
		{
			Endpoint:    "POST /networks/{id}/actions/change_ip_range",
			Description: "answers 422 service_error while servers are attached",
		},
		{
			Endpoint:    "POST /networks",
			Description: "answers 403 resource_limit_exceeded when all 4091 VLAN IDs of the cloud are in use",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_to_network",
			Description: "a fourth network for the server answers 403 resource_limit_exceeded, at POST /servers as well",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/change_alias_ips",
			Description: "more than 5 alias IPs of a server in one network answer 422 invalid_input on the field alias_ips",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_to_network",
			Description: "no free IP or no subnet in the server's network zone answers 422 no_subnet_available",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_to_network",
			Description: "an ip or alias IP that is taken or reserved answers 422 ip_not_available",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_to_network",
			Description: "an ip outside the subnets of the server's network zone answers 422 invalid_input on the field ip",
		},
		{
			Endpoint:    "POST /primary_ips",
			Description: "an IPv6 primary IP is formal and carries no traffic: the server's /64 or 2001:db8:8000:<id>::/64",
		},
		{
			Endpoint:    "GET /primary_ips/{id}",
			Description: "primary IPs show their location and have no datacenter field",
		},
		{
			Endpoint:    "POST /primary_ips",
			Description: "a primary IP created without a name is named primary_ip-<id>",
		},
		{
			Endpoint:    "GET /primary_ips",
			Description: "servers of an earlier HLab version get assigned primary IP records with auto_delete true at the next cloud start",
		},
		{
			Endpoint:    "POST /primary_ips/{id}/actions/change_protection",
			Description: "delete true on a primary IP with auto_delete answers 422 invalid_input on the field delete",
		},
		{
			Endpoint:    "DELETE /primary_ips/{id}",
			Description: "an assigned primary IP answers 422 must_be_unassigned",
		},
		{
			Endpoint:    "POST /floating_ips/{id}/actions/assign",
			Description: "an IPv4 floating IP is routed to the server's public IPv4; a server without public IPv4 receives no traffic for it",
		},
		{
			Endpoint:    "POST /floating_ips",
			Description: "an IPv6 floating IP is formal and carries no traffic: 2001:db8:c000:<id>::/64",
		},
		{
			Endpoint:    "POST /floating_ips/{id}/actions/assign",
			Description: "a server outside the network zone of the home location answers 422 invalid_input on the field server; the server holding it answers 422 floating_ip_assigned",
		},
		{
			Endpoint:    "POST /floating_ips",
			Description: "a floating IP created without a name is named floating_ip-<id>",
		},
		{
			Endpoint:    "DELETE /floating_ips/{id}",
			Description: "an assigned floating IP answers 422 must_be_unassigned",
		},
		{
			Endpoint:    "POST /firewalls/{id}/actions/apply_to_resources",
			Description: "filters the server's public IPv4 and the floating IPs assigned to it; Hetzner does not document floating IPs",
		},
		{
			Endpoint:    "POST /firewalls/{id}/actions/set_rules",
			Description: "IPv6 rules are stored but have no effect",
		},
		{
			Endpoint:    "GET /servers/{id}",
			Description: "the status of a firewall in public_net.firewalls is always applied, never pending",
		},
		{
			Endpoint:    "POST /firewalls",
			Description: "Hetzner's limits of 500 effective rules per firewall and 50 firewalls per project are not enforced",
		},
		{
			Endpoint:    "POST /firewalls/{id}/actions/set_rules",
			Description: "IPs must be CIDRs with a mask and without host bits, else 422 invalid_input on rules[i].source_ips or destination_ips; ports are returned as sent, IPv6 CIDRs in canonical form",
		},
		{
			Endpoint:    "POST /firewalls/{id}/actions/apply_to_resources",
			Description: "the actions for a label selector name only the firewall in their resources; incompatible_network_type is never returned",
		},
		{
			Endpoint:    "POST /firewalls/{id}/actions/set_rules",
			Description: "returns set_firewall_rules plus one apply_firewall per server the firewall is applied to directly",
		},
		{
			Endpoint:    "POST /firewalls/{id}/actions/remove_from_resources",
			Description: "a server or label selector the firewall is not applied to answers 404 firewall_resource_not_found",
		},
		{
			Endpoint:    "POST /firewalls/{id}/actions/apply_to_resources",
			Description: "a label selector the firewall already has answers 422 firewall_already_applied",
		},
		{
			Endpoint:    "POST /firewalls/{id}/actions/apply_to_resources",
			Description: "a sixth firewall for a server answers 403 resource_limit_exceeded; a label selector skips such a server",
		},
		{
			Endpoint:    "DELETE /firewalls/{id}",
			Description: "a firewall applied only through a label selector answers 422 resource_in_use",
		},
		{
			Endpoint:    "POST /servers",
			Description: "a firewall that does not exist answers 404 not_found; firewalls without public IPv4 answer 422 private_net_only_server; a firewall listed twice is applied once",
		},
		{
			Endpoint:    "POST /floating_ips/{id}/actions/change_dns_ptr",
			Description: "an IPv4 without an own entry shows Hetzner's default static.<d>.<c>.<b>.<a>.clients.your-server.de; dns_ptr needs two labels, null resets it, a /64 holds 100 entries",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/change_dns_ptr",
			Description: "the load balancer's IPv4 starts with the default name and resolves also while its public interface is off",
		},
		{
			Endpoint:    "GET /load_balancers/{id}",
			Description: "health_status of a target is unknown until HAProxy checked it and shows only listen_port and status",
		},
		{
			Endpoint:    "POST /load_balancers",
			Description: "max_connections of the type becomes HAProxy's maxconn and nothing else limits the traffic; at most one network",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/add_service",
			Description: "redirect_http of an https service takes port 80; an https service whose managed certificate is not issued is not served",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/add_service",
			Description: "beyond the limits of the load balancer type answers 403 resource_limit_exceeded, as add_target does",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/change_type",
			Description: "a type whose limits the load balancer exceeds answers 422 invalid_load_balancer_type",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/add_target",
			Description: "a server target in another network zone answers 422 invalid_input on the field server; without use_private_ip while the public interface is off 422 load_balancer_public_interface_disabled",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/add_service",
			Description: "a listen port in use answers 412 source_port_already_used; without health_check a tcp check on the destination port is set",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/update_service",
			Description: "moving a service onto a listen port in use answers 422 source_port_already_used; a listen port without a service 422 invalid_input",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/delete_service",
			Description: "a listen port without a service answers 422 invalid_input on the field listen_port",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/remove_target",
			Description: "a target the load balancer does not have answers 404 not_found",
		},
		{
			Endpoint:    "POST /load_balancers/{id}/actions/detach_from_network",
			Description: "answers 422 invalid_input on the field network while targets use private IPs of the network",
		},
		{
			Endpoint:    "POST /certificates",
			Description: "managed certificates are valid 90 days with an ECDSA P-256 key, renewed 30 days before they expire, trusted after hlab ca trust on",
		},
		{
			Endpoint:    "POST /certificates",
			Description: "a managed certificate whose domains lie below no zone of the project fails with dns_zone_not_found, below a secondary zone with dns_zone_is_secondary_zone",
		},
		{
			Endpoint:    "POST /certificates/{id}/actions/retry",
			Description: "a certificate that has not failed, or an uploaded one, answers 422 invalid_input",
		},
		{
			Endpoint:    "DELETE /certificates/{id}",
			Description: "a certificate a load balancer service uses answers 422 resource_in_use",
		},
		{
			Endpoint:    "POST /certificates",
			Description: "uploaded certificates are not checked for expiry or chain order; an unreadable PEM or a key that does not match answers 422 invalid_input",
		},
	}
}
