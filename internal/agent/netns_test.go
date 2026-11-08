//go:build linux && netns

// The agent's tests in a network namespace of their own: they change links,
// addresses, routes, nftables and sysctls for real, so they need
// CAP_NET_ADMIN and must never run on a developer's machine as it is.
// `bin/dev test --agent` runs them in a container without network; CI runs
// them with sudo in a new namespace (docs/tools.md).

package agent

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/picture"
)

// The appliance's NICs are veth pairs: the agent renames the one end by its
// MAC address, the test watches the other end, the "wire".
var wires = map[string]string{
	picture.DeviceLink:    "wire-link",
	picture.DevicePublic:  "wire-public",
	picture.DevicePrivate: "wire-private",
	picture.DeviceUplink:  "wire-uplink",
}

// The servers of the test lab; the first one's MAC is on the public wire,
// so it can ask for its address by DHCP.
var (
	web = picture.Server{
		ID: 1000001, Name: "web", Location: "fsn1", MAC: "96:00:00:0f:42:41",
		IPv4: netip.MustParseAddr("198.19.0.1"), IPv6: netip.MustParsePrefix("2001:db8:1:1::/64"),
		Internet: true,
	}
	db = picture.Server{
		ID: 1000002, Name: "db", Location: "nbg1", MAC: "96:00:00:0f:42:42",
		IPv4: netip.MustParseAddr("198.19.0.2"),
	}
)

// setUp removes what an earlier test left and creates the appliance's four
// NICs with MAC set A and names the agent must change.
func setUp(t *testing.T) *Agent {
	t.Helper()
	links, err := netlink.LinkList()
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.Type() == "veth" || link.Type() == "vrf" || link.Type() == "vlan" {
			_ = netlink.LinkDel(link) // a pair goes with either end
		}
	}
	mustRun(t, "nft", "flush", "ruleset")
	// lo as a fresh namespace has it (CI's unshare): down, without
	// addresses; Docker's comes up with them
	mustRun(t, "ip", "link", "set", "lo", "down")
	mustRun(t, "ip", "address", "flush", "dev", "lo")
	for nic, device := range []string{picture.DeviceLink, picture.DevicePublic, picture.DevicePrivate, picture.DeviceUplink} {
		mac, _ := net.ParseMAC(picture.MACSetA.MAC(nic + 1))
		veth := &netlink.Veth{
			LinkAttrs: netlink.LinkAttrs{Name: fmt.Sprintf("eth%d", nic), HardwareAddr: mac},
			PeerName:  wires[device],
		}
		if device == picture.DevicePublic {
			veth.PeerHardwareAddr, _ = net.ParseMAC(web.MAC)
		}
		if err := netlink.LinkAdd(veth); err != nil {
			t.Fatal(err)
		}
		setLink(t, wires[device], true)
	}
	dir := t.TempDir()
	config := filepath.Join(dir, dhcpFile)
	t.Cleanup(func() { stopDNSMasq(config) })
	return New(Options{
		Version:     "test",
		Dir:         dir,
		RestartDHCP: []string{"sh", "-c", restartDNSMasq, config},
		Log:         slog.New(slog.NewTextHandler(t.Output(), nil)),
	})
}

// restartDNSMasq stands in for systemd: it stops the dnsmasq of the
// configuration in $0 and starts it again.
const restartDNSMasq = `pid="$0.pid"
if [ -f "$pid" ]; then
  kill "$(cat "$pid")"
  while kill -0 "$(cat "$pid")" 2>/dev/null; do sleep 0.05; done
fi
dnsmasq --conf-file="$0" --pid-file="$pid"`

// stopDNSMasq stops the dnsmasq that restartDNSMasq started, if any.
func stopDNSMasq(config string) {
	if pid, err := os.ReadFile(config + ".pid"); err == nil {
		_ = exec.Command("kill", strings.TrimSpace(string(pid))).Run()
	}
}

// testLabPicture returns the links picture of the test lab plus a private
// network in a VRF, which phase 2a's pictures will have.
func testLabPicture(servers ...picture.Server) agentproto.Links {
	lab := picture.DefaultLab()
	lab.Servers = servers
	links := picture.Links(lab)
	links.VRFs = []agentproto.VRF{{Name: "vrf-net-7", Table: 1007}}
	links.VLANs = []agentproto.VLAN{{Name: "private.100", Parent: picture.DevicePrivate, ID: 100, VRF: "vrf-net-7"}}
	links.Addresses = append(links.Addresses, agentproto.Address{
		Device: "private.100", Prefix: netip.MustParsePrefix("10.0.0.1/16"),
	})
	links.Routes = append(links.Routes, agentproto.Route{
		Destination: netip.MustParsePrefix("10.1.0.0/16"), Device: "private.100",
		Gateway: netip.MustParseAddr("10.0.0.2"), Table: 1007,
	})
	return links
}

