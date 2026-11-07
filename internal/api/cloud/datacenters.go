package cloud

import "github.com/webcodingcat/hlab/internal/web"

// datacentersRemoved is the changelog entry of Hetzner's answer to the
// datacenter endpoints, which it removed in 2026 together with the
// datacenter fields of servers and Primary IPs.
const datacentersRemoved = "https://docs.hetzner.cloud/changelog#2026-06-02-datacenters-deprecated"

// registerDatacenters adds the datacenter routes, which answer as Hetzner's
// API does now: the functionality was removed. The lab has no datacenters,
// and no answer of another resource names one.
func registerDatacenters(api *web.API) {
	removed := func(*web.Request) (int, any, error) {
		return 0, nil, web.Deprecated(datacentersRemoved)
	}
	api.Handle("GET /datacenters", removed)
	api.Handle("GET /datacenters/{id}", removed)
}
