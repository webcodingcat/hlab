package pricing_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resources/pricing"
)

const shippedDir = "../../../catalog"

type price struct {
	Location     string            `json:"location"`
	PriceHourly  map[string]string `json:"price_hourly"`
	PriceMonthly map[string]string `json:"price_monthly"`
}

type typePrices struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	Prices []price `json:"prices"`
}

func TestPricing(t *testing.T) {
	t.Run("CAT-05", func(t *testing.T) {
		own := map[string]any{
			"id": 100001, "name": "own-a", "description": "own type", "architecture": "x86",
			"cores": 2, "cpu_type": "shared", "category": "regular_purpose", "memory": 4, "disk": 40,
			"storage_type": "local", "deprecation": nil, "prices": []any{},
			"locations": []any{
				map[string]any{"id": 1, "name": "fsn1", "available": true, "recommended": false, "deprecation": nil},
				map[string]any{"id": 3, "name": "hel1", "available": true, "recommended": false, "deprecation": nil},
			},
		}
		customFile := filepath.Join(t.TempDir(), "server-types.custom.json")
		data, _ := json.Marshal([]any{own})
		if err := os.WriteFile(customFile, data, 0o644); err != nil {
			t.Fatal(err)
		}
		cat, warns, err := catalog.LoadAll(shippedDir, t.TempDir(), customFile)
		if err != nil || len(warns) != 0 {
			t.Fatalf("LoadAll: %v, warnings %q", err, warns)
		}
		env := apitest.New(t, pricing.New(cat))

		resp := env.Do(http.MethodGet, "/pricing", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /pricing: status %d, want 200", resp.StatusCode)
		}
		var body struct {
			Pricing struct {
				Currency          string       `json:"currency"`
				VATRate           string       `json:"vat_rate"`
				ServerTypes       []typePrices `json:"server_types"`
				LoadBalancerTypes []typePrices `json:"load_balancer_types"`
				Volume            struct {
					PricePerGBMonth map[string]string `json:"price_per_gb_month"`
				} `json:"volume"`
				PrimaryIPs []struct {
					Type   string  `json:"type"`
					Prices []price `json:"prices"`
				} `json:"primary_ips"`
				FloatingIPs []struct {
					Type   string  `json:"type"`
					Prices []price `json:"prices"`
				} `json:"floating_ips"`
			} `json:"pricing"`
		}
		apitest.Decode(t, resp, &body)
		p := body.Pricing

		if p.Currency == "" || p.VATRate == "" {
			t.Errorf("currency %q, vat_rate %q; want both set", p.Currency, p.VATRate)
		}

		// Every server type of the merged catalog, and nothing else.
		priced := map[string]typePrices{}
		for _, st := range p.ServerTypes {
			priced[st.Name] = st
		}
		var want, got []string
		for _, st := range cat.Types() {
			want = append(want, st.Name)
		}
		for name := range priced {
			got = append(got, name)
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("pricing server_types = %v, want the catalog's %v", got, want)
		}
		for _, st := range p.ServerTypes {
			if len(st.Prices) == 0 {
				t.Errorf("server type %s has no prices", st.Name)
			}
		}
		ownPrices := priced["own-a"]
		var locs []string
		for _, pr := range ownPrices.Prices {
			locs = append(locs, pr.Location)
			for _, v := range []string{pr.PriceHourly["net"], pr.PriceHourly["gross"], pr.PriceMonthly["net"], pr.PriceMonthly["gross"]} {
				if f, err := strconv.ParseFloat(v, 64); err != nil || f != 0 {
					t.Errorf("own type price in %s = %q, want zero", pr.Location, v)
				}
			}
		}
		slices.Sort(locs)
		if ownPrices.ID != 100001 || !slices.Equal(locs, []string{"fsn1", "hel1"}) {
			t.Errorf("own type pricing = ID %d, locations %v; want ID 100001 with fsn1 and hel1", ownPrices.ID, locs)
		}

		// Every load balancer type of the catalog.
		var wantLB, gotLB []string
		for _, raw := range cat.LoadBalancerTypes {
			var lb struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &lb); err != nil {
				t.Fatal(err)
			}
			wantLB = append(wantLB, lb.Name)
		}
		for _, lb := range p.LoadBalancerTypes {
			if len(lb.Prices) == 0 {
				t.Errorf("load balancer type %s has no prices", lb.Name)
			}
			gotLB = append(gotLB, lb.Name)
		}
		slices.Sort(wantLB)
		slices.Sort(gotLB)
		if len(wantLB) == 0 || !slices.Equal(gotLB, wantLB) {
			t.Errorf("pricing load_balancer_types = %v, want the catalog's %v", gotLB, wantLB)
		}

		if p.Volume.PricePerGBMonth["net"] == "" {
			t.Errorf("volume price_per_gb_month = %v, want a net price", p.Volume.PricePerGBMonth)
		}
		// The primary IP prices of the catalog (Hetzner lists only ipv4).
		shippedPricing, err := os.ReadFile(filepath.Join(shippedDir, "pricing.json"))
		if err != nil {
			t.Fatalf("read shipped pricing: %v", err)
		}
		var shipped struct {
			Pricing struct {
				PrimaryIPs []struct {
					Type string `json:"type"`
				} `json:"primary_ips"`
			} `json:"pricing"`
		}
		if err := json.Unmarshal(shippedPricing, &shipped); err != nil {
			t.Fatalf("decode shipped pricing: %v", err)
		}
		var wantIP, gotIP []string
		for _, ip := range shipped.Pricing.PrimaryIPs {
			wantIP = append(wantIP, ip.Type)
		}
		for _, ip := range p.PrimaryIPs {
			if len(ip.Prices) == 0 {
				t.Errorf("primary IP type %s has no prices", ip.Type)
			}
			gotIP = append(gotIP, ip.Type)
		}
		slices.Sort(wantIP)
		slices.Sort(gotIP)
		if !slices.Contains(wantIP, "ipv4") || !slices.Equal(gotIP, wantIP) {
			t.Errorf("primary_ips types = %v, want the catalog's %v (ipv4, no ipv6)", gotIP, wantIP)
		}
		floating := false
		for _, ip := range p.FloatingIPs {
			floating = floating || len(ip.Prices) > 0
		}
		if !floating {
			t.Errorf("floating_ips have no prices")
		}
	})
}
