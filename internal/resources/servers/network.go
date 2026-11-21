package servers

import (
	"errors"
	"net"
	"net/netip"
	"strconv"

	"github.com/webcodingcat/hlab/internal/cidata"
	"github.com/webcodingcat/hlab/internal/store"
)

// PrivateNet is an entry of a server's private_net (Hetzner's shape).
type PrivateNet struct {
	Network    int64    `json:"network"`
	IP         string   `json:"ip"`
	AliasIPs   []string `json:"alias_ips"`
	MACAddress string   `json:"mac_address"`
}

// NetInternal is what adapter and seed need of an attached network.
type NetInternal struct {
	VLAN    int    `json:"vlan"`
	IPRange string `json:"ip_range"` // gateway = first host address
}

// NetState is the network state of a server.
type NetState struct {
	PrivateNet []PrivateNet          `json:"private_net"`
	Networks   map[int64]NetInternal `json:"networks"`
}

// shownPrivateNet is the private_net the API shows of r.
func (r Record) shownPrivateNet() []PrivateNet {
	if r.Hlab.NetBefore != nil {
		return r.Hlab.NetBefore.PrivateNet
	}
	return r.PrivateNet
}

// PublicNetRequest is the public_net of a server create; IPv4 or IPv6 0
// means make a new primary IP.
type PublicNetRequest struct {
	EnableIPv4, EnableIPv6 bool // default true
	IPv4, IPv6             int64
}

// PublicIPs makes or takes the primary IPs of a new server inside the
// create transaction. Without it (nil) the server takes its IPv4 from
// ipam and the IDs from the primary_ips sequence, without records.
type PublicIPs interface {
	Allocate(tx store.Tx, projectID, serverID int64, location string, req PublicNetRequest) (PublicNet, error)
}

// Networks checks and allocates the networks of a server create inside the
// create transaction; errors are api field errors as at Hetzner.
type Networks interface {
	AttachAtCreate(tx store.Tx, projectID, serverID int64, location string, ids []int64) ([]PrivateNet, map[int64]NetInternal, error)
}

// SetPublicIPs sets the primary IP hook of server create.
func (m *Module) SetPublicIPs(h PublicIPs) { m.publicIPs = h }

// SetNetworks sets the network hook of server create.
func (m *Module) SetNetworks(h Networks) { m.networks = h }

// OnDelete registers f; server deletion runs every f inside the
// transaction that deletes the record.
func (m *Module) OnDelete(f func(tx store.Tx, serverID int64) error) {
	m.onDelete = append(m.onDelete, f)
}

// Update loads server id, applies f and stores it.
func Update(tx store.Tx, id int64, f func(*Record) error) error {
	r, err := Load(tx, id)
	if err != nil {
		return err
	}
	if err := f(&r); err != nil {
		return err
	}
	return put(tx, r)
}

// privateMACKey is the kv counter of private adapter MACs.
const privateMACKey = "mac.private.last"

// PrivateMAC is the MAC of private adapter number n: 02:48:4c, then 0x2
// and the low 20 bits of n.
func PrivateMAC(n int64) net.HardwareAddr {
	v := uint32(n)&0xfffff | 0x200000
	return net.HardwareAddr{0x02, 0x48, 0x4c, byte(v >> 16), byte(v >> 8), byte(v)}
}

// NextPrivateMAC counts up the private adapter counter and returns its MAC.
func NextPrivateMAC(tx store.Tx) (net.HardwareAddr, error) {
	var last int64
	v, err := store.GetKV(tx, privateMACKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		if last, err = strconv.ParseInt(v, 10, 64); err != nil {
			return nil, err
		}
	}
	last++
	if err := store.SetKV(tx, privateMACKey, strconv.FormatInt(last, 10)); err != nil {
		return nil, err
	}
	return PrivateMAC(last), nil
}

// netGateway is the gateway of a network range: its first host address.
func netGateway(ipRange netip.Prefix) netip.Addr {
	return ipRange.Masked().Addr().Next()
}

// privateInterfaces are the seed interfaces of rec's private networks: the
// private IP as /32 and the network range via the network gateway (on-link,
// cidata marks it). The network's routes act only in the agent's VRF.
func privateInterfaces(rec Record) []cidata.Interface {
	var out []cidata.Interface
	for _, p := range rec.PrivateNet {
		ip, err := netip.ParseAddr(p.IP)
		if err != nil {
			continue
		}
		iface := cidata.Interface{MAC: p.MACAddress, Addresses: []netip.Prefix{netip.PrefixFrom(ip, 32)}}
		if rng, err := netip.ParsePrefix(rec.Hlab.Networks[p.Network].IPRange); err == nil {
			iface.Routes = []cidata.Route{{To: rng.Masked(), Via: netGateway(rng)}}
		}
		out = append(out, iface)
	}
	return out
}