func TestLinksPictureSetsNamesAddressesRoutesAndVRFs(t *testing.T) {
	agent := setUp(t)
	putPicture(t, agent, agentproto.PictureLinks, testLabPicture(web, db))

	for _, device := range []string{"link", "public", "private"} {
		if link := mustLink(t, device); link.Attrs().Flags&net.FlagUp == 0 {
			t.Errorf("%s is down", device)
		}
	}
	if link := mustLink(t, "uplink"); link.Attrs().Flags&net.FlagUp != 0 {
		t.Error("the agent set the uplink up, which is networkd's job")
	}
	checkAddresses(t, "link", "198.18.0.2/29", "198.18.0.3/32", "2001:db8:0:ffff::2/64", "fe80::2/64")
	checkAddresses(t, "public", "172.31.1.1/32", "fe80::1/64")
	checkAddresses(t, "lo", "127.0.0.1/8", "169.254.169.254/32", "::1/128")
	checkAddresses(t, "private.100", "10.0.0.1/16")

	vrf, isVRF := mustLink(t, "vrf-net-7").(*netlink.Vrf)
	if !isVRF || vrf.Table != 1007 {
		t.Fatalf("vrf-net-7 is %+v", mustLink(t, "vrf-net-7"))
	}
	vlan := mustLink(t, "private.100").(*netlink.Vlan)
	if vlan.VlanId != 100 || vlan.ParentIndex != mustLink(t, "private").Attrs().Index ||
		vlan.MasterIndex != vrf.Index {
		t.Errorf("private.100 is %+v", vlan.Attrs())
	}
	checkRoutes(t, 0, "198.19.0.1/32 public src 172.31.1.1", "198.19.0.2/32 public src 172.31.1.1",
		"2001:db8:1:1::/64 public")
	checkRoutes(t, 1007, "10.1.0.0/16 private.100 via 10.0.0.2")
	for _, key := range []string{"net/ipv4/conf/public/send_redirects", "net/ipv4/conf/uplink/send_redirects"} {
		if value := readSysctl(t, key); value != "0" {
			t.Errorf("%s is %s", key, value)
		}
	}
	if value := readSysctl(t, "net/ipv6/conf/link/addr_gen_mode"); value != "1" {
		t.Errorf("addr_gen_mode of link is %s", value)
	}
}

func TestNewLinksPictureRemovesWhatItLacks(t *testing.T) {
	agent := setUp(t)
	putPicture(t, agent, agentproto.PictureLinks, testLabPicture(web, db))
	// what networkd sets on the uplink is not the agent's
	uplink := mustLink(t, "uplink")
	mustRun(t, "ip", "address", "add", "172.23.240.5/20", "dev", "uplink")
	if err := netlink.LinkSetUp(uplink); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "ip", "route", "add", "default", "via", "172.23.240.1", "dev", "uplink", "proto", "dhcp")

	smaller := picture.Links(func() picture.Lab {
		lab := picture.DefaultLab()
		lab.Servers = []picture.Server{web}
		return lab
	}())
	smaller.Addresses = slices.DeleteFunc(smaller.Addresses, func(a agentproto.Address) bool {
		return a.Prefix == netip.MustParsePrefix("198.18.0.3/32")
	})
	putPicture(t, agent, agentproto.PictureLinks, smaller)

	checkAddresses(t, "link", "198.18.0.2/29", "2001:db8:0:ffff::2/64", "fe80::2/64")
	// the uplink keeps its addresses, the kernel's link-local one too
	checkAddresses(t, "uplink", "172.23.240.5/20", "fe80::48:4cff:fe0a:4/64")
	checkRoutes(t, 0, "198.19.0.1/32 public src 172.31.1.1", "2001:db8:1:1::/64 public")
	if _, err := netlink.LinkByName("vrf-net-7"); err == nil {
		t.Error("the VRF is still there")
	}
	if _, err := netlink.LinkByName("private.100"); err == nil {
		t.Error("the VLAN is still there")
	}
	if output := mustRun(t, "ip", "route", "show", "default"); !strings.Contains(output, "172.23.240.1") {
		t.Errorf("networkd's default route is gone: %q", output)
	}
}

