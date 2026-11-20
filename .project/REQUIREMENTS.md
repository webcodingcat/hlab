# REQUIREMENTS: hlab

One line per requirement: one checkable capability, its goal in IDEA.md, its
test level. IDs are a category prefix and a number; they never change and
are not reused.

Test levels: `unit` (checked in code), `e2e` (against the real environment
or a browser), `none` (wiring or configuration the tests around it cover),
`manual` (only what no test can judge; the user checks it).

"Client" means the unmodified hcloud CLI, OpenTofu provider
hetznercloud/hcloud and Ansible collection hetzner.hcloud in the versions
pinned in SPEC.md. "Hetzner behaviour" means the behaviour described in the
frozen OpenAPI description and Hetzner's API documentation.

## Milestone 1

### API core (`API`)

Behaviour all resource types share; tested once, table-driven over the
resource types.

- API-01 clients reach the API at `http://api.hetzner.internal/v1` from the host, from WSL2 and from servers (goal 1) [e2e]
- API-02 a request without a valid token gets `401 unauthorized`; a valid token selects its project (goal 1) [unit]
- API-03 a read-only token can read everything, every write returns `403 token_readonly` (goal 1) [unit]
- API-04 resources of one project are invisible and inaccessible to tokens of another project (goal 1) [unit]
- API-05 every list endpoint paginates with `page`, `per_page` and `meta.pagination` like Hetzner (goal 1) [unit]
- API-06 every list endpoint filters by `name` and `label_selector` (all Hetzner selector operators) and sorts by the documented `sort` keys (goal 1) [unit]
- API-07 every resource with labels accepts labels on create and update and rejects invalid keys or values with `invalid_input` (goal 1) [unit]
- API-08 every asynchronous operation returns an action whose status and progress move from `running` to `success` or `error`, readable at `/actions/{id}` and the resource's action endpoints (goal 1) [unit]
- API-09 errors use Hetzner's error object and codes, with `details.fields` for `invalid_input` (goal 1) [unit]
- API-10 every response of every endpoint validates against the frozen Hetzner OpenAPI description (goal 1) [unit]
- API-11 a name that Hetzner requires to be unique per project returns `uniqueness_error` when reused (goal 1) [unit]
- API-12 a resource with delete (or rebuild) protection refuses that operation with `protected` until protection is removed (goal 1) [unit]
- API-13 a second conflicting action on a resource with a running action returns `locked` (goal 1) [unit]
- API-14 a request to an unknown path or with malformed JSON returns `not_found` or `json_error` (goal 1) [unit]

### Projects and tokens (`PROJ`)

- PROJ-01 user creates, lists and deletes projects with the `hlab` management command (goal 1) [unit]
- PROJ-02 user creates a token of exactly 64 characters with permission read or read/write; it is shown only once (goal 1) [unit]
- PROJ-03 user lists and revokes tokens; a revoked token gets `401 unauthorized` (goal 1) [unit]
- PROJ-04 deleting a project deletes all of its resources including VMs and disks (goal 4) [unit]
- PROJ-05 an interactive user without administrator rights runs the management command without an admin prompt (goal 3) [e2e]

### Catalog (`CAT`)

- CAT-01 the API lists the six Hetzner locations fsn1, nbg1, hel1, ash, hil and sin with their network zones; datacenter fields and endpoints behave as the frozen OpenAPI description says (`/datacenters` is gone at Hetzner since 2026-10-01) (goal 1) [unit]
- CAT-02 server types come from the shipped catalog file plus the user's own types file, and appear in `/server_types` with their per-location availability (goal 1) [unit]
- CAT-03 user updates the server type catalog from the real Hetzner API with a real Hetzner token they enter (hidden prompt, or an environment variable named with `--hetzner-token-env`, or the token field in the tray); hlab never stores the token and needs no hcloud for it (goal 1) [unit]
- CAT-04 creating a server of an ARM type (CAX) returns `resource_unavailable` (goal 1) [unit]
- CAT-05 `/pricing` returns prices for every server type, load balancer type, volume, primary IP and floating IP from the catalog (goal 1) [unit]
- CAT-06 system images ubuntu-22.04, ubuntu-24.04, ubuntu-26.04, debian-12 and debian-13 are listed under Hetzner's names and attributes (goal 1) [unit]
- CAT-07 an image is downloaded from its official source on first use, verified by checksum, converted once and reused from the cache (goal 2) [unit]
- CAT-08 `/isos` lists the ISO catalog; an ISO is downloaded on first attach, verified by checksum and cached (goal 2) [unit]

