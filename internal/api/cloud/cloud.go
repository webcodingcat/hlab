// Package cloud is Hetzner's Cloud API: one file per resource, each with its
// routes, its JSON and its action table. It decides what to change and asks
// the lab to do it; it never calls the platform.
package cloud

import (
	"math/big"
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/web"
)

// Register adds the Cloud API's routes to api.
func Register(api *web.API, l *lab.Lab) {
	registerActions(api, l)
	registerDatacenters(api)
	registerLocations(api, l.Catalog())
	registerServerTypes(api, l.Catalog())
	registerLoadBalancerTypes(api, l.Catalog())
	registerPricing(api, l.Catalog())
	registerImages(api, l.Catalog())
	registerISOs(api, l.Catalog())
	registerSSHKeys(api, l)
	registerServers(api, l)
	registerPrimaryIPs(api, l)
	registerFloatingIPs(api, l)
	registerFirewalls(api, l)
	registerNetworks(api, l)
}

// listJSON answers a list request: the page of items the query asks for
// under the resource's key ("ssh_keys"), and the meta with the pagination.
func listJSON[T any](req *web.Request, key string, items []T) (int, any, error) {
	page, meta := web.Paginate(req, items)
	if page == nil {
		page = []T{} // an empty list, not null
	}
	return http.StatusOK, map[string]any{key: page, "meta": meta}, nil
}

// byName keeps the items whose name the query parameter name gives, if it
// gives one; Hetzner's name filters match the whole name.
func byName[T any](req *web.Request, items []T, nameOf func(T) string) []T {
	name, given := req.URL.Query()["name"]
	if !given {
		return items
	}
	var kept []T
	for _, item := range items {
		if nameOf(item) == name[0] {
			kept = append(kept, item)
		}
	}
	return kept
}

// deprecationJSON returns a catalog entry's deprecation as the API shows it.
func deprecationJSON(deprecation *catalog.Deprecation) *schema.DeprecationInfo {
	if deprecation == nil {
		return nil
	}
	return &schema.DeprecationInfo{Announced: deprecation.Announced, UnavailableAfter: deprecation.UnavailableAfter}
}

// priceJSON returns a price as the API shows it.
func priceJSON(price catalog.Price) schema.Price {
	return schema.Price{Net: price.Net, Gross: price.Gross}
}

// roundPrice returns a price with its net and gross rounded to that many
// decimals, as some of Hetzner's answers show prices. The catalog's prices
// are decimals in strings, so a Rat rounds them exactly.
func roundPrice(price schema.Price, decimals int) schema.Price {
	round := func(value string) string {
		number, ok := new(big.Rat).SetString(value)
		if !ok {
			return value // the catalog's check lets only decimals through
		}
		return number.FloatString(decimals)
	}
	return schema.Price{Net: round(price.Net), Gross: round(price.Gross)}
}

// typePricesJSON returns the prices of a server or load balancer type per
// location; schema's PricingLoadBalancerTypePrice has the same fields and
// converts from it.
func typePricesJSON(prices []catalog.TypePrice) []schema.PricingServerTypePrice {
	out := []schema.PricingServerTypePrice{}
	for _, price := range prices {
		out = append(out, schema.PricingServerTypePrice{
			Location:          price.Location,
			PriceHourly:       priceJSON(price.PriceHourly),
			PriceMonthly:      priceJSON(price.PriceMonthly),
			IncludedTraffic:   price.IncludedTraffic,
			PricePerTBTraffic: priceJSON(price.PricePerTBTraffic),
		})
	}
	return out
}
