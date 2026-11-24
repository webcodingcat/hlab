package agent_test

import (
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/agentapi"
)

// faultPublic is the public device the fault plans are built for.
const faultPublic = "eth0"

// Servers of the fault fixture: A and B in fsn1, C in hel1, D of another
// project with A's private IP on another VLAN.
var (
	faultA = agentapi.FaultServer{ID: 1, PublicIPv4: addr("198.19.1.10"), Private: []agentapi.FaultPrivate{
		{VLAN: 20, IP: addr("10.0.0.2"), AliasIPs: []netip.Addr{addr("10.0.0.5")}},
	}}
	faultB = agentapi.FaultServer{ID: 2, PublicIPv4: addr("198.19.1.11"), Private: []agentapi.FaultPrivate{
		{VLAN: 20, IP: addr("10.0.0.3")},
	}}
	faultC = agentapi.FaultServer{ID: 3, PublicIPv4: addr("198.19.1.12"), Private: []agentapi.FaultPrivate{
		{VLAN: 20, IP: addr("10.0.0.4")},
		{VLAN: 40, IP: addr("10.0.2.4")},
	}}
)

// faultCfg is a document with the fixture's servers, networks and floating
// IPs (198.19.2.5 routed to A, 198.19.2.6 to B) and the given faults.
func faultCfg(faults ...agentapi.NetFault) agentapi.Config {
	return agentapi.Config{
		Schema:     agentapi.Schema,
		Generation: 1,
		Servers: []agentapi.Server{
			{ID: 1, Location: "fsn1", PublicIPv4: addr("198.19.1.10")},
			{ID: 2, Location: "fsn1", PublicIPv4: addr("198.19.1.11")},
			{ID: 3, Location: "hel1", PublicIPv4: addr("198.19.1.12")},
			{ID: 4, Location: "fsn1", PublicIPv4: addr("198.19.1.13")},
		},
		FloatingIPs: []agentapi.FloatingIP{
			{IP: addr("198.19.2.5"), Target: addr("198.19.1.10")},
			{IP: addr("198.19.2.6"), Target: addr("198.19.1.11")},
		},
		Networks: []agentapi.Network{
			{ID: 10, VLAN: 20, IPRange: netip.MustParsePrefix("10.0.0.0/16"), Gateway: addr("10.0.0.1"), Members: []agentapi.Member{
				{ServerID: 1, IP: addr("10.0.0.2"), AliasIPs: []netip.Addr{addr("10.0.0.5")}},
				{ServerID: 2, IP: addr("10.0.0.3")},
				{ServerID: 3, IP: addr("10.0.0.4")},
			}},
			{ID: 11, VLAN: 30, IPRange: netip.MustParsePrefix("10.0.0.0/16"), Gateway: addr("10.0.0.1"), Members: []agentapi.Member{
				{ServerID: 4, IP: addr("10.0.0.2")},
			}},
			{ID: 12, VLAN: 40, IPRange: netip.MustParsePrefix("10.0.0.0/16"), Gateway: addr("10.0.0.1"), Members: []agentapi.Member{
				{ServerID: 3, IP: addr("10.0.2.4")},
			}},
		},
		Faults: faults,
	}
}

func addrText(a netip.Addr) string {
	if !a.IsValid() {
		return "*"
	}
	return a.String()
}

// faultRuleKey renders a rule; empty fields and "*" match anything.
func faultRuleKey(r agent.FaultRule) string {
	return fmt.Sprintf("%s iif=%s oif=%s sdif=%s saddr=%s daddr=%s",
		r.Chain, r.IIf, r.OIf, r.SdIf, addrText(r.Saddr), addrText(r.Daddr))
}

func faultRuleKeys(rules []agent.FaultRule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = faultRuleKey(r)
	}
	slices.Sort(out)
	return out
}

// rule builds an expected rule; an empty address matches anything.
func rule(chain, iif, oif, sdif, saddr, daddr string) agent.FaultRule {
	r := agent.FaultRule{Chain: chain, IIf: iif, OIf: oif, SdIf: sdif}
	if saddr != "" {
		r.Saddr = addr(saddr)
	}
	if daddr != "" {
		r.Daddr = addr(daddr)
	}
	return r
}

