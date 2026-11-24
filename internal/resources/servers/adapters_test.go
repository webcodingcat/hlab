package servers_test

import (
	"context"
	"encoding/json"
	"net"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// testAdapters holds the rows of adapters_test.go; TestServers runs them.
func testAdapters(t *testing.T) {
	t.Run("NET-02", func(t *testing.T) {
		fake := platformtest.NewFake()
		fake.SetResources(roomy)
		env, mod, d := newFakeEnv(t, fake, nil)
		const (
			netID   = int64(77)
			vlan    = 12
			ipRange = "10.0.0.0/16"
		)
		ctx, cancel := context.WithTimeout(context.Background(), apitest.ActionTimeout)
		defer cancel()

		names := []string{"a", "b", "c"}
		ips := []string{"10.0.1.2", "10.0.1.3", "10.0.1.4"}
		var ids []int64
		for _, n := range names {
			id := createdServer(t, env, n, nil)
			waitStatus(t, env, id, "running")
			ids = append(ids, id)
		}
		vm := func(id int64) string { return servers.VMName(d.Settings.VMPrefix, id) }

		macs := map[int64]string{}
		for i, id := range ids {
			powerBefore := len(callsOf(fake, "Power"))
			err := env.Store.Tx(func(tx store.Tx) error {
				mac, err := servers.NextPrivateMAC(tx)
				if err != nil {
					return err
				}
				macs[id] = strings.ToLower(mac.String())
				return servers.Update(tx, id, func(r *servers.Record) error {
					r.PrivateNet = append(r.PrivateNet, servers.PrivateNet{
						Network: netID, IP: ips[i], AliasIPs: []string{}, MACAddress: mac.String(),
					})
					if r.Hlab.Networks == nil {
						r.Hlab.Networks = map[int64]servers.NetInternal{}
					}
					r.Hlab.Networks[netID] = servers.NetInternal{VLAN: vlan, IPRange: ipRange}
					return nil
				})
			})
			if err != nil {
				t.Fatalf("attach server %d in its record: %v", id, err)
			}
			if err := mod.SyncAdapters(ctx, env.Store, id, []int64{netID}); err != nil {
				t.Fatalf("SyncAdapters server %d: %v", id, err)
			}
			if got := len(callsOf(fake, "Power")); got != powerBefore {
				t.Errorf("SyncAdapters server %d changed the power state (%d power calls)", id, got-powerBefore)
			}
		}

		privateMAC := regexp.MustCompile(`^02:48:4c:2[0-9a-f]:[0-9a-f]{2}:[0-9a-f]{2}$`)
		for _, id := range ids {
			if !privateMAC.MatchString(macs[id]) {
				t.Errorf("server %d private MAC %s, want 02:48:4c:2x:xx:xx", id, macs[id])
			}
			if s, err := fake.State(ctx, vm(id)); err != nil || s != platform.StateRunning {
				t.Errorf("VM %s state %v (%v) after the hot add, want running", vm(id), s, err)
			}
			nic, ok := nicOf(fake, vm(id), macs[id])
			if !ok {
				t.Errorf("VM %s has no adapter %s", vm(id), macs[id])
				continue
			}
			if nic.Segment.VLAN != vlan {
				t.Errorf("adapter %s of %s on segment %+v, want VLAN %d", macs[id], vm(id), nic.Segment, vlan)
			}
		}
		checkDeny(t, fake, vm, macs, ids)

		// The seed of server a carries the /32 and the onlink route to the
		// network range via its gateway, nothing else on that interface.
		eth, ok := seedEthernet(t, fake, vm(ids[0]), macs[ids[0]])
		if !ok {
			t.Fatalf("seed network-config of server %d has no interface %s", ids[0], macs[ids[0]])
		}
		if !slices.Equal(eth.Addresses, []string{"10.0.1.2/32"}) {
			t.Errorf("seed addresses %v, want [10.0.1.2/32]", eth.Addresses)
		}
		if want := []seedRoute{{To: ipRange, Via: "10.0.0.1", OnLink: true}}; !slices.Equal(eth.Routes, want) {
			t.Errorf("seed routes %+v, want %+v", eth.Routes, want)
		}

		// Detach server a.
		a := ids[0]
		err := env.Store.Tx(func(tx store.Tx) error {
			return servers.Update(tx, a, func(r *servers.Record) error {
				r.PrivateNet = slices.DeleteFunc(r.PrivateNet, func(p servers.PrivateNet) bool { return p.Network == netID })
				delete(r.Hlab.Networks, netID)
				return nil
			})
		})
		if err != nil {
			t.Fatalf("detach server %d in its record: %v", a, err)
		}
		if err := mod.SyncAdapters(ctx, env.Store, a, []int64{netID}); err != nil {
			t.Fatalf("SyncAdapters after detach: %v", err)
		}
		if _, ok := nicOf(fake, vm(a), macs[a]); ok {
			t.Errorf("VM %s keeps adapter %s after detach", vm(a), macs[a])
		}
		macA := macs[a]
		delete(macs, a)
		checkDeny(t, fake, vm, macs, ids[1:])
		if _, ok := seedEthernet(t, fake, vm(a), macA); ok {
			t.Errorf("seed of server %d keeps the detached interface", a)
		}
	})

	t.Run("SRV-01/seed_hold", func(t *testing.T) {
		p := newRecPlatform()
		p.SetResources(roomy)
		clk := newTestClock(time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC))
		env, mod, d := newFakeEnv(t, p, func(d *servers.Deps) {
			d.Clock = clk
			d.SeedHold = seedHold
		})
		id := createdServer(t, env, "early", nil)
		waitStatus(t, env, id, "running")
		vm := servers.VMName(d.Settings.VMPrefix, id)
		ctx, cancel := context.WithTimeout(context.Background(), apitest.ActionTimeout)
		defer cancel()

		clk.Add(10 * time.Second)
		mac := attachInRecord(t, env, id, 77, 12, "10.0.1.2")
		n := len(p.dvdSets(0))
		waiters := clk.waiting()
		if err := mod.SyncAdapters(ctx, env.Store, id, []int64{77}); err != nil {
			t.Fatalf("SyncAdapters 10 s after the create: %v", err)
		}
		if _, ok := nicOf(p.Fake, vm, mac); !ok {
			t.Errorf("VM %s has no adapter %s after SyncAdapters within the hold", vm, mac)
		}
		if got := p.dvdSets(0)[n:]; len(got) != 0 {
			t.Errorf("SetDVD calls on slot 0 within the hold: %+v, want none", got)
		}
		if !seedDue(t, env, id) {
			t.Error("seed_due not set after SyncAdapters within the hold")
		}

		waitWaiter(clk, waiters)
		clk.Add(seedHold - 10*time.Second + time.Second)
		sets := waitSeedInserted(t, env, p, vm, id, n, mac)
		if sets[0].iso != "" {
			t.Errorf("slot 0 sets after the hold %+v, want an eject before the insert", sets)
		}

		// After the hold a second attach swaps the seed before it returns.
		mac2 := attachInRecord(t, env, id, 78, 13, "10.1.1.2")
		n = len(p.dvdSets(0))
		if err := mod.SyncAdapters(ctx, env.Store, id, []int64{78}); err != nil {
			t.Fatalf("SyncAdapters after the hold: %v", err)
		}
		if sets := p.dvdSets(0)[n:]; len(sets) == 0 || sets[len(sets)-1].iso == "" {
			t.Fatalf("slot 0 after SyncAdapters after the hold: %+v, want a new seed inserted", sets)
		}
		if _, ok := seedEthernet(t, p.Fake, vm, mac2); !ok {
			t.Errorf("seed after the second attach has no interface %s", mac2)
		}
		if seedDue(t, env, id) {
			t.Error("seed_due set after SyncAdapters after the hold, want cleared")
		}
	})

	t.Run("SRV-01/seed_due_at_start", func(t *testing.T) {
		cases := []struct {
			name     string
			behavior string
			poweroff bool
			held     bool
		}{
			{"saved_resumed", "save", false, false},
			{"off_powered_on", "shutdown", false, true},
			{"off_stays_off", "shutdown", true, false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				p := newRecPlatform()
				p.SetResources(roomy)
				clk := newTestClock(time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC))
				env, mod, d := newFakeEnv(t, p, func(d *servers.Deps) {
					shortHalt(d)
					d.Clock = clk
					d.SeedHold = seedHold
				})
				lc := lifecycleOf(t, mod)
				id := createdServer(t, env, "due", nil)
				waitStatus(t, env, id, "running")
				vm := servers.VMName(d.Settings.VMPrefix, id)
				if c.poweroff {
					runAction(t, env, id, "poweroff", nil)
				}
				clk.Add(seedHold + time.Second)
				if err := lc.Halt(lifeCtx(t), c.behavior, false); err != nil {
					t.Fatalf("Halt %s: %v", c.behavior, err)
				}

				// hlabd ended with the swap due: the record holds a new
				// network and seed_due, slot 0 the old seed.
				mac := attachInRecord(t, env, id, 77, 12, "10.0.1.2")
				err := env.Store.Tx(func(tx store.Tx) error {
					return servers.Update(tx, id, func(r *servers.Record) error {
						r.Hlab.SeedDue = true
						return nil
					})
				})
				if err != nil {
					t.Fatalf("set seed_due on server %d: %v", id, err)
				}

				n := len(p.dvdSets(0))
				waiters := clk.waiting()
				if err := lc.Restore(lifeCtx(t), c.behavior); err != nil {
					t.Fatalf("Restore %s: %v", c.behavior, err)
				}
				if c.held {
					if got := p.dvdSets(0)[n:]; len(got) != 0 {
						t.Errorf("SetDVD calls on slot 0 within the hold after the power-on: %+v, want none", got)
					}
					if !seedDue(t, env, id) {
						t.Error("seed_due cleared within the hold after the power-on, want set")
					}
					waitWaiter(clk, waiters)
					clk.Add(seedHold + time.Second)
				}
				waitSeedInserted(t, env, p, vm, id, n, mac)
			})
		}
	})
}