### Servers (`SRV`)

- SRV-01 a server created from a system image boots with its name as hostname, the given SSH keys and the given user_data executed by cloud-init (goal 2) [e2e]
- SRV-02 a server created without SSH keys returns a root password that logs in via SSH (goal 2) [e2e]
- SRV-03 a server gets a public IPv4 as /32 with gateway 172.31.1.1 and is reachable via SSH from the host (goal 2) [e2e]
- SRV-04 a server gets a formal IPv6 /64 in the API that carries no traffic (goal 1) [unit]
- SRV-05 a server reaches the internet outbound (goal 2) [e2e]
- SRV-06 a server is not reachable from the LAN or the internet: hlab creates no inbound NAT, port mapping or route that exposes the public range beyond the host (goal 2) [unit]
- SRV-07 power off, power on, shutdown, reboot and reset change the VM's real state and the server status (goal 2) [e2e]
- SRV-08 rebuild replaces the disk with the given image and keeps ID, IPs and networks (goal 2) [e2e]
- SRV-09 change type of a stopped server changes vCPUs and RAM, and grows the disk if `upgrade_disk` is set; a running server is refused (goal 2) [e2e]
- SRV-10 reset root password sets a new root password on a running server that works for SSH login (goal 2) [e2e]
- SRV-11 enable rescue boots a Linux rescue system that does not run from the server disk, with the given SSH keys and the returned root password; the server disk stays `/dev/sda`; after disable and reboot the installed system runs (goal 2) [e2e]
- SRV-12 create image (snapshot) stores the server's disk as an image; a server created from it boots with the same content (goal 2) [e2e]
- SRV-13 a server with backups enabled gets one backup per day while the cloud runs; the oldest beyond 7 is deleted (goal 2) [unit]
- SRV-14 create image with type backup creates a manual backup of a server with backups enabled (goal 2) [unit]
- SRV-15 attach ISO inserts the ISO into the server's virtual DVD drive and the server boots from it after reset; detach ISO removes it (goal 2) [e2e]
- SRV-16 deleting a server removes its VM and disk and releases its IPs, except primary IPs with `auto_delete` false (goal 4) [e2e]
- SRV-17 request console returns `unsupported_error` with the message documented in SPEC.md (goal 1) [unit]
- SRV-18 a VM always gets the full RAM of its type; a server that would exceed RAM or disk minus the Windows reserve, or a type with more vCPUs than the host's logical cores, returns `resource_unavailable` (goal 2) [unit]
- SRV-19 server disks are dynamic (thin-provisioned) VHDX files in the configured data folder (goal 2) [e2e]
- SRV-20 the server status follows Hetzner's states (initializing, starting, running, stopping, off, rebuilding, migrating, deleting) for every action (goal 1) [unit]
- SRV-21 `/servers/{id}/metrics` returns cpu, disk and network time series measured on the VM (goal 2) [e2e]
- SRV-22 placement groups of type spread hold at most 10 servers and list their servers (goal 1) [unit]

### SSH keys (`KEY`)

- KEY-01 user creates, lists, updates and deletes SSH keys; the fingerprint is computed like Hetzner and a duplicate key returns `uniqueness_error` (goal 1) [unit]

### Metadata service (`META`)

- META-01 a server reads its Hetzner metadata (hostname, instance-id, public-ipv4, private-networks, region, availability-zone, public-keys, userdata) at `http://169.254.169.254/hetzner/v1/metadata` (goal 2) [e2e]

### Private networks (`NET`)

- NET-01 user creates networks with IP range, subnets and routes, adds and deletes subnets and routes, and changes the IP range, validated like Hetzner (goal 1) [unit]
- NET-02 servers attached to the same network get their private IP and reach each other on it; servers in different networks do not (goal 2) [e2e]
- NET-03 alias IPs of a server are reachable from other servers in the network (goal 2) [e2e]
- NET-04 traffic to a network route's destination is delivered to the route's gateway server (goal 2) [e2e]
- NET-05 Hetzner's location rules hold: a subnet belongs to a network zone, a server attaches only to a subnet of its location's zone (goal 1) [unit]
- NET-06 a subnet of type vswitch returns `invalid_input` with the message documented in SPEC.md (goal 1) [unit]

