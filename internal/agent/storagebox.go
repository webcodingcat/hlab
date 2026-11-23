package agent

import (
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/transit"
)

// boxFilter is what the firewall enforces for the storage boxes: sources
// outside Public or inside HostNet are external, and external sources never
// reach the box IPs in Closed.
type boxFilter struct {
	Public  netip.Prefix // the public range, masked
	HostNet netip.Prefix // the host's transit /30, masked
	All     []netip.Addr // every box IP
	Closed  []netip.Addr // box IPs neither the box nor a subaccount opens externally
}

// compileStorageBoxes returns the storage box filter of a document.
func compileStorageBoxes(cfg agentapi.Config) boxFilter {
	f := boxFilter{Public: cfg.PublicRange.Masked()}
	if f.Public.Addr().Is4() {
		f.HostNet = transit.For(f.Public).Host.Masked()
	}
	for _, b := range cfg.StorageBoxes {
		if !b.IP.Is4() {
			continue
		}
		f.All = append(f.All, b.IP)
		if !b.ReachableExternally {
			f.Closed = append(f.Closed, b.IP)
		}
	}
	return f
}

// external reports whether src counts as an external source: the host and
// WSL2, that is outside the public range or inside the host's transit /30.
func (f boxFilter) external(src netip.Addr) bool {
	return !f.Public.Contains(src) || (f.HostNet.IsValid() && f.HostNet.Contains(src))
}

// StorageBoxDropped reports whether the appliance drops forwarded traffic
// from src to dst because dst is a box IP that is not reachable externally
// and src is external.
func StorageBoxDropped(cfg agentapi.Config, src, dst netip.Addr) bool {
	f := compileStorageBoxes(cfg)
	return slices.Contains(f.Closed, dst) && f.external(src)
}
