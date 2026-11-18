# Phase 01 context

Confirmed with the user on 2026-11-18, one line each.

- API scope: only what hcloud needs for `server create/list/describe/delete` and `ssh-key create`: `/ssh_keys` (create, get, list, delete), `/actions/{id}`, `/server_types`, `/images`, `/locations`, `/datacenters` (410); pagination, filters, labels and locks follow in phase 02; lists return one page with a valid `meta.pagination`.
- Servers in 01: create, get, list, delete; the create action succeeds when the VM runs, not when SSH answers; `root_password` is null (passwords are phase 03).
- Public IPv4: allocation continues after the last address handed out and wraps to `.4` only at the end of the range, skipping used ones (DECISIONS.md).
- IPv6: a formal /64 per server from a fixed documentation range, no traffic.
- Appliance: Debian 13 generic, 2 vCPU, 1024 MB, 10 GB disk; the current hlab-agent binary is put on the appliance's cidata at every cloud start and installed by a systemd unit at boot.
- hlab-agent in 01: /32 routes, API proxy 198.19.0.1:80 -> 198.19.0.2:8780, DNS server (hetzner.internal and reverse zones itself, the rest forwarded), nftables isolation backstop; no HAProxy yet.
- DNS forwarding: to the host's DNS servers collected by hlabd at cloud start (`agentapi.Config.Upstream`), else 1.1.1.1 and 9.9.9.9.
- Catalogs: `task catalog` generates locations, server types, load balancer types and pricing once from the real API via the hcloud context `HLab` (read only); `catalog/images.json` with the five images by hand (Ubuntu `releases/<codename>/release/` + SHA256SUMS, Debian `cloud.debian.org/images/cloud/<codename>/latest/` + SHA512SUMS; checksum read from the source at download).
- OpenAPI: `task openapi` downloads the description, checks it against `api/openapi.lock` and caches it in the user cache folder; a shared test helper validates every handler response; a missing cache file fails the tests with "run `task openapi`".
- WSL2: hostnet enables forwarding on the WSL and hlab adapters already in 01 (API-01 from WSL2); WSL mode detection (HOST-04) is phase 02.
- Host settings hostnet changes but does not own are recorded in `hostnet-restore.json` and restored at the next Up, Down or uninstall-cleanup.
- Firewall: one inbound rule (group per profile) for TCP 8780 on the hlab adapter from 198.19.0.1 only; nothing listens on host port 80.
- Profiles: `release` (service `hlab`) and `dev` (service `hlab-dev`, built by `task build`) differ in every colliding name; only one cloud runs (LIFE-10).
- E2E harness: uses the dev service and `%ProgramData%\hlab-dev`; `task build` first; refuses to run while the release cloud runs or when `hlab-dev` does not point at this working copy's `bin\hlabd.exe`; starts the dev cloud if stopped and stops it at the end if it started it; one project `e2e-<time>` per run; servers deleted one by one (project delete is phase 02); test image ubuntu-24.04.
- `hlabd install --dev` is built in 01; the user runs `scripts\dev-setup.ps1` elevated; the run never calls it.
