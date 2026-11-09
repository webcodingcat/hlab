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
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	t.Cleanup(func() { stopAllDNSMasq(dir) })
	return New(Options{
		Version: "test",
		Dir:     dir,
		RestartDHCP: func(device string) []string {
			return []string{"sh", "-c", stopDNSMasq + "\n" + startDNSMasq, filepath.Join(dir, dhcpFile(device))}
		},
		StopDHCP: func(device string) []string {
			return []string{"sh", "-c", stopDNSMasq, filepath.Join(dir, dhcpFile(device))}
		},
		Log: testLog(t),
	})
}

// testLog returns a logger that writes to the test's output until the test
// ends: the agent announces its addresses in goroutines a second after a
// links picture, often after the test, whose output then refuses writes.
func testLog(t *testing.T) *slog.Logger {
	out := &endingWriter{out: t.Output()}
	t.Cleanup(func() {
		out.mu.Lock()
		defer out.mu.Unlock()
		out.ended = true
	})
	return slog.New(slog.NewTextHandler(out, nil))
}

// endingWriter writes to out until it has ended, and drops what comes
// after.
type endingWriter struct {
	mu    sync.Mutex
	out   io.Writer
	ended bool
}

func (w *endingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ended {
		return len(data), nil
	}
	return w.out.Write(data)
}

// stopDNSMasq and startDNSMasq stand in for systemd: they stop and start
// the dnsmasq of the configuration in $0.
const (
	stopDNSMasq = `pid="$0.pid"
if [ -f "$pid" ]; then
  kill "$(cat "$pid")"
  while kill -0 "$(cat "$pid")" 2>/dev/null; do sleep 0.05; done
  rm "$pid"
fi`
	startDNSMasq = `dnsmasq --conf-file="$0" --pid-file="$0.pid"`
)