### Primary IPs (`PIP`)

- PIP-01 user creates, assigns, unassigns and deletes primary IPv4 and IPv6 addresses following Hetzner's rules (datacenter, server powered off, `auto_delete`) (goal 1) [unit]
- PIP-02 a primary IPv4 reassigned to another server reaches that server (goal 2) [e2e]

### Floating IPs (`FIP`)

- FIP-01 user creates, assigns, unassigns and deletes floating IPs with a home location following Hetzner's rules (goal 1) [unit]
- FIP-02 traffic to a floating IP reaches the assigned server once the guest has configured the address, and reaches the new server after reassignment without a change on the old one (goal 2) [e2e]

### Reverse DNS (`RDNS`)

- RDNS-01 change DNS PTR sets and resets the PTR record of servers, primary IPs and floating IPs, with Hetzner's default when unset (goal 1) [unit]
- RDNS-02 a PTR record resolves from servers, the host and WSL2 (goal 2) [e2e]

### Firewalls (`FW`)

- FW-01 user creates firewalls with rules (direction, protocol, ports, port ranges, source and destination IPs), sets rules, and applies them to servers and label selectors, validated like Hetzner (goal 1) [unit]
- FW-02 an applied firewall lets only allowed inbound traffic reach a server's public interface; a server without firewall accepts all (goal 2) [e2e]
- FW-03 outbound rules of an applied firewall restrict outbound traffic of the server (goal 2) [e2e]
- FW-04 firewalls do not filter traffic in private networks, like Hetzner (goal 2) [unit]
- FW-05 a server that gets a label matching an applied label selector is filtered by that firewall (goal 1) [unit]

### Load balancers (`LB`)

- LB-01 user creates load balancers with type, location, algorithm and network, changes type and algorithm, and enables or disables the public interface, validated like Hetzner (goal 1) [unit]
- LB-02 a TCP service distributes connections across its healthy targets by the chosen algorithm (goal 2) [e2e]
- LB-03 an HTTP service forwards requests, sets sticky session cookies when enabled and redirects HTTP to HTTPS when enabled (goal 2) [e2e]
- LB-04 an HTTPS service terminates TLS with its certificates (goal 2) [e2e]
- LB-05 a failing target is marked unhealthy by the health check, gets no traffic and is reported in `health_status` (goal 2) [e2e]
- LB-06 targets can be servers, label selectors and IPs, over the public or the private network (goal 1) [unit]
- LB-07 the proxy protocol option sends a PROXY header to the targets (goal 2) [unit]
- LB-08 `/load_balancers/{id}/metrics` returns open connections, connections and requests per second and bandwidth (goal 2) [unit]
- LB-09 change DNS PTR sets and resets the PTR record of a load balancer's IPs, and it resolves like other PTR records (goal 1) [unit]

### Certificates (`CERT`)

- CERT-01 an uploaded certificate is stored with its chain and key; fingerprint, domain names and validity are read from it (goal 1) [unit]
- CERT-02 a managed certificate is issued by the hlab CA when all its domains lie in hlab DNS zones, fails with an error otherwise, and retry issues it again (goal 1) [unit]
- CERT-03 managed certificates are renewed before they expire (goal 2) [unit]
- CERT-04 the trust switch adds the hlab CA to the Windows trust store and to every WSL2 distro, and removes it again (goal 2) [e2e]
- CERT-05 `GET /mgmt/v1/ca.pem` and `hlab ca export <file>` return the hlab CA certificate (goal 2) [unit]

### Volumes (`VOL`)

- VOL-01 user creates, resizes and deletes volumes of 10 to 10240 GB in a location, validated like Hetzner (goal 1) [unit]
- VOL-02 an attached volume appears in the server at `/dev/disk/by-id/scsi-0HC_Volume_<id>` and keeps its data across detach and attach (goal 2) [e2e]
- VOL-03 a volume created with `format` and `automount` is formatted and mounted at `/mnt/HC_Volume_<id>` (goal 2) [e2e]
- VOL-04 resizing a volume grows the disk the server sees (goal 2) [e2e]
- VOL-05 a volume attaches only to one server at a time in its own location (goal 1) [unit]

