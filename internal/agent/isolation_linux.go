//go:build linux

package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// metadataAddr is the address of the metadata service.
var metadataAddr = netip.MustParseAddr("169.254.169.254")

// Isolation is the Applier of the isolation backstop in table inet hlab.
// The private VLAN already keeps guests from talking to each other
// directly, and guest-to-guest traffic routed by the appliance is
// legitimate. The backstop drops forwarded IPv4 packets from a known guest
// MAC whose source address is not one of that guest's addresses (its public
// IPv4 and the floating IPs assigned to it). Traffic from other MACs (the
// host, which carries internet and WSL2 traffic) passes. The same check
// drops TCP to the metadata service (169.254.169.254:80) on the input path,
// since that service selects the server by source address. Every apply
// replaces the whole table atomically; other tables are never touched.
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
	if err := c.Flush(); err != nil {
		return fmt.Errorf("isolation: %w", err)
	}
	return nil
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
