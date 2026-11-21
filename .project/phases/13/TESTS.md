# Phase 13 test map

Client rows are subtests of `TestE2E`, named `CLI-0n/<type>` (CLI-04:
`CLI-04/<client>`), run through `testClients` (plan 13-01). "Fixture" rows
run one after another on the client's fixture server. Each row runs the
client with only its endpoint and token changed, checks every step
through the API, deletes what it made with the same client and checks
through the API that it is gone. Each row runs every write command,
resource or module of its type once and every read command or data
source, as listed in its plan (CONTEXT.md "Scope").

Left out of all clients (CONTEXT.md "Scope"): hcloud `datacenter`, tofu
`hcloud_datacenter`/`hcloud_datacenters`, Ansible `datacenter_info` (410
as at Hetzner); `server request-console` (`unsupported_error`); vswitch
subnets and `network expose-routes-to-vswitch`; the tofu `datacenter`
arguments (removed in provider 1.67.0); storage boxes (phase 21). `hcloud
all list` moves to CLI-05 in phase 21: hcloud v1.70.2 always fetches
storage boxes from the Hetzner endpoint, so without one it fails and sends
the hlab token to api.hetzner.com.

The unit rows of CLI-02 prove the action queue that lets OpenTofu create
sibling resources in parallel (CONTEXT.md "Actions").

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| CLI-02 | an action started with `StartQueued` that shares only a queuing ref with a running action is created `running`, its work starts after the earlier action finished, three actions on one ref run in order of creation, actions without a shared ref run at the same time, and more queued actions than workers all finish | unit | internal/actions/actions_test.go: TestActions/CLI-02/queue | 13-02 |
| CLI-02 | `StartQueued` returns `ErrLocked` when a running or queued action references one of its refuse refs, and `Start` still refuses on every ref, including one held only by a queued action | unit | internal/actions/actions_test.go: TestActions/CLI-02/refuse | 13-02 |
| CLI-02 | two servers' `attach_to_network` to one network and two `add_subnet` or `add_route` on one network sent together all succeed, the second after the first; `change_ip_range` while an action holds the network and a second `attach_to_network` of the same server answer 423 `locked` | unit | internal/resources/networks/networks_test.go: TestNetworks/CLI-02/queue | 13-03 |
| CLI-02 | while `attach_to_network`, `add_subnet` or `add_route` runs, GET of the server and the network shows neither the attachment nor the subnet or route; at `success` they are there | unit | internal/resources/networks/networks_test.go: TestNetworks/CLI-02/effect | 13-03 |
| CLI-02 | two `add_target` and two `add_service` on one load balancer sent together all succeed in order; `change_type` while a target action runs answers 423 `locked`; a second `attach_to_network` of the same load balancer answers 423 `locked` | unit | internal/resources/loadbalancers/loadbalancers_test.go: TestLoadBalancers/CLI-02/queue | 13-04 |
| CLI-02 | while `add_target`, `add_service` or `attach_to_network` of a load balancer runs, its GET shows neither the target, the service nor `private_net`; at `success` they are there; `add_target` with `use_private_ip` sent while the load balancer's `attach_to_network` still runs answers 422 `load_balancer_not_attached_to_network` | unit | internal/resources/loadbalancers/loadbalancers_test.go: TestLoadBalancers/CLI-02/effect | 13-04 |
| CLI-02 | attaching a volume and a second volume to one server sent together both succeed in order; change_protection of the volume and of the server during the attach still answer 423 `locked` | unit | internal/resources/volumes/volumes_test.go: TestVolumes/CLI-02/queue | 13-05 |
| CLI-02 | while a volume attach runs, GET of the volume shows no `server` and the server no volume; at `success` both show it | unit | internal/resources/volumes/volumes_test.go: TestVolumes/CLI-02/effect | 13-05 |
| CLI-02 | a floating IP assign sent while a volume attach on the same server runs succeeds after it; while the assign runs, GET of the floating IP shows the old `server`, at `success` the new one; a second assign of the same floating IP answers 423 `locked` | unit | internal/resources/floatingips/floatingips_test.go: TestFloatingIPs/CLI-02 | 13-06 |
| CLI-02 | a primary IP assign sent while another action holds only the server as a secondary ref succeeds after it; `assignee_id` and the server's `public_net` change at `success`; a second assign of the same primary IP answers 423 `locked` | unit | internal/resources/primaryips/primaryips_test.go: TestPrimaryIPs/CLI-02 | 13-06 |
| CLI-02 | `apply_to_resources` of two firewalls to one server sent together both succeed in order, `applied_to` changes at `success`, and a second `apply_to_resources` of the same firewall while its first runs answers 423 `locked` | unit | internal/resources/firewalls/firewalls_test.go: TestFirewalls/CLI-02 | 13-07 |
| CLI-02 | two `POST /servers` sent together with the same network and firewall both succeed with all their next actions | unit | internal/resources/servers/servers_test.go: TestServers/CLI-02 | 13-07 |
| CLI-01 | hcloud manages a server through every server command of the CLI except request-console: create, update, labels, power commands, reset-password, change-type, rebuild, rescue, backups, create-image, ISO, set-rdns, metrics, protection, placement group, delete | e2e | test/e2e/clients_servers_hcloud_test.go: TestE2E/CLI-01/servers | 13-08 |
| CLI-02 | OpenTofu creates `hcloud_server` with labels, keys, user data, placement group, backups, rescue, ISO and protection, updates name, labels, type, backups, ISO and protection in place, reads `hcloud_server` and `hcloud_servers`, and destroys it | e2e | test/e2e/clients_servers_tofu_test.go: TestE2E/CLI-02/servers | 13-09 |
| CLI-03 | the `server` module creates, stops, starts, restarts, rebuilds, puts into rescue and back, changes type and deletes a server; `server_info` finds it without filter, by name, ID and label selector (no ISO: the `server` module of hetzner.hcloud 7.2.0 has no ISO option) | e2e | test/e2e/clients_servers_ansible_test.go: TestE2E/CLI-03/servers | 13-10 |
| CLI-01 | `hcloud server-type list` and `describe` by name and ID match the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-01/server_types | 13-11 |
| CLI-01 | `hcloud location list` and `describe` by name and ID match the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-01/locations | 13-11 |
| CLI-01 | `hcloud load-balancer-type list` and `describe` by name and ID match the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-01/load_balancer_types | 13-11 |
| CLI-01 | `hcloud iso list` and `describe` by name and ID match the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-01/isos | 13-11 |
| CLI-02 | `hcloud_server_type` and `hcloud_server_types` match the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-02/server_types | 13-11 |
| CLI-02 | `hcloud_location` and `hcloud_locations` match the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-02/locations | 13-11 |
| CLI-02 | `hcloud_load_balancer_type` and `hcloud_load_balancer_types` match the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-02/load_balancer_types | 13-11 |
| CLI-03 | `server_type_info` by name and without filter matches the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-03/server_types | 13-11 |
| CLI-03 | `location_info` by name and without filter matches the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-03/locations | 13-11 |
| CLI-03 | `load_balancer_type_info` by name and without filter matches the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-03/load_balancer_types | 13-11 |
| CLI-03 | `iso_info` by name and without filter matches the API | e2e | test/e2e/clients_catalog_test.go: TestE2E/CLI-03/isos | 13-11 |
| CLI-01 | hcloud creates a snapshot of the fixture server, lists, describes, updates, protects and unprotects, and deletes it | e2e | test/e2e/clients_images_test.go: TestE2E/CLI-01/images | 13-12 |
| CLI-02 | OpenTofu creates `hcloud_snapshot` of the fixture server, updates it in place, reads `hcloud_image` (system image by name, snapshot by ID) and `hcloud_images` (by label), and destroys it | e2e | test/e2e/clients_images_test.go: TestE2E/CLI-02/images | 13-12 |
| CLI-03 | `image_info` finds a system image by name and a snapshot of the fixture server by label | e2e | test/e2e/clients_images_test.go: TestE2E/CLI-03/images | 13-12 |
| CLI-01 | hcloud creates, lists, describes, updates, labels and deletes an SSH key | e2e | test/e2e/clients_keys_test.go: TestE2E/CLI-01/ssh_keys | 13-13 |
| CLI-01 | hcloud creates, lists, describes, updates, labels and deletes a placement group | e2e | test/e2e/clients_keys_test.go: TestE2E/CLI-01/placement_groups | 13-13 |
| CLI-02 | OpenTofu creates, updates in place and destroys `hcloud_ssh_key` (existing row) and also reads `hcloud_ssh_key` and `hcloud_ssh_keys` | e2e | test/e2e/tofu_test.go: TestE2E/CLI-02/ssh_keys | 13-13 |
| CLI-02 | OpenTofu creates, updates in place and destroys `hcloud_placement_group` (existing row) and also reads `hcloud_placement_group` and `hcloud_placement_groups` | e2e | test/e2e/tofu_test.go: TestE2E/CLI-02/placement_groups | 13-13 |
| CLI-03 | `ssh_key` creates, updates and deletes a key; `ssh_key_info` finds it | e2e | test/e2e/clients_keys_test.go: TestE2E/CLI-03/ssh_keys | 13-13 |
| CLI-03 | `placement_group` creates, updates and deletes a placement group | e2e | test/e2e/clients_keys_test.go: TestE2E/CLI-03/placement_groups | 13-13 |
| CLI-01 | hcloud manages a network with subnets, routes, IP range, labels and protection, attaches the fixture server with alias IPs, changes them, lists members, detaches and deletes | e2e | test/e2e/clients_networks_test.go: TestE2E/CLI-01/networks | 13-14 |
| CLI-02 | OpenTofu creates a network, two subnets in parallel, a route and `hcloud_server_network` with alias IPs on the fixture server, updates them, reads `hcloud_network`, `hcloud_networks` and `hcloud_network_members`, and destroys them | e2e | test/e2e/clients_networks_test.go: TestE2E/CLI-02/networks | 13-14 |
| CLI-03 | `network`, `subnetwork`, `route` and `server_network` create, update and remove; `network_info` and `network_member_info` show them | e2e | test/e2e/clients_networks_test.go: TestE2E/CLI-03/networks | 13-14 |
| CLI-01 | hcloud creates, updates, labels, protects a primary IP, sets its reverse DNS, assigns it to the powered-off fixture server, unassigns and deletes it | e2e | test/e2e/clients_primaryips_test.go: TestE2E/CLI-01/primary_ips | 13-15 |
| CLI-02 | OpenTofu manages `hcloud_primary_ip` with an assignee, updates it in place, reads `hcloud_primary_ip` and `hcloud_primary_ips`, and destroys it | e2e | test/e2e/clients_primaryips_test.go: TestE2E/CLI-02/primary_ips | 13-15 |
| CLI-03 | `primary_ip` creates, assigns, updates and deletes a primary IP; `primary_ip_info` finds it | e2e | test/e2e/clients_primaryips_test.go: TestE2E/CLI-03/primary_ips | 13-15 |
| CLI-01 | hcloud creates, updates, labels, protects a floating IP, sets its reverse DNS, assigns it to the fixture server, unassigns and deletes it | e2e | test/e2e/clients_floatingips_test.go: TestE2E/CLI-01/floating_ips | 13-16 |
| CLI-02 | OpenTofu manages `hcloud_floating_ip` and `hcloud_floating_ip_assignment` to the fixture server, updates them, reads `hcloud_floating_ip` and `hcloud_floating_ips`, and destroys them | e2e | test/e2e/clients_floatingips_test.go: TestE2E/CLI-02/floating_ips | 13-16 |
| CLI-03 | `floating_ip` creates, assigns, updates and deletes a floating IP; `floating_ip_info` finds it | e2e | test/e2e/clients_floatingips_test.go: TestE2E/CLI-03/floating_ips | 13-16 |
| CLI-02 | `hcloud_rdns` sets, changes and on destroy resets the PTR of the fixture server, a floating IP and a primary IP (table) | e2e | test/e2e/clients_floatingips_test.go: TestE2E/CLI-02/rdns | 13-16 |
| CLI-03 | the `rdns` module sets, changes and removes the PTR of the fixture server, a floating IP and a primary IP (table) | e2e | test/e2e/clients_floatingips_test.go: TestE2E/CLI-03/rdns | 13-16 |
| CLI-01 | hcloud creates a volume in a location and one on the fixture server with automount and format, attaches, detaches, resizes, updates, labels, protects and deletes them | e2e | test/e2e/clients_volumes_test.go: TestE2E/CLI-01/volumes | 13-17 |
| CLI-02 | OpenTofu manages `hcloud_volume` and `hcloud_volume_attachment` to the fixture server, resizes in place, reads `hcloud_volume` and `hcloud_volumes`, and destroys them | e2e | test/e2e/clients_volumes_test.go: TestE2E/CLI-02/volumes | 13-17 |
| CLI-03 | `volume` and `volume_attachment` create, attach, resize, detach and delete; `volume_info` finds the volume | e2e | test/e2e/clients_volumes_test.go: TestE2E/CLI-03/volumes | 13-17 |
| CLI-01 | hcloud creates a firewall with rules, adds, deletes and replaces rules, updates, labels, applies it to the fixture server and a label selector, removes it and deletes it | e2e | test/e2e/clients_firewalls_test.go: TestE2E/CLI-01/firewalls | 13-18 |
| CLI-02 | OpenTofu manages `hcloud_firewall` and `hcloud_firewall_attachment` (fixture server and label selector), changes rules in place, reads `hcloud_firewall` and `hcloud_firewalls`, and destroys them | e2e | test/e2e/clients_firewalls_test.go: TestE2E/CLI-02/firewalls | 13-18 |
| CLI-03 | `firewall` and `firewall_resource` create, change rules, apply, remove and delete; `firewall_info` finds the firewall | e2e | test/e2e/clients_firewalls_test.go: TestE2E/CLI-03/firewalls | 13-18 |
| CLI-01 | hcloud manages a load balancer through every load-balancer command: network, services, targets (server with private IP, label selector, IP), algorithm, type, public interface, reverse DNS, metrics, labels, protection, delete | e2e | test/e2e/clients_lb_hcloud_test.go: TestE2E/CLI-01/load_balancers | 13-19 |
| CLI-02 | OpenTofu creates a load balancer, its network, two services and two targets in parallel, updates type, algorithm and a service in place, reads `hcloud_load_balancer(s)` and `hcloud_load_balancer_type(s)`, and destroys them | e2e | test/e2e/clients_lb_tofu_test.go: TestE2E/CLI-02/load_balancers | 13-20 |
| CLI-03 | `load_balancer`, `load_balancer_network`, `load_balancer_service` and `load_balancer_target` create, update and remove; `load_balancer_info` shows them | e2e | test/e2e/clients_lb_ansible_test.go: TestE2E/CLI-03/load_balancers | 13-21 |
| CLI-01 | hcloud creates an uploaded and a managed certificate, describes, lists, updates, labels, retries and deletes them | e2e | test/e2e/clients_certificates_test.go: TestE2E/CLI-01/certificates | 13-22 |
| CLI-02 | OpenTofu manages `hcloud_uploaded_certificate` and `hcloud_managed_certificate`, updates them in place, reads `hcloud_certificate` and `hcloud_certificates`, and destroys them | e2e | test/e2e/clients_certificates_test.go: TestE2E/CLI-02/certificates | 13-22 |
| CLI-03 | `certificate` creates an uploaded and a managed certificate, updates and deletes them; `certificate_info` finds them | e2e | test/e2e/clients_certificates_test.go: TestE2E/CLI-03/certificates | 13-22 |
| CLI-01 | hcloud manages a zone and its RRSets through every zone and zone rrset command, including zone file import and export | e2e | test/e2e/clients_zones_test.go: TestE2E/CLI-01/zones | 13-23 |
| CLI-02 | OpenTofu manages `hcloud_zone`, `hcloud_zone_rrset` and `hcloud_zone_record`, updates them in place, reads `hcloud_zone(s)` and `hcloud_zone_rrset(s)`, and destroys them | e2e | test/e2e/clients_zones_test.go: TestE2E/CLI-02/zones | 13-23 |
| CLI-03 | `zone` and `zone_rrset` create, update and delete; `zone_info` and `zone_rrset_info` find them | e2e | test/e2e/clients_zones_test.go: TestE2E/CLI-03/zones | 13-23 |
| CLI-03 | the inventory plugin `hetzner.hcloud.hcloud` lists the fixture server with `ansible_host` and `hcloud_ipv4` equal to its public IPv4, in the group of its label | e2e | test/e2e/clients_inventory_test.go: TestE2E/CLI-03/inventory | 13-24 |
| CLI-04 | in its own project, hcloud builds the example environment; both servers answer SSH from host and WSL2 on their public IPv4 and once through the LB's TCP service; hcloud deletes it, every list of the project is empty and no VM of its servers remains | e2e | test/e2e/clients_example_hcloud_test.go: TestE2E/CLI-04/hcloud | 13-25 |
| CLI-04 | the same with one `main.tf`, `tofu apply` and `tofu destroy` | e2e | test/e2e/clients_example_tofu_test.go: TestE2E/CLI-04/tofu | 13-26 |
| CLI-04 | the same with a create and a delete playbook run in WSL2 | e2e | test/e2e/clients_example_ansible_test.go: TestE2E/CLI-04/ansible | 13-27 |
