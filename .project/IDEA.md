# IDEA: hlab

## Idea

hlab is a local fake Hetzner Cloud on Hyper-V under Windows. Unmodified
Hetzner clients (hcloud CLI, OpenTofu provider hetznercloud/hcloud, Ansible
collection hetzner.hcloud) work against it, so cluster setups, OpenTofu and
Ansible scripts can be tested realistically: no costs, no Hetzner account, no
risk for real resources. Everything really works; it is not an API mock.

## Project type

API/infrastructure

## Problem

Testing Hetzner Cloud setups needs a Hetzner account, costs money and risks
real resources. No local environment behaves like the Hetzner Cloud for its
official clients.

## Goals

1. Unmodified hcloud, OpenTofu provider and Ansible collection work against
   hlab by changing only their endpoint setting.
2. Every resource behaves for real: VMs boot, networks connect, firewalls
   filter, load balancers distribute, floating IPs move, DNS resolves.
3. Running hlab is as easy as Docker Desktop: a tray program starts and
   stops the cloud without admin prompts, and stop/start keeps everything.
4. Leaving no trace: uninstalling removes all VMs, networks, routes and DNS
   rules.
5. AI agents control hlab: an MCP server offers the functions of the
   management command to MCP clients such as Claude Code.
6. Failure handling of setups can be tested: deliberate, repeatable faults
   (servers, network, API, storage) are set, seen and ended in the tray,
   and through the management API for tests and automation.

## Scope of the first version

- All Hetzner Cloud API resources, each verified against each of the three
  clients that supports it:
  - Servers with all actions (power on/off, reboot, reset, shutdown,
    rebuild, change type, rescue as a real Linux rescue system, reset root
    password, create image/snapshot, backups (daily automatic, 7 kept, plus
    manual), attach/detach ISO, protection), server types, locations,
    datacenters, images, ISOs, and the console: request_console returns a
    working wss VNC console that the tray also opens
    (`decisions/server-console.md`).
  - SSH keys, primary IPs, floating IPs (really move), reverse DNS (really
    resolves)
  - Private networks with subnets, routes and alias IPs (servers really
    reach each other)
  - Firewalls (really filter), placement groups
  - Load balancers with types, targets and services (really distribute),
    certificates (uploaded and managed; managed ones are issued by an hlab
    CA in the service when all domains are in hlab DNS zones, trust on host
    and WSL2 is an opt-in switch, off by default;
    `decisions/managed-certificates.md`)
  - Volumes (real disks; attach, detach, resize)
  - DNS zones and RRSets that resolve for servers, host and WSL2. A zone
    shadows the real domain on host and WSL2 while the cloud runs (one NRPT
    rule per zone); the UI warns when a zone shadows a real domain.
  - Actions, pricing, metadata service for the VMs (169.254.169.254)
  - Storage Boxes of the Hetzner API with types, subaccounts, snapshots and
    snapshot plans, holding real data: SFTP/SCP, rsync, BorgBackup, SMB,
    WebDAV and FTP/FTPS work as at Hetzner, in a storage VM of their own
- API at `http://api.hetzner.internal/v1`, plain HTTP, no CA
  (`decisions/api-transport.md`); the Storage Box API of the Hetzner API
  at `http://api.hetzner.internal/hetzner/v1`, Storage Boxes at
  `uNNNNN.storagebox.hetzner.internal` (`decisions/storage-box.md`); the
  server console at `wss://console.hetzner.internal` with a certificate of
  the hlab CA (`decisions/server-console.md`).
- Locations: all six real Hetzner locations with datacenters and network
  zones; one shared network segment, Hetzner's location rules enforced by
  the API, all guest traffic routed (/32) through the appliance
  (`decisions/locations.md`).
- Images: every current Hetzner x86 system image (Ubuntu, Debian, Rocky,
  Alma, CentOS Stream, Fedora, openSUSE) from its official cloud image
  under Hetzner's image name, downloaded on demand and cached, with every
  server function working on each; servers log in as root only, like
  Hetzner's images. No app images. ISOs: a catalog of publicly
  downloadable ISOs.
- Server types: the current Hetzner catalog as its own file, own types can be
  added, updatable from the real Hetzner API (optional real token). ARM types
  (CAX) appear in the catalog; creating one returns resource_unavailable.
- Resources: VMs always get the full RAM of their type. RAM and disk are hard
  limits with a configurable reserve for Windows; total vCPUs may be
  overcommitted, but one VM gets at most the host's logical cores. Otherwise
  resource_unavailable. Disks are thin-provisioned in a configurable place.
- IPv4 only; IPv6 addresses are assigned formally but carry no traffic. Public
  IPv4 range configurable, collision check at start.
- Projects and tokens (exactly 64 characters, read or read/write) via an own
  management command; several isolated projects at the same time.
- MCP server `hlab mcp` (stdio) with the functions of the management command
  and the resource overview per project, without discarding the state or
  the catalog update; Hetzner resources stay with the three clients
  (`decisions/mcp-server.md`).
