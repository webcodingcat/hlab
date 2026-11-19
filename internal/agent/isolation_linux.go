//go:build linux

package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// metadataAddr is the address of the metadata service.
var metadataAddr = netip.MustParseAddr("169.254.169.254")

// metaKeySDIFNAME is NFT_META_SDIFNAME (kernel 5.1+), the name of the
// enslaved device a packet arrived on. On the input hook of a VRF, iifname
// is the VRF device; sdifname is the original ingress subinterface t<vlan>.
// Neither golang.org/x/sys nor google/nftables define the key yet.
const metaKeySDIFNAME expr.MetaKey = 34

// Isolation is the Applier of the isolation backstop in table inet hlab.
// The private VLAN already keeps guests from talking to each other
// directly, and guest-to-guest traffic routed by the appliance is
// legitimate. The backstop drops forwarded IPv4 packets from a known guest
// MAC whose source address is not one of that guest's addresses (its public
// IPv4 and the floating IPs assigned to it). Traffic from other MACs (the
// host, which carries internet and WSL2 traffic) passes. The same check
// drops TCP to the metadata service (169.254.169.254:80) on the input path,
// since that service selects the server by source address.
//
// On the subinterface t<vlan> of each private network the same backstop
// holds per member: forwarded IPv4 from a member MAC passes only with the
// member's IP, an alias IP, or a source inside the destination of a network
// route whose gateway is that member (Hetzner's unicast RPF). A raw-priority
// prerouting chain puts each t<vlan> into conntrack zone <vlan>, so
// overlapping ranges of different networks never share conntrack entries.
// TCP to the metadata service arriving on t<vlan> gets the same member
// check on the input path.
// Every apply replaces the whole table atomically; other tables are never
// touched.
type Isolation struct {
	Interface string
}

// NewIsolation returns the isolation Applier for the public interface.
func NewIsolation(iface string) *Isolation { return &Isolation{Interface: iface} }

// Name implements Applier.
func (n *Isolation) Name() string { return "isolation" }

// Apply implements Applier.
func (n *Isolation) Apply(_ context.Context, cfg agentapi.Config) error {
	macs, pairs := guestSources(cfg)

	c, err := nftables.New()
	if err != nil {
		return fmt.Errorf("isolation: %w", err)
	}
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: "hlab"}
	// Adding before deleting makes the delete succeed when the table does
	// not exist yet; the whole batch is one transaction.
	c.AddTable(table)
	c.DelTable(table)
	c.AddTable(table)

	macSet := &nftables.Set{Table: table, Name: "guest_macs", KeyType: nftables.TypeEtherAddr}
	if err := c.AddSet(macSet, macs); err != nil {
		return fmt.Errorf("isolation: %w", err)
	}
	pairType, err := nftables.ConcatSetType(nftables.TypeEtherAddr, nftables.TypeIPAddr)
	if err != nil {
		return fmt.Errorf("isolation: %w", err)
	}
	pairSet := &nftables.Set{Table: table, Name: "guest_sources", KeyType: pairType, Concatenation: true}
	if err := c.AddSet(pairSet, pairs); err != nil {
		return fmt.Errorf("isolation: %w", err)
	}

	accept := nftables.ChainPolicyAccept
	chain := c.AddChain(&nftables.Chain{
		Table:    table,
		Name:     "forward",
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &accept,
	})
	// iifname <iface> meta nfproto ipv4 ether saddr @guest_macs
	// ether saddr . ip saddr != @guest_sources drop
	c.AddRule(&nftables.Rule{
		Table: table,
		Chain: chain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(n.Interface)},
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseLLHeader, Offset: 6, Len: 6},
			&expr.Lookup{SourceRegister: 1, SetName: macSet.Name, SetID: macSet.ID},
			// The MAC stays in register 1 (32-bit registers 8 and 9, padded);
			// the address follows in 32-bit register 10.
			&expr.Payload{DestRegister: 10, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
			&expr.Lookup{SourceRegister: 1, SetName: pairSet.Name, SetID: pairSet.ID, Invert: true},
			&expr.Verdict{Kind: expr.VerdictDrop},
		},
	})

	// The metadata service answers by source address, so the same binding
	// holds on its input path:
	// iifname <iface> meta nfproto ipv4 meta l4proto tcp
	// ip daddr 169.254.169.254 tcp dport 80 ether saddr @guest_macs
	// ether saddr . ip saddr != @guest_sources drop
	input := c.AddChain(&nftables.Chain{
		Table:    table,
		Name:     "input",
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookInput,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &accept,
	})
	c.AddRule(&nftables.Rule{
		Table: table,
		Chain: input,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(n.Interface)},
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: metadataAddr.AsSlice()},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{0, 80}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseLLHeader, Offset: 6, Len: 6},
			&expr.Lookup{SourceRegister: 1, SetName: macSet.Name, SetID: macSet.ID},
			// As in the forward rule: MAC in register 1, address in 10.
			&expr.Payload{DestRegister: 10, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
			&expr.Lookup{SourceRegister: 1, SetName: pairSet.Name, SetID: pairSet.ID, Invert: true},
			&expr.Verdict{Kind: expr.VerdictDrop},
		},
	})
	if err := addPrivate(c, table, chain, input, cfg.Networks); err != nil {
		return fmt.Errorf("isolation: %w", err)
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("isolation: %w", err)
	}
	return nil
}

