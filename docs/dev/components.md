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
| hlab-agent | [cmd/hlab-agent](../../cmd/hlab-agent), [internal/agent](../../internal/agent) | Static Linux program in the appliance: `/32` routes to the servers, API proxy, DNS for `hetzner.internal` and the reverse zones (other names forwarded to the host's DNS servers), nftables isolation. |
| hlab-tray | [cmd/hlab-tray](../../cmd/hlab-tray) | Placeholder; prints its version. |

## Packages of hlabd

| Package | Role |
|---|---|
| [internal/cloud](../../internal/cloud) | Start and stop of one cloud: settings, host checks, store, switch, appliance, host network, API, management pipe; the list of resource modules. |
| [internal/api](../../internal/api) | HTTP layer of the Hetzner API: routing under `/v1`, token authentication, read-only guard, JSON checks, error and list helpers. |
| [internal/resource](../../internal/resource) | The `Module` interface every resource type implements, and the optional `NetContributor` and `Deleter`. |
| [internal/resources](../../internal/resources) | One package per resource type: actions, locations, datacenters, server types, pricing, images, SSH keys, placement groups, servers. |
| [internal/actions](../../internal/actions) | Hetzner actions and the worker pool that runs their work. |
| [internal/store](../../internal/store) | SQLite state: projects, tokens, resources as JSON documents, actions, ID counters. |
| [internal/mgmt](../../internal/mgmt) | Management API on the named pipe: status, projects (deletion as an action with its resources), tokens, catalog update. |
| [internal/catalog](../../internal/catalog) | Loads the shipped catalogs from [catalog/](../../catalog), an updated server type catalog and the own server types; updates the server types from the real Hetzner API. |
| [internal/labels](../../internal/labels) | Hetzner's label rules and label selectors. |
| [internal/images](../../internal/images) | Downloads, verifies and converts system images into cached base disks. |
| [internal/ipam](../../internal/ipam) | Public IPv4 allocation. |
| [internal/cidata](../../internal/cidata) | Cloud-init seed ISOs for servers and the appliance. |
| [internal/platform](../../internal/platform), [internal/platform/hyperv](../../internal/platform/hyperv) | VMs, disks, adapters; Hyper-V implementation through one PowerShell process. |
| [internal/hostnet](../../internal/hostnet), [internal/hostnet/windows](../../internal/hostnet/windows) | Host IP, route, NAT, NRPT, firewall rule, WSL2 forwarding, collision checks, check of the console user's `.wslconfig`. |
| [internal/agentapi](../../internal/agentapi), [internal/agentclient](../../internal/agentclient) | The document hlabd pushes to hlab-agent, and the client that pushes it. |
| [internal/sysint](../../internal/sysint) | Service, paths, pipe and installer of the operating system. |
| [internal/settings](../../internal/settings) | Reads `settings.toml`. |
| [internal/profile](../../internal/profile) | Names of the `release` and `dev` profiles. |

Other folders:

- [appliance/](../../appliance): boot files of the appliance VM (cloud-init
  user data, systemd unit that installs the current agent at every boot).
- [tools/](../../tools): `openapi` and `catalog`, run by `task openapi` and
  `task catalog`.
- [internal/apitest](../../internal/apitest): test helper that runs API
  handlers in process and validates every response against Hetzner's
  OpenAPI description.
- [test/e2e](../../test/e2e), [test/docs](../../test/docs): end-to-end and
  documentation tests.

To add an API resource, see [Adding a resource](adding-a-resource.md).
