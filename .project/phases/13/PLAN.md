# Phase 13: Client matrix and success criterion

Confirmed scope, exclusions and harness rules are in CONTEXT.md; the
client command trees, resources and modules in RESEARCH.md. Every group
plan first runs `hcloud <type> --help`, `ansible-doc -l hetzner.hcloud`
and `ansible-doc hetzner.hcloud.<module>` in WSL2 (and `tofu validate`
where an argument is in doubt); flags and modules come from that output,
not from RESEARCH.md. A product bug a client exposes first gets a failing
unit test in its own module and is fixed there; a bug outside the plan's
files goes to a fresh implementer.

## Interfaces

### internal/actions (plan 13-02)

```go
// StartQueued creates a running action of projectID for command on
// resources in tx. It returns ErrLocked if a running action (one that
// still waits in the queue included) references one of refuse; refuse is
// a subset of resources and names the resources whose own state the
// action changes. A resource of resources that is not in refuse never
// refuses: the action queues behind the actions holding it.
func (m *Manager) StartQueued(tx store.Tx, projectID int64, command string, resources, refuse []Ref) (*Action, error)

// Run (signature unchanged) now waits, without holding a worker, until
// every running action with a lower ID that shares a ref with action id
// has finished, then runs fn. Start and StartAll keep their behaviour
// (Start refuses on every ref; StartAll refuses on lock only), and their
// actions queue in Run the same way.
func (m *Manager) Run(id int64, fn func(ctx context.Context, progress func(int)) error)
```

Who refuses and who queues (CONTEXT "Actions"):