// addPrivate adds the per-network rules: the conntrack zones in a prerouting
// chain, and in the forward chain an accept rule per network route and the
// drop of member traffic with a foreign source, and in the input chain the
// same drop for TCP to the metadata service.
func addPrivate(c *nftables.Conn, table *nftables.Table, forward, input *nftables.Chain, networks []agentapi.Network) error {
	macs, sources := memberSources(networks)

	macType, err := nftables.ConcatSetType(nftables.TypeIFName, nftables.TypeEtherAddr)
	if err != nil {
		return err
	}
	macSet := &nftables.Set{Table: table, Name: "member_macs", KeyType: macType, Concatenation: true}
	if err := c.AddSet(macSet, macs); err != nil {
		return err
	}
	srcType, err := nftables.ConcatSetType(nftables.TypeIFName, nftables.TypeEtherAddr, nftables.TypeIPAddr)
	if err != nil {
		return err
	}
	srcSet := &nftables.Set{Table: table, Name: "member_sources", KeyType: srcType, Concatenation: true}
	if err := c.AddSet(srcSet, sources); err != nil {
		return err
	}

	accept := nftables.ChainPolicyAccept
	pre := c.AddChain(&nftables.Chain{
		Table:    table,
		Name:     "prerouting",
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRaw,
		Policy:   &accept,
	})
	for _, nw := range networks {
		sub := ifname("t" + strconv.Itoa(nw.VLAN))
		// iifname t<vlan> ct zone set <vlan>
		c.AddRule(&nftables.Rule{
			Table: table,
			Chain: pre,
			Exprs: []expr.Any{
				&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: sub},
				&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint16(uint16(nw.VLAN))},
				&expr.Ct{Key: expr.CtKeyZONE, Register: 1, SourceRegister: true},
			},
		})

		// iifname t<vlan> meta nfproto ipv4 ether saddr <gateway's MAC>
		// ip saddr <destination> accept
		for _, r := range nw.Routes {
			mac, ok := memberMAC(nw, r.Gateway)
			if !ok || !r.Destination.Addr().Is4() {
				continue
			}
			dst := r.Destination.Masked()
			mask := net.CIDRMask(dst.Bits(), 32)
			c.AddRule(&nftables.Rule{
				Table: table,
				Chain: forward,
				Exprs: []expr.Any{
					&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: sub},
					&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
					&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseLLHeader, Offset: 6, Len: 6},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: mac},
					&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
					&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: mask, Xor: make([]byte, 4)},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: dst.Addr().AsSlice()},
					&expr.Verdict{Kind: expr.VerdictAccept},
				},
			})
		}
	}

	// meta nfproto ipv4 iifname . ether saddr @member_macs
	// iifname . ether saddr . ip saddr != @member_sources drop
	c.AddRule(&nftables.Rule{
		Table: table,
		Chain: forward,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
			// The name fills 32-bit registers 8 to 11 (register 1), the MAC
			// 12 and 13 (padded), the address 14.
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Payload{DestRegister: 12, Base: expr.PayloadBaseLLHeader, Offset: 6, Len: 6},
			&expr.Lookup{SourceRegister: 1, SetName: macSet.Name, SetID: macSet.ID},
			&expr.Payload{DestRegister: 14, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
			&expr.Lookup{SourceRegister: 1, SetName: srcSet.Name, SetID: srcSet.ID, Invert: true},
			&expr.Verdict{Kind: expr.VerdictDrop},
		},
	})

	// meta nfproto ipv4 meta l4proto tcp ip daddr 169.254.169.254
	// tcp dport 80 meta sdifname . ether saddr @member_macs
	// meta sdifname . ether saddr . ip saddr != @member_sources drop
	c.AddRule(&nftables.Rule{
		Table: table,
		Chain: input,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: metadataAddr.AsSlice()},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{0, 80}},
			// Registers as in the forward rule, with the subinterface name.
			&expr.Meta{Key: metaKeySDIFNAME, Register: 1},
			&expr.Payload{DestRegister: 12, Base: expr.PayloadBaseLLHeader, Offset: 6, Len: 6},
			&expr.Lookup{SourceRegister: 1, SetName: macSet.Name, SetID: macSet.ID},
			&expr.Payload{DestRegister: 14, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
			&expr.Lookup{SourceRegister: 1, SetName: srcSet.Name, SetID: srcSet.ID, Invert: true},
			&expr.Verdict{Kind: expr.VerdictDrop},
		},
	})
	return nil
}

