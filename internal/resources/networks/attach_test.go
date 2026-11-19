package networks_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
)

// testAttach holds the rows of attach_test.go; TestNetworks runs them.
func testAttach(t *testing.T) {
	t.Run("NET-05/attach", func(t *testing.T) {
		e := newNetEnv(t)
		eu := createNetwork(t, e, netBody("eu", "10.0.0.0/16", "10.0.1.0/24", "10.0.2.0/24"))
		us := createNetwork(t, e, map[string]any{
			"name": "us", "ip_range": "10.1.0.0/16",
			"subnets": []any{subnet("cloud", "10.1.1.0/24", "us-east")},
		})
		a := createServer(t, e, "a", "fsn1", nil)
		b := createServer(t, e, "b", "fsn1", nil)

		attach(t, e, a, eu.ID, map[string]any{"ip_range": "10.0.2.0/24"})
		if ip := mustPrivateNet(t, e, a, eu.ID).IP; !netip.MustParsePrefix("10.0.2.0/24").Contains(netip.MustParseAddr(ip)) {
			t.Errorf("attach with ip_range 10.0.2.0/24: ip %s, want one in that subnet", ip)
		}
		attach(t, e, b, eu.ID, nil)
		if p := mustPrivateNet(t, e, b, eu.ID); p.IP == "" {
			t.Errorf("server in fsn1 attached to an eu-central subnet without an IP")
		}

		wantError(t, serverAction(e, b, "attach_to_network", map[string]any{"network": us.ID}),
			http.StatusUnprocessableEntity, "no_subnet_available", "attach of an fsn1 server to a us-east network")
		if _, ok := privateNet(t, e, b, us.ID); ok {
			t.Errorf("server %d attached to the us-east network after the refusal", b)
		}
		wantError(t, postServer(e, "c", "fsn1", map[string]any{"networks": []int64{us.ID}}),
			http.StatusUnprocessableEntity, "no_subnet_available", "server create in fsn1 with a us-east network")
	})

	t.Run("NET-02/attach", func(t *testing.T) {
		e := newNetEnv(t)
		n1 := createNetwork(t, e, netBody("n1", "10.0.0.0/16", "10.0.0.0/24"))
		a := createServer(t, e, "a", "fsn1", nil)
		b := createServer(t, e, "b", "fsn1", nil)
		c := createServer(t, e, "c", "fsn1", nil)

		// 10.0.0.0 is the subnet address, 10.0.0.1 the network gateway.
		attach(t, e, a, n1.ID, nil)
		if ip := mustPrivateNet(t, e, a, n1.ID).IP; ip != "10.0.0.2" {
			t.Errorf("first attach: ip %s, want the lowest free host address 10.0.0.2", ip)
		}
		attach(t, e, b, n1.ID, map[string]any{"ip": "10.0.0.9"})
		if ip := mustPrivateNet(t, e, b, n1.ID).IP; ip != "10.0.0.9" {
			t.Errorf("attach with ip 10.0.0.9: ip %s", ip)
		}
		wantError(t, serverAction(e, c, "attach_to_network", map[string]any{"network": n1.ID, "ip": "10.0.0.9"}),
			http.StatusUnprocessableEntity, "ip_not_available", "attach with a used ip")
		attach(t, e, c, n1.ID, nil)
		if ip := mustPrivateNet(t, e, c, n1.ID).IP; ip != "10.0.0.3" {
			t.Errorf("third attach: ip %s, want 10.0.0.3", ip)
		}

		// A /30 holding the gateway has one host address left: .2; .3 is
		// its broadcast address.
		small := createNetwork(t, e, netBody("small", "10.3.0.0/16", "10.3.0.0/30"))
		attach(t, e, a, small.ID, nil)
		if ip := mustPrivateNet(t, e, a, small.ID).IP; ip != "10.3.0.2" {
			t.Errorf("attach to the /30: ip %s, want 10.3.0.2", ip)
		}
		if resp := serverAction(e, b, "attach_to_network", map[string]any{"network": small.ID}); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("second attach to the full /30: status %d, want 422 (no gateway or broadcast address handed out)", resp.StatusCode)
		}
		if p, ok := privateNet(t, e, b, small.ID); ok {
			t.Errorf("server %d got %s in the full /30", b, p.IP)
		}

		wantError(t, serverAction(e, a, "attach_to_network", map[string]any{"network": n1.ID}),
			http.StatusUnprocessableEntity, "server_already_attached", "second attach to the same network")
		overlap := createNetwork(t, e, netBody("overlap", "10.0.0.0/12", "10.1.0.0/24"))
		wantError(t, serverAction(e, a, "attach_to_network", map[string]any{"network": overlap.ID}),
			http.StatusUnprocessableEntity, "networks_overlap", "attach to a network overlapping one of the server's")
		n2 := createNetwork(t, e, netBody("n2", "172.16.0.0/16", "172.16.0.0/24"))
		attach(t, e, a, n2.ID, nil)
		n4 := createNetwork(t, e, netBody("n4", "192.168.0.0/16", "192.168.0.0/24"))
		wantError(t, serverAction(e, a, "attach_to_network", map[string]any{"network": n4.ID}),
			http.StatusForbidden, "resource_limit_exceeded", "attach to a fourth network")
		if got := len(privateNets(t, e, a)); got != 3 {
			t.Errorf("server %d in %d networks, want 3", a, got)
		}

		if p := mustPrivateNet(t, e, a, n1.ID); p.MACAddress == "" {
			t.Errorf("private_net of server %d in network %d has no mac_address", a, n1.ID)
		}
		if got := sorted(getNetwork(t, e, n1.ID).Servers); !slices.Equal(got, sorted([]int64{a, b, c})) {
			t.Errorf("network %d servers %v, want %v", n1.ID, got, []int64{a, b, c})
		}

		runServerAction(t, e, a, "detach_from_network", map[string]any{"network": n1.ID})
		if _, ok := privateNet(t, e, a, n1.ID); ok {
			t.Errorf("server %d keeps network %d in private_net after detach", a, n1.ID)
		}
		if got := getNetwork(t, e, n1.ID).Servers; slices.Contains(got, a) {
			t.Errorf("network %d servers %v still hold detached server %d", n1.ID, got, a)
		}

		if resp := e.Do(http.MethodDelete, fmt.Sprintf("/networks/%d", n1.ID), nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE /networks/%d with members: status %d (%s), want 204", n1.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
		}
		for _, s := range []int64{b, c} {
			if _, ok := privateNet(t, e, s, n1.ID); ok {
				t.Errorf("server %d keeps deleted network %d in private_net", s, n1.ID)
			}
		}
	})

	t.Run("NET-03", func(t *testing.T) {
		e := newNetEnv(t)
		n := createNetwork(t, e, netBody("n", "10.0.0.0/16", "10.0.0.0/24", "10.0.1.0/24"))
		a := createServer(t, e, "a", "fsn1", nil)
		b := createServer(t, e, "b", "fsn1", nil)
		attach(t, e, a, n.ID, map[string]any{"ip": "10.0.0.2"})
		attach(t, e, b, n.ID, map[string]any{"ip": "10.0.0.3"})

		change := func(ips ...string) *http.Response {
			return serverAction(e, a, "change_alias_ips", map[string]any{"network": n.ID, "alias_ips": ips})
		}
		runAction(t, e, change("10.0.0.20", "10.0.1.20"), "change_alias_ips to two")
		if got := sortedStrings(mustPrivateNet(t, e, a, n.ID).AliasIPs); !slices.Equal(got, []string{"10.0.0.20", "10.0.1.20"}) {
			t.Errorf("alias_ips %v, want 10.0.0.20 and 10.0.1.20", got)
		}
		runAction(t, e, change("10.0.0.21"), "change_alias_ips to one")
		want := []string{"10.0.0.21"}
		if got := mustPrivateNet(t, e, a, n.ID).AliasIPs; !slices.Equal(got, want) {
			t.Errorf("alias_ips %v after replacing, want %v", got, want)
		}

		wantFieldError(t, change("10.0.0.30", "10.0.0.31", "10.0.0.32", "10.0.0.33", "10.0.0.34", "10.0.0.35"),
			"alias_ips", "six alias IPs")
		if resp := change("10.0.5.5"); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("alias IP outside every subnet: status %d, want 422", resp.StatusCode)
		}
		if resp := change("10.0.0.3"); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("alias IP used by server %d: status %d, want 422", b, resp.StatusCode)
		}
		if got := mustPrivateNet(t, e, a, n.ID).AliasIPs; !slices.Equal(got, want) {
			t.Errorf("alias_ips %v after refused changes, want %v", got, want)
		}

		alias := netip.MustParseAddr("10.0.0.21")
		e.waitPushed(t, "alias IP 10.0.0.21 for the member", func(doc *agentapi.Config) bool {
			for _, nw := range doc.Networks {
				for _, m := range nw.Members {
					if m.ServerID == a && slices.Equal(m.AliasIPs, []netip.Addr{alias}) {
						return true
					}
				}
			}
			return false
		})
	})
}

func sorted(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

func sortedStrings(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
