package cloud

import (
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// loadBalancerTypeAnswer is a load balancer type as Hetzner's API sends it.
// It is HLab's own type because schema.LoadBalancerType also sends
// "deprecated", which the real API no longer sends (measured 2026-11-07).
type loadBalancerTypeAnswer struct {
	ID                      int64                                 `json:"id"`
	Name                    string                                `json:"name"`
	Description             string                                `json:"description"`
	MaxConnections          int                                   `json:"max_connections"`
	MaxServices             int                                   `json:"max_services"`
	MaxTargets              int                                   `json:"max_targets"`
	MaxAssignedCertificates int                                   `json:"max_assigned_certificates"`
	Deprecation             *schema.DeprecationInfo               `json:"deprecation"`
	Prices                  []schema.PricingLoadBalancerTypePrice `json:"prices"`
}

// registerLoadBalancerTypes adds the routes of the load balancer types, from
// the catalog. The tools read them before load balancers exist in the lab.
// The list takes no sort: Hetzner ignores it here (measured 2026-11-07).
func registerLoadBalancerTypes(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /load_balancer_types", func(req *web.Request) (int, any, error) {
		var balancerTypes []loadBalancerTypeAnswer
		for _, balancerType := range byName(req, cat.LoadBalancerTypes, func(t catalog.LoadBalancerType) string { return t.Name }) {
			balancerTypes = append(balancerTypes, loadBalancerTypeJSON(balancerType))
		}
		return listJSON(req, "load_balancer_types", balancerTypes)
	})

	api.Handle("GET /load_balancer_types/{id}", func(req *web.Request) (int, any, error) {
		for _, balancerType := range cat.LoadBalancerTypes {
			if balancerType.ID == web.PathID(req, "id") {
				return http.StatusOK, map[string]loadBalancerTypeAnswer{"load_balancer_type": loadBalancerTypeJSON(balancerType)}, nil
			}
		}
		return 0, nil, web.NotFoundNull("load balancer type not found")
	})
}

// loadBalancerTypeJSON returns a load balancer type as the API shows it.
// Its prices have four decimals here, unlike in GET /pricing (measured
// 2026-11-07).
func loadBalancerTypeJSON(balancerType catalog.LoadBalancerType) loadBalancerTypeAnswer {
	out := loadBalancerTypeAnswer{
		ID:                      balancerType.ID,
		Name:                    balancerType.Name,
		Description:             balancerType.Description,
		MaxConnections:          balancerType.MaxConnections,
		MaxServices:             balancerType.MaxServices,
		MaxTargets:              balancerType.MaxTargets,
		MaxAssignedCertificates: balancerType.MaxAssignedCertificates,
		Deprecation:             deprecationJSON(balancerType.Deprecation),
		Prices:                  []schema.PricingLoadBalancerTypePrice{},
	}
	for _, price := range typePricesJSON(balancerType.Prices) {
		price.PriceHourly = roundPrice(price.PriceHourly, 4)
		price.PriceMonthly = roundPrice(price.PriceMonthly, 4)
		price.PricePerTBTraffic = roundPrice(price.PricePerTBTraffic, 4)
		out.Prices = append(out.Prices, schema.PricingLoadBalancerTypePrice(price))
	}
	return out
}
