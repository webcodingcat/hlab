package servers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// testSeed holds the rows of seed_test.go; TestServers runs them.
func testSeed(t *testing.T) {
	t.Run("SRV-23", func(t *testing.T) {
		const (
			netID   = int64(77)
			vlan    = 12
			ipRange = "10.0.0.0/16"
		)

		cases := []struct {
			image  string
			onLink bool
		}{
			{"ubuntu-24.04", true},
			{"debian-13", true},
			{"rocky-9", false},
			{"alma-10", false},
			{"centos-stream-10", false},
			{"fedora-44", false},
			{"opensuse-16", false},
		}
		for _, c := range cases {
			t.Run(c.image, func(t *testing.T) {
				p := newRecPlatform()
				p.SetResources(roomy)
				env, mod, d := newImageEnv(t, p, nil)
				id := createdServer(t, env, "seed", map[string]any{"image": c.image})
				waitStatus(t, env, id, "running")

				ctx, cancel := context.WithTimeout(context.Background(), apitest.ActionTimeout)
				defer cancel()
				err := env.Store.Tx(func(tx store.Tx) error {
					mac, err := servers.NextPrivateMAC(tx)
					if err != nil {
						return err
					}
					return servers.Update(tx, id, func(r *servers.Record) error {
						r.PrivateNet = append(r.PrivateNet, servers.PrivateNet{
							Network: netID, IP: "10.0.1.2", AliasIPs: []string{}, MACAddress: mac.String(),
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
					t.Fatalf("SyncAdapters: %v", err)
				}

				routes := seedRoutes(t, p.Fake, servers.VMName(d.Settings.VMPrefix, id))
				checkOnLink(t, routes, c.onLink, ipRange)
			})
		}

		t.Run("snapshot_and_rebuild", func(t *testing.T) {
			p := newRecPlatform()
			p.SetResources(roomy)
			env, _, d := newImageEnv(t, p, nil)
			vm := func(id int64) string { return servers.VMName(d.Settings.VMPrefix, id) }

			src := createdServer(t, env, "rocky", map[string]any{"image": "rocky-9"})
			snap := createImage(t, env, src, map[string]any{"type": "snapshot"})
			copyID := createdServer(t, env, "from-snap", map[string]any{"image": fmt.Sprint(snap)})
			if routes := seedRoutes(t, p.Fake, vm(copyID)); hasOnLink(routes) {
				t.Errorf("seed of the server from a rocky-9 snapshot has on-link on a route: %v", routes)
			}

			runAction(t, env, src, "rebuild", map[string]any{"image": "debian-13"})
			checkOnLink(t, seedRoutes(t, p.Fake, vm(src)), true)
		})
	})
}

// seedRoutes returns the routes of every interface, as raw objects, in
// the network-config of the seed last inserted into vm.
func seedRoutes(t *testing.T, fake *platformtest.Fake, vm string) []map[string]any {
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
			Ethernets map[string]struct {
				Routes []map[string]any `json:"routes"`
			} `json:"ethernets"`
		} `json:"network"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode network-config of %s: %v\n%s", seed, err, data)
	}
	var routes []map[string]any
	for _, eth := range cfg.Network.Ethernets {
		routes = append(routes, eth.Routes...)
	}
	return routes
}

// hasOnLink reports whether any route carries the key on-link.
func hasOnLink(routes []map[string]any) bool {
	for _, r := range routes {
		if _, ok := r["on-link"]; ok {
			return true
		}
	}
	return false
}

// checkOnLink checks that the IPv4 default route and the routes to ranges
// exist and, with want, carry on-link true; without want, no route carries
// the key on-link.
func checkOnLink(t *testing.T, routes []map[string]any, want bool, ranges ...string) {
	t.Helper()
	targets := append([]string{"default"}, ranges...)
	for _, target := range targets {
		var found map[string]any
		for _, r := range routes {
			to, _ := r["to"].(string)
			if to == target || (target == "default" && to == "0.0.0.0/0") {
				found = r
			}
		}
		if found == nil {
			t.Errorf("seed network-config has no route to %s: %v", target, routes)
			continue
		}
		if want && found["on-link"] != true {
			t.Errorf("route to %s = %v, want on-link true", target, found)
		}
	}
	if !want && hasOnLink(routes) {
		t.Errorf("seed network-config routes %v, want no on-link on any route", routes)
	}
}
