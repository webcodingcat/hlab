// Package transit derives the addresses of the host, the appliance, the
// cloud API and the agent from the public range.
package transit

import "net/netip"

// Ports of the API backend on the host, the API proxy and the agent on the
// appliance.
const (
	apiListenPort = 8780
	apiProxyPort  = 80
	agentPort     = 9100
)

// Addrs are the transit addresses of one public range.
type Addrs struct {
	Host      netip.Prefix   // range .2/30
	Appliance netip.Addr     // range .1
	APIListen netip.AddrPort // .2:8780
	APIProxy  netip.AddrPort // .1:80
	Agent     netip.AddrPort // .1:9100
}

// For returns the transit addresses of the range r: the appliance takes the
// first address after the network address, the host the second.
func For(r netip.Prefix) Addrs {
	appliance := r.Masked().Addr().Next()
	host := appliance.Next()
	return Addrs{
		Host:      netip.PrefixFrom(host, 30),
		Appliance: appliance,
		APIListen: netip.AddrPortFrom(host, apiListenPort),
		APIProxy:  netip.AddrPortFrom(appliance, apiProxyPort),
		Agent:     netip.AddrPortFrom(appliance, agentPort),
	}
}
