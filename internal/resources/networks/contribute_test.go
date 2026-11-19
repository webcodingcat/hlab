package networks_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// testContribute holds the rows of contribute_test.go; TestNetworks runs
// them.
func testContribute(t *testing.T) {
	t.Run("NET-04", func(t *testing.T) {
		e := newNetEnv(t)
		b1 := netBody("n1", "10.0.0.0/16", "10.0.0.0/24")
		b1["routes"] = []any{route("10.99.0.0/24", "10.0.0.3")}
		n1 := createNetwork(t, e, b1)
		n2 := createNetwork(t, e, netBody("n2", "172.16.0.0/16", "172.16.0.0/24"))

		a := createServer(t, e, "a", "fsn1", nil)
		b := createServer(t, e, "b", "fsn1", nil)
		c := createServer(t, e, "c", "fsn1", nil)
		d := createServer(t, e, "d", "fsn1", nil)
		attach(t, e, a, n1.ID, map[string]any{"ip": "10.0.0.2", "alias_ips": []string{"10.0.0.20"}})
		attach(t, e, b, n1.ID, map[string]any{"ip": "10.0.0.3"})
		attach(t, e, c, n1.ID, map[string]any{"ip": "10.0.0.4"})
		attach(t, e, d, n2.ID, map[string]any{"ip": "172.16.0.2"})
		macs := map[int64]string{
			a: mustPrivateNet(t, e, a, n1.ID).MACAddress,
			b: mustPrivateNet(t, e, b, n1.ID).MACAddress,
			d: mustPrivateNet(t, e, d, n2.ID).MACAddress,
		}

		var vlan1, vlan2 int
		err := e.Store.Tx(func(tx store.Tx) error {
			r1, err := networks.Load(tx, e.Project.ID, n1.ID)
			if err != nil {
				return err
			}
			r2, err := networks.Load(tx, e.Project.ID, n2.ID)
			if err != nil {
				return err
			}
			vlan1, vlan2 = r1.Hlab.VLAN, r2.Hlab.VLAN
			return servers.SetStatus(tx, c, servers.StatusDeleting)
		})
		if err != nil {
			t.Fatalf("load networks and mark server %d deleting: %v", c, err)
		}
		for _, v := range []int{vlan1, vlan2} {
			if v < 2 || v > 4092 {
				t.Errorf("network VLAN %d, want 2..4092", v)
			}
		}
		if vlan1 == vlan2 {
			t.Errorf("both networks on VLAN %d", vlan1)
		}

		doc, err := e.contribute()
		if err != nil {
			t.Fatalf("contribute agent document: %v", err)
		}
		if len(doc.Networks) != 2 {
			t.Fatalf("agent document has %d networks, want 2: %+v", len(doc.Networks), doc.Networks)
		}
		byID := map[int64]agentapi.Network{}
		for _, nw := range doc.Networks {
			byID[nw.ID] = nw
		}

		addr := netip.MustParseAddr
		type member struct {
			id    int64
			ip    string
			alias []netip.Addr
		}
		for _, w := range []struct {
			id      int64
			vlan    int
			ipRange string
			routes  []agentapi.Route
			members []member
		}{
			{n1.ID, vlan1, "10.0.0.0/16",
				[]agentapi.Route{{Destination: netip.MustParsePrefix("10.99.0.0/24"), Gateway: addr("10.0.0.3")}},
				[]member{{a, "10.0.0.2", []netip.Addr{addr("10.0.0.20")}}, {b, "10.0.0.3", nil}}},
			{n2.ID, vlan2, "172.16.0.0/16", nil, []member{{d, "172.16.0.2", nil}}},
		} {
			nw, ok := byID[w.id]
			if !ok {
				t.Errorf("no Network %d in the agent document", w.id)
				continue
			}
			rng := netip.MustParsePrefix(w.ipRange)
			if nw.VLAN != w.vlan || nw.IPRange != rng || nw.Gateway != rng.Addr().Next() {
				t.Errorf("Network %d: VLAN %d range %s gateway %s, want %d, %s, %s",
					w.id, nw.VLAN, nw.IPRange, nw.Gateway, w.vlan, rng, rng.Addr().Next())
			}
			if !slices.Equal(nw.Routes, w.routes) {
				t.Errorf("Network %d routes %+v, want %+v", w.id, nw.Routes, w.routes)
			}
			if len(nw.Members) != len(w.members) {
				t.Errorf("Network %d members %+v, want %d (deleting server %d left out)", w.id, nw.Members, len(w.members), c)
			}
			for _, wm := range w.members {
				i := slices.IndexFunc(nw.Members, func(m agentapi.Member) bool { return m.ServerID == wm.id })
				if i < 0 {
					t.Errorf("Network %d has no member %d", w.id, wm.id)
					continue
				}
				m := nw.Members[i]
				if !strings.EqualFold(m.MAC, macs[wm.id]) || m.IP != addr(wm.ip) || !slices.Equal(m.AliasIPs, wm.alias) {
					t.Errorf("Network %d member %d: MAC %s IP %s alias %v, want %s, %s, %v",
						w.id, wm.id, m.MAC, m.IP, m.AliasIPs, macs[wm.id], wm.ip, wm.alias)
				}

				j := slices.IndexFunc(doc.Metadata, func(md agentapi.Metadata) bool {
					return md.VLAN == w.vlan && md.IP == addr(wm.ip)
				})
				if j < 0 {
					t.Errorf("no metadata entry for VLAN %d IP %s", w.vlan, wm.ip)
				} else if doc.Metadata[j].ServerID != wm.id {
					t.Errorf("metadata entry VLAN %d IP %s is server %d, want %d", w.vlan, wm.ip, doc.Metadata[j].ServerID, wm.id)
				}
			}
			for _, m := range nw.Members {
				if m.ServerID == c {
					t.Errorf("Network %d lists deleting server %d", w.id, c)
				}
			}
		}
		for _, md := range doc.Metadata {
			if md.ServerID == c {
				t.Errorf("metadata entry %+v for deleting server %d", md, c)
			}
		}

		// Every metadata entry of server a, public and private, shows n1.
		seen := 0
		for _, md := range doc.Metadata {
			if md.ServerID != a {
				continue
			}
			seen++
			i := slices.IndexFunc(md.PrivateNetworks, func(p agentapi.MetadataNetwork) bool { return p.NetworkID == n1.ID })
			if i < 0 {
				t.Errorf("metadata VLAN %d of server %d: private_networks %+v without network %d", md.VLAN, a, md.PrivateNetworks, n1.ID)
				continue
			}
			p := md.PrivateNetworks[i]
			if p.NetworkName != "n1" || p.Subnet != netip.MustParsePrefix("10.0.0.0/24") ||
				p.Gateway != addr("10.0.0.1") || p.Network != netip.MustParsePrefix("10.0.0.0/16") || p.IP != addr("10.0.0.2") {
				t.Errorf("metadata VLAN %d of server %d: private network %+v, want name n1, subnet 10.0.0.0/24, gateway 10.0.0.1, range 10.0.0.0/16, ip 10.0.0.2",
					md.VLAN, a, p)
			}
		}
		if seen < 2 {
			t.Errorf("server %d has %d metadata entries, want its public and its private one", a, seen)
		}
	})
}
