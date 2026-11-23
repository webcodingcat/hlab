// Package transit derives the addresses of the host, the appliance, the
// cloud API, the agent and the storage VM from the public range.
package transit

import "net/netip"

// Ports of the API backend and the console backend on the host, the API
// proxy, the console forward and the agent on the appliance; the storage
// agent listens on the same port as the agent.
const (
	apiListenPort     = 8780
	apiProxyPort      = 80
	consoleListenPort = 8781
	consoleProxyPort  = 443
	agentPort         = 9100
)

// Addrs are the transit addresses of one public range.
type Addrs struct {
	Host          netip.Prefix   // range .2/30
	Appliance     netip.Addr     // range .1
	APIListen     netip.AddrPort // .2:8780
	APIProxy      netip.AddrPort // .1:80
	ConsoleListen netip.AddrPort // .2:8781
	ConsoleProxy  netip.AddrPort // .1:443
	Agent         netip.AddrPort // .1:9100
	Storage       netip.Addr     // broadcast minus 2 of the range, 198.19.255.253 by default
	StorageAgent  netip.AddrPort // Storage:9100
}

// For returns the transit addresses of the range r: the appliance takes the
// first address after the network address, the host the second, the
// storage VM the second before the broadcast address.
func For(r netip.Prefix) Addrs {
	appliance := r.Masked().Addr().Next()
	host := appliance.Next()
	storage := Broadcast(r).Prev().Prev()
	return Addrs{
		Host:          netip.PrefixFrom(host, 30),
		Appliance:     appliance,
		APIListen:     netip.AddrPortFrom(host, apiListenPort),
		APIProxy:      netip.AddrPortFrom(appliance, apiProxyPort),
		ConsoleListen: netip.AddrPortFrom(host, consoleListenPort),
		ConsoleProxy:  netip.AddrPortFrom(appliance, consoleProxyPort),
		Agent:         netip.AddrPortFrom(appliance, agentPort),
		Storage:       storage,
		StorageAgent:  netip.AddrPortFrom(storage, agentPort),
	}
}

// Broadcast returns the last address of the IPv4 range r.
func Broadcast(r netip.Prefix) netip.Addr {
	r = r.Masked()
	b := r.Addr().As4()
	host := 32 - r.Bits()
	for i := 3; i >= 0 && host > 0; i-- {
		n := min(host, 8)
		b[i] |= byte(1<<n - 1)
		host -= n
	}
	return netip.AddrFrom4(b)
}
