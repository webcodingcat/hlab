package servers_test

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resources/servers"
)

func TestServers(t *testing.T) {
	t.Run("SRV-04", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t, servers.New(servers.Deps{
			Catalog:     cat,
			PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
			Machines:    apitest.NoopMachines{},
		}))

		var nets []netip.Prefix
		for _, name := range []string{"srv-a", "srv-b"} {
			resp := env.Do(http.MethodPost, "/servers", map[string]any{
				"name":               name,
				"server_type":        "cx23",
				"image":              "ubuntu-24.04",
				"location":           "fsn1",
				"start_after_create": true,
			})
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /servers %s: status %d, want 201", name, resp.StatusCode)
			}
			var body struct {
				Server struct {
					PublicNet struct {
						IPv6 *struct {
							IP string `json:"ip"`
						} `json:"ipv6"`
					} `json:"public_net"`
				} `json:"server"`
			}
			apitest.Decode(t, resp, &body)
			if body.Server.PublicNet.IPv6 == nil {
				t.Fatalf("%s: public_net.ipv6 is null", name)
			}
			p, err := netip.ParsePrefix(body.Server.PublicNet.IPv6.IP)
			if err != nil || !p.Addr().Is6() || p.Bits() != 64 {
				t.Fatalf("%s: public_net.ipv6.ip = %q, want an IPv6 /64", name, body.Server.PublicNet.IPv6.IP)
			}
			nets = append(nets, p.Masked())
		}
		if nets[0] == nets[1] {
			t.Errorf("both servers got IPv6 %s, want distinct /64s", nets[0])
		}
	})
}
