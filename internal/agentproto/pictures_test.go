package agentproto

import (
	"net/netip"
	"strings"
	"testing"
)

// goodLinks returns a picture that passes Check.
func goodLinks() Links {
	return Links{
		Interfaces: []Interface{
			{Name: "public", MAC: "02:48:4c:0a:00:02"},
			{Name: "private", MAC: "02:48:4c:0a:00:03"},
			{Name: "uplink", MAC: "02:48:4c:0a:00:04", Networkd: true},
		},
		VRFs:  []VRF{{Name: "vrf-net-1", Table: 1001}},
		VLANs: []VLAN{{Name: "private.100", Parent: "private", ID: 100, VRF: "vrf-net-1"}},
		Addresses: []Address{
			{Device: "public", Prefix: netip.MustParsePrefix("172.31.1.1/32"), Announce: true},
			{Device: "private.100", Prefix: netip.MustParsePrefix("10.0.0.1/16")},
			{Device: "lo", Prefix: netip.MustParsePrefix("169.254.169.254/32")},
		},
		Routes: []Route{
			{Destination: netip.MustParsePrefix("198.19.0.1/32"), Device: "public",
				Source: netip.MustParsePrefix("172.31.1.1/32").Addr()},
			{Destination: netip.MustParsePrefix("10.1.0.0/16"), Device: "private.100",
				Gateway: netip.MustParseAddr("10.0.0.2"), Table: 1001},
		},
		Sysctls: []Sysctl{{Key: "net/ipv4/conf/public/send_redirects", Value: "0"}},
	}
}

func TestLinksCheckAcceptsAGoodPicture(t *testing.T) {
	if err := goodLinks().Check(); err != nil {
		t.Fatal(err)
	}
}

func TestLinksCheckNamesEachProblem(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Links)
		want   string
	}{
		{"bad MAC", func(l *Links) { l.Interfaces[0].MAC = "96:00" }, "bad MAC"},
		{"same MAC", func(l *Links) { l.Interfaces[1].MAC = l.Interfaces[0].MAC }, "is taken"},
		{"long name", func(l *Links) { l.Interfaces[0].Name = "a-name-that-is-too-long" }, "not a device name"},
		{"name twice", func(l *Links) { l.VRFs[0].Name = "public" }, "the name is taken"},
		{"kernel table", func(l *Links) { l.VRFs[0].Table = 254 }, "not free"},
		{"VLAN on a VLAN", func(l *Links) { l.VLANs[0].Parent = "private.100" }, `no interface "private.100"`},
		{"VLAN ID", func(l *Links) { l.VLANs[0].ID = 4095 }, "not 1 to 4094"},
		{"unknown VRF", func(l *Links) { l.VLANs[0].VRF = "vrf-x" }, `no VRF "vrf-x"`},
		{"address device", func(l *Links) { l.Addresses[0].Device = "eth9" }, `no device "eth9"`},
		{"host bits", func(l *Links) {
			l.Routes[0].Destination = netip.MustParsePrefix("198.19.0.1/16")
		}, "host bits"},
		{"route device", func(l *Links) { l.Routes[0].Device = "eth9" }, `no device "eth9"`},
		{"route on networkd's NIC", func(l *Links) { l.Routes[0].Device = "uplink" }, `networkd manages "uplink"`},
		{"address on networkd's NIC", func(l *Links) { l.Addresses[0].Device = "uplink" }, `networkd manages "uplink"`},
		{"gateway family", func(l *Links) { l.Routes[1].Gateway = netip.MustParseAddr("fe80::1") }, "other family"},
		{"route table", func(l *Links) { l.Routes[1].Table = 7 }, "no VRF has table 7"},
		{"blackhole with a device", func(l *Links) { l.Routes[0].Blackhole = true }, "a blackhole has no device"},
		{"sysctl", func(l *Links) { l.Sysctls[0].Key = "net/../kernel/panic" }, "not below net/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			links := goodLinks()
			test.change(&links)
			err := links.Check()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("Check() = %v, want an error with %q", err, test.want)
			}
		})
	}
}

func TestMetadataCheckRefusesSharedSources(t *testing.T) {
	metadata := Metadata{Servers: []MetadataServer{
		{Sources: []netip.Prefix{netip.MustParsePrefix("2001:db8:1:1::/64")}},
		{Sources: []netip.Prefix{netip.MustParsePrefix("2001:db8:1:1::1/128")}},
	}}
	if err := metadata.Check(); err == nil {
		t.Error("Check() accepted two servers with overlapping sources")
	}
	metadata.Servers[1].Sources[0] = netip.MustParsePrefix("198.19.0.1/32")
	if err := metadata.Check(); err != nil {
		t.Errorf("Check() = %v for distinct sources", err)
	}
}

func TestEmptyRulesetAndDHCPAreRefused(t *testing.T) {
	if (Ruleset{Text: " \n"}).Check() == nil {
		t.Error("an empty ruleset passed")
	}
	if (DHCP{}).Check() == nil {
		t.Error("an empty DHCP configuration passed")
	}
}