- A small appliance (Debian cloud image, set up by cloud-init) routes traffic
  and serves DNS, bound into Windows via NRPT. Servers are reachable directly
  from host and WSL2, reach the internet outbound, and are not reachable
  from LAN or internet. WSL2 is supported in NAT mode with dnsTunneling (the
  defaults); hlab detects mirrored mode and reports it as not supported,
  and never changes `.wslconfig` or WSL distros
  (`decisions/wsl2-networking.md`).
- Tray program like Docker Desktop: only it starts and stops the cloud,
  without admin prompts; the cloud never starts with Windows. Stop either
  saves VM state or shuts servers down cleanly (setting, default save); a
  Windows shutdown follows the same setting. Start menu and desktop shortcuts
  open only the tray program.
- The tray window is a full console like the Hetzner Cloud Console (same
  structure and workflows, own design, no Hetzner branding, English): view,
  create, configure and delete all resources, plus cloud status, settings,
  server types, projects/tokens and logs.
- Installer (EXE with MSI, installs WebView2 if missing) that checks Hyper-V
  and says exactly what is missing; updates via a new installer. Uninstalling
  always removes VMs, networks, routes and NRPT rules, and asks whether to
  keep user data (disks, volumes, snapshots, backups, storage box data,
  image cache, logs;
  default: delete).
- Deliberate fault injection per project: server crash and freeze, network
  disruption (cut or packet loss) per server, latency between locations or
  per server, API errors and rate limits for the project's clients, slow or
  unavailable volumes and server disks, unavailable storage boxes; each
  fault has an optional duration and ends at the latest at cloud stop;
  random choices take a seed, so a test can repeat them. The tray console
  sets, shows and ends faults; the management API, `hlab fault` and MCP
  tools do the same for tests and automation
  (`decisions/fault-injection.md`).
- Responses are validated against a frozen copy of Hetzner's OpenAPI
  description.
- The state has a version number from the start; until a 1.0 the user
  declares, an update may discard the state with a warning.

## Target vision (final scope)

The architecture is designed for this target vision; only the first version
is built for now.

- Linux as a full host with KVM/libvirt, including the UI

## Non-goals

- Billing
- Security for multi-user or production use
- Reproducing Hetzner's performance, outages or rate limits (only when
  deliberately injected)
- Running ARM (CAX) servers on x64 hosts
- Shrinking a server's RAM below its type

## Possible later (neither goal nor ruled out)

- Real IPv6 connectivity
- HTTPS for the API (opt-in, with a local CA)
- Separate network segments per location or network zone
- German UI (translation)
- Automatic update check

## Constraints

- Go; the API service runs as a Windows service with admin rights on the
  host, only while the cloud runs; the tray window uses Wails v3. No further
  stack requirements; the rest is chosen in the spec.
- Everything platform-specific behind interfaces (later KVM/libvirt).
- Any Windows edition with Hyper-V and desktop, x64: Windows 10/11
  Pro/Enterprise/Education, Windows Server with Desktop Experience.
- Clients are installed by the user (Ansible in WSL2); no telemetry; hlab
  needs no outside service except image and ISO downloads and the optional
  server type update from the real Hetzner API.
- Open source on GitHub under Apache-2.0; outside contributions possible.
  End-to-end tests against Hyper-V run locally, not in hosted CI.

## Repository and publication

- Repository: public on GitHub (`webcodingcat/hlab`) under Apache-2.0
- Publication: release artifacts: a GitHub release per version with the
  MSI, the setup EXE and `SHA256SUMS.txt`

## Documentation

- End users: developers testing Hetzner setups; README with installation,
  first steps, commands, tray and uninstallation; a list of known deviations
  from Hetzner; a reference of the configuration files; help inside the
  program.
- Developers: outside contributors; developer guide under `docs/development/` with
  setup, build/test, E2E against Hyper-V, components, and how to add a
  resource.

## Success criterion

On a fresh Windows 11 Pro with Hyper-V, unmodified hcloud, OpenTofu and
Ansible build a small environment from all resources (e.g. two servers in a
private network behind a load balancer and firewall, with volume, floating IP
and DNS zone), reach it via SSH from host and WSL2, and delete it completely.
The same environment can be created and deleted by hand in the tray UI.
Stopping and starting via the tray keeps everything. After uninstalling
with the default choice, nothing of hlab remains. Resources the example
environment does not use (primary IPs, placement groups, certificates,
reverse DNS, snapshots, backups, rescue, ISOs, Storage Boxes) are covered by
the per-resource client tests.

## In clarification

(none)

## Open for the SPEC

- Default public IPv4 range and collision handling
- Where load balancers, firewalls and floating IPs are implemented
  (appliance, separate VMs, Hyper-V features)
- How start/stop works without admin prompts (service permissions)
- How WSL2 in NAT mode reaches the servers and resolves hlab names (needs
  the feasibility experiment listed in `decisions/wsl2-networking.md`)
- Frontend framework for Wails, installer tool, rescue image, ISO catalog
- Backup schedule details and storage layout
