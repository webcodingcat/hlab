package cloud

import (
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// pricingJSON is Hetzner's GET /pricing. It is HLab's own type because
// schema.Pricing also sends the removed "traffic" and a "datacenter" in each
// Primary IP price, which the real API no longer sends.
type pricingJSON struct {
	Currency          string                           `json:"currency"`
	VATRate           string                           `json:"vat_rate"`
	Image             schema.PricingImage              `json:"image"`
	Volume            schema.PricingVolume             `json:"volume"`
	ServerBackup      schema.PricingServerBackup       `json:"server_backup"`
	FloatingIP        schema.PricingFloatingIP         `json:"floating_ip"`
	FloatingIPs       []schema.PricingFloatingIPType   `json:"floating_ips"`
	PrimaryIPs        []primaryIPPricesJSON            `json:"primary_ips"`
	ServerTypes       []schema.PricingServerType       `json:"server_types"`
	LoadBalancerTypes []schema.PricingLoadBalancerType `json:"load_balancer_types"`
}

// primaryIPPricesJSON are the prices of one type of Primary IP.
type primaryIPPricesJSON struct {
	Type   string               `json:"type"`
	Prices []primaryIPPriceJSON `json:"prices"`
}

// primaryIPPriceJSON is the price of a Primary IP in a location.
type primaryIPPriceJSON struct {
	Location     string       `json:"location"`
	PriceHourly  schema.Price `json:"price_hourly"`
	PriceMonthly schema.Price `json:"price_monthly"`
}

// registerPricing adds the route of the prices, from the catalog.
func registerPricing(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /pricing", func(*web.Request) (int, any, error) {
		return http.StatusOK, map[string]pricingJSON{"pricing": pricesJSON(cat)}, nil
	})
}

// pricesJSON returns the catalog's prices as GET /pricing shows them.
func pricesJSON(cat *catalog.Catalog) pricingJSON {
	pricing := cat.Pricing
	out := pricingJSON{
		Currency:          pricing.Currency,
		VATRate:           pricing.VATRate,
		Image:             schema.PricingImage{PricePerGBMonth: priceJSON(pricing.Image.PricePerGBMonth)},
		Volume:            schema.PricingVolume{PricePerGBPerMonth: priceJSON(pricing.Volume.PricePerGBMonth)},
		ServerBackup:      schema.PricingServerBackup{Percentage: pricing.ServerBackup.Percentage},
		FloatingIP:        schema.PricingFloatingIP{PriceMonthly: priceJSON(pricing.FloatingIP.PriceMonthly)},
		FloatingIPs:       []schema.PricingFloatingIPType{},
		PrimaryIPs:        []primaryIPPricesJSON{},
		ServerTypes:       []schema.PricingServerType{},
		LoadBalancerTypes: []schema.PricingLoadBalancerType{},
	}
	for _, ipType := range pricing.FloatingIPs {
		prices := schema.PricingFloatingIPType{Type: ipType.Type, Prices: []schema.PricingFloatingIPTypePrice{}}
		for _, price := range ipType.Prices {
			prices.Prices = append(prices.Prices, schema.PricingFloatingIPTypePrice{Location: price.Location, PriceMonthly: priceJSON(price.PriceMonthly)})
		}
		out.FloatingIPs = append(out.FloatingIPs, prices)
	}
	for _, ipType := range pricing.PrimaryIPs {
		prices := primaryIPPricesJSON{Type: ipType.Type, Prices: []primaryIPPriceJSON{}}
		for _, price := range ipType.Prices {
			prices.Prices = append(prices.Prices, primaryIPPriceJSON{
				Location:     price.Location,
				PriceHourly:  priceJSON(*price.PriceHourly), // the catalog's check found it
				PriceMonthly: priceJSON(price.PriceMonthly),
			})
		}
		out.PrimaryIPs = append(out.PrimaryIPs, prices)
	}
	for _, serverType := range cat.ServerTypes {
		out.ServerTypes = append(out.ServerTypes, schema.PricingServerType{
			ID: serverType.ID, Name: serverType.Name, Prices: typePricesJSON(serverType.Prices),
		})
	}
	for _, balancerType := range cat.LoadBalancerTypes {
		entry := schema.PricingLoadBalancerType{ID: balancerType.ID, Name: balancerType.Name, Prices: []schema.PricingLoadBalancerTypePrice{}}
		for _, price := range typePricesJSON(balancerType.Prices) {
			entry.Prices = append(entry.Prices, schema.PricingLoadBalancerTypePrice(price))
		}
		out.LoadBalancerTypes = append(out.LoadBalancerTypes, entry)
	}
	return out
}