// memberSources returns the private set elements: every pair of
// subinterface and member MAC, and every triple of subinterface, member MAC
// and an address that member may send from (its IP and alias IPs).
func memberSources(networks []agentapi.Network) (macs, sources []nftables.SetElement) {
	for _, nw := range networks {
		sub := ifname("t" + strconv.Itoa(nw.VLAN))
		for _, m := range nw.Members {
			mac, err := net.ParseMAC(m.MAC)
			if err != nil || len(mac) != 6 {
				continue
			}
			key := append(append(append([]byte{}, sub...), mac...), 0, 0)
			macs = append(macs, nftables.SetElement{Key: key})
			for _, a := range append([]netip.Addr{m.IP}, m.AliasIPs...) {
				if a.Is4() {
					a4 := a.As4()
					sources = append(sources, nftables.SetElement{Key: append(append([]byte{}, key...), a4[:]...)})
				}
			}
		}
	}
	return macs, sources
}

// memberMAC returns the MAC of the member of nw that has the address gw.
func memberMAC(nw agentapi.Network, gw netip.Addr) (net.HardwareAddr, bool) {
	for _, m := range nw.Members {
		if m.IP != gw && !slices.Contains(m.AliasIPs, gw) {
			continue
		}
		mac, err := net.ParseMAC(m.MAC)
		return mac, err == nil && len(mac) == 6
	}
	return nil, false
}

// guestSources returns the set elements: every known guest MAC, and every
// pair of guest MAC and an address that guest may send from.
func guestSources(cfg agentapi.Config) (macs, pairs []nftables.SetElement) {
	byIP := map[netip.Addr]net.HardwareAddr{}
	for _, s := range cfg.Servers {
		mac, err := net.ParseMAC(s.MAC)
		if err != nil || len(mac) != 6 {
			continue
		}
		macs = append(macs, nftables.SetElement{Key: mac})
		if s.PublicIPv4.Is4() {
			byIP[s.PublicIPv4] = mac
			pairs = append(pairs, nftables.SetElement{Key: pairKey(mac, s.PublicIPv4)})
		}
	}
	for _, f := range cfg.FloatingIPs {
		if mac, ok := byIP[f.Target]; ok && f.IP.Is4() {
			pairs = append(pairs, nftables.SetElement{Key: pairKey(mac, f.IP)})
		}
	}
	return macs, pairs
}

// pairKey is the key of an ether_addr . ipv4_addr element: the MAC padded
// to 8 bytes, then the address.
func pairKey(mac net.HardwareAddr, ip netip.Addr) []byte {
	a := ip.As4()
	k := make([]byte, 0, 12)
	k = append(k, mac...)
	k = append(k, 0, 0)
	return append(k, a[:]...)
}

// ifname returns an interface name as nftables compares it.
func ifname(name string) []byte {
	b := make([]byte, unix.IFNAMSIZ)
	copy(b, name)
	return b
}
