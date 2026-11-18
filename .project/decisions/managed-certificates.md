# Decision: Managed certificates for load balancers

- Date: 2026-11-18
- Status: decided by the user
- Cost of change: medium. The CA is an internal part of the service and can be
  replaced without changing the API. Once users have switched trust on,
  replacing the CA means trusting it again on the host and in WSL2.

## Decision

- hlab supports both certificate types: `uploaded` and `managed`.
- Managed certificates are issued by an hlab CA inside the hlab service,
  signed with Go `crypto/x509`. There is no separate ACME server and no
  additional component.
- Issuance follows Hetzner's visible behaviour. The create call returns an
  action, and the certificate starts with issuance status `pending`. It
  moves to `issued` when every domain belongs to a DNS zone managed by
  hlab, and to `failed` with an error otherwise, just as Hetzner requires
  the domains to be in Hetzner DNS. The `retry` action re-runs the issuance.
  Managed certificates are renewed automatically before they expire.
- The CA key pair is created once at installation and stays the same until
  uninstall. The private key is readable by SYSTEM only (stored under
  ProgramData). Uninstall deletes it.
- Trust is opt-in and off by default. A switch in the tray app adds the CA
  certificate to the Windows trust store and to the WSL2 distros. Turning
  the switch off removes it again, and so does uninstall.
- hlab never installs the CA in VMs. The CA certificate can be downloaded
  (for example via the tray app), so users can pass it to VMs themselves,
  e.g. via cloud-init, or use it with `curl --cacert`.
- This does not contradict `api-transport.md` ("hlab ships no CA"): that
  decision is about the API transport, which stays plain HTTP. This CA only
  signs load balancer certificates and is trusted nowhere unless the user
  turns the switch on.

## Client facts (checked 2026-11-18 against the current source code)

| Client | Behaviour for managed certificates |
|---|---|
| hcloud CLI `certificate create --type managed` | Waits for the action returned by the create call. If the action fails, the command returns an error. |
| OpenTofu `hcloud_managed_certificate` | Waits for the create action, with no timeout of its own. If it fails, `apply` fails. The ID is already set, so the resource most likely stays in the state as tainted. Read does not evaluate `status`. |
| Ansible `hetzner.hcloud.certificate` (type managed) | Waits for the action for about 5 minutes (`max_retries=62`). If it fails, the module fails. |

All three clients therefore fail whenever issuance fails. Scripts with managed
certificates only run unchanged if hlab really issues them.

## Reasons

- IDEA.md promises "certificates (uploaded and managed)", and scripts are
  meant to run unchanged. A managed certificate that always fails would
  break every script using one, including load balancer services that
  reference it.
- An in-service CA is the smallest solution. The clients see exactly the
  same API, status and action as with a full ACME flow.
- Checking that the domains belong to hlab zones keeps the failure case
  testable and close to Hetzner's behaviour.
- Trust is opt-in because the CA cannot usefully be name-constrained. Zones
  may use any domain, including real ones like `example.com`. If the key
  were stolen while trust is switched on, it could be used to forge TLS for
  any domain. Off by default, a SYSTEM-only key and removal at uninstall
  keep that risk small. The risk is documented next to the switch.

## Rejected alternatives

- Managed certificates always go to `failed` (only `uploaded` works): no
  CA and no effort, but all three clients fail on it, which contradicts the
  goal that scripts run unchanged.
- A local ACME server (Pebble or step-ca) with DNS-01 against hlab's DNS:
  closer to Let's Encrypt internally, but the clients cannot tell the
  difference. It adds a process to run, monitor and uninstall. Pebble is
  not meant for long-running use and creates a new root CA in memory at
  every start, which would break the trust switch and invalidate issued
  certificates after each restart. step-ca avoids that but is much heavier.
- Never trusting the CA anywhere (not even downloadable): certificates
  would only be formally issued, and HTTPS through a load balancer could
  not be tested without `-k`.
- Trusting the CA automatically on host and WSL2: convenient, but it turns
  a CA key without name constraints into a standing risk for every
  installation.
