package cloud

import (
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// registerLoadBalancerTypes adds the routes of the load balancer types, from
// the catalog. The tools read them before load balancers exist in the lab.
func registerLoadBalancerTypes(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /load_balancer_types", func(req *web.Request) (int, any, error) {
		var balancerTypes []schema.LoadBalancerType
		for _, balancerType := range byName(req, cat.LoadBalancerTypes, func(t catalog.LoadBalancerType) string { return t.Name }) {
			balancerTypes = append(balancerTypes, loadBalancerTypeJSON(balancerType))
		}
		return listJSON(req, "load_balancer_types", balancerTypes)
	})

	api.Handle("GET /load_balancer_types/{id}", func(req *web.Request) (int, any, error) {
		for _, balancerType := range cat.LoadBalancerTypes {
			if balancerType.ID == web.PathID(req, "id") {
				return http.StatusOK, schema.LoadBalancerTypeGetResponse{LoadBalancerType: loadBalancerTypeJSON(balancerType)}, nil
			}
		}
		return 0, nil, web.NotFound("load balancer type")
	})
}

// loadBalancerTypeJSON returns a load balancer type as the API shows it.
func loadBalancerTypeJSON(balancerType catalog.LoadBalancerType) schema.LoadBalancerType {
	out := schema.LoadBalancerType{
		ID:                      balancerType.ID,
		Name:                    balancerType.Name,
		Description:             balancerType.Description,
		MaxConnections:          balancerType.MaxConnections,
		MaxServices:             balancerType.MaxServices,
		MaxTargets:              balancerType.MaxTargets,
		MaxAssignedCertificates: balancerType.MaxAssignedCertificates,
		Prices:                  []schema.PricingLoadBalancerTypePrice{},
	}
	out.Deprecation = deprecationJSON(balancerType.Deprecation)
	for _, price := range typePricesJSON(balancerType.Prices) {
		out.Prices = append(out.Prices, schema.PricingLoadBalancerTypePrice(price))
	}
	return out
}
