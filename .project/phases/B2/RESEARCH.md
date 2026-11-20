# Phase B2 research: LB health-check protocol `https`

## Real Hetzner Cloud API: UNCLEAR
- Not confirmed whether the live API accepts `https` for `health_check.protocol`.
  Not confirmed whether it returns `https` or `http` with `http.tls: true`.
- The docs.hetzner.cloud reference pages and the changelog could not be read for
  this. The changelog excerpt that was readable (up to 2023) has no entry on it.
  The 3.5 MB cloud.spec.json was only readable up to its Certificates section, so
  the enums near the load balancer paths were not seen. The frozen spec in the repo
  was not available (no cloud.spec.json file found), so lines 19642, 20933 and 78337
  were not re-checked.
- hcloud-go's schema struct has `Protocol string` with no allowed values listed.
  `HTTP.TLS bool` is a separate field. This fits the OpenAPI reading that the API
  only knows tcp and http and expresses https as http plus `tls: true`, but that is
  an inference.
- Needs a live check with a throwaway load balancer: add a service with
  `--health-check-protocol https` and read the result back.

## hcloud CLI v1.70.2 (hcloud-go v2.53.0 per go.mod): confirmed from source
- `internal/cmd/loadbalancer/add_service.go` accepts `--health-check-protocol`
  values `http`, `https`, `tcp`. Anything else gives "invalid health check protocol".
  The value is sent unchanged as `hcloud.LoadBalancerServiceProtocol` in
  `opts.HealthCheck.Protocol`. The CLI does no translation to http+tls.
- `--health-check-http-tls` (bool) is read only for http or https. It is sent as
  `HealthCheck.HTTP.TLS` only if the flag was set explicitly.
- hcloud-go: constants `tcp`, `http`, `https`. The health-check protocol uses the
  same `LoadBalancerServiceProtocol` type. `HealthCheck.HTTP` has Domain, Path,
  Response, StatusCodes and TLS.
- Sources:
  https://raw.githubusercontent.com/hetznercloud/cli/v1.70.2/internal/cmd/loadbalancer/add_service.go
  https://raw.githubusercontent.com/hetznercloud/cli/v1.70.2/go.mod
  https://raw.githubusercontent.com/hetznercloud/hcloud-go/v2.37.0/hcloud/load_balancer.go
  (read at v2.37.0, not at v2.53.0; the constants are probably unchanged)

## Terraform provider hetznercloud/hcloud 1.70.2: confirmed from source
- `health_check.protocol` is required and validated with `StringInSlice`
  `http`, `https`, `tcp`. It is not ForceNew, so a change updates in place.
- `health_check.http.tls` is an optional bool, not Computed. It is always sent,
  default false, and always written back to state on read. A server-side difference
  shows as drift.
- No cross-field check that `https` needs `tls = true`. The provider leaves that to
  the API. The provider does not translate https to http+tls.
- Source:
  https://raw.githubusercontent.com/hetznercloud/terraform-provider-hcloud/v1.70.2/internal/loadbalancer/resource_service.go

## Ansible hetzner.hcloud 7.2.0: confirmed from source
- Module `load_balancer_service`: `health_check.protocol` choices `http`, `https`,
  `tcp` (required). Service `protocol` choices are the same.
- `_make_service_health_check` compares wanted and current `protocol` with plain
  `!=`, without normalization. If the API returned `http` for a requested `https`,
  the module would report changed on every run. The wanted value is compared with
  whatever the API returns.
- `health_check.http.tls` defaults to False and is never None. Once an `http` block
  is given, it is compared on every run. The docs say it is only for protocol https;
  the code does not enforce that.
- Source:
  https://raw.githubusercontent.com/ansible-collections/hetzner.hcloud/7.2.0/plugins/modules/load_balancer_service.py

## Consequences for the plan
- All three clients pass `https` through to the API unchanged. None maps it to
  http+tls. Whether tests and the fake API should accept `https` is decided by the
  real API and is unconfirmed.
- Safest for the fake: accept `tcp`, `http` and `https` on input. Store and return
  what was sent.
- This follows from the clients, not from the API. Verify against a real load
  balancer before relying on it.

## Not checked
- Latest stable versions and release dates of the three tools (the pinned versions
  were used).
