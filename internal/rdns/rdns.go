// Package rdns holds the reverse DNS entries shared by primary IPs,
// floating IPs and servers.
package rdns

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/webcodingcat/hlab/internal/api"
)

// maxIPv6Entries is the most reverse DNS entries an IPv6 /64 holds.
const maxIPv6Entries = 100

// Entry is one reverse DNS entry as the API shows it.
type Entry struct {
	IP     string `json:"ip"`
	DNSPtr string `json:"dns_ptr"`
}

// Default is Hetzner's default PTR of an IPv4 address,
// "static.<d>.<c>.<b>.<a>.clients.your-server.de"; "" for IPv6.
func Default(ip netip.Addr) string {
	if !ip.Is4() {
		return ""
	}
	b := ip.As4()
	return fmt.Sprintf("static.%d.%d.%d.%d.clients.your-server.de", b[3], b[2], b[1], b[0])
}

// Change applies change_dns_ptr to the stored entries of a resource owning
// owner (an IPv4 /32 or an IPv6 /64): ip must be the exact IPv4 or lie in
// the /64; ptr nil removes the entry (IPv4 falls back to the default); ptr
// must be an FQDN without trailing dot; at most 100 IPv6 entries. Errors are
// *api.FieldError on "ip" or "dns_ptr".
func Change(entries []Entry, owner netip.Prefix, ip string, ptr *string) ([]Entry, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || addr.Zone() != "" {
		return nil, &api.FieldError{Field: "ip", Message: "invalid IP address"}
	}
	if owner.Addr().Is4() {
		if !addr.Is4() || addr != owner.Addr() {
			return nil, &api.FieldError{Field: "ip", Message: "IP does not belong to this resource"}
		}
	} else if !addr.Is6() || addr.Is4In6() || !owner.Contains(addr) {
		return nil, &api.FieldError{Field: "ip", Message: "IP does not belong to this resource"}
	}
	if ptr != nil && !validFQDN(*ptr) {
		return nil, &api.FieldError{Field: "dns_ptr", Message: "dns_ptr must be a fully qualified domain name without trailing dot"}
	}
	key := addr.String()
	out := make([]Entry, 0, len(entries)+1)
	found := false
	for _, e := range entries {
		if e.IP != key {
			out = append(out, e)
			continue
		}
		found = true
		if ptr != nil {
			out = append(out, Entry{IP: key, DNSPtr: *ptr})
		}
	}
	if !found && ptr != nil {
		if addr.Is6() && len(entries) >= maxIPv6Entries {
			return nil, &api.FieldError{Field: "ip", Message: fmt.Sprintf("at most %d reverse DNS entries per IPv6 network", maxIPv6Entries)}
		}
		out = append(out, Entry{IP: key, DNSPtr: *ptr})
	}
	return out, nil
}

// Show returns entries as the API shows them: an IPv4 owner without entry
// gets its default entry.
func Show(entries []Entry, owner netip.Prefix) []Entry {
	out := append([]Entry{}, entries...)
	if a := owner.Addr(); a.Is4() {
		for _, e := range entries {
			if e.IP == a.String() {
				return out
			}
		}
		out = append(out, Entry{IP: a.String(), DNSPtr: Default(a)})
	}
	return out
}

// PTR returns the effective IPv4 PTR name (stored or default).
func PTR(entries []Entry, ip netip.Addr) string {
	for _, e := range entries {
		if e.IP == ip.String() {
			return e.DNSPtr
		}
	}
	return Default(ip)
}

// validFQDN accepts host names of at least two labels without trailing dot.
func validFQDN(name string) bool {
	if len(name) > 253 || strings.HasSuffix(name, ".") {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}
