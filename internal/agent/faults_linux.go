//go:build linux

package agent

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strings"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// Faults is the Applier of the network, latency and storage box faults
// (PlanFaults). Its drop rules are table inet hlab_fault (chains forward,
// input and output at priority -10, before hlab and hlab_fw), replaced
// whole on every apply and deleted when the plan has none. Its shaping is
// HTB trees with netem children on the egress of the public device and the
// t<vlan> links, changed by `tc` from the applied state it keeps in
// memory. While a loss fault is active, GRO, GSO, TSO and LRO are off on
// the public and the trunk adapter, so loss counts real packets.
type Faults struct {
	Interface string
	TrunkMAC  net.HardwareAddr

	tc          TcState // nil: unknown, the root qdiscs are deleted first
	offloads    bool    // offloads known to be in the state offloadsOff
	offloadsOff bool
}

// NewFaults returns the faults Applier for the public interface and the
// trunk adapter with trunkMAC.
func NewFaults(iface string, trunkMAC net.HardwareAddr) *Faults {
	return &Faults{Interface: iface, TrunkMAC: trunkMAC}
}

// Name implements Applier.
func (f *Faults) Name() string { return "faults" }

// Apply implements Applier.
func (f *Faults) Apply(ctx context.Context, cfg agentapi.Config) error {
	plan := PlanFaults(cfg, f.Interface)
	if err := f.applyRules(plan.Rules); err != nil {
		return fmt.Errorf("faults: %w", err)
	}

	links, err := dump(netlink.LinkList)
	if err != nil {
		return fmt.Errorf("faults: list links: %w", err)
	}
	candidates := []string{f.Interface}
	trunk := ""
	for _, l := range links {
		a := l.Attrs()
		if _, ok := agentVLAN(a.Name, "t"); ok && l.Type() == "vlan" {
			candidates = append(candidates, a.Name)
		}
		if bytes.Equal(a.HardwareAddr, f.TrunkMAC) && l.Type() == "device" {
			trunk = a.Name
		}
	}
	slices.Sort(candidates)
	if err := f.applyTc(ctx, plan.Devices, candidates); err != nil {
		return fmt.Errorf("faults: %w", err)
	}

	if !f.offloads || f.offloadsOff != plan.OffloadsOff {
		state := "on"
		if plan.OffloadsOff {
			state = "off"
		}
		f.offloads = false
		for _, dev := range []string{f.Interface, trunk} {
			if dev == "" {
				continue
			}
			args := []string{"-K", dev, "gro", state, "gso", state, "tso", state, "lro", state}
			if out, err := exec.CommandContext(ctx, "/usr/sbin/ethtool", args...).CombinedOutput(); err != nil {
				return fmt.Errorf("faults: ethtool %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
			}
		}
		f.offloads, f.offloadsOff = true, plan.OffloadsOff
	}
	return nil
}

// applyRules replaces table inet hlab_fault with rules, or deletes it
// (a leftover one too) when there is none.
func (f *Faults) applyRules(rules []FaultRule) error {
	c, err := nftables.New()
	if err != nil {
		return err
	}
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: "hlab_fault"}
	// Adding before deleting makes the delete succeed when the table does
	// not exist yet; the whole batch is one transaction.
	c.AddTable(table)
	c.DelTable(table)
	if len(rules) > 0 {
		c.AddTable(table)
		accept := nftables.ChainPolicyAccept
		chains := map[string]*nftables.Chain{}
		for name, hook := range map[string]*nftables.ChainHook{
			"forward": nftables.ChainHookForward,
			"input":   nftables.ChainHookInput,
			"output":  nftables.ChainHookOutput,
		} {
			chains[name] = c.AddChain(&nftables.Chain{
				Table:    table,
				Name:     name,
				Type:     nftables.ChainTypeFilter,
				Hooknum:  hook,
				Priority: nftables.ChainPriorityRef(-10),
				Policy:   &accept,
			})
		}
		for _, r := range rules {
			chain := chains[r.Chain]
			if chain == nil {
				return fmt.Errorf("fault rule in unknown chain %q", r.Chain)
			}
			c.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: faultRuleExprs(r)})
		}
	}
	return c.Flush()
}

// faultRuleExprs is the drop rule r: its interface names, then its IPv4
// addresses.
func faultRuleExprs(r FaultRule) []expr.Any {
	var exprs []expr.Any
	for _, m := range []struct {
		key  expr.MetaKey
		name string
	}{{expr.MetaKeyIIFNAME, r.IIf}, {expr.MetaKeyOIFNAME, r.OIf}, {metaKeySDIFNAME, r.SdIf}} {
		if m.name != "" {
			exprs = append(exprs,
				&expr.Meta{Key: m.key, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(m.name)},
			)
		}
	}
	if r.Saddr.IsValid() || r.Daddr.IsValid() {
		exprs = append(exprs,
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
		)
	}
	if r.Saddr.IsValid() {
		exprs = append(exprs,
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: r.Saddr.AsSlice()},
		)
	}
	if r.Daddr.IsValid() {
		exprs = append(exprs,
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: r.Daddr.AsSlice()},
		)
	}
	return append(exprs, &expr.Verdict{Kind: expr.VerdictDrop})
}

// applyTc runs the tc commands that turn the kept state into want. A
// device that is gone took its qdiscs with it. After a failed command the
// state is unknown again, so the next apply starts from scratch.
func (f *Faults) applyTc(ctx context.Context, want map[string][]NetemClass, candidates []string) error {
	have := f.tc
	if have != nil {
		have = TcState{}
		for dev, cs := range f.tc {
			if slices.Contains(candidates, dev) {
				have[dev] = cs
			}
		}
	}
	cmds, next := TcCommands(have, want, candidates)
	f.tc = nil
	for _, args := range cmds {
		out, err := exec.CommandContext(ctx, "tc", args...).CombinedOutput()
		if err == nil {
			continue
		}
		// Deleting a root qdisc that is not there (or the device's default
		// one, with handle zero) is no error.
		if len(args) > 1 && args[1] == "del" && (bytes.Contains(out, []byte("No such file")) || bytes.Contains(out, []byte("handle of zero"))) {
			continue
		}
		return fmt.Errorf("tc %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	f.tc = next
	return nil
}