func TestFloatingIPRoutesGoThroughTheirServerAndMove(t *testing.T) {
	agent := setUp(t)
	floating := func(serverID int64) agentproto.Links {
		lab := picture.DefaultLab()
		lab.Servers = []picture.Server{web, db}
		lab.FloatingIPs = []picture.FloatingIP{
			{Prefix: netip.MustParsePrefix("198.19.0.9/32"), ServerID: serverID},
			{Prefix: netip.MustParsePrefix("2001:db8:1:9::/64"), ServerID: web.ID},
		}
		return picture.Links(lab)
	}
	// a fresh namespace: the routes through a gateway need the on-link
	// routes to their servers first, which the same picture brings
	putPicture(t, agent, agentproto.PictureLinks, floating(web.ID))
	checkRoutes(t, 0, "198.19.0.1/32 public src 172.31.1.1", "198.19.0.2/32 public src 172.31.1.1",
		"2001:db8:1:1::/64 public", "198.19.0.9/32 public via 198.19.0.1 src 172.31.1.1",
		"2001:db8:1:9::/64 public via 2001:db8:1:1::1")

	putPicture(t, agent, agentproto.PictureLinks, floating(db.ID))
	checkRoutes(t, 0, "198.19.0.1/32 public src 172.31.1.1", "198.19.0.2/32 public src 172.31.1.1",
		"2001:db8:1:1::/64 public", "198.19.0.9/32 public via 198.19.0.2 src 172.31.1.1",
		"2001:db8:1:9::/64 public via 2001:db8:1:1::1")
}

func TestRenamingKeepsNetworkdsUplinkUp(t *testing.T) {
	agent := setUp(t)
	setLink(t, "eth3", true) // networkd brought it up before the agent's first picture
	putPicture(t, agent, agentproto.PictureLinks, testLabPicture(web))

	if link := mustLink(t, "uplink"); link.Attrs().Flags&net.FlagUp == 0 {
		t.Error("the uplink is down after the agent renamed it; networkd would leave it so")
	}
}

func TestLinksPictureWithAnUnknownMACChangesNothing(t *testing.T) {
	agent := setUp(t)
	links := testLabPicture(web)
	links.Interfaces[3].MAC = "02:48:4c:0b:00:04" // set B: not there
	answer := put(t, agent, agentproto.PictureLinks, mustJSON(t, links))
	if answer.StatusCode != 422 {
		t.Fatalf("status %d, want 422", answer.StatusCode)
	}
	if _, err := netlink.LinkByName("link"); err == nil {
		t.Error("a NIC was renamed although the picture was refused")
	}
}

func TestRulesetPictureIsLoadedWholeOrNotAtAll(t *testing.T) {
	agent := setUp(t)
	lab := picture.DefaultLab()
	lab.Servers = []picture.Server{web, db}
	putPicture(t, agent, agentproto.PictureRuleset, picture.Ruleset(lab))
	listed := mustRun(t, "nft", "list", "set", "inet", "hlab", "no_internet")
	if !strings.Contains(listed, "198.19.0.2") || strings.Contains(listed, "198.19.0.1") {
		t.Errorf("no_internet holds the wrong servers:\n%s", listed)
	}
	broken := picture.Ruleset(picture.DefaultLab())
	broken.Text += "table inet broken { chain c { no such rule } }\n"
	if answer := put(t, agent, agentproto.PictureRuleset, mustJSON(t, broken)); answer.StatusCode != 422 {
		t.Errorf("a broken ruleset got %d, want 422", answer.StatusCode)
	}
	if listed := mustRun(t, "nft", "list", "set", "inet", "hlab", "no_internet"); !strings.Contains(listed, "198.19.0.2") {
		t.Errorf("the broken ruleset replaced the old one:\n%s", listed)
	}
}

func TestDHCPPictureGivesAServerItsAddress(t *testing.T) {
	agent := setUp(t)
	lab := picture.DefaultLab()
	lab.Servers = []picture.Server{web, db}
	putPicture(t, agent, agentproto.PictureLinks, picture.Links(lab))
	putPicture(t, agent, agentproto.PictureDHCP, picture.DHCP(lab))

	// the public wire carries web's MAC: it plays the server
	output, err := exec.Command("busybox", "udhcpc", "-i", wires[picture.DevicePublic],
		"-n", "-q", "-f", "-s", "/bin/true", "-t", "5").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "lease of 198.19.0.1 obtained from 172.31.1.1") {
		t.Errorf("udhcpc: %v\n%s", err, output)
	}
	broken := picture.DHCP(lab)
	broken.Config += "no-such-option\n"
	if answer := put(t, agent, agentproto.PictureDHCP, mustJSON(t, broken)); answer.StatusCode != 422 {
		t.Errorf("a broken configuration got %d, want 422", answer.StatusCode)
	}
}