// seedHold is the SeedHold the seed rows use.
const seedHold = 90 * time.Second

// attachInRecord adds a private network to the record of server id, as an
// attach does before it calls SyncAdapters, and returns its MAC.
func attachInRecord(t *testing.T, env *apitest.Env, id, netID int64, vlan int, ip string) string {
	t.Helper()
	var mac string
	err := env.Store.Tx(func(tx store.Tx) error {
		m, err := servers.NextPrivateMAC(tx)
		if err != nil {
			return err
		}
		mac = strings.ToLower(m.String())
		return servers.Update(tx, id, func(r *servers.Record) error {
			r.PrivateNet = append(r.PrivateNet, servers.PrivateNet{
				Network: netID, IP: ip, AliasIPs: []string{}, MACAddress: m.String(),
			})
			if r.Hlab.Networks == nil {
				r.Hlab.Networks = map[int64]servers.NetInternal{}
			}
			r.Hlab.Networks[netID] = servers.NetInternal{VLAN: vlan, IPRange: "10.0.0.0/8"}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("attach server %d to network %d in its record: %v", id, netID, err)
	}
	return mac
}

// seedDue reports the seed_due note of server id.
func seedDue(t *testing.T, env *apitest.Env, id int64) bool {
	t.Helper()
	var due bool
	err := env.Store.Tx(func(tx store.Tx) error {
		r, err := servers.Load(tx, id)
		due = r.Hlab.SeedDue
		return err
	})
	if err != nil {
		t.Fatalf("load server %d: %v", id, err)
	}
	return due
}

// waiting is the number of pending After calls of the clock.
func (c *testClock) waiting() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// waitWaiter waits up to a second for a new After call on clk, so a
// delayed swap has set its deadline before the clock moves; a module that
// waits otherwise goes on after that second.
func waitWaiter(clk *testClock, before int) {
	deadline := time.Now().Add(time.Second)
	for clk.waiting() <= before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}

// waitSeedInserted waits for a seed with the interface mac in slot 0 of vm
// after its n-th set of slot 0 and seed_due cleared; it returns the sets
// after n.
func waitSeedInserted(t *testing.T, env *apitest.Env, p *recPlatform, vm string, id int64, n int, mac string) []dvdSet {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		sets := p.dvdSets(0)[n:]
		if len(sets) > 0 && sets[len(sets)-1].iso != "" && !seedDue(t, env, id) {
			if _, ok := seedEthernet(t, p.Fake, vm, mac); !ok {
				t.Errorf("seed inserted into slot 0 of %s has no interface %s", vm, mac)
			}
			return sets
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot 0 of %s after %s: sets %+v, seed_due %v; want the current seed inserted and seed_due cleared",
				vm, apitest.ActionTimeout, sets, seedDue(t, env, id))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// nicOf returns the adapter of vm with MAC mac on the fake platform.
func nicOf(fake *platformtest.Fake, vm, mac string) (platform.NICSpec, bool) {
	for _, n := range fake.NICs(vm) {
		if strings.EqualFold(n.MAC.String(), mac) {
			return n, true
		}
	}
	return platform.NICSpec{}, false
}

// checkDeny checks that the private adapter of every member holds exactly
// the MACs of the other members in its deny list.
func checkDeny(t *testing.T, fake *platformtest.Fake, vm func(int64) string, macs map[int64]string, members []int64) {
	t.Helper()
	for _, id := range members {
		nic, ok := nicOf(fake, vm(id), macs[id])
		if !ok {
			t.Errorf("VM %s has no adapter %s", vm(id), macs[id])
			continue
		}
		var got, want []string
		for _, m := range nic.DenyMACs {
			got = append(got, strings.ToLower(m.String()))
		}
		for _, other := range members {
			if other != id {
				want = append(want, macs[other])
			}
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("deny list of %s on %s: %v, want %v", macs[id], vm(id), got, want)
		}
	}
}

type seedRoute struct {
	To     string `json:"to"`
	Via    string `json:"via"`
	OnLink bool   `json:"on-link"`
}

type seedEth struct {
	Match struct {
		MACAddress string `json:"macaddress"`
	} `json:"match"`
	Addresses []string    `json:"addresses"`
	Routes    []seedRoute `json:"routes"`
}

// seedEthernet returns the interface with MAC mac of the network-config in
// the seed last inserted into vm.
func seedEthernet(t *testing.T, fake *platformtest.Fake, vm, mac string) (seedEth, bool) {
	t.Helper()
	var seed string
	for _, c := range callsOf(fake, "SetDVD") {
		if p, ok := c.Arg.(string); ok && c.VM == vm && p != "" {
			seed = p
		}
	}
	if seed == "" {
		t.Fatalf("no seed inserted into %s", vm)
	}
	data, ok := isoFiles(t, seed)["network-config"]
	if !ok {
		t.Fatalf("seed %s has no network-config", seed)
	}
	var cfg struct {
		Network struct {
			Ethernets map[string]seedEth `json:"ethernets"`
		} `json:"network"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode network-config of %s: %v\n%s", seed, err, data)
	}
	want, err := net.ParseMAC(mac)
	if mac == "" || err != nil {
		return seedEth{}, false
	}
	for _, eth := range cfg.Network.Ethernets {
		if got, err := net.ParseMAC(eth.Match.MACAddress); err == nil && got.String() == want.String() {
			return eth, true
		}
	}
	return seedEth{}, false
}