// stopAllDNSMasq stops the dnsmasq instances that a test started and
// waits until they are gone, so that none answers the next test. A
// container's first process, the test, reaps no orphans: a stopped
// dnsmasq stays a zombie there, which counts as gone.
func stopAllDNSMasq(dir string) {
	pidFiles, _ := filepath.Glob(filepath.Join(dir, "*.pid"))
	for _, pidFile := range pidFiles {
		text, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(text)))
		if err != nil || syscall.Kill(pid, syscall.SIGTERM) != nil {
			continue
		}
		for range 100 {
			stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			if err != nil || strings.Contains(string(stat), ") Z ") {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
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
	// the lab's ranges end in a blackhole: an address nobody holds
	// answers nothing
	for _, family := range []string{"-4", "-6"} {
		output := mustRun(t, "ip", family, "route", "show", "type", "blackhole")
		if !strings.Contains(output, "198.18.0.0/15") && !strings.Contains(output, "2001:db8::/32") {
			t.Errorf("ip %s route show type blackhole: %q, want the lab's range", family, output)
		}
	}
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

// TestRulesetWithFirewallsLoads applies a ruleset with every kind of
// firewall rule: nft must take the maps, the servers' chains and the
// firewalls' chains as the picture writes them.
func TestRulesetWithFirewallsLoads(t *testing.T) {
	agent := setUp(t)
	lab := picture.DefaultLab()
	lab.Servers = []picture.Server{web, db}
	lab.FloatingIPs = []picture.FloatingIP{{Prefix: netip.MustParsePrefix("2001:db8:1:5::/64"), ServerID: db.ID}}
	everywhere := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	some := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	lab.Firewalls = []picture.Firewall{
		{ID: 1000010, ServerIDs: []int64{web.ID, db.ID}, Rules: []picture.FirewallRule{
			{Direction: picture.DirectionIn, Protocol: "tcp", Port: "22", Addresses: everywhere},
			{Direction: picture.DirectionIn, Protocol: "udp", Port: "1000-2000", Addresses: some},
			{Direction: picture.DirectionIn, Protocol: "icmp", Addresses: everywhere},
			{Direction: picture.DirectionIn, Protocol: "esp", Addresses: some},
			{Direction: picture.DirectionIn, Protocol: "gre", Addresses: some},
			{Direction: picture.DirectionOut, Protocol: "tcp", Port: "443", Addresses: some},
			{Direction: picture.DirectionOut, Protocol: "gre", Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")}},
		}},
		{ID: 1000011, ServerIDs: []int64{db.ID}},
	}
	putPicture(t, agent, agentproto.PictureRuleset, picture.Ruleset(lab))
	listed := mustRun(t, "nft", "list", "chain", "inet", "hlab", "server_1000002_in")
	if !strings.Contains(listed, "jump firewall_1000010_in") || !strings.Contains(listed, "drop") {
		t.Errorf("db's inbound chain:\n%s", listed)
	}
	listed = mustRun(t, "nft", "list", "map", "inet", "hlab", "firewall_in6")
	if !strings.Contains(listed, "2001:db8:1:5::/64") {
		t.Errorf("db's Floating IP is missing from the inbound map:\n%s", listed)
	}
}

// TestDHCPPictureGivesAServerItsAddress checks Hetzner's public lease
// (measured 2026-11-09): the server's address and the routes to the
// on-link gateway, then the default route, in options 121 and 249 alike.
func TestDHCPPictureGivesAServerItsAddress(t *testing.T) {
	agent := setUp(t)
	lab := picture.DefaultLab()
	lab.Servers = []picture.Server{web, db}
	putPicture(t, agent, agentproto.PictureLinks, picture.Links(lab))
	putPicture(t, agent, agentproto.PictureDHCP, picture.DHCP(lab))

	// the public wire carries web's MAC: it plays the server
	output, err := exec.Command("busybox", "udhcpc", "-i", wires[picture.DevicePublic],
		"-n", "-q", "-f", "-s", leaseScript(t), "-t", "5", "-O", "staticroutes", "-O", "msstaticroutes").CombinedOutput()
	routes := "172.31.1.1/32 0.0.0.0 0.0.0.0/0 172.31.1.1"
	want := []string{"ip=198.19.0.1 ", " routes=" + routes + " ms=" + routes + "\n"}
	if err != nil || !strings.Contains(string(output), want[0]) || !strings.Contains(string(output), want[1]) {
		t.Errorf("udhcpc: %v\n%s\nwant %q", err, output, want)
	}
	broken := picture.DHCP(lab)
	broken.Instances[0].Config += "no-such-option\n"
	if answer := put(t, agent, agentproto.PictureDHCP, mustJSON(t, broken)); answer.StatusCode != 422 {
		t.Errorf("a broken configuration got %d, want 422", answer.StatusCode)
	}
}

// TestDHCPPictureGivesAServerWithIPv6OnlyItsServiceLease checks Hetzner's
// lease for a server with IPv6 only (measured 2026-11-09): its service
// address, routes only to the gateway, the metadata service and Hetzner's
// DHCP server in options 121 and 249, and no DNS server.
func TestDHCPPictureGivesAServerWithIPv6OnlyItsServiceLease(t *testing.T) {
	agent := setUp(t)
	lab := picture.DefaultLab()
	onlySix := web
	onlySix.IPv4 = netip.Addr{}
	onlySix.ServiceIPv4 = netip.MustParseAddr("100.64.0.1")
	lab.Servers = []picture.Server{onlySix, db}
	putPicture(t, agent, agentproto.PictureLinks, picture.Links(lab))
	putPicture(t, agent, agentproto.PictureDHCP, picture.DHCP(lab))

	output, err := exec.Command("busybox", "udhcpc", "-i", wires[picture.DevicePublic],
		"-n", "-q", "-f", "-s", leaseScript(t), "-t", "5", "-O", "staticroutes", "-O", "msstaticroutes").CombinedOutput()
	routes := "172.31.1.1/32 0.0.0.0 169.254.169.254/32 172.31.1.1 188.40.24.200/32 172.31.1.1"
	want := "ip=100.64.0.1 dns= routes=" + routes + " ms=" + routes
	if err != nil || !strings.Contains(string(output), want) {
		t.Errorf("udhcpc: %v\n%s\nwant %s", err, output, want)
	}
}

// leaseScript returns udhcpc's script that prints a lease's address, DNS
// servers and the routes of options 121 and 249.
func leaseScript(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "lease")
	text := "#!/bin/sh\n" +
		`[ "$1" = bound ] && echo "ip=$ip dns=$dns routes=$staticroutes ms=$msstaticroutes"` + "\n"
	if err := os.WriteFile(script, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
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
		if route.Type == syscall.RTN_BLACKHOLE {
			continue // TestLinksPictureSetsNamesAddressesRoutesAndVRFs checks them
		}
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
