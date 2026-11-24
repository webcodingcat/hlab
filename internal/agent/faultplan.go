package agent

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// FaultPlan is what the faults applier enforces for one document.
type FaultPlan struct {
	Rules       []FaultRule             // table inet hlab_fault, every rule drops
	Devices     map[string][]NetemClass // egress device ("eth0", "t<vlan>") -> classes; absent: no fault qdisc
	OffloadsOff bool                    // a loss fault below 100 % is active
}

// FaultRule is one drop rule; empty strings and invalid addresses match
// anything. SdIf is never set in chain "output" (E40).
type FaultRule struct {
	Chain        string // "forward" | "input" | "output"
	IIf, OIf     string // iifname, oifname
	SdIf         string // meta sdifname
	Saddr, Daddr netip.Addr
}

// NetemClass is one HTB class with its netem child and flower filters.
// Key names the flows it shapes (destination server and source group:
// all sources, or the servers of one locations fault), stable across
// documents, so an unchanged class is never touched.
type NetemClass struct {
	Key   string
	Minor uint16 // class 1:<Minor>, netem <Minor>:; set by TcCommands
	Netem Netem
	Flows []Flow
}

// Netem settings; Seed is always set (a netem without loss takes the
// latency fault's ID, E42).
type Netem struct {
	DelayUs, JitterUs int
	LossPercent       int
	Seed              uint64
}

// Flow selects packets to Dst, from Src (invalid: any source; such
// filters get a lower priority than source/destination pairs).
type Flow struct{ Src, Dst netip.Addr }

// Fault kinds of the agent document.
const (
	faultNetwork = "network"
	faultLatency = "latency"
	faultBox     = "storage_box_unavailable"
)

// faultDev is a device with the addresses of one server on it.
type faultDev struct {
	dev   string
	addrs []netip.Addr
}

// serverShape is what the server-level faults (loss, latency on the
// server) put on the packets to one server on one device.
type serverShape struct {
	delayUs, jitterUs int
	loss              int
	lossSeed          uint64
	latencyID         int64
	flows             []Flow
}

func (s *serverShape) seed() uint64 {
	if s.loss > 0 {
		return s.lossSeed
	}
	return uint64(s.latencyID)
}

type shapeKey struct {
	dev    string
	server int64
}

// faultPlanner builds a FaultPlan from one document.
type faultPlanner struct {
	public   string
	floating map[netip.Addr][]netip.Addr // server public IPv4 -> floating IPs routed to it
	plan     FaultPlan
}

// PlanFaults builds the plan of cfg.Faults; public is the public device.
func PlanFaults(cfg agentapi.Config, public string) FaultPlan {
	p := &faultPlanner{public: public, floating: map[netip.Addr][]netip.Addr{}}
	for _, fip := range cfg.FloatingIPs {
		if fip.IP.Is4() && fip.Target.IsValid() {
			p.floating[fip.Target] = append(p.floating[fip.Target], fip.IP)
		}
	}

	shapes := map[shapeKey]*serverShape{}
	shape := func(dev string, server int64) *serverShape {
		k := shapeKey{dev, server}
		if shapes[k] == nil {
			shapes[k] = &serverShape{}
		}
		return shapes[k]
	}

	// Cuts, box drops and the server-level shaping first; locations
	// latencies carry the destination's server-level shaping.
	for _, f := range cfg.Faults {
		switch f.Kind {
		case faultNetwork:
			if f.Server == nil || f.LossPercent <= 0 {
				continue
			}
			if f.LossPercent >= 100 {
				p.cut(*f.Server, f.Traffic)
				continue
			}
			p.plan.OffloadsOff = true
			for _, d := range p.devices(*f.Server, f.Traffic) {
				s := shape(d.dev, f.Server.ID)
				s.loss, s.lossSeed = f.LossPercent, f.Seed
				for _, a := range d.addrs {
					s.flows = append(s.flows, Flow{Dst: a})
				}
			}
		case faultLatency:
			if f.Server == nil {
				continue
			}
			for _, d := range p.devices(*f.Server, "all") {
				s := shape(d.dev, f.Server.ID)
				s.delayUs, s.jitterUs, s.latencyID = f.DelayMs*1000, f.JitterMs*1000, f.ID
				for _, a := range d.addrs {
					s.flows = append(s.flows, Flow{Dst: a})
				}
			}
		case faultBox:
			if !f.BoxIP.IsValid() {
				continue
			}
			p.plan.Rules = append(p.plan.Rules,
				FaultRule{Chain: "forward", OIf: public, Daddr: f.BoxIP},
				FaultRule{Chain: "forward", IIf: public, Saddr: f.BoxIP},
			)
		}
	}
	for k, s := range shapes {
		p.addClass(k.dev, NetemClass{
			Key:   fmt.Sprintf("s%d", k.server),
			Netem: Netem{DelayUs: s.delayUs, JitterUs: s.jitterUs, LossPercent: s.loss, Seed: s.seed()},
			Flows: s.flows,
		})
	}

	for _, f := range cfg.Faults {
		if f.Kind != faultLatency || f.Server != nil {
			continue
		}
		p.locations(f, f.To, f.From, shapes)
		p.locations(f, f.From, f.To, shapes)
	}

	p.finish()
	return p.plan
}

