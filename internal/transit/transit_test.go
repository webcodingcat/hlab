package transit_test

import (
	"net/netip"
	"testing"

	"github.com/webcodingcat/hlab/internal/transit"
)

func TestTransit(t *testing.T) {
	t.Run("HOST-02", func(t *testing.T) {
		for _, want := range []struct {
			r                         string
			host, appliance           string
			apiListen, apiProxy, agnt string
		}{
			{r: "198.19.0.0/16", host: "198.19.0.2/30", appliance: "198.19.0.1",
				apiListen: "198.19.0.2:8780", apiProxy: "198.19.0.1:80", agnt: "198.19.0.1:9100"},
			{r: "203.0.112.0/22", host: "203.0.112.2/30", appliance: "203.0.112.1",
				apiListen: "203.0.112.2:8780", apiProxy: "203.0.112.1:80", agnt: "203.0.112.1:9100"},
		} {
			got := transit.For(netip.MustParsePrefix(want.r))
			if got.Host != netip.MustParsePrefix(want.host) {
				t.Errorf("For(%s).Host = %s, want %s", want.r, got.Host, want.host)
			}
			if got.Appliance != netip.MustParseAddr(want.appliance) {
				t.Errorf("For(%s).Appliance = %s, want %s", want.r, got.Appliance, want.appliance)
			}
			if got.APIListen != netip.MustParseAddrPort(want.apiListen) {
				t.Errorf("For(%s).APIListen = %s, want %s", want.r, got.APIListen, want.apiListen)
			}
			if got.APIProxy != netip.MustParseAddrPort(want.apiProxy) {
				t.Errorf("For(%s).APIProxy = %s, want %s", want.r, got.APIProxy, want.apiProxy)
			}
			if got.Agent != netip.MustParseAddrPort(want.agnt) {
				t.Errorf("For(%s).Agent = %s, want %s", want.r, got.Agent, want.agnt)
			}
		}
	})
}
