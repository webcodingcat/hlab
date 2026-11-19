//go:build linux

package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// Firewall is the Applier of the server firewalls in table inet hlab_fw.
// Its forward chain runs at filter priority + 10, beside the isolation
// table, and only drops. It is stateful: packets of established or related
// connections pass. A forwarded IPv4 packet that leaves on the public
// interface to a filtered address jumps to the server's in-chain, one that
// arrives on it from a filtered address jumps to the server's out-chain
// (only when the server's outbound traffic is restricted). A server chain
// returns for a packet one of its rules allows and drops everything else;
// returning lets a packet between two filtered servers pass both checks.
// Private networks never use the public interface and are never filtered.
// Every apply replaces the whole table atomically.
type Firewall struct {
	Interface string
}

// NewFirewall returns the firewall Applier for the public interface.
func NewFirewall(iface string) *Firewall { return &Firewall{Interface: iface} }

// Name implements Applier.
func (f *Firewall) Name() string { return "firewall" }

// Apply implements Applier.
func (f *Firewall) Apply(_ context.Context, cfg agentapi.Config) error {
	filters, err := CompileFirewalls(cfg)
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	c, err := nftables.New()
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: "hlab_fw"}
	// Adding before deleting makes the delete succeed when the table does
	// not exist yet; the whole batch is one transaction.
	c.AddTable(table)
	c.DelTable(table)
	c.AddTable(table)

	accept := nftables.ChainPolicyAccept
	forward := c.AddChain(&nftables.Chain{
		Table:    table,
		Name:     "forward",
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityRef(*nftables.ChainPriorityFilter + 10),
		Policy:   &accept,
	})
	if len(filters) > 0 {
		// ct state established,related accept
		c.AddRule(&nftables.Rule{
			Table: table,
			Chain: forward,
			Exprs: []expr.Any{
				&expr.Ct{Key: expr.CtKeySTATE, Register: 1},
				&expr.Bitwise{
					SourceRegister: 1,
					DestRegister:   1,
					Len:            4,
					Mask:           binaryutil.NativeEndian.PutUint32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
					Xor:            make([]byte, 4),
				},
				&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: make([]byte, 4)},
				&expr.Verdict{Kind: expr.VerdictAccept},
			},
		})
	}
	pub := ifname(f.Interface)
	for _, sf := range filters {
		id := strconv.FormatInt(sf.ServerID, 10)
		in := c.AddChain(&nftables.Chain{Table: table, Name: "in" + id})
		addServerChain(c, table, in, sf.In, 12)
		var out *nftables.Chain
		if sf.RestrictOut {
			out = c.AddChain(&nftables.Chain{Table: table, Name: "out" + id})
			addServerChain(c, table, out, sf.Out, 16)
		}
		for _, a := range sf.Addrs {
			// oifname <pub> meta nfproto ipv4 ip daddr <addr> jump in<id>
			c.AddRule(addrJump(table, forward, expr.MetaKeyOIFNAME, pub, 16, a, in.Name))
			if out != nil {
				// iifname <pub> meta nfproto ipv4 ip saddr <addr> jump out<id>
				c.AddRule(addrJump(table, forward, expr.MetaKeyIIFNAME, pub, 12, a, out.Name))
			}
		}
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	return nil
}

// addrJump is the rule that sends packets on the public interface with the
// address a at offset off of the IPv4 header to the chain target.
func addrJump(table *nftables.Table, chain *nftables.Chain, key expr.MetaKey, iface []byte, off uint32, a netip.Addr, target string) *nftables.Rule {
	return &nftables.Rule{
		Table: table,
		Chain: chain,
		Exprs: []expr.Any{
			&expr.Meta{Key: key, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: iface},
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: off, Len: 4},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: a.AsSlice()},
			&expr.Verdict{Kind: expr.VerdictJump, Chain: target},
		},
	}
}

// addServerChain fills a server chain: per rule and prefix a return for the
// matching packets (the remote address at offset off of the IPv4 header),
// then a drop.
func addServerChain(c *nftables.Conn, table *nftables.Table, chain *nftables.Chain, rules []FilterRule, off uint32) {
	for _, r := range rules {
		match := []expr.Any{
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{r.Protocol}},
		}
		if r.FromPort != 0 {
			match = append(match, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2})
			if r.FromPort == r.ToPort {
				match = append(match, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(r.FromPort)})
			} else {
				match = append(match, &expr.Range{
					Op:       expr.CmpOpEq,
					Register: 1,
					FromData: binaryutil.BigEndian.PutUint16(r.FromPort),
					ToData:   binaryutil.BigEndian.PutUint16(r.ToPort),
				})
			}
		}
		for _, p := range r.Prefixes {
			exprs := append([]expr.Any{}, match...)
			if p.Bits() > 0 {
				exprs = append(exprs,
					&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: off, Len: 4},
					&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: net.CIDRMask(p.Bits(), 32), Xor: make([]byte, 4)},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: p.Addr().AsSlice()},
				)
			}
			exprs = append(exprs, &expr.Verdict{Kind: expr.VerdictReturn})
			c.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: exprs})
		}
	}
	c.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{&expr.Verdict{Kind: expr.VerdictDrop}}})
}