// publicAddrs are the server's public IPv4 and the floating IPs routed to it.
func (p *faultPlanner) publicAddrs(s agentapi.FaultServer) []netip.Addr {
	if !s.PublicIPv4.IsValid() {
		return nil
	}
	return append([]netip.Addr{s.PublicIPv4}, p.floating[s.PublicIPv4]...)
}

// privateAddrs are the server's IP and alias IPs in one private network.
func privateAddrs(fp agentapi.FaultPrivate) []netip.Addr {
	var out []netip.Addr
	if fp.IP.IsValid() {
		out = append(out, fp.IP)
	}
	return append(out, fp.AliasIPs...)
}

func trunkDev(vlan int) string { return fmt.Sprintf("t%d", vlan) }

// devices returns the egress devices toward the server for the traffic
// ("public", "private", "all") with its addresses on each.
func (p *faultPlanner) devices(s agentapi.FaultServer, traffic string) []faultDev {
	var out []faultDev
	if traffic == "public" || traffic == "all" {
		if addrs := p.publicAddrs(s); len(addrs) > 0 {
			out = append(out, faultDev{p.public, addrs})
		}
	}
	if traffic == "private" || traffic == "all" {
		for _, fp := range s.Private {
			if addrs := privateAddrs(fp); len(addrs) > 0 {
				out = append(out, faultDev{trunkDev(fp.VLAN), addrs})
			}
		}
	}
	return out
}

// cut adds the drop rules of a network fault with 100 % (E40).
func (p *faultPlanner) cut(s agentapi.FaultServer, traffic string) {
	if traffic == "public" || traffic == "all" {
		for _, a := range p.publicAddrs(s) {
			p.plan.Rules = append(p.plan.Rules,
				FaultRule{Chain: "forward", IIf: p.public, Saddr: a},
				FaultRule{Chain: "forward", OIf: p.public, Daddr: a},
				FaultRule{Chain: "input", IIf: p.public, Saddr: a},
				FaultRule{Chain: "output", OIf: p.public, Daddr: a},
			)
		}
	}
	if traffic == "private" || traffic == "all" {
		for _, fp := range s.Private {
			vrf, trunk := vrfDev(fp.VLAN), trunkDev(fp.VLAN)
			for _, a := range privateAddrs(fp) {
				p.plan.Rules = append(p.plan.Rules,
					FaultRule{Chain: "forward", IIf: vrf, Saddr: a},
					FaultRule{Chain: "forward", OIf: trunk, Daddr: a},
					FaultRule{Chain: "input", IIf: vrf, SdIf: trunk, Saddr: a},
					FaultRule{Chain: "output", OIf: trunk, Daddr: a},
				)
			}
		}
	}
}

