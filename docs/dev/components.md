# Components

```
 host (Windows)                                  appliance VM (Debian 13)
 hlab (CLI) --pipe--> hlabd ---- HTTP ---------> hlab-agent
                        |  Hetzner API            routes, DNS, API proxy,
                        |  state (SQLite)         nftables isolation
                        |  Hyper-V, host network
                        +---- Hyper-V ----> server VMs (cloud-init seed ISO)
```

A client calls `http://api.hetzner.internal/v1`. The name resolves (NRPT
rule on the host) to the appliance at `198.19.0.1`, whose agent proxies
port 80 to hlabd on `198.19.0.2:8780`. hlabd keeps the state, drives
Hyper-V and the host network, and pushes the complete network
configuration to the agent as one document after every change. Every
packet of a server passes the appliance.

## Programs

| Program | Code | Role |
|---|---|---|
| hlabd | [cmd/hlabd](../../cmd/hlabd) | Windows service; runs the cloud; `install` and `uninstall-cleanup` register and remove it. |
| hlab | [cmd/hlab](../../cmd/hlab), [internal/cli](../../internal/cli) | Management command. `cloud start/stop` use the service controller, the rest the management pipe. |
| hlab-agent | [cmd/hlab-agent](../../cmd/hlab-agent), [internal/agent](../../internal/agent) | Static Linux program in the appliance: `/32` routes to the servers, API proxy, DNS for `hetzner.internal` and the reverse zones (other names forwarded to the host's DNS servers), nftables isolation, and the metadata service on `169.254.169.254:80`, which also hands pending root password resets to the guests, collects their confirmations and lists each server's automount volumes at `/hlab/v1/volumes`. It also runs jobs that need Linux tools on a disk hlabd hot-adds to the appliance (`POST /v1/jobs`): `mkfs` formats a volume, `rescue-prepare` gives a copy of the debian-13 image new partition and filesystem identifiers for the rescue base. |
| hlab-tray | [cmd/hlab-tray](../../cmd/hlab-tray) | Placeholder; prints its version. |

## Packages of hlabd

| Package | Role |
|---|---|
| [internal/cloud](../../internal/cloud) | Start and stop of one cloud: settings, host checks, store, switch, appliance, host network, API, management pipe; the list of resource modules. Runs appliance jobs one at a time (the disk at LUN 1 of the appliance, detached again on every path) and downloads the packages of `catalog/appliance-debs.json` into `<data>\cache\debs` for the appliance's seed ISO. |
| [internal/api](../../internal/api) | HTTP layer of the Hetzner API: routing under `/v1`, token authentication, read-only guard, JSON checks, error and list helpers. |
| [internal/resource](../../internal/resource) | The `Module` interface every resource type implements, and the optional `NetContributor`, `Deleter`, `Reconciler` (make the VM, disk and seed match the stored state), `Scheduled` (time-driven jobs) and `ActionUser` (receives the action manager to start actions outside a request, as the servers' daily backups do). |
| [internal/resources](../../internal/resources) | One package per resource type: actions, locations, datacenters, server types, pricing, images (catalog images and the projects' snapshots and backups), ISOs, SSH keys, placement groups, servers (with snapshots, backups, rescue and ISOs), volumes. Servers and volumes meet through the `VolumeHooks` interface of the servers package, which the cloud wires. |
| [internal/actions](../../internal/actions) | Hetzner actions and the worker pool that runs their work. |
| [internal/scheduler](../../internal/scheduler) | Runs the jobs of `Scheduled` modules while the cloud runs, with a clock tests can replace; the servers use it for the status watch (every 5 s, which also ends an unused rescue), the metrics sampling (every 10 s) and the daily backups (checked every hour). |
| [internal/metrics](../../internal/metrics) | Keeps the samples of each server's VM in memory for 24 hours and derives Hetzner's cpu, disk and network series from them. |
| [internal/shacrypt](../../internal/shacrypt) | SHA-512 crypt of root passwords; only the hash leaves hlabd. |
| [internal/store](../../internal/store) | SQLite state: projects, tokens, resources as JSON documents, actions, ID counters. |
| [internal/mgmt](../../internal/mgmt) | Management API on the named pipe: status, projects (deletion as an action with its resources), tokens, catalog update. |
| [internal/catalog](../../internal/catalog) | Loads the shipped catalogs from [catalog/](../../catalog), an updated server type catalog and the own server types; updates the server types from the real Hetzner API. |
| [internal/labels](../../internal/labels) | Hetzner's label rules and label selectors. |
| [internal/images](../../internal/images) | Downloads, verifies and converts system images into cached base disks; downloads and verifies catalog ISOs unchanged; prepares the rescue base `rescue-v2.vhdx` once through an appliance job. |
| [internal/ipam](../../internal/ipam) | Public IPv4 allocation. |
| [internal/cidata](../../internal/cidata) | Cloud-init seed ISOs for servers and the appliance. A server's vendor-data is multipart MIME, built in [internal/resources/servers](../../internal/resources/servers): a cloud-config part (root login, root password without expiry) and a shell script part that installs the sshd drop-in, the `hlab-guest` timer for password resets and grown volumes, and the udev rule and `hlab-volume@.service` that name volume disks `scsi-0HC_Volume_<id>` and mount automount volumes, so user data with its own `runcmd` or `write_files` cannot drop them. |
| [internal/platform](../../internal/platform), [internal/platform/hyperv](../../internal/platform/hyperv) | VMs, disks (copies, differencing child disks, snapshots of running VMs through a Standard checkpoint), adapters, DVD drives, boot order, power, VM samples and host resources; Hyper-V implementation through one PowerShell process. Every VM gets COM1 on a named pipe that hlabd itself serves and drains before each start, with an ACL for SYSTEM, administrators and the VM worker processes only, because the console carries user data output and a pipe Hyper-V creates would be readable by everyone. |
| [internal/hostnet](../../internal/hostnet), [internal/hostnet/windows](../../internal/hostnet/windows) | Host IP, route, NAT, NRPT, firewall rule, WSL2 forwarding, collision checks, check of the console user's `.wslconfig`. |
| [internal/agentapi](../../internal/agentapi), [internal/agentclient](../../internal/agentclient) | The document hlabd pushes to hlab-agent, and the client that pushes it. |
| [internal/sysint](../../internal/sysint) | Service, paths, pipe and installer of the operating system. |
| [internal/settings](../../internal/settings) | Reads `settings.toml`. |
| [internal/profile](../../internal/profile) | Names of the `release` and `dev` profiles. |

Other folders:

- [appliance/](../../appliance): boot files of the appliance VM (cloud-init
  user data, systemd unit that installs the current agent at every boot and
  the packages of `appliance-debs.json` while `mkfs.xfs` is missing).
- [tools/](../../tools): `openapi` and `catalog`, run by `task openapi` and
  `task catalog`.
- [internal/apitest](../../internal/apitest): test helper that runs API
  handlers in process and validates every response against Hetzner's
  OpenAPI description; `apitest.ServersDeps` wires the servers module to
  a fake platform.
- [internal/platform/platformtest](../../internal/platform/platformtest):
  in-memory fake of the platform for unit tests, with VM states, host
  resources, samples, injected failures and blocked calls.
- [test/e2e](../../test/e2e), [test/docs](../../test/docs): end-to-end and
  documentation tests.

To add an API resource, see [Adding a resource](adding-a-resource.md).
