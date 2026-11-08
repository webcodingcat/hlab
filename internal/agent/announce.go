//go:build linux

package agent

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// announcer tells the neighbors about the appliance's addresses whenever a
// device's carrier comes up. After an update the new appliance has other MAC
// addresses than the old one, and the neighbors' caches would hold the old
// MACs for seconds: the kernel's arp_notify alone does not reach them.
type announcer struct {
	log *slog.Logger
	// mu guards the fields below.
	mu        sync.Mutex
	addresses map[string][]netip.Prefix // to announce, by device
	carrier   map[string]bool           // the last state seen, by device
}

func newAnnouncer(log *slog.Logger) *announcer {
	return &announcer{log: log, carrier: map[string]bool{}}
}

// set takes the addresses to announce from a links picture and announces
// those of devices that have carrier now; the others follow when their
// carrier comes up.
func (n *announcer) set(addresses []agentproto.Address) {
	byDevice := map[string][]netip.Prefix{}
	for _, address := range addresses {
		if address.Announce {
			byDevice[address.Device] = append(byDevice[address.Device], address.Prefix)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addresses = byDevice
	for device, prefixes := range byDevice {
		link, err := netlink.LinkByName(device)
		if err != nil {
			continue
		}
		n.carrier[device] = link.Attrs().OperState == netlink.OperUp
		if n.carrier[device] {
			go n.announce(link, prefixes)
		}
	}
}

// WatchCarrier announces a device's addresses each time its carrier comes
// up, until ctx ends.
func (a *Agent) WatchCarrier(ctx context.Context) error {
	n := a.announcer
	updates := make(chan netlink.LinkUpdate)
	if err := netlink.LinkSubscribe(updates, ctx.Done()); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case update := <-updates:
			device := update.Attrs().Name
			up := update.Attrs().OperState == netlink.OperUp
			n.mu.Lock()
			cameUp := up && !n.carrier[device]
			n.carrier[device] = up
			prefixes := n.addresses[device]
			n.mu.Unlock()
			if cameUp && len(prefixes) > 0 {
				go n.announce(update.Link, prefixes)
			}
		}
	}
}

// announce sends each address twice, a second apart, as arping -U does:
// a gratuitous ARP for IPv4, an unsolicited neighbor advertisement for
// IPv6.
func (n *announcer) announce(link netlink.Link, prefixes []netip.Prefix) {
	device := link.Attrs().Name
	for round := range 2 {
		if round > 0 {
			time.Sleep(time.Second)
		}
		for _, prefix := range prefixes {
			var err error
			if prefix.Addr().Is4() {
				err = sendGratuitousARP(link, prefix.Addr())
			} else {
				err = sendNeighborAdvertisement(link, prefix.Addr())
			}
			if err != nil {
				n.log.Warn("announcing an address", "device", device, "address", prefix, "error", err)
			}
		}
	}
	n.log.Info("addresses announced", "device", device, "addresses", prefixes)
}

// sendGratuitousARP broadcasts an ARP request for the device's own address,
// with its MAC as the sender.
func sendGratuitousARP(link netlink.Link, address netip.Addr) error {
	const ethARP = 0x0806
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(ethARP)))
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Close(fd) }() // closing a socket that sent cannot fail
	mac := link.Attrs().HardwareAddr
	broadcast := net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	ip := address.As4()
	frame := make([]byte, 0, 42)
	frame = append(frame, broadcast...)
	frame = append(frame, mac...)
	frame = binary.BigEndian.AppendUint16(frame, ethARP)
	frame = append(frame, 0, 1, 8, 0, 6, 4, 0, 1) // Ethernet, IPv4, request
	frame = append(frame, mac...)
	frame = append(frame, ip[:]...)
	frame = append(frame, 0, 0, 0, 0, 0, 0) // the target MAC is unknown
	frame = append(frame, ip[:]...)
	target := &syscall.SockaddrLinklayer{Protocol: htons(ethARP), Ifindex: link.Attrs().Index, Halen: 6}
	copy(target.Addr[:], broadcast)
	return syscall.Sendto(fd, frame, 0, target)
}

// sendNeighborAdvertisement sends an unsolicited neighbor advertisement
// for the address to all nodes, with the override flag, so that they
// replace the MAC they have cached. The kernel fills in the checksum.
func sendNeighborAdvertisement(link netlink.Link, address netip.Addr) error {
	fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_RAW, syscall.IPPROTO_ICMPV6)
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Close(fd) }() // closing a socket that sent cannot fail
	// neighbor discovery requires the hop limit 255
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_HOPS, 255); err != nil {
		return err
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_IF, link.Attrs().Index); err != nil {
		return err
	}
	// the address itself is the source: the appliance has no other
	// link-local address on its devices
	source := &syscall.SockaddrInet6{Addr: address.As16(), ZoneId: uint32(link.Attrs().Index)}
	if err := syscall.Bind(fd, source); err != nil {
		return errors.Join(errors.New("binding to the address"), err)
	}
	target := address.As16()
	message := []byte{136, 0, 0, 0, 0x20, 0, 0, 0} // advertisement, override
	message = append(message, target[:]...)
	message = append(message, 2, 1) // option: target link-layer address
	message = append(message, link.Attrs().HardwareAddr...)
	allNodes := &syscall.SockaddrInet6{Addr: netip.MustParseAddr("ff02::1").As16(),
		ZoneId: uint32(link.Attrs().Index)}
	return syscall.Sendto(fd, message, 0, allNodes)
}

// htons converts a 16-bit value to network byte order.
func htons(value uint16) uint16 {
	return value<<8 | value>>8
}