### DNS zones (`DNS`)

- DNS-01 user creates, updates and deletes zones and RRSets of all record types Hetzner supports, with TTLs and zone file import and export, validated like Hetzner; zone names may also lie under the private-use suffixes `.internal`, `.test` and `.home.arpa` (e.g. `lab.internal`), except `hetzner.internal` and its subdomains, while `.local` stays refused (goal 1) [unit]
- DNS-02 records of hlab zones resolve from servers, the host and WSL2 while the cloud runs (goal 2) [e2e]
- DNS-03 every zone has exactly one NRPT rule on the host while the cloud runs; deleting the zone or stopping the cloud removes it (goal 4) [e2e]
- DNS-04 `/mgmt/v1/status` lists every zone that shadows a domain resolving publicly, so tray and `hlab cloud status` can warn (goal 2) [unit]
- DNS-05 names outside hlab zones resolve normally for servers (goal 2) [none]

### Host integration (`HOST`)

- HOST-01 all guest traffic, east-west included, passes the appliance; a guest cannot reach another guest directly even with a hand-made route (goal 2) [e2e]
- HOST-02 the public IPv4 range is configurable; the cloud refuses to start and names the colliding interface or route if the range overlaps a host network (goal 2) [unit]
- HOST-03 from WSL2 in NAT mode with dnsTunneling, servers are reachable via SSH (goal 2) [e2e]
- HOST-04 hlab detects WSL2 in mirrored mode or with dnsTunneling off and reports it as not supported, without changing `.wslconfig` or a distro (goal 2) [unit]

### Cloud lifecycle (`LIFE`)

- LIFE-01 an interactive user without administrator rights starts and stops the cloud without an admin prompt (goal 3) [e2e]
- LIFE-02 with stop behaviour "save", stop saves the state of running VMs and start resumes them (goal 3) [e2e]
- LIFE-03 with stop behaviour "shutdown", stop shuts servers down cleanly and start powers on the servers that were running (goal 3) [e2e]
- LIFE-04 after stop and start all resources still work: servers, IPs, networks, firewalls, load balancers, volumes and DNS (goal 3) [e2e]
- LIFE-05 a Windows shutdown with a running cloud follows the stop behaviour; after the reboot the cloud is stopped (goal 3) [manual]
- LIFE-06 the cloud never starts with Windows (goal 3) [none]
- LIFE-07 stopping the cloud removes the host routes, forwarding settings and NRPT rules it added; starting adds them again (goal 4) [e2e]
- LIFE-08 the state carries a version; an incompatible state is discarded after a warning the user confirms (goal 3) [unit]
- LIFE-09 the service writes a log with rotation that `hlab logs` shows and follows (goal 3) [unit]
- LIFE-10 the cloud of one profile (`release` or `dev`) refuses to start while the cloud of the other profile runs, naming it (goal 3) [unit]

### Tray program (`UI`)

- UI-01 the tray icon shows the cloud status and starts and stops the cloud (goal 3) [e2e]
- UI-02 the console window lists, shows, creates, configures and deletes every resource type of the API in the active project (goal 3) [e2e]
- UI-03 the settings view changes every setting of the settings file (goal 3) [e2e]
- UI-04 the server types view shows the catalog, adds, edits and removes own types and starts the catalog update (goal 3) [e2e]
- UI-05 the projects view creates and deletes projects and tokens (goal 3) [e2e]
- UI-06 the logs view shows the service log and follows it (goal 3) [e2e]
- UI-07 every view opens its help text inside the program (goal 3) [unit]
- UI-08 the console looks and behaves as UI-SPEC.md describes (goal 3) [manual]
- UI-09 the example environment of the success criterion can be created and deleted by hand in the console (goal 3) [manual]

### Installer (`INST`)

