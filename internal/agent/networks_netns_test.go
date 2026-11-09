//go:build linux && netns

package agent

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/picture"
)

// The private wire's MAC: the guest of network 1000008 asks for its
// address by DHCP with it.
const guestMAC = "86:00:00:00:00:03"

// twoNetworksLab returns a lab with two private networks of the same range,
// as two projects may have them: each holds a server at 10.0.0.2, the first
// another at 10.0.0.3 with an alias IP and a route through it, the second
// one at 10.0.0.9.
func twoNetworksLab() picture.Lab {
	lab := picture.DefaultLab()
	ipRange := netip.MustParsePrefix("10.0.0.0/16")
	lab.Networks = []picture.Network{
		{ID: 1000007, VLAN: 2, IPRange: ipRange,
			Routes: []picture.NetworkRoute{{
				Destination: netip.MustParsePrefix("10.100.0.0/24"), Gateway: netip.MustParseAddr("10.0.0.3"),
			}},
			Members: []picture.NetworkMember{
				{ServerID: 1000001, MAC: "86:00:00:00:00:01", IP: netip.MustParseAddr("10.0.0.2")},
				{ServerID: 1000002, MAC: "86:00:00:00:00:02", IP: netip.MustParseAddr("10.0.0.3"),
					AliasIPs: []netip.Addr{netip.MustParseAddr("10.0.5.5")}},
			}},
		{ID: 1000008, VLAN: 3, IPRange: ipRange,
			Members: []picture.NetworkMember{
				{ServerID: 1000003, MAC: guestMAC, IP: netip.MustParseAddr("10.0.0.2")},
				{ServerID: 1000004, MAC: "86:00:00:00:00:04", IP: netip.MustParseAddr("10.0.0.9")},
			}},
	}
	return lab
}

// addGuest plays a server's private NIC on the private wire: a device in
// the VLAN of its network, in a VRF of its own so that it does not share
// the namespace's routes with the appliance or the other guests, with its
// address as a /32 and the network's range through the gateway, as the
// lab's DHCP gives it. A parent device that exists (the VLAN of another
// guest) gets a macvlan, which has a MAC of its own.
func addGuest(t *testing.T, name string, vlan int, address string, table uint32) {
	t.Helper()
	vrf := &netlink.Vrf{LinkAttrs: netlink.LinkAttrs{Name: "vrf-" + name}, Table: table}
	if err := netlink.LinkAdd(vrf); err != nil {
		t.Fatal(err)
	}
	parent := wires[picture.DevicePrivate] + "." + strconv.Itoa(vlan)
	var device netlink.Link
	if existing, err := netlink.LinkByName(parent); err == nil {
		device = &netlink.Macvlan{
			LinkAttrs: netlink.LinkAttrs{Name: name, ParentIndex: existing.Attrs().Index},
			Mode:      netlink.MACVLAN_MODE_BRIDGE,
		}
	} else {
		device = &netlink.Vlan{
			LinkAttrs: netlink.LinkAttrs{Name: parent, ParentIndex: mustLink(t, wires[picture.DevicePrivate]).Attrs().Index},
			VlanId:    vlan,
		}
		name = parent
	}
	if err := netlink.LinkAdd(device); err != nil {
		t.Fatal(err)
	}
	writeSysctl(t, "net/ipv6/conf/"+name+"/disable_ipv6", "1")
	mustRun(t, "ip", "link", "set", "dev", name, "master", vrf.Name)
	mustRun(t, "ip", "link", "set", "dev", vrf.Name, "up")
	mustRun(t, "ip", "link", "set", "dev", name, "up")
	mustRun(t, "ip", "address", "add", address+"/32", "dev", name)
	tableArg := strconv.Itoa(int(table))
	mustRun(t, "ip", "route", "add", "10.0.0.1/32", "dev", name, "table", tableArg)
	mustRun(t, "ip", "route", "add", "10.0.0.0/16", "via", "10.0.0.1", "dev", name, "table", tableArg)
}

