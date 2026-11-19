package servers_test

import (
	"context"
	"encoding/json"
	"net"
	"regexp"
	"slices"
	"strings"
	"testing"

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