func vrfDev(vlan int) string { return fmt.Sprintf("vrf%d", vlan) }

// locations adds, for each server in dsts, the classes of a locations
// latency for packets from the servers in srcs: half the delay and jitter,
// public with public and private within the same VLAN, plus the
// destination's server-level shaping on that device.
func (p *faultPlanner) locations(f agentapi.NetFault, dsts, srcs []agentapi.FaultServer, shapes map[shapeKey]*serverShape) {
	key := func(dst agentapi.FaultServer) string { return fmt.Sprintf("s%d/f%d", dst.ID, f.ID) }
	for _, dst := range dsts {
		var srcPublic []netip.Addr
		for _, src := range srcs {
			srcPublic = append(srcPublic, p.publicAddrs(src)...)
		}
		p.addPairs(f, p.public, key(dst), srcPublic, p.publicAddrs(dst), shapes[shapeKey{p.public, dst.ID}])

		for _, fp := range dst.Private {
			var srcPrivate []netip.Addr
			for _, src := range srcs {
				for _, sp := range src.Private {
					if sp.VLAN == fp.VLAN {
						srcPrivate = append(srcPrivate, privateAddrs(sp)...)
					}
				}
			}
			dev := trunkDev(fp.VLAN)
			p.addPairs(f, dev, key(dst), srcPrivate, privateAddrs(fp), shapes[shapeKey{dev, dst.ID}])
		}
	}
}

// addPairs adds one class for the flows from every source to every
// destination address, if there is any.
func (p *faultPlanner) addPairs(f agentapi.NetFault, dev, key string, srcs, dsts []netip.Addr, s *serverShape) {
	if len(srcs) == 0 || len(dsts) == 0 {
		return
	}
	c := NetemClass{Key: key, Netem: Netem{
		DelayUs:  f.DelayMs * 1000 / 2,
		JitterUs: f.JitterMs * 1000 / 2,
		Seed:     uint64(f.ID),
	}}
	if s != nil {
		c.Netem.DelayUs += s.delayUs
		c.Netem.JitterUs += s.jitterUs
		if s.loss > 0 {
			c.Netem.LossPercent, c.Netem.Seed = s.loss, s.lossSeed
		}
	}
	for _, d := range dsts {
		for _, src := range srcs {
			c.Flows = append(c.Flows, Flow{Src: src, Dst: d})
		}
	}
	p.addClass(dev, c)
}

func (p *faultPlanner) addClass(dev string, c NetemClass) {
	if len(c.Flows) == 0 {
		return
	}
	if p.plan.Devices == nil {
		p.plan.Devices = map[string][]NetemClass{}
	}
	p.plan.Devices[dev] = append(p.plan.Devices[dev], c)
}

// finish sorts and deduplicates rules, classes and flows, so the same
// document gives the same plan.
func (p *faultPlanner) finish() {
	slices.SortFunc(p.plan.Rules, func(a, b FaultRule) int {
		return cmp.Or(
			cmp.Compare(a.Chain, b.Chain),
			cmp.Compare(a.IIf, b.IIf),
			cmp.Compare(a.OIf, b.OIf),
			cmp.Compare(a.SdIf, b.SdIf),
			a.Saddr.Compare(b.Saddr),
			a.Daddr.Compare(b.Daddr),
		)
	})
	p.plan.Rules = slices.Compact(p.plan.Rules)
	for dev, classes := range p.plan.Devices {
		for i := range classes {
			slices.SortFunc(classes[i].Flows, func(a, b Flow) int {
				return cmp.Or(a.Src.Compare(b.Src), a.Dst.Compare(b.Dst))
			})
			classes[i].Flows = slices.Compact(classes[i].Flows)
		}
		slices.SortFunc(classes, func(a, b NetemClass) int { return cmp.Compare(a.Key, b.Key) })
		p.plan.Devices[dev] = classes
	}
}