// publicCut is the cut of one public address on the public device.
func publicCut(a string) []agent.FaultRule {
	return []agent.FaultRule{
		rule("forward", faultPublic, "", "", a, ""),
		rule("forward", "", faultPublic, "", "", a),
		rule("input", faultPublic, "", "", a, ""),
		rule("output", "", faultPublic, "", "", a),
	}
}

// privateCut is the cut of one private address on its VLAN.
func privateCut(vlan int, a string) []agent.FaultRule {
	vrf, trunk := fmt.Sprintf("vrf%d", vlan), fmt.Sprintf("t%d", vlan)
	return []agent.FaultRule{
		rule("forward", vrf, "", "", a, ""),
		rule("forward", "", trunk, "", "", a),
		rule("input", vrf, "", trunk, a, ""),
		rule("output", "", trunk, "", "", a),
	}
}

// classKey renders a class without its Key and Minor, flows sorted.
func classKey(c agent.NetemClass) string {
	flows := make([]string, len(c.Flows))
	for i, f := range c.Flows {
		flows[i] = addrText(f.Src) + ">" + addrText(f.Dst)
	}
	slices.Sort(flows)
	return fmt.Sprintf("delay=%dus jitter=%dus loss=%d%% seed=%d flows=%s",
		c.Netem.DelayUs, c.Netem.JitterUs, c.Netem.LossPercent, c.Netem.Seed, strings.Join(flows, ","))
}

// deviceKeys renders the plan's classes per device; devices without a
// class are left out.
func deviceKeys(devices map[string][]agent.NetemClass) map[string][]string {
	out := map[string][]string{}
	for dev, classes := range devices {
		if len(classes) == 0 {
			continue
		}
		keys := make([]string, len(classes))
		for i, c := range classes {
			keys[i] = classKey(c)
		}
		slices.Sort(keys)
		out[dev] = keys
	}
	return out
}

// class builds an expected class rendering; flows are "src>dst" with "*"
// for any source.
func class(delayUs, jitterUs, loss int, seed uint64, flows ...string) string {
	slices.Sort(flows)
	return fmt.Sprintf("delay=%dus jitter=%dus loss=%d%% seed=%d flows=%s",
		delayUs, jitterUs, loss, seed, strings.Join(flows, ","))
}

func classCount(devices map[string][]agent.NetemClass) int {
	n := 0
	for _, cs := range devices {
		n += len(cs)
	}
	return n
}