// writeSysctl sets a kernel setting below /proc/sys.
func writeSysctl(t *testing.T, key, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join("/proc/sys", key), []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pings reports whether a ping from a guest's VRF gets an answer.
func pings(guestVRF, address string) bool {
	return exec.Command("busybox", "ping", "-c", "1", "-W", "2", "-I", guestVRF, address).Run() == nil
}

func TestPrivateNetworksWithTheSameRangeStayApart(t *testing.T) {
	agent := setUp(t)
	lab := twoNetworksLab()
	putPicture(t, agent, agentproto.PictureLinks, picture.Links(lab))
	putPicture(t, agent, agentproto.PictureRuleset, picture.Ruleset(lab))
	checkAddresses(t, "private.2", "10.0.0.1/32", "169.254.169.254/32")
	checkAddresses(t, "private.3", "10.0.0.1/32", "169.254.169.254/32")
	checkRoutes(t, 10002, "10.0.0.2/32 private.2 src 10.0.0.1", "10.0.0.3/32 private.2 src 10.0.0.1",
		"10.0.5.5/32 private.2 src 10.0.0.1", "10.100.0.0/24 private.2 via 10.0.0.3")
	checkRoutes(t, 10003, "10.0.0.2/32 private.3 src 10.0.0.1", "10.0.0.9/32 private.3 src 10.0.0.1")

	addGuest(t, "a", 2, "10.0.0.2", 102) // network 1000007
	addGuest(t, "b", 2, "10.0.0.3", 103) // network 1000007, a macvlan
	addGuest(t, "c", 3, "10.0.0.2", 104) // network 1000008, the same address
	addGuest(t, "d", 3, "10.0.0.9", 105) // network 1000008
	for _, check := range []struct {
		from, to string
		answers  bool
	}{
		{"vrf-a", "10.0.0.1", true},  // each network's gateway
		{"vrf-c", "10.0.0.1", true},  // has the same address
		{"vrf-b", "10.0.0.2", true},  // through the gateway, to a in network 1000007
		{"vrf-d", "10.0.0.2", true},  // to c, never to a
		{"vrf-a", "10.0.0.9", false}, // d is in the other network
		{"vrf-c", "10.0.0.3", false}, // and b too
		{"vrf-a", "10.0.0.4", false}, // nobody holds it: no answer
	} {
		if got := pings(check.from, check.to); got != check.answers {
			t.Errorf("ping from %s to %s answered: %v, want %v", check.from, check.to, got, check.answers)
		}
	}
	// d reached c, not a: the appliance sent nothing into VLAN 2
	before := mustLink(t, "private.2").Attrs().Statistics.TxPackets
	pings("vrf-d", "10.0.0.2")
	if sent := mustLink(t, "private.2").Attrs().Statistics.TxPackets - before; sent != 0 {
		t.Errorf("network 1000008's ping sent %d packets into VLAN 2", sent)
	}
}

func TestPrivateNetworkDHCPGivesTheAddressAsHetznerDoes(t *testing.T) {
	agent := setUp(t)
	mac, _ := net.ParseMAC(guestMAC)
	if err := netlink.LinkSetHardwareAddr(mustLink(t, wires[picture.DevicePrivate]), mac); err != nil {
		t.Fatal(err)
	}
	lab := twoNetworksLab()
	putPicture(t, agent, agentproto.PictureLinks, picture.Links(lab))
	putPicture(t, agent, agentproto.PictureRuleset, picture.Ruleset(lab))
	putPicture(t, agent, agentproto.PictureDHCP, picture.DHCP(lab))
	addGuest(t, "c", 3, "10.0.0.250", 104)

	script := printScript(t)
	// a guest asks for these, as networkd does
	output, err := exec.Command("busybox", "udhcpc", "-i", wires[picture.DevicePrivate]+".3",
		"-n", "-q", "-f", "-s", script, "-t", "5",
		"-O", "staticroutes", "-O", "mtu", "-O", "router", "-O", "dns").CombinedOutput()
	text := string(output)
	if err != nil || !strings.Contains(text, "lease of 10.0.0.2 obtained from 10.0.0.1") {
		t.Fatalf("udhcpc: %v\n%s", err, text)
	}
	for _, want := range []string{"mask=32", "mtu=1450", "staticroutes=10.0.0.1/32 0.0.0.0 10.0.0.0/16 10.0.0.1"} {
		if !strings.Contains(text, want+"\n") {
			t.Errorf("the lease lacks %s:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"router=", "dns="} {
		if strings.Contains(text, unwanted) {
			t.Errorf("the lease has %s, which Hetzner's private networks do not send:\n%s", unwanted, text)
		}
	}
}

// A server without public addresses gets the route to the metadata
// service in its lease, and the service answers it through its network's
// VRF, as on Hetzner (measured 2026-11-09); a server of the other network
// with the same address is another server.
func TestServerWithoutPublicAddressesGetsMetadataThroughItsNetwork(t *testing.T) {
	agent := setUp(t)
	mac, _ := net.ParseMAC(guestMAC)
	if err := netlink.LinkSetHardwareAddr(mustLink(t, wires[picture.DevicePrivate]), mac); err != nil {
		t.Fatal(err)
	}
	lab := twoNetworksLab()
	lab.Servers = []picture.Server{
		{ID: 1000001, Name: "first", Location: "fsn1", MAC: "96:00:00:0f:42:41"},
		{ID: 1000003, Name: "third", Location: "fsn1", MAC: "96:00:00:0f:42:43"},
	}
	lab.Networks[0].Members[0].PrivateOnly = true // 10.0.0.2 in network 1000007
	lab.Networks[1].Members[0].PrivateOnly = true // 10.0.0.2 in network 1000008
	putPicture(t, agent, agentproto.PictureLinks, picture.Links(lab))
	putPicture(t, agent, agentproto.PictureRuleset, picture.Ruleset(lab))
	putPicture(t, agent, agentproto.PictureDHCP, picture.DHCP(lab))
	putPicture(t, agent, agentproto.PictureMetadata, picture.Metadata(lab, catalog.Builtin()))
	listener, err := ListenMetadata(picture.MetadataAddress.String() + ":80")
	if err != nil {
		t.Fatal(err)
	}
	server := agent.MetadataServer()
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	addGuest(t, "c", 3, "10.0.0.250", 104)
	output, err := exec.Command("busybox", "udhcpc", "-i", wires[picture.DevicePrivate]+".3",
		"-n", "-q", "-f", "-s", printScript(t), "-t", "5", "-O", "staticroutes").CombinedOutput()
	if want := "staticroutes=10.0.0.1/32 0.0.0.0 10.0.0.0/16 10.0.0.1 169.254.169.254/32 10.0.0.1\n"; err != nil || !strings.Contains(string(output), want) {
		t.Fatalf("udhcpc: %v, want %q in\n%s", err, want, output)
	}

	// the table local holds the service's address on lo, and in the
	// shared namespace it would take the guests' requests: it goes after
	// the VRFs' rule, as Linux's VRF documentation advises
	mustRun(t, "ip", "rule", "add", "pref", "32765", "lookup", "local")
	mustRun(t, "ip", "rule", "del", "pref", "0", "lookup", "local")
	t.Cleanup(func() {
		_ = exec.Command("ip", "rule", "add", "pref", "0", "lookup", "local").Run()
		_ = exec.Command("ip", "rule", "del", "pref", "32765", "lookup", "local").Run()
	})
	// both ask from 10.0.0.2, each in its own network, as the lease says;
	// the guests share the namespace with the appliance, whose chain
	// input would drop the answers they get
	mustRun(t, "nft", "insert", "rule", "inet", "hlab", "input", "iifname", "{ vrf-a, vrf-c }", "accept")
	addGuest(t, "a", 2, "10.0.0.2", 102)
	mustRun(t, "ip", "address", "add", "10.0.0.2/32", "dev", wires[picture.DevicePrivate]+".3")
	for vrf, want := range map[string]string{"vrf-a": "first", "vrf-c": "third"} {
		mustRun(t, "ip", "route", "add", "169.254.169.254/32", "via", "10.0.0.1", "src", "10.0.0.2", "vrf", vrf)
		if got := metadataFrom(t, vrf, "/hetzner/v1/metadata/hostname"); got != want {
			t.Errorf("the hostname through %s is %q, want %q", vrf, got, want)
		}
	}
}

// printScript returns a udhcpc script that prints what udhcpc hands it:
// the lease's options.
func printScript(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "print")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv | grep -E '^(ip|mask|router|dns|mtu|staticroutes)='\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// metadataFrom asks the metadata service from a guest's VRF.
func metadataFrom(t *testing.T, vrf, path string) string {
	t.Helper()
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, _ string, conn syscall.RawConn) error {
		var bindErr error
		err := conn.Control(func(fd uintptr) {
			bindErr = syscall.BindToDevice(int(fd), vrf)
		})
		return errors.Join(err, bindErr)
	}}
	client := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}, Timeout: 5 * time.Second}
	answer, err := client.Get("http://" + picture.MetadataAddress.String() + path)
	if err != nil {
		t.Fatalf("GET %s through %s: %v", path, vrf, err)
	}
	defer answer.Body.Close()
	body, _ := io.ReadAll(answer.Body)
	return string(body)
}
