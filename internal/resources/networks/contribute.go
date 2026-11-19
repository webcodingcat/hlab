package networks

import (
	"net/netip"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Contribute implements resource.NetContributor: one Network per network
// with its members (servers not being deleted), a metadata entry per member
// on the network's VLAN, and the private networks of every server's
// metadata. It runs after the servers module, whose public metadata
// entries it completes.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	nets, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	recs, err := store.ListAll[servers.Record](tx, servers.Kind)
	if err != nil {
		return err
	}
	byID := map[int64]Record{}
	for _, n := range nets {
		byID[n.ID] = n
	}

	index := map[int64]int{} // network ID -> index in doc.Networks
	for _, n := range nets {
		rng, err := netip.ParsePrefix(n.IPRange)
		if err != nil {
			continue
		}
		nw := agentapi.Network{ID: n.ID, VLAN: n.Hlab.VLAN, IPRange: rng, Gateway: Gateway(rng)}
		for _, r := range n.Routes {
			dst, err1 := netip.ParsePrefix(r.Destination)
			gw, err2 := netip.ParseAddr(r.Gateway)
			if err1 == nil && err2 == nil {
				nw.Routes = append(nw.Routes, agentapi.Route{Destination: dst, Gateway: gw})
			}
		}
		index[n.ID] = len(doc.Networks)
		doc.Networks = append(doc.Networks, nw)
	}

	private := map[int64][]agentapi.MetadataNetwork{} // per server
	var entries []agentapi.Metadata                    // the members' private entries
	for _, r := range recs {
		if r.Status == servers.StatusDeleting || len(r.PrivateNet) == 0 {
			continue
		}
		base, err := m.d.Servers.Metadata(tx, r)
		if err != nil {
			return err
		}
		start := len(entries)
		for i, p := range r.PrivateNet {
			n, ok := byID[p.Network]
			j, shown := index[p.Network]
			ip, err := netip.ParseAddr(p.IP)
			if !ok || !shown || err != nil {
				continue
			}
			var alias []netip.Addr
			for _, s := range p.AliasIPs {
				if a, err := netip.ParseAddr(s); err == nil {
					alias = append(alias, a)
				}
			}
			nw := &doc.Networks[j]
			nw.Members = append(nw.Members, agentapi.Member{ServerID: r.ID, MAC: p.MACAddress, IP: ip, AliasIPs: alias})
			private[r.ID] = append(private[r.ID], agentapi.MetadataNetwork{
				IP:           ip,
				AliasIPs:     alias,
				InterfaceNum: i + 1,
				MAC:          p.MACAddress,
				NetworkID:    n.ID,
				NetworkName:  n.Name,
				Network:      nw.IPRange,
				Subnet:       subnetOf(n, ip),
				Gateway:      nw.Gateway,
			})
			md := base
			md.VLAN, md.IP = nw.VLAN, ip
			entries = append(entries, md)
		}
		for k := start; k < len(entries); k++ {
			entries[k].PrivateNetworks = private[r.ID]
		}
	}
	for k := range doc.Metadata {
		if p, ok := private[doc.Metadata[k].ServerID]; ok {
			doc.Metadata[k].PrivateNetworks = p
		}
	}
	doc.Metadata = append(doc.Metadata, entries...)
	return nil
}

// subnetOf is the subnet of n holding ip, or the invalid prefix.
func subnetOf(n Record, ip netip.Addr) netip.Prefix {
	for _, s := range n.Subnets {
		if p, err := netip.ParsePrefix(s.IPRange); err == nil && p.Contains(ip) {
			return p
		}
	}
	return netip.Prefix{}
}