These run in the elevated end-to-end suite (see SPEC.md, "Machine
prerequisites").

- INST-01 the installer EXE installs hlab and WebView2 if it is missing (goal 3) [e2e]
- INST-02 the installer stops and names exactly what is missing when Hyper-V or a required Windows feature is not available (goal 3) [unit]
- INST-03 the installer creates start menu and desktop shortcuts that open only the tray program (goal 3) [none]
- INST-04 uninstalling removes all VMs, virtual switches, host routes, forwarding settings, NRPT rules, the service, the CA and its trust entries (goal 4) [e2e]
- INST-05 uninstalling asks whether to keep user data; with the default answer no file of hlab remains (goal 4) [e2e]
- INST-06 installing a newer version over an older one keeps settings and state (goal 3) [e2e]

### Client compatibility (`CLI`)

One requirement per client; the test map has one row per resource type.

- CLI-01 the hcloud CLI manages every resource type it supports against hlab with only the endpoint changed (goal 1) [e2e]
- CLI-02 the OpenTofu provider manages every resource and reads every data source against hlab with only the endpoint changed (goal 1) [e2e]
- CLI-03 the Ansible collection runs every module and the inventory plugin against hlab from WSL2 with only the endpoint changed (goal 1) [e2e]
- CLI-04 each client builds the example environment of the success criterion, it is reachable via SSH from host and WSL2, and the client deletes it completely (goal 1) [e2e]

### Documentation (`DOC`)

They hold at the close of every phase, measured against what is built so
far, and belong to no single phase. They serve the "Documentation" section
of IDEA.md rather than a numbered goal.

- DOC-01 every command and flag of `hlab` is described in `docs/commands.md` [unit]
- DOC-02 every key of every configuration file is described in `docs/configuration.md` [unit]
- DOC-03 every API endpoint hlab serves differently from Hetzner, and every `unsupported_error`, is listed in `docs/development/api-coverage.md`, and `docs/deviations.md` describes the deviations for users by topic without routes [unit]
- DOC-04 every link in README.md and docs/ resolves [unit]
- DOC-05 every view of the tray has a help text and every help text belongs to a view, with the app taking its view list from `views.json` [unit]; that each help text names only views and buttons that exist is checked by hand with DOC-07
- DOC-06 every command in the developer guide exists in the Taskfile [unit]
- DOC-07 README and guides are understandable for their audience, and the help texts name only views and buttons that exist [manual]

## Later

What the target vision in IDEA.md will need, not built yet.

- FAULT-01 inject server failure, network disruption, latency between locations, API errors, rate limits and storage errors
- LINUX-01 run hlab with the tray on Linux with KVM/libvirt
- SBOX-01 Storage Box API at its own `.internal` base address
- CON-01 request console returns a working wss VNC console
- HOST-05 WSL2 in mirrored networking mode
- CAT-09 further system images (Rocky, Alma, Fedora, CentOS Stream, openSUSE) and app images

## Out of scope

| What | Why |
|------|-----|
| Billing | Non-goal in IDEA.md |
| Security for multi-user or production use | Non-goal in IDEA.md; tokens are fake values, the API is plain HTTP |
| Hetzner's performance, outages and rate limits | Non-goal in IDEA.md; only deliberate fault injection later |
| Running ARM (CAX) servers | Non-goal in IDEA.md; the types are listed but return `resource_unavailable` |
| Shrinking a server's RAM below its type | Non-goal in IDEA.md |
| Server console in v1 | `decisions/server-console.md`; returns `unsupported_error` |
| vSwitch subnets (Robot coupling) | No dedicated servers exist in hlab; returns `invalid_input` |
| Real IPv6 traffic | Possible later in IDEA.md; addresses are formal only |
| Hetzner's private ISOs | Not downloadable; a public ISO catalog replaces them |

## Coverage

| Requirement | Phase | Done |
|-------------|-------|------|
| API-01 | 01 | x |
| API-02 | 01 | x |
| API-03 | 02 | x |
| API-04 | 02 | x |
| API-05 | 02 | x |
| API-06 | 02 | x |
| API-07 | 02 | x |
| API-08 | 02 | x |
| API-09 | 02 | x |
| API-10 | 01 | x |
| API-11 | 02 | x |
| API-12 | 02 | x |
| API-13 | 02 | x |
| API-14 | 02 | x |
| PROJ-01 | 01 | x |
| PROJ-02 | 01 | x |
| PROJ-03 | 02 | x |
| PROJ-04 | 02 | x |
| PROJ-05 | 01 | x |
| CAT-01 | 01 | x |
| CAT-02 | 02 | x |
| CAT-03 | 02 | x |
| CAT-04 | 02 | x |
| CAT-05 | 02 | x |
| CAT-06 | 01 | x |
| CAT-07 | 01 | x |
| CAT-08 | 04 | x |
| SRV-01 | 01 | x |
| SRV-02 | 03 | x |
| SRV-03 | 01 | x |
| SRV-04 | 01 | x |
| SRV-05 | 01 | x |
| SRV-06 | 01 | x |
| SRV-07 | 03 | x |
| SRV-08 | 03 | x |
| SRV-09 | 03 | x |
| SRV-10 | 03 | x |
| SRV-11 | 04 | x |
| SRV-12 | 04 | x |
| SRV-13 | 04 | x |
| SRV-14 | 04 | x |
| SRV-15 | 04 | x |
| SRV-16 | 01 | x |
| SRV-17 | 02 | x |
| SRV-18 | 03 | x |
| SRV-19 | 01 | x |
| SRV-20 | 03 | x |
| SRV-21 | 03 | x |
| SRV-22 | 02 | x |
| KEY-01 | 02 | x |
| META-01 | 03 | x |
| NET-01 | 05 | x |
| NET-02 | 05 | x |
| NET-03 | 05 | x |
| NET-04 | 05 | x |
| NET-05 | 05 | x |
| NET-06 | 05 | x |
| PIP-01 | 05 | x |
| PIP-02 | 05 | x |
| FIP-01 | 05 | x |
| FIP-02 | 05 | x |
| RDNS-01 | 07 | x |
| RDNS-02 | 07 | x |
| FW-01 | 06 | x |
| FW-02 | 06 | x |
| FW-03 | 06 | x |
| FW-04 | 06 | x |
| FW-05 | 06 | x |
| LB-01 | 08 | x |
| LB-02 | 08 | x |
| LB-03 | 08 | x |
| LB-04 | 08 | x |
| LB-05 | 08 | x |
| LB-06 | 08 | x |
| LB-07 | 08 | x |
| LB-08 | 08 | x |
| LB-09 | 08 | x |
| CERT-01 | 08 | x |
| CERT-02 | 08 | x |
| CERT-03 | 08 | x |
| CERT-04 | 08 | x |
| CERT-05 | 08 | x |
| VOL-01 | 04 | x |
| VOL-02 | 04 | x |
| VOL-03 | 04 | x |
| VOL-04 | 04 | x |
| VOL-05 | 04 | x |
| DNS-01 | 07 | x |
| DNS-02 | 07 | x |
| DNS-03 | 07 | x |
| DNS-04 | 07 | x |
| DNS-05 | 07 | x |
| HOST-01 | 05 | x |
| HOST-02 | 09 | x |
| HOST-03 | 02 | x |
| HOST-04 | 02 | x |
| LIFE-01 | 01 | x |
| LIFE-02 | 09 | x |
| LIFE-03 | 09 | x |
| LIFE-04 | 09 | x |
| LIFE-05 | 09 | x |
| LIFE-06 | 01 | x |
| LIFE-07 | 09 | x |
| LIFE-08 | 09 | x |
| LIFE-09 | 09 | x |
| LIFE-10 | 01 | x |
| UI-01 | 10 | x |
| UI-02 | 11, 14 | |
| UI-03 | 10 | x |
| UI-04 | 10 | x |
| UI-05 | 10 | x |
| UI-06 | 10 | x |
| UI-07 | 10 | x |
| UI-08 | 10 | x |
| UI-09 | 14 | |
| INST-01 | 12 | |
| INST-02 | 12 | |
| INST-03 | 12 | |
| INST-04 | 12 | |
| INST-05 | 12 | |
| INST-06 | 12 | |
| CLI-01 | 13 | |
| CLI-02 | 13 | |
| CLI-03 | 13 | |
| CLI-04 | 13 | |
| DOC-01 | all | |
| DOC-02 | all | |
| DOC-03 | all | |
| DOC-04 | all | |
| DOC-05 | all | |
| DOC-06 | all | |
| DOC-07 | all | |