func checkRules(t *testing.T, plan agent.FaultPlan, want []agent.FaultRule) {
	t.Helper()
	if got, w := faultRuleKeys(plan.Rules), faultRuleKeys(want); !slices.Equal(got, w) {
		t.Errorf("rules:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(w, "\n     "))
	}
}

func checkDevices(t *testing.T, plan agent.FaultPlan, want map[string][]string) {
	t.Helper()
	for _, cs := range want {
		slices.Sort(cs)
	}
	if got := deviceKeys(plan.Devices); !reflect.DeepEqual(got, want) {
		t.Errorf("classes per device:\n got %v\nwant %v", got, want)
	}
}

// testFaultCut: a network fault with 100 % cuts the chosen traffic of the
// server with drop rules, pinned to the public device or to the VLAN.
func testFaultCut(t *testing.T) {
	public := slices.Concat(publicCut("198.19.1.10"), publicCut("198.19.2.5"))
	private := slices.Concat(privateCut(20, "10.0.0.2"), privateCut(20, "10.0.0.5"))
	for _, tc := range []struct {
		traffic string
		want    []agent.FaultRule
	}{
		{"public", public},
		{"private", private},
		{"all", slices.Concat(public, private)},
	} {
		t.Run(tc.traffic, func(t *testing.T) {
			server := faultA
			plan := agent.PlanFaults(faultCfg(agentapi.NetFault{
				ID: 5, Kind: "network", Server: &server, Traffic: tc.traffic, LossPercent: 100,
			}), faultPublic)
			checkRules(t, plan, tc.want)
			for _, r := range plan.Rules {
				if r.Chain == "output" && (r.SdIf != "" || r.IIf != "") {
					t.Errorf("output rule %s has sdifname or iifname", faultRuleKey(r))
				}
			}
			if n := classCount(plan.Devices); n != 0 {
				t.Errorf("cut has %d netem classes, want none: %v", n, deviceKeys(plan.Devices))
			}
		})
	}
}

// testFaultLoss: a network fault below 100 % is one netem class on the
// chosen device for packets to the server; it turns offloads off, a cut or
// a latency does not.
func testFaultLoss(t *testing.T) {
	for _, tc := range []struct {
		traffic string
		want    map[string][]string
	}{
		{"public", map[string][]string{
			faultPublic: {class(0, 0, 20, 42, "*>198.19.1.10", "*>198.19.2.5")},
		}},
		{"private", map[string][]string{
			"t20": {class(0, 0, 20, 42, "*>10.0.0.2", "*>10.0.0.5")},
		}},
	} {
		t.Run(tc.traffic, func(t *testing.T) {
			server := faultA
			plan := agent.PlanFaults(faultCfg(agentapi.NetFault{
				ID: 5, Kind: "network", Server: &server, Traffic: tc.traffic, LossPercent: 20, Seed: 42,
			}), faultPublic)
			checkDevices(t, plan, tc.want)
			checkRules(t, plan, nil)
			if !plan.OffloadsOff {
				t.Error("OffloadsOff = false with a loss fault, want true")
			}
		})
	}

	server := faultA
	cut := agent.PlanFaults(faultCfg(agentapi.NetFault{
		ID: 5, Kind: "network", Server: &server, Traffic: "all", LossPercent: 100,
	}), faultPublic)
	if cut.OffloadsOff {
		t.Error("OffloadsOff = true with only a cut, want false")
	}
	latency := agent.PlanFaults(faultCfg(agentapi.NetFault{
		ID: 7, Kind: "latency", Server: &server, DelayMs: 100,
	}), faultPublic)
	if latency.OffloadsOff {
		t.Error("OffloadsOff = true with only a latency fault, want false")
	}
}

// testFaultMerge: a loss and a latency fault on the same server share one
// class per flow set; a class without loss takes the latency fault's ID as
// seed; the plan is deterministic.
func testFaultMerge(t *testing.T) {
	a1, a2 := faultA, faultA
	cfg := faultCfg(
		agentapi.NetFault{ID: 5, Kind: "network", Server: &a1, Traffic: "public", LossPercent: 20, Seed: 42},
		agentapi.NetFault{ID: 7, Kind: "latency", Server: &a2, DelayMs: 100, JitterMs: 20},
	)
	plan := agent.PlanFaults(cfg, faultPublic)
	checkDevices(t, plan, map[string][]string{
		faultPublic: {class(100000, 20000, 20, 42, "*>198.19.1.10", "*>198.19.2.5")},
		"t20":       {class(100000, 20000, 0, 7, "*>10.0.0.2", "*>10.0.0.5")},
	})
	if again := agent.PlanFaults(cfg, faultPublic); !reflect.DeepEqual(plan, again) {
		t.Errorf("same document, different plans:\n%+v\n%+v", plan, again)
	}
}

// testFaultLocations: a latency between fsn1 and hel1 shapes, with half
// the delay and jitter, packets of each direction between the servers of
// the two locations, public with public and private within the VLAN.
func testFaultLocations(t *testing.T) {
	cfg := faultCfg(agentapi.NetFault{
		ID: 9, Kind: "latency", From: []agentapi.FaultServer{faultA, faultB}, To: []agentapi.FaultServer{faultC},
		DelayMs: 100, JitterMs: 20,
	})
	cfg.FloatingIPs = nil
	plan := agent.PlanFaults(cfg, faultPublic)
	checkDevices(t, plan, map[string][]string{
		faultPublic: {
			class(50000, 10000, 0, 9, "198.19.1.10>198.19.1.12", "198.19.1.11>198.19.1.12"),
			class(50000, 10000, 0, 9, "198.19.1.12>198.19.1.10"),
			class(50000, 10000, 0, 9, "198.19.1.12>198.19.1.11"),
		},
		"t20": {
			class(50000, 10000, 0, 9, "10.0.0.2>10.0.0.4", "10.0.0.5>10.0.0.4", "10.0.0.3>10.0.0.4"),
			class(50000, 10000, 0, 9, "10.0.0.4>10.0.0.2", "10.0.0.4>10.0.0.5"),
			class(50000, 10000, 0, 9, "10.0.0.4>10.0.0.3"),
		},
	})
	checkRules(t, plan, nil)
}

// testFaultServerLatency: a latency on a server delays packets to each of
// its addresses from any source on every device it is on.
func testFaultServerLatency(t *testing.T) {
	server := faultC
	plan := agent.PlanFaults(faultCfg(agentapi.NetFault{
		ID: 8, Kind: "latency", Server: &server, DelayMs: 100,
	}), faultPublic)
	checkDevices(t, plan, map[string][]string{
		faultPublic: {class(100000, 0, 0, 8, "*>198.19.1.12")},
		"t20":       {class(100000, 0, 0, 8, "*>10.0.0.4")},
		"t40":       {class(100000, 0, 0, 8, "*>10.0.2.4")},
	})
	checkRules(t, plan, nil)
}

// testFaultBox: storage_box_unavailable drops forwarded packets to and
// from the box's IP on the public device.
func testFaultBox(t *testing.T) {
	plan := agent.PlanFaults(faultCfg(agentapi.NetFault{
		ID: 11, Kind: "storage_box_unavailable", BoxIP: addr("198.19.3.4"),
	}), faultPublic)
	checkRules(t, plan, []agent.FaultRule{
		rule("forward", "", faultPublic, "", "", "198.19.3.4"),
		rule("forward", faultPublic, "", "", "198.19.3.4", ""),
	})
	if n := classCount(plan.Devices); n != 0 {
		t.Errorf("box fault has %d netem classes, want none: %v", n, deviceKeys(plan.Devices))
	}
}

// testFaultEmptyPlan: a document without faults plans nothing.
func testFaultEmptyPlan(t *testing.T) {
	plan := agent.PlanFaults(faultCfg(), faultPublic)
	if len(plan.Rules) != 0 || len(plan.Devices) != 0 || plan.OffloadsOff {
		t.Errorf("plan without faults = %+v, want no rule, no device, OffloadsOff false", plan)
	}
}

var tcCandidates = []string{faultPublic, "t20"}

// tcArg returns the argument after key in a tc command, or "".
func tcArg(cmd []string, key string) string {
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] == key {
			return cmd[i+1]
		}
	}
	return ""
}

