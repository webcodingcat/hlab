# Phase 13 context

Confirmed assumptions and answers from the start of the phase, one line each.

## Scope

- Cloud API resource types: servers, server types, images (snapshots, backups), ISOs, locations, SSH keys, placement groups, volumes, networks (subnets, routes, server attachment), primary IPs, floating IPs, firewalls, load balancers and LB types, certificates (uploaded, managed), DNS zones with RRSets, reverse DNS, pricing where a client reads it; storage boxes belong to phase 21.
- Excluded in all clients: hcloud `datacenter`, tofu `hcloud_datacenter(s)`, Ansible `datacenter_info` (410 like Hetzner), `server request-console`, vswitch subnets and `network expose-routes-to-vswitch`, the tofu `datacenter` arguments (removed in provider 1.67.0), storage boxes; the test map header names them.
- `hcloud all list` runs in CLI-01 only if it works with the Cloud API endpoint alone; otherwise it moves to the CLI-05 row of phase 21 and the test map says so.
- A row runs every write command, resource or module the client offers for the type once, plus every read command or data source; each OpenTofu row also reads its type's data sources; `tofu import` is not used.
- Each client's servers row also covers rescue, enabling backups, creating a snapshot, attaching an ISO, `metrics` and protection; servers use x86 types only (CAX gives resource_unavailable).
- A row checks that the client did its job by reading back through the API; it does not repeat behaviour the SRV, NET, FW, LB, DNS and FIP rows prove.

## Rows and harness

- One subtest per client and type: `CLI-01/<type>`, `CLI-02/<type>`, `CLI-03/<type>`, plus `CLI-03/inventory`; all Ansible modules of one type share one row (`CLI-03/server_info` merges into `CLI-03/servers`, under `Adjusts:`).
- Clients are set up as today: hcloud and tofu on the host with only `HCLOUD_ENDPOINT`/`HCLOUD_TOKEN`, Ansible in WSL2 via `WSLENV`; resources are named `<runID>-<client>-<type>`; zone names are valid domains such as `<runID>-<client>.test`.
- Each row deletes its resources with the same client and checks through the API that they are gone.
- The server slot pool stays at 3; a row takes all its slots in one step and never holds a slot while waiting for another.
- Each client's fixture server for attach rows holds one slot only while that client's attach rows run, one after another on it, and is deleted afterwards.
- The plan estimates the run time from this (three CLI-04 runs in sequence, three servers rows, three fixture phases).
- Uploaded certificates are generated self-signed in Go; managed certificates use an hlab DNS zone the same row creates, issued by the hlab CA.
- Ansible inventory row: a `*.hcloud.yml` with `plugin: hetzner.hcloud.hcloud`, `keyed_groups` on `hcloud_labels`, endpoint and token from the environment; it checks `ansible_host`, `hcloud_ipv4` and the label group of the fixture server.
- Each group plan first checks locally `hcloud <type> --help`, `ansible-doc -l hetzner.hcloud` and `ansible-doc hetzner.hcloud.<module>` in WSL2; flags and modules come from that output, not from RESEARCH.md.

## CLI-04

- One subtest per client (`CLI-04/hcloud`, `CLI-04/tofu`, `CLI-04/ansible`), each in its own hlab project with its own token, each taking 2 slots in one step (so they run one after another).
- Environment: two cx23 Ubuntu servers in one private network, an LB with both as private targets and a TCP service, a firewall allowing 22 inbound on both, a volume and a floating IP on server 1, a zone under `.test` with an A record per server; hcloud as a command sequence, OpenTofu as one `main.tf`, Ansible as a create and a delete playbook.
- Reachability: SSH from host and from WSL2 to the public IPv4 of both servers, plus one SSH connection through the LB's TCP service; nobody connects by DNS name.
- "Deleted completely": every list endpoint of the project is empty and no Hyper-V VM of its server IDs remains; `<tag>-appliance` and `<tag>-storage` are not checked.

## Actions (own plan in internal/actions, after the shared helpers)

- hlab keeps Hetzner's error codes; `locked` is never replaced by `conflict`.
- `locked` (423) stays for state changes of the action's own resource while a running or queued action holds it (poweroff, reboot, rebuild, change_type, delete, attach and detach of the resource itself and similar).
- An action that holds only a secondary ref already held by a running or queued action (network for attach_to_network, LB for add_target, server for volume attach and floating IP assign, servers of apply_to_resources) is created `running` and queues behind every action holding a shared ref, in order of creation.
- Actions that add or remove an element of a resource also queue on their own resource instead of `locked`: add_target, remove_target, add_service, update_service, delete_service on the LB; add_subnet, delete_subnet, add_route, delete_route on the network (OpenTofu creates siblings in parallel and does not retry on `locked`).
- The list of queuing actions goes into DECISIONS.md with its cost of change, and as a deviation into deviations.md and api.Deviations(); existing tests that fix `locked` for these actions are listed under `Adjusts:`.
- The same plan checks that each action's effect appears on GET only at `success` (e.g. private_net after attach_to_network, add_target against the committed state) and repairs it where not.

## Process

- Each product bug a client exposes first gets a failing unit test in its own module and is fixed there; a bug outside the plan goes to a fresh implementer.
- The shared helpers plan sets `task e2e` to `-timeout 180m` and makes each row log its start, end and elapsed time; after the first full run the phase close sets the Taskfile to about twice the measured time, and the documentation only takes that value.
- Plan order: shared test helpers, internal/actions, one plan per resource group, documentation; a group above 400 lines across three clients is split per client.
