# Decision: API transport and hostname

- Date: 2026-11-18
- Status: decided by the user
- Cost of change: high for the hostname (it ends up in users' scripts and
  client configs); medium for adding HTTPS later (additive, existing HTTP
  endpoint keeps working)

## Decision

- The hlab API is served over plain HTTP only. hlab ships no CA and
  installs no certificates.
- The API hostname is `api.hetzner.internal`; clients use
  `http://api.hetzner.internal/v1` as their endpoint.
- The Storage Box API planned for version 2 gets its own base address under
  `.internal` as well. The clients' second endpoint setting
  (`HETZNER_ENDPOINT`, see below) points there.

## Client facts (checked 2026-11-18 against current docs)

| Client | Cloud API endpoint | Hetzner API endpoint (Storage Box) | TLS options |
|---|---|---|---|
| hcloud CLI | `--endpoint`, `HCLOUD_ENDPOINT`, `endpoint` in config/context | `--hetzner-endpoint`, `HETZNER_ENDPOINT`, `hetzner_endpoint` | none |
| OpenTofu provider hetznercloud/hcloud | `endpoint` | `endpoint_hetzner` | none documented |
| Ansible hetzner.hcloud | `api_endpoint`, `HCLOUD_ENDPOINT` | `api_endpoint_hetzner`, `HETZNER_ENDPOINT` | no `validate_certs`/`ca_path` |

None of the clients can switch off certificate verification. HTTPS would
therefore only work with a CA that every client trusts.

## Reasons

- No CA means no certificate installation in the Windows store, in every
  WSL distro (including distros and Python venvs added later), or in the
  VMs, and nothing to remove at uninstall.
- Clients running inside the VMs also call the API (e.g.
  hcloud-cloud-controller-manager and the CSI driver in cluster setups).
  With HTTPS, every VM and container image would need the CA as well.
- Ansible runs only in WSL. Its Python `requests` uses certifi and would
  need `REQUESTS_CA_BUNDLE` in every shell.
- What hlab is for, testing OpenTofu/Ansible/cluster setups, does not
  depend on TLS. API tokens in hlab are fake values, so sending them
  unencrypted on the local network is acceptable.
- `.local` is reserved for mDNS (RFC 6762). On WSL distros with
  `libnss-mdns` (`mdns4_minimal [NOTFOUND=return]`), `.local` lookups never
  reach DNS, so the NRPT route would not help. `.internal` is reserved by
  ICANN (2024) for private use and is never resolved publicly.

## Rejected alternatives

- HTTPS only with a local CA: more realistic, but needs CA installation and
  removal on the host, in every WSL distro and in every VM, plus
  `REQUESTS_CA_BUNDLE` for Ansible. A CA key on the host is also a security
  risk unless it is name-constrained.
- HTTP and HTTPS in parallel: has all the CA effort of HTTPS and doubles the
  test matrix. HTTPS can still be added later as an opt-in without
  breaking the HTTP endpoint.
- Shadowing the real `api.hetzner.cloud` via NRPT with a local CA: no
  endpoint configuration would be needed, but the real Hetzner Cloud would
  be unreachable while hlab runs, a CA for a foreign domain would be
  needed, and fake and real would be easy to confuse.
- `api.hetzner.local`: conflicts with mDNS resolution (see above).
- `api.hetzner.test`: works as well; `.internal` was preferred.