func tcHas(cmd []string, tokens ...string) bool {
	for _, tok := range tokens {
		if !slices.Contains(cmd, tok) {
			return false
		}
	}
	return true
}

// tcRefs reports whether a command names class 1:<minor> or qdisc <minor>:.
func tcRefs(cmd []string, minor uint16) bool {
	refs := []string{
		fmt.Sprintf("1:%x", minor), fmt.Sprintf("1:0x%x", minor),
		fmt.Sprintf("%x:", minor), fmt.Sprintf("0x%x:", minor),
	}
	return slices.ContainsFunc(cmd, func(tok string) bool { return slices.Contains(refs, tok) })
}

func tcText(cmds [][]string) string {
	lines := make([]string, len(cmds))
	for i, c := range cmds {
		lines[i] = strings.Join(c, " ")
	}
	return strings.Join(lines, "\n  ")
}

func tcMinor(t *testing.T, st agent.TcState, dev, key string) uint16 {
	t.Helper()
	for _, c := range st[dev] {
		if c.Key == key {
			return c.Minor
		}
	}
	t.Fatalf("state %v has no class %q on %s", st, key, dev)
	return 0
}

// tcApplied is the state after the classes of want were applied to a
// device set without fault qdiscs.
func tcApplied(t *testing.T, want map[string][]agent.NetemClass) agent.TcState {
	t.Helper()
	_, st := agent.TcCommands(agent.TcState{}, want, tcCandidates)
	return st
}

func netemClass(key string, n agent.Netem, dsts ...string) agent.NetemClass {
	c := agent.NetemClass{Key: key, Netem: n}
	for _, d := range dsts {
		c.Flows = append(c.Flows, agent.Flow{Dst: addr(d)})
	}
	return c
}

