package agent

import (
	"fmt"
	"slices"
	"strconv"
)

// TcState is the applied classes per device, with their minors.
type TcState map[string][]NetemClass

// firstFaultMinor is the lowest minor of a fault class; 1:1 is the
// unshaped default class.
const firstFaultMinor = 0x10

// TcCommands returns the tc argument lists (without "tc") that turn have
// into want, and the new state. have nil means unknown (first document
// after the agent started): the root qdisc of every candidate device is
// deleted first. An unchanged class gives no command; a change of delay
// or jitter only is `qdisc change` without seed; a new or changed loss is
// `qdisc change` or `add` with seed (E42).
func TcCommands(have TcState, want map[string][]NetemClass, candidates []string) ([][]string, TcState) {
	var cmds [][]string
	if have == nil {
		for _, dev := range candidates {
			cmds = append(cmds, []string{"qdisc", "del", "dev", dev, "root"})
		}
		have = TcState{}
	}

	devs := map[string]bool{}
	for dev, cs := range have {
		if len(cs) > 0 {
			devs[dev] = true
		}
	}
	for dev, cs := range want {
		if len(cs) > 0 {
			devs[dev] = true
		}
	}
	names := make([]string, 0, len(devs))
	for dev := range devs {
		names = append(names, dev)
	}
	slices.Sort(names)

	st := TcState{}
	for _, dev := range names {
		old, next := have[dev], want[dev]
		switch {
		case len(next) == 0:
			// The device leaves want: its root qdisc takes every class,
			// netem and filter with it.
			cmds = append(cmds, []string{"qdisc", "del", "dev", dev, "root"})
		case len(old) == 0:
			cmds = append(cmds,
				[]string{"qdisc", "add", "dev", dev, "root", "handle", "1:", "htb", "default", "1"},
				[]string{"class", "add", "dev", dev, "parent", "1:", "classid", "1:1", "htb", "rate", "10gbit", "quantum", "200000"},
			)
			fallthrough
		default:
			var classes []NetemClass
			cmds, classes = tcDevice(cmds, dev, old, next)
			st[dev] = classes
		}
	}
	return cmds, st
}

// tcDevice appends the commands that turn the classes old into next on a
// device with the root HTB in place, and returns the applied classes.
func tcDevice(cmds [][]string, dev string, old, next []NetemClass) ([][]string, []NetemClass) {
	oldByKey := map[string]NetemClass{}
	used := map[uint16]bool{}
	for _, c := range old {
		oldByKey[c.Key] = c
		used[c.Minor] = true
	}
	wanted := map[string]bool{}
	for _, c := range next {
		wanted[c.Key] = true
	}

	// Gone classes first: their filters, then the class with its netem.
	for _, c := range old {
		if wanted[c.Key] {
			continue
		}
		cmds = tcDelFilters(cmds, dev, c)
		cmds = append(cmds, []string{"class", "del", "dev", dev, "classid", tcClassID(c.Minor)})
	}

	minor := uint16(firstFaultMinor)
	out := make([]NetemClass, 0, len(next))
	for _, c := range next {
		c.Flows = slices.Clone(c.Flows)
		prev, ok := oldByKey[c.Key]
		if !ok {
			for used[minor] {
				minor++
			}
			c.Minor = minor
			used[minor] = true
			cmds = append(cmds,
				[]string{"class", "add", "dev", dev, "parent", "1:", "classid", tcClassID(c.Minor), "htb", "rate", "10gbit", "quantum", "200000"},
				tcNetem("add", dev, c.Minor, c.Netem, true),
			)
			cmds = tcAddFilters(cmds, dev, c)
			out = append(out, c)
			continue
		}

		c.Minor = prev.Minor
		if c.Netem != prev.Netem {
			seed := c.Netem.LossPercent != prev.Netem.LossPercent || c.Netem.Seed != prev.Netem.Seed
			cmds = append(cmds, tcNetem("change", dev, c.Minor, c.Netem, seed))
		}
		if !slices.Equal(c.Flows, prev.Flows) {
			cmds = tcDelFilters(cmds, dev, prev)
			cmds = tcAddFilters(cmds, dev, c)
		}
		out = append(out, c)
	}
	return cmds, out
}

func tcClassID(minor uint16) string { return fmt.Sprintf("1:%x", minor) }

// tcNetem is the netem qdisc of class 1:<minor> with every parameter, since
// change resets what it does not name (E29); seed only where asked (E42).
func tcNetem(verb, dev string, minor uint16, n Netem, seed bool) []string {
	cmd := []string{"qdisc", verb, "dev", dev, "parent", tcClassID(minor), "handle", fmt.Sprintf("%x:", minor), "netem"}
	if n.DelayUs > 0 || n.JitterUs > 0 {
		cmd = append(cmd, "delay", fmt.Sprintf("%dus", n.DelayUs))
		if n.JitterUs > 0 {
			cmd = append(cmd, fmt.Sprintf("%dus", n.JitterUs))
		}
	}
	if n.LossPercent > 0 {
		cmd = append(cmd, "loss", "random", fmt.Sprintf("%d%%", n.LossPercent))
	}
	if seed {
		cmd = append(cmd, "seed", strconv.FormatUint(n.Seed, 10))
	}
	return append(cmd, "limit", "100000")
}

// tcFilterPrio: source/destination pairs before destination-only flows.
func tcFilterPrio(f Flow) string {
	if f.Src.IsValid() {
		return "1"
	}
	return "2"
}

// tcFilterHandle is unique on the device: the class minor and the flow's
// index in the class.
func tcFilterHandle(minor uint16, i int) string {
	return fmt.Sprintf("0x%x", (uint32(minor)<<16)|(uint32(i+1)&0xffff))
}

func tcAddFilters(cmds [][]string, dev string, c NetemClass) [][]string {
	for i, f := range c.Flows {
		cmd := []string{"filter", "add", "dev", dev, "parent", "1:", "protocol", "ip",
			"prio", tcFilterPrio(f), "handle", tcFilterHandle(c.Minor, i), "flower"}
		if f.Src.IsValid() {
			cmd = append(cmd, "src_ip", f.Src.String())
		}
		cmd = append(cmd, "dst_ip", f.Dst.String(), "classid", tcClassID(c.Minor))
		cmds = append(cmds, cmd)
	}
	return cmds
}

func tcDelFilters(cmds [][]string, dev string, c NetemClass) [][]string {
	for i, f := range c.Flows {
		cmds = append(cmds, []string{"filter", "del", "dev", dev, "parent", "1:", "protocol", "ip",
			"prio", tcFilterPrio(f), "handle", tcFilterHandle(c.Minor, i), "flower"})
	}
	return cmds
}