| Action | refuse | queue on |
|---|---|---|
| server `attach_to_network`, `detach_from_network`, `change_alias_ips` | server | network |
| LB `attach_to_network`, `detach_from_network` | load balancer | network |
| network `add_subnet`, `delete_subnet`, `add_route`, `delete_route` | - | network |
| LB `add_target`, `remove_target`, `add_service`, `update_service`, `delete_service` | - | load balancer, target servers |
| volume `attach`, `detach`; create with `server` | volume | server |
| floating IP `assign`, `unassign` | floating IP | server |
| primary IP `assign`, `unassign` | primary IP | server |
| firewall `apply_to_resources`, `remove_from_resources`, and the firewall actions of `POST /servers` with `firewalls` | firewall (only for the firewall's own actions) | servers, firewall (from server create) |
| everything else (power, rebuild, change_type, change_protection, delete, change_ip_range, change_algorithm, change_type of the LB, set_rules ...) | all refs, as today | - |

### test/e2e client matrix (plan 13-01)

```go
// clientRow is one row of the client matrix, registered from init() of
// its group file so group plans never touch a shared file.
type clientRow struct {
	Req     string // "CLI-01" (hcloud), "CLI-02" (tofu), "CLI-03" (Ansible), "CLI-04" (example)
	Name    string // subtest name: resource type ("servers", "load_balancers") or client ("hcloud")
	Fixture bool   // runs on the client's fixture server, one row after another
	Slots   int    // server slots taken in one step before Run; 0 for Fixture rows
	Run     func(t *testing.T, c *rowCtx)
}

// rowCtx is what a row gets: the run's env (CLI-04 makes its own) and, for
// Fixture rows, the client's fixture server.
type rowCtx struct {
	E       *env
	Fixture *server // nil unless the row is a Fixture row
}

func registerRow(r clientRow)

// testClients runs the rows of req as subtests named r.Name: first the
// Fixture rows one after another (the fixture server is created lazily by
// the first one that runs, holding one slot, and deleted after the last),
// then the other rows in parallel, each taking r.Slots in one step. Every
// row is wrapped in logged.
func testClients(t *testing.T, req string)

// logged logs the start, the end and the elapsed time of a row.
func logged(f func(t *testing.T)) func(t *testing.T)

// newProjectEnv creates a project of its own (name <runID>-<suffix>) with a
// read/write token, an hcloud environment and the run's SSH key registered
// under the run's key name; the project is deleted at cleanup.
func newProjectEnv(t *testing.T, suffix string) *env

// hcloudOK runs hcloud with the env's endpoint and token and fails the
// test on a non-zero exit; hcloudJSON adds "-o json" and decodes stdout.
func (e *env) hcloudOK(t *testing.T, args ...string) result
func (e *env) hcloudJSON(t *testing.T, out any, args ...string)

// ansibleOK runs a playbook in WSL2 and fails unless it ends with failed=0.
func (e *env) ansibleOK(t *testing.T, w *wslEnv, name, playbook string) result

// assertGoneIDs fails if a list endpoint still lists one of ids.
func assertGoneIDs(t *testing.T, e *env, path, key string, ids ...int64)

// assertProjectEmpty fails unless every Cloud API list endpoint of e's
// project is empty and no Hyper-V VM of serverIDs exists.
func assertProjectEmpty(t *testing.T, e *env, serverIDs []int64)

// sshAddr runs remote as root at addr:port with the run's key, from the
// host; (w *wslEnv).sshAddr does the same from the WSL2 distro.
func (e *env) sshAddr(addr string, port int, timeout time.Duration, remote string) (result, error)
func (w *wslEnv) sshAddr(addr string, port int, timeout time.Duration, remote string) (result, error)
```

The existing `assertGone(t, e, path, key, id string)`, `hclMap`,
`newTofuDir`, `playbook`, `sharedTofu`, `sharedAnsible`, `takeServerSlots`
and `ownServer` stay as they are. Group files are
`test/e2e/clients_<group>_test.go` (one per plan, `_<client>` added where
a group is split per client).

## Plans

- [ ] 13-01 Shared client-matrix helpers
  - Wave: 1 | Depends on: - | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03, CLI-04 (harness only, no row of its own) | Tests: test/e2e/tofu_test.go, test/e2e/ansible_test.go (existing rows, registered anew)
  - Reads: CONTEXT.md "Rows and harness", "CLI-04", "Process"; SPEC "Machine prerequisites"; test/e2e/main_test.go (env, slots), clients_test.go, tofu_test.go, ansible_test.go, e2e_test.go
  - Size: ~320 lines in 5 files
  - Adjusts: TestE2E/CLI-02/ssh_keys, CLI-02/placement_groups, CLI-03/server_info (registered through `registerRow` instead of the `testCLI02`/`testCLI03` dispatchers; what they check stays)
  - [ ] `test/e2e/clients_matrix_test.go`: `clientRow`, `registerRow`, `testClients` (fixture rows first and sequential with a lazy fixture server of one slot, deleted after the last fixture row; then parallel rows with their slots in one step), `logged`, `newProjectEnv`, `hcloudOK`, `hcloudJSON`, `ansibleOK`, `assertGoneIDs`, `assertProjectEmpty` (all list endpoints of the Cloud API through `apiIDs`, VMs through `vmExists`), `sshAddr` for host and WSL2.
  - [ ] `test/e2e/e2e_test.go`, `tofu_test.go`, `ansible_test.go`: `CLI-01` to `CLI-04` run through `testClients`; the existing rows register themselves; `parallel` rows are wrapped in `logged`.
  - [ ] `Taskfile.yml`: `task e2e` with `-timeout 180m`.

- [x] 13-02 Action queue
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CLI-02 | Tests: internal/actions/actions_test.go
  - Reads: CONTEXT.md "Actions"; RESEARCH.md §4 "Does hlab have to finish actions in a certain order?"; internal/actions/actions.go; internal/api/deviations.go; docs/development/api-coverage.md (head and "Endpoints"); docs/deviations.md
  - Size: ~300 lines in 5 files
  - [x] `internal/actions/actions.go`: `StartQueued`; `Run` waits before it takes a worker until every running action with a lower ID sharing a ref has finished (woken by `finished`), so more queued actions than workers never deadlock.
  - [x] `internal/actions/actions_test.go`: the rows of the test map.
  - [x] `internal/api/deviations.go`, `docs/development/api-coverage.md`, `docs/deviations.md`: one deviation per queuing endpoint of the table above ("a second action on the same network/load balancer/server waits as running instead of answering 423 locked"); the user page says it in prose under the matching topics without routes (DOC-03 keeps holding).

- [ ] 13-03 Networks: queue and effect at success
  - Wave: 2 | Depends on: 13-02 | Review: alone
  - Requirements: CLI-02 | Tests: internal/resources/networks/networks_test.go
  - Reads: CONTEXT.md "Actions"; Interfaces "internal/actions"; internal/resources/networks/attach.go, actions.go
  - Size: ~220 lines in 3 files
  - Adjusts: tests in networks_test.go or attach_test.go that expect 423 `locked` for a queuing action (none found by grep at planning)
  - [ ] `attach.go`: server `attach_to_network`, `detach_from_network`, `change_alias_ips` and the LB attach path through `StartQueued` (refuse server or LB, queue network); the server's `private_net` and the network's `servers` change only when the work commits at `success`, checked against the state after the earlier actions.
  - [ ] `actions.go`: `add_subnet`, `delete_subnet`, `add_route`, `delete_route` queue on the network; subnets and routes appear on GET at `success`; `change_ip_range`, `change_protection` keep refusing.
  - [ ] `networks_test.go`: the rows of the test map.

- [x] 13-04 Load balancers: queue and effect at success
  - Wave: 2 | Depends on: 13-02 | Review: alone
  - Requirements: CLI-02 | Tests: internal/resources/loadbalancers/loadbalancers_test.go
  - Reads: CONTEXT.md "Actions"; Interfaces "internal/actions"; internal/resources/loadbalancers/actions.go, targets.go, services.go, network.go
  - Size: ~240 lines in 5 files
  - Adjusts: tests in loadbalancers_test.go, targets_test.go or services_test.go that expect 423 `locked` for a queuing action (none found by grep at planning)
  - [x] `actions.go` (shared start helper), `targets.go`, `services.go`: target and service actions through `StartQueued` (queue on the LB and the target servers); targets and services appear on GET and in the agent document at `success`; `add_target` with `use_private_ip` checks the committed network state; `change_type`, `change_algorithm`, public interface and protection keep refusing.
  - [x] `network.go`: LB `attach_to_network`/`detach_from_network` refuse the LB, queue the network; `private_net` at `success`.
  - [x] `loadbalancers_test.go`: the rows of the test map.

- [ ] 13-05 Volumes: queue and effect at success
  - Wave: 2 | Depends on: 13-02 | Review: alone
  - Requirements: CLI-02 | Tests: internal/resources/volumes/volumes_test.go
  - Reads: CONTEXT.md "Actions"; Interfaces "internal/actions"; internal/resources/volumes/attach.go, volumes.go (create with `server`, around lines 420 to 490)
  - Size: ~160 lines in 3 files
  - Adjusts: none (`wantLocked` checks change_protection of the volume and the server, which keep refusing)
  - [ ] `attach.go`, `volumes.go`: attach, detach and the attach of a create with `server` refuse the volume and queue on the server; `server` on the volume and `volumes` on the server change at `success`.
  - [ ] `volumes_test.go`: the rows of the test map.

- [ ] 13-06 Floating and primary IPs: queue and effect at success
  - Wave: 2 | Depends on: 13-02 | Review: alone
  - Requirements: CLI-02 | Tests: internal/resources/floatingips/floatingips_test.go, internal/resources/primaryips/primaryips_test.go
  - Reads: CONTEXT.md "Actions"; Interfaces "internal/actions"; internal/resources/floatingips/actions.go, internal/resources/primaryips/actions.go
  - Size: ~200 lines in 4 files
  - [ ] `floatingips/actions.go`, `primaryips/actions.go`: assign and unassign refuse the IP and queue on the server; `server`/`assignee_id` and the server's `public_net` change at `success`.
  - [ ] `floatingips_test.go`, `primaryips_test.go`: the rows of the test map.

- [ ] 13-07 Firewalls and server create: queue
  - Wave: 2 | Depends on: 13-02 | Review: alone
  - Requirements: CLI-02 | Tests: internal/resources/firewalls/firewalls_test.go, internal/resources/servers/servers_test.go
  - Reads: CONTEXT.md "Actions"; Interfaces "internal/actions"; internal/resources/firewalls/actions.go; internal/resources/servers/servers.go (create, around lines 700 to 830)
  - Size: ~200 lines in 4 files
  - [ ] `firewalls/actions.go`: `apply_to_resources`/`remove_from_resources` refuse the firewall and queue on the servers; the firewall actions of a server create queue on the firewall; `applied_to` changes at `success`.
  - [ ] `servers/servers.go`: the next actions of `POST /servers` with `networks`, `firewalls` and `volumes` queue on network, firewall and volume refs instead of refusing, so two servers created in parallel with the same network and firewall both succeed.
  - [ ] `firewalls_test.go`, `servers_test.go`: the rows of the test map.

- [ ] 13-08 Servers with hcloud
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-01 | Tests: test/e2e/clients_servers_hcloud_test.go
  - Reads: CONTEXT.md "Scope"; RESEARCH.md §1 (server); `hcloud server --help` and each subcommand's help; REQUIREMENTS SRV-11, SRV-13, SRV-14, SRV-15
  - Size: ~260 lines in 1 file
  - [ ] Row `CLI-01/servers` (own server, 1 slot): create with labels, ssh key, user data, placement group; list, describe, ip, update, labels; poweroff, poweron, reboot, reset, shutdown; reset-password; change-type (powered off, x86 type); rebuild; enable/disable-rescue; enable/disable-backup; create-image (snapshot, deleted afterwards); attach-iso, detach-iso; set-rdns; metrics; enable/disable-protection; add-to/remove-from-placement-group; delete; each step checked through the API.

- [ ] 13-09 Servers with OpenTofu
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-02 | Tests: test/e2e/clients_servers_tofu_test.go
  - Reads: CONTEXT.md "Scope"; RESEARCH.md §2 (resources, data sources, datacenter handling); provider docs of `hcloud_server`
  - Size: ~240 lines in 1 file
  - [ ] Row `CLI-02/servers` (own server, 1 slot): `hcloud_server` with labels, ssh keys, user data, placement group, `backups`, `rescue`, `iso`, protection; in-place updates of name, labels, server type (x86), `backups`, `iso`, protection; data sources `hcloud_server` and `hcloud_servers`; destroy and gone.

- [ ] 13-10 Servers with Ansible
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-03 | Tests: test/e2e/clients_servers_ansible_test.go, test/e2e/ansible_test.go
  - Reads: CONTEXT.md "Scope"; RESEARCH.md §3; `ansible-doc hetzner.hcloud.server`, `server_info`
  - Size: ~250 lines in 2 files
  - Adjusts: TestE2E/CLI-03/server_info (its checks move into CLI-03/servers; the row goes)
  - [ ] Row `CLI-03/servers` (own server, 1 slot): `server` present with labels, ssh key, user data, placement group, backups, protection; states stopped, started, restarted, rebuild, rescue mode on and off; server type change; ISO; `server_info` without filter, by name, by ID and by label selector; absent and gone.

- [ ] 13-11 Catalog types with all clients
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_catalog_test.go
  - Reads: CONTEXT.md "Scope" (exclusions, `all list`); RESEARCH.md §1, §2, §3; open item 4
  - Size: ~260 lines in 1 file
  - [ ] hcloud rows `server_types`, `locations`, `load_balancer_types`, `isos` (list and describe by name and ID, compared with the API) and `all` (`hcloud all list`; if it fails on storage boxes without a Hetzner endpoint, the row is left out and the test map entry moves to phase 21 with the reason).
  - [ ] tofu rows `server_types`, `locations`, `load_balancer_types` (both data sources each, compared with the API).
  - [ ] Ansible rows `server_types`, `locations`, `load_balancer_types`, `isos` (the `*_info` modules by name and without filter).

- [ ] 13-12 Images with all clients
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_images_test.go
  - Reads: CONTEXT.md "Scope"; RESEARCH.md §2 (`hcloud_snapshot`), §3 (`image_info`)
  - Size: ~230 lines in 1 file
  - [ ] Fixture rows `CLI-01/images`, `CLI-02/images`, `CLI-03/images`: hcloud `server create-image` of the fixture server, then `image list/describe/update/enable-protection/disable-protection/delete`; tofu `hcloud_snapshot` with labels, update, data sources `hcloud_image` and `hcloud_images` (system image by name, snapshot by ID and label), destroy; Ansible `image_info` for a system image by name and a snapshot made through the API by label; each removes its snapshot and checks it is gone.

- [ ] 13-13 SSH keys and placement groups with all clients
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_keys_test.go, test/e2e/tofu_test.go
  - Reads: RESEARCH.md §1, §2, §3
  - Size: ~260 lines in 2 files
  - [ ] hcloud rows `ssh_keys`, `placement_groups` (create, list, describe, update, labels, delete, gone).
  - [ ] Ansible rows `ssh_keys`, `placement_groups` (`ssh_key`, `ssh_key_info`, `placement_group` present, updated, absent).
  - [ ] `tofu_test.go`: rows `ssh_keys` and `placement_groups` also read `hcloud_ssh_key`/`hcloud_ssh_keys` and `hcloud_placement_group`/`hcloud_placement_groups`.

- [ ] 13-14 Networks with all clients
  - Wave: 3 | Depends on: 13-01, 13-03 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_networks_test.go
  - Reads: CONTEXT.md "Scope" (vswitch exclusions); RESEARCH.md §1, §2, §3; REQUIREMENTS NET-01
  - Size: ~350 lines in 1 file
  - [ ] Fixture rows `CLI-01/networks`: create, add-subnet, add-route, change-ip-range, update, labels, protection, server attach-to-network with alias IPs, change-alias-ips, list-members, detach, remove-route, remove-subnet, delete.
  - [ ] `CLI-02/networks`: `hcloud_network`, `hcloud_network_subnet` (two in parallel), `hcloud_network_route`, `hcloud_server_network` with alias IPs on the fixture server, updates, data sources `hcloud_network`, `hcloud_networks`, `hcloud_network_members`, destroy.
  - [ ] `CLI-03/networks`: `network`, `subnetwork`, `route`, `server_network`, `network_info`, `network_member_info`, absent.

- [ ] 13-15 Primary IPs with all clients
  - Wave: 3 | Depends on: 13-01, 13-06 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_primaryips_test.go
  - Reads: RESEARCH.md §1, §2, §3; REQUIREMENTS PIP-01
  - Size: ~280 lines in 1 file
  - [ ] Fixture rows `CLI-01/primary_ips`, `CLI-02/primary_ips`, `CLI-03/primary_ips`: create in a location, update, labels, auto_delete, protection, set-rdns, assign to the powered-off fixture server (an IPv6 primary IP, after unassigning the fixture's own IPv6 one) and unassign, restore the fixture, delete; tofu also reads `hcloud_primary_ip(s)`; Ansible `primary_ip` and `primary_ip_info`.

- [ ] 13-16 Floating IPs and reverse DNS with all clients
  - Wave: 3 | Depends on: 13-01, 13-06 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_floatingips_test.go
  - Reads: RESEARCH.md §1, §2 (`hcloud_floating_ip_assignment`, `hcloud_rdns`), §3 (`rdns`)
  - Size: ~300 lines in 1 file
  - [ ] Fixture rows `CLI-01/floating_ips`, `CLI-02/floating_ips`, `CLI-03/floating_ips`: create with home location, update, labels, protection, set-rdns, assign to and unassign from the fixture server, delete; tofu `hcloud_floating_ip`, `hcloud_floating_ip_assignment`, data sources `hcloud_floating_ip(s)`; Ansible `floating_ip`, `floating_ip_info`.
  - [ ] Fixture rows `CLI-02/rdns`, `CLI-03/rdns`: PTR of the fixture server, a floating IP and a primary IP set, changed and reset (tofu `hcloud_rdns`, Ansible `rdns`).

- [ ] 13-17 Volumes with all clients
  - Wave: 3 | Depends on: 13-01, 13-05 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_volumes_test.go
  - Reads: RESEARCH.md §1, §2, §3; REQUIREMENTS VOL-01
  - Size: ~260 lines in 1 file
  - [ ] Fixture rows `CLI-01/volumes`, `CLI-02/volumes`, `CLI-03/volumes`: create in a location and on the fixture server with automount and format, attach, detach, resize, update, labels, protection, delete; tofu `hcloud_volume`, `hcloud_volume_attachment`, data sources `hcloud_volume(s)`; Ansible `volume`, `volume_attachment`, `volume_info`.

- [ ] 13-18 Firewalls with all clients
  - Wave: 3 | Depends on: 13-01, 13-07 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_firewalls_test.go
  - Reads: RESEARCH.md §1, §2 (`hcloud_firewall_attachment`), §3 (`firewall_resource`); REQUIREMENTS FW-01
  - Size: ~280 lines in 1 file
  - [ ] Fixture rows `CLI-01/firewalls`, `CLI-02/firewalls`, `CLI-03/firewalls`: create with rules, add-rule, delete-rule, replace-rules, update, labels, apply to the fixture server and a label selector, remove, delete; tofu `hcloud_firewall`, `hcloud_firewall_attachment`, data sources `hcloud_firewall(s)`; Ansible `firewall`, `firewall_resource`, `firewall_info`. Rules always allow 22 inbound so the fixture stays reachable.

- [ ] 13-19 Load balancers with hcloud
  - Wave: 3 | Depends on: 13-01, 13-04 | Review: alone | e2e
  - Requirements: CLI-01 | Tests: test/e2e/clients_lb_hcloud_test.go
  - Reads: RESEARCH.md §1 (load-balancer); REQUIREMENTS LB-01, LB-06
  - Size: ~220 lines in 1 file
  - [ ] Fixture row `CLI-01/load_balancers`: create, attach-to-network (network of the row), add-service (tcp and http), update-service, add-target (the fixture server with `--use-private-ip` after attaching it, a label selector, an IP), remove-target, delete-service, change-algorithm, change-type, disable/enable-public-interface, set-rdns, metrics, update, labels, protection, detach-from-network, delete.

- [ ] 13-20 Load balancers with OpenTofu
  - Wave: 3 | Depends on: 13-01, 13-04 | Review: alone | e2e
  - Requirements: CLI-02 | Tests: test/e2e/clients_lb_tofu_test.go
  - Reads: RESEARCH.md §2 (load balancer resources, call order), §4
  - Size: ~220 lines in 1 file
  - [ ] Fixture row `CLI-02/load_balancers`: `hcloud_load_balancer`, `hcloud_load_balancer_network`, two `hcloud_load_balancer_service` and two `hcloud_load_balancer_target` (server with private IP, label selector) created in parallel, updates of type, algorithm and a service, data sources `hcloud_load_balancer(s)`, `hcloud_load_balancer_type(s)`, destroy.

- [ ] 13-21 Load balancers with Ansible
  - Wave: 3 | Depends on: 13-01, 13-04 | Review: alone | e2e
  - Requirements: CLI-03 | Tests: test/e2e/clients_lb_ansible_test.go
  - Reads: RESEARCH.md §3; `ansible-doc` of the load balancer modules
  - Size: ~200 lines in 1 file
  - [ ] Fixture row `CLI-03/load_balancers`: `load_balancer`, `load_balancer_network`, `load_balancer_service`, `load_balancer_target` (server with private IP, label selector, IP), `load_balancer_info`, updates, absent.

- [ ] 13-22 Certificates with all clients
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_certificates_test.go
  - Reads: CONTEXT.md "Rows and harness" (certificates); RESEARCH.md §1, §2, §3; REQUIREMENTS CERT-01 to CERT-03
  - Size: ~300 lines in 1 file
  - [ ] Rows `CLI-01/certificates`, `CLI-02/certificates`, `CLI-03/certificates`: an uploaded certificate (self-signed, made in Go in the row's temp folder) and a managed certificate for a name in a zone `<runID>-<client>-cert.test` the same client creates; describe, list, update, labels, `retry` (hcloud), delete; tofu `hcloud_uploaded_certificate`, `hcloud_managed_certificate`, data sources `hcloud_certificate(s)`; Ansible `certificate`, `certificate_info`; zone and certificates gone at the end.

- [ ] 13-23 Zones with all clients
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-01, CLI-02, CLI-03 | Tests: test/e2e/clients_zones_test.go
  - Reads: RESEARCH.md §1 (zone, zone rrset), §2 (`hcloud_zone`, `hcloud_zone_rrset`, `hcloud_zone_record`), §3; REQUIREMENTS DNS-01
  - Size: ~380 lines in 1 file
  - [ ] Rows `CLI-01/zones`: zone create, change-ttl, protection, add/set/remove-records, import-zonefile, export-zonefile, rrset create, change-ttl, add/set/remove-records, labels, protection, delete; `CLI-02/zones`: `hcloud_zone`, `hcloud_zone_rrset`, `hcloud_zone_record`, updates, data sources `hcloud_zone(s)`, `hcloud_zone_rrset(s)`; `CLI-03/zones`: `zone`, `zone_info`, `zone_rrset`, `zone_rrset_info`; zone names `<runID>-<client>.test`.

- [ ] 13-24 Ansible inventory
  - Wave: 2 | Depends on: 13-01 | Review: alone | e2e
  - Requirements: CLI-03 | Tests: test/e2e/clients_inventory_test.go
  - Reads: CONTEXT.md "Rows and harness" (inventory); RESEARCH.md §3 (inventory plugin); `ansible-doc -t inventory hetzner.hcloud.hcloud`
  - Size: ~120 lines in 1 file
  - [ ] Fixture row `CLI-03/inventory`: `ansible-inventory --list` with a `<name>.hcloud.yml` (`plugin: hetzner.hcloud.hcloud`, `keyed_groups` on `hcloud_labels`) in WSL2; the fixture server is a host with `ansible_host` and `hcloud_ipv4` equal to its public IPv4 and is in the group of its label.

- [ ] 13-25 Example environment with hcloud
  - Wave: 3 | Depends on: 13-01, 13-03, 13-04, 13-05, 13-06, 13-07 | Review: alone | e2e
  - Requirements: CLI-04 | Tests: test/e2e/clients_example_hcloud_test.go
  - Reads: CONTEXT.md "CLI-04"; IDEA.md "Success criterion"; RESEARCH.md §4 (hcloud)
  - Size: ~220 lines in 1 file
  - [ ] Row `CLI-04/hcloud` (2 slots, own project): network with subnet, firewall allowing 22, two cx23 Ubuntu servers in the network with the firewall, LB with both as private targets and a TCP service on a free listen port to 22, volume and floating IP on server 1, zone `<runID>-hcloud.test` with an A record per server; SSH from host and WSL2 to both public IPv4s and once through the LB's service; then every resource deleted with hcloud and `assertProjectEmpty`.

- [ ] 13-26 Example environment with OpenTofu
  - Wave: 3 | Depends on: 13-01, 13-03, 13-04, 13-05, 13-06, 13-07 | Review: alone | e2e
  - Requirements: CLI-04 | Tests: test/e2e/clients_example_tofu_test.go
  - Reads: CONTEXT.md "CLI-04"; RESEARCH.md §4 (OpenTofu call order, parallelism)
  - Size: ~220 lines in 1 file
  - [ ] Row `CLI-04/tofu` (2 slots, own project): the same environment as one `main.tf` with default parallelism, `apply`, the same SSH checks, `destroy` and `assertProjectEmpty`.

- [ ] 13-27 Example environment with Ansible
  - Wave: 3 | Depends on: 13-01, 13-03, 13-04, 13-05, 13-06, 13-07 | Review: alone | e2e
  - Requirements: CLI-04 | Tests: test/e2e/clients_example_ansible_test.go
  - Reads: CONTEXT.md "CLI-04"; RESEARCH.md §4 (Ansible)
  - Size: ~240 lines in 1 file
  - [ ] Row `CLI-04/ansible` (2 slots, own project): the same environment from a create playbook in WSL2, the same SSH checks from host and WSL2, a delete playbook and `assertProjectEmpty`.

- [ ] 13-28 Documentation
  - Wave: 4 | Depends on: all other plans | Review: alone
  - Requirements: DOC-01 to DOC-06 (held), DOC-07 (manual) | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation", "Accounts and secrets" (hlab-docs); DECISIONS entries of this phase
  - Size: ~150 lines in 4 files
  - [ ] `docs/development/build-and-test.md`: the client matrix (rows `CLI-0n/<type>`, fixture rows, filters such as `task e2e -- -run 'TestE2E/CLI-02/volumes'`, run time and the timeout the phase close set), the exclusions.
  - [ ] `README.md` and `docs/guide.md`: client settings and the example environment only where something the phase built differs from what they say; `docs/deviations.md` and `api-coverage.md` checked against the deviations the phase added.

## Run-time estimate

Based on 3 server slots, about 4 minutes to create a ready server from the
cached image and about 1 minute to delete one:

- Servers rows: three rows of about 25 minutes each with 1 slot (power
  cycles, rebuild, change type, rescue, snapshot, ISO).
- Fixture phases: per client about 7 to 9 rows of 3 to 4 minutes on one
  fixture server, so about 30 minutes with 1 slot; the three clients run in
  parallel.
- CLI-04: three runs of about 15 minutes with 2 slots each, one after
  another as slots free up.
- Rows without servers (catalog, keys, certificates, zones): about 2 to 5
  minutes each, no slots, in parallel.

That is about 255 slot-minutes for the new rows, about 85 minutes on 3
slots, on top of the existing rows (under 60 minutes now). So the client
rows alone take about 90 minutes, and a full `task e2e` takes about 2 to
2.5 hours, within the 180 minutes set by 13-01. The phase close sets the
timeout to about twice the measured run.