// testTcCommands: the tc commands turn the applied state into the wanted
// one, touch only what changed and pass the seed only for a new or changed
// loss.
func testTcCommands(t *testing.T) {
	k1 := netemClass("k1", agent.Netem{LossPercent: 20, Seed: 42}, "198.19.1.10")
	k2 := netemClass("k2", agent.Netem{DelayUs: 50000, Seed: 9}, "198.19.1.11")
	one := map[string][]agent.NetemClass{faultPublic: {k1}}

	for _, tc := range []struct {
		name  string
		have  func(t *testing.T) agent.TcState
		want  map[string][]agent.NetemClass
		check func(t *testing.T, cmds [][]string, have, st agent.TcState)
	}{
		{
			name: "unknown_state_empty_want",
			have: func(*testing.T) agent.TcState { return nil },
			want: map[string][]agent.NetemClass{},
			check: func(t *testing.T, cmds [][]string, _, st agent.TcState) {
				var got []string
				for _, c := range cmds {
					got = append(got, strings.Join(c, " "))
				}
				slices.Sort(got)
				want := []string{"qdisc del dev eth0 root", "qdisc del dev t20 root"}
				if !slices.Equal(got, want) {
					t.Errorf("commands:\n  %s\nwant\n  %s", tcText(cmds), strings.Join(want, "\n  "))
				}
				if classCount(st) != 0 {
					t.Errorf("state = %v, want empty", st)
				}
			},
		},
		{
			name: "new_class",
			have: func(*testing.T) agent.TcState { return agent.TcState{} },
			want: one,
			check: func(t *testing.T, cmds [][]string, _, st agent.TcState) {
				m := tcMinor(t, st, faultPublic, "k1")
				for _, c := range cmds {
					if tcArg(c, "dev") != faultPublic {
						t.Errorf("command on another device: %s", strings.Join(c, " "))
					}
					if c[1] == "del" {
						t.Errorf("delete in a new tree: %s", strings.Join(c, " "))
					}
				}
				for _, w := range []struct {
					what string
					ok   func(c []string) bool
				}{
					{"root htb", func(c []string) bool { return tcHas(c, "qdisc", "add", "root", "htb") }},
					{"class 1:1", func(c []string) bool { return tcHas(c, "class", "add", "1:1") }},
					{"class of k1", func(c []string) bool { return tcHas(c, "class", "add") && tcRefs(c, m) }},
					{"netem with loss and seed", func(c []string) bool {
						return tcHas(c, "qdisc", "add", "netem", "20%", "seed", "42") && tcRefs(c, m)
					}},
					{"filter to 198.19.1.10", func(c []string) bool {
						return tcHas(c, "filter", "add", "flower", "dst_ip", "198.19.1.10") && tcRefs(c, m)
					}},
				} {
					if !slices.ContainsFunc(cmds, w.ok) {
						t.Errorf("no command for %s (minor %#x) in\n  %s", w.what, m, tcText(cmds))
					}
				}
			},
		},
		{
			name: "delay_changed",
			have: func(t *testing.T) agent.TcState { return tcApplied(t, one) },
			want: map[string][]agent.NetemClass{faultPublic: {
				netemClass("k1", agent.Netem{DelayUs: 10000, LossPercent: 20, Seed: 42}, "198.19.1.10"),
			}},
			check: func(t *testing.T, cmds [][]string, have, st agent.TcState) {
				m := tcMinor(t, have, faultPublic, "k1")
				if len(cmds) != 1 || !tcHas(cmds[0], "qdisc", "change", "netem") || !tcRefs(cmds[0], m) || tcHas(cmds[0], "seed") {
					t.Errorf("commands:\n  %s\nwant one qdisc change of netem %#x: without seed", tcText(cmds), m)
				}
			},
		},
		{
			name: "loss_changed",
			have: func(t *testing.T) agent.TcState { return tcApplied(t, one) },
			want: map[string][]agent.NetemClass{faultPublic: {
				netemClass("k1", agent.Netem{LossPercent: 30, Seed: 42}, "198.19.1.10"),
			}},
			check: func(t *testing.T, cmds [][]string, have, st agent.TcState) {
				m := tcMinor(t, have, faultPublic, "k1")
				if len(cmds) != 1 || !tcHas(cmds[0], "qdisc", "change", "netem", "30%", "seed", "42") || !tcRefs(cmds[0], m) {
					t.Errorf("commands:\n  %s\nwant one qdisc change of netem %#x: with loss 30%% and seed 42", tcText(cmds), m)
				}
			},
		},
		{
			name: "flows_changed",
			have: func(t *testing.T) agent.TcState { return tcApplied(t, one) },
			want: map[string][]agent.NetemClass{faultPublic: {
				netemClass("k1", agent.Netem{LossPercent: 20, Seed: 42}, "198.19.1.10", "198.19.2.5"),
			}},
			check: func(t *testing.T, cmds [][]string, have, st agent.TcState) {
				m := tcMinor(t, have, faultPublic, "k1")
				for _, c := range cmds {
					if c[0] != "filter" {
						t.Errorf("command other than a filter: %s", strings.Join(c, " "))
					}
				}
				if !slices.ContainsFunc(cmds, func(c []string) bool {
					return tcHas(c, "filter", "add", "dst_ip", "198.19.2.5") && tcRefs(c, m)
				}) {
					t.Errorf("no filter to 198.19.2.5 for class %#x in\n  %s", m, tcText(cmds))
				}
				if got := tcMinor(t, st, faultPublic, "k1"); got != m {
					t.Errorf("minor of k1 = %#x, want %#x", got, m)
				}
			},
		},
		{
			name: "class_gone",
			have: func(t *testing.T) agent.TcState {
				return tcApplied(t, map[string][]agent.NetemClass{faultPublic: {k1, k2}})
			},
			want: map[string][]agent.NetemClass{faultPublic: {k2}},
			check: func(t *testing.T, cmds [][]string, have, st agent.TcState) {
				m1, m2 := tcMinor(t, have, faultPublic, "k1"), tcMinor(t, have, faultPublic, "k2")
				for _, c := range cmds {
					if c[1] != "del" || tcRefs(c, m2) || tcHas(c, "root") {
						t.Errorf("command beyond deleting k1: %s", strings.Join(c, " "))
					}
				}
				if !slices.ContainsFunc(cmds, func(c []string) bool { return c[0] == "filter" }) {
					t.Errorf("no filter deleted in\n  %s", tcText(cmds))
				}
				if !slices.ContainsFunc(cmds, func(c []string) bool { return tcHas(c, "class", "del") && tcRefs(c, m1) }) {
					t.Errorf("class %#x not deleted in\n  %s", m1, tcText(cmds))
				}
				if len(st[faultPublic]) != 1 || tcMinor(t, st, faultPublic, "k2") != m2 {
					t.Errorf("state on %s = %v, want only k2 with minor %#x", faultPublic, st[faultPublic], m2)
				}
			},
		},
		{
			name: "last_class_gone",
			have: func(t *testing.T) agent.TcState {
				return tcApplied(t, map[string][]agent.NetemClass{faultPublic: {k1}, "t20": {k2}})
			},
			want: map[string][]agent.NetemClass{"t20": {k2}},
			check: func(t *testing.T, cmds [][]string, have, st agent.TcState) {
				m2 := tcMinor(t, have, "t20", "k2")
				for _, c := range cmds {
					if tcArg(c, "dev") != faultPublic || c[1] != "del" {
						t.Errorf("command beyond deleting on %s: %s", faultPublic, strings.Join(c, " "))
					}
				}
				if !slices.ContainsFunc(cmds, func(c []string) bool {
					return slices.Equal(c, []string{"qdisc", "del", "dev", faultPublic, "root"})
				}) {
					t.Errorf("root qdisc of %s not deleted in\n  %s", faultPublic, tcText(cmds))
				}
				if len(st[faultPublic]) != 0 {
					t.Errorf("state on %s = %v, want none", faultPublic, st[faultPublic])
				}
				if tcMinor(t, st, "t20", "k2") != m2 {
					t.Errorf("minor of k2 on t20 changed")
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			have := tc.have(t)
			cmds, st := agent.TcCommands(have, tc.want, tcCandidates)
			tc.check(t, cmds, have, st)
			if again, _ := agent.TcCommands(st, tc.want, tcCandidates); len(again) != 0 {
				t.Errorf("same state again gives commands:\n  %s", tcText(again))
			}
		})
	}
}