func TestAddressesAreAnnouncedWhenTheCarrierComesUp(t *testing.T) {
	agent := setUp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = agent.WatchCarrier(ctx) }()
	time.Sleep(100 * time.Millisecond) // let it subscribe

	// as after an update: the picture comes first, the carrier later
	setLink(t, wires[picture.DeviceLink], false)
	putPicture(t, agent, agentproto.PictureLinks, testLabPicture(web))
	frames := capture(t, wires[picture.DeviceLink], func() { setLink(t, wires[picture.DeviceLink], true) })
	mac := picture.MACSetA.MAC(1)
	if !slices.Contains(frames, "arp "+mac+" 198.18.0.2") {
		t.Errorf("no gratuitous ARP for 198.18.0.2 from %s; seen: %v", mac, frames)
	}
	if !slices.Contains(frames, "na "+mac+" 2001:db8:0:ffff::2") {
		t.Errorf("no neighbor advertisement for 2001:db8:0:ffff::2; seen: %v", frames)
	}
}

// capture runs action and returns the announcements that arrive on a
// device within two seconds after it, as "arp <mac> <ip>" and
// "na <mac> <ip>".
func capture(t *testing.T, device string, action func()) []string {
	t.Helper()
	const ethAll = 0x0300 // htons(ETH_P_ALL)
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, ethAll)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	link := mustLink(t, device)
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: ethAll, Ifindex: link.Attrs().Index}); err != nil {
		t.Fatal(err)
	}
	timeout := syscall.NsecToTimeval((200 * time.Millisecond).Nanoseconds())
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &timeout); err != nil {
		t.Fatal(err)
	}
	action()
	var seen []string
	buffer := make([]byte, 2048)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		n, _, err := syscall.Recvfrom(fd, buffer, 0)
		if err != nil || n < 14 {
			continue
		}
		frame := buffer[:n]
		source := net.HardwareAddr(frame[6:12]).String()
		switch binary.BigEndian.Uint16(frame[12:14]) {
		case 0x0806: // ARP; the sender's address follows its MAC
			if n >= 42 {
				seen = append(seen, "arp "+source+" "+net.IP(frame[28:32]).String())
			}
		case 0x86dd: // IPv6: ICMPv6 (58) type 136 is a neighbor advertisement
			if n >= 78 && frame[20] == 58 && frame[54] == 136 {
				seen = append(seen, "na "+source+" "+net.IP(frame[62:78]).String())
			}
		}
	}
	return seen
}

// checkAddresses fails unless a device has exactly these addresses.
func checkAddresses(t *testing.T, device string, want ...string) {
	t.Helper()
	addresses, err := netlink.AddrList(mustLink(t, device), netlink.FAMILY_ALL)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, address := range addresses {
		got = append(got, toPrefix(address.IPNet).String())
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s has %v, want %v", device, got, want)
	}
}

// checkRoutes fails unless the agent's routes in a table (0: main) are
// exactly these, as "<destination> <device>[ via <gateway>][ src <source>]".
func checkRoutes(t *testing.T, table int, want ...string) {
	t.Helper()
	if table == 0 {
		table = syscall.RT_TABLE_MAIN
	}
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL,
		&netlink.Route{Protocol: routeProtocol, Table: table}, netlink.RT_FILTER_PROTOCOL|netlink.RT_FILTER_TABLE)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, route := range routes {
		link, err := netlink.LinkByIndex(route.LinkIndex)
		if err != nil {
			t.Fatal(err)
		}
		text := toPrefix(route.Dst).String() + " " + link.Attrs().Name
		if route.Gw != nil {
			text += " via " + route.Gw.String()
		}
		if route.Src != nil {
			text += " src " + route.Src.String()
		}
		got = append(got, text)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("routes of table %d: %v, want %v", table, got, want)
	}
}

func mustLink(t *testing.T, name string) netlink.Link {
	t.Helper()
	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return link
}

func setLink(t *testing.T, name string, up bool) {
	t.Helper()
	link := mustLink(t, name)
	set := netlink.LinkSetDown
	if up {
		set = netlink.LinkSetUp
	}
	if err := set(link); err != nil {
		t.Fatal(err)
	}
}

func readSysctl(t *testing.T, key string) string {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("/proc/sys", key))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(value))
}

func mustRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return string(output)
}
