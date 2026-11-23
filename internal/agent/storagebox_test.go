package agent_test

import (
	"net/netip"
	"testing"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/agentapi"
)

func testStorageBoxExternal(t *testing.T) {
	const (
		closed  = "198.19.0.20" // box without reachable_externally
		open    = "198.19.0.21" // box (or a subaccount) reachable externally
		server  = "198.19.0.10"
		host    = "198.19.0.2" // the host's end of the transit /30
		wsl     = "172.28.0.5" // WSL2, outside the public range
		outside = "203.0.113.7"
	)
	cfg := agentapi.Config{
		PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
		Servers:     []agentapi.Server{{ID: 1, PublicIPv4: addr(server)}},
		Storage:     agentapi.StorageVM{Addr: addr("198.19.255.253"), MAC: "02:48:4c:00:00:03"},
		StorageBoxes: []agentapi.StorageBoxRoute{
			{IP: addr(closed), Names: []string{"u100000"}},
			{IP: addr(open), Names: []string{"u100001", "u100001-sub1"}, ReachableExternally: true},
		},
	}
	cases := []struct {
		name     string
		src, dst string
		dropped  bool
	}{
		{name: "server_to_closed_box", src: server, dst: closed},
		{name: "server_to_open_box", src: server, dst: open},
		{name: "host_to_closed_box", src: host, dst: closed, dropped: true},
		{name: "host_to_open_box", src: host, dst: open},
		{name: "wsl2_to_closed_box", src: wsl, dst: closed, dropped: true},
		{name: "wsl2_to_open_box", src: wsl, dst: open},
		{name: "outside_to_closed_box", src: outside, dst: closed, dropped: true},
		{name: "host_to_server", src: host, dst: server},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := agent.StorageBoxDropped(cfg, addr(c.src), addr(c.dst)); got != c.dropped {
				t.Errorf("StorageBoxDropped(%s -> %s) = %v, want %v", c.src, c.dst, got, c.dropped)
			}
		})
	}
}
