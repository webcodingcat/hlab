package api

// Deviation is one known difference between hlab and the Hetzner Cloud API.
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
			Description: "rescue is disabled only on boots hlab drives (poweron, reboot, reset) or after 60 minutes; a reboot inside the guest keeps it enabled",
		},
		{
			Endpoint:    "GET /isos",
			Description: "lists the five ISOs of hlab's catalog with hlab's own IDs from 130001; no private ISOs",
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
			Description: "linux_device link, automount and pick-up of a grown volume work only through hlab guest hooks; without them a volume is a plain SCSI disk",
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
			Description: "works through the hlab guest hook, not qemu-guest-agent; 409 conflict when the server is not running",
		},
		{
			Endpoint:    "GET /servers/{id}/metrics",
			Description: "samples every 10 s kept 24 h in memory, lost at cloud stop; disk.0 sums all disks of the VM",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/request_console",
			Description: "answers unsupported_error: server console is not supported by hlab yet",
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
			Description: "no DNSSEC, DS records are served as plain data; CNAME conflicts answer 422 invalid_input on type, an existing RRSet 409 uniqueness_error; the apex SOA is managed by hlab except its TTL",
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
			Description: "proxyprotocol sends PROXY protocol v2, Hetzner's version is not documented; an https health check is stored as http with http.tls true and does not verify the certificate; undocumented errors use hlab's chosen codes",
		},
		{
			Endpoint:    "POST /primary_ips/{id}/actions/assign",
			Description: "a primary IP held by a load balancer shows assignee_type load_balancer; assign answers 422 primary_ip_already_assigned, unassign 422 invalid_input",
		},
		{
			Endpoint:    "POST /certificates",
			Description: "managed certificates are issued by the hlab CA, not Let's Encrypt, and name the CRL http://api.hetzner.internal/hlab/v1/ca.crl",
		},
		{
			Endpoint:    "GET /load_balancers/{id}/metrics",
			Description: "HAProxy counters read every 10 s and kept 24 h in memory; the smallest step is 10 s",
		},
	}
}
