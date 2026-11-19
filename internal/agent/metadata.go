package agent

import (
	"context"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// Metadata is the Applier and the HTTP handler of the metadata service on
// 169.254.169.254:80. It answers each request with the entry of
// Config.Metadata whose VLAN is the segment of the listener (0 = public,
// see HandlerFor) and whose address is the request's source address; any
// other source gets 404. Besides Hetzner's
// paths it serves the guest hook of the root password reset and remembers
// the resets the guests confirmed while the reset is still in the config.
type Metadata struct {
	mu      sync.RWMutex
	servers map[segmentAddr]agentapi.Metadata
	done    map[int64]bool
	mux     *http.ServeMux
}

// segmentAddr identifies a server by its segment (VLAN, 0 = public) and
// its source address there.
type segmentAddr struct {
	vlan int
	ip   netip.Addr
}

// vlanKey is the request context key of the listener's VLAN.
type vlanKey struct{}

// NewMetadata returns an empty metadata service; it answers 404 until the
// first Apply.
func NewMetadata() *Metadata {
	m := &Metadata{servers: map[segmentAddr]agentapi.Metadata{}, done: map[int64]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hetzner/v1/metadata", m.serve(m.getDocument))
	mux.HandleFunc("GET /hetzner/v1/metadata/{key}", m.serve(m.getKey))
	mux.HandleFunc("GET /hetzner/v1/userdata", m.serve(m.getUserData))
	mux.HandleFunc("GET /hlab/v1/password-reset", m.serve(m.getPasswordReset))
	mux.HandleFunc("POST /hlab/v1/password-reset/{id}/done", m.serve(m.postPasswordResetDone))
	mux.HandleFunc("GET /hlab/v1/volumes", m.serve(m.getVolumes))
	m.mux = mux
	return m
}

// Name implements Applier.
func (m *Metadata) Name() string { return "metadata" }

// Apply implements Applier. Confirmed resets whose ID is no longer pending
// in cfg are forgotten.
func (m *Metadata) Apply(_ context.Context, cfg agentapi.Config) error {
	servers := map[segmentAddr]agentapi.Metadata{}
	pending := map[int64]bool{}
	for _, s := range cfg.Metadata {
		if !s.IP.IsValid() {
			continue
		}
		servers[segmentAddr{s.VLAN, s.IP.Unmap()}] = s
		if s.PasswordReset != nil {
			pending[s.PasswordReset.ID] = true
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers = servers
	for id := range m.done {
		if !pending[id] {
			delete(m.done, id)
		}
	}
	return nil
}

// Done returns the IDs of the password resets the guests confirmed, sorted.
func (m *Metadata) Done() []int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]int64, 0, len(m.done))
	for id := range m.done {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// ServeHTTP implements http.Handler for the public segment (VLAN 0).
func (m *Metadata) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

// HandlerFor returns the handler of the listener on segment vlan (0 = the
// public listener): it answers by (vlan, source address).
func (m *Metadata) HandlerFor(vlan int) http.Handler {
	if vlan == 0 {
		return m
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), vlanKey{}, vlan)))
	})
}

// serve looks up the asking server by the listener's VLAN and the
// request's source address and hands it to h; an unknown source gets 404.
func (m *Metadata) serve(h func(http.ResponseWriter, *http.Request, agentapi.Metadata)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		vlan, _ := r.Context().Value(vlanKey{}).(int)
		m.mu.RLock()
		s, ok := m.servers[segmentAddr{vlan, ap.Addr().Unmap()}]
		m.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r, s)
	}
}

func (m *Metadata) getDocument(w http.ResponseWriter, _ *http.Request, s agentapi.Metadata) {
	var b strings.Builder
	b.WriteString("availability-zone: " + yamlString(s.AvailabilityZone) + "\n")
	b.WriteString("hostname: " + yamlString(s.Hostname) + "\n")
	b.WriteString("instance-id: " + strconv.FormatInt(s.ServerID, 10) + "\n")
	b.WriteString("public-ipv4: " + yamlString(s.PublicIPv4) + "\n")
	b.WriteString("public-keys:")
	if len(s.PublicKeys) == 0 {
		b.WriteString(" []\n")
	} else {
		b.WriteString("\n" + yamlList(s.PublicKeys))
	}
	b.WriteString("region: " + yamlString(s.Region) + "\n")
	writeText(w, b.String())
}

func (m *Metadata) getKey(w http.ResponseWriter, r *http.Request, s agentapi.Metadata) {
	var body string
	switch r.PathValue("key") {
	case "availability-zone":
		body = s.AvailabilityZone + "\n"
	case "hostname":
		body = s.Hostname + "\n"
	case "instance-id":
		body = strconv.FormatInt(s.ServerID, 10) + "\n"
	case "public-ipv4":
		body = s.PublicIPv4 + "\n"
	case "region":
		body = s.Region + "\n"
	case "public-keys":
		body = yamlList(s.PublicKeys)
	case "private-networks":
		body = privateNetworksYAML(s.PrivateNetworks)
	default:
		http.NotFound(w, r)
		return
	}
	writeText(w, body)
}

func (m *Metadata) getUserData(w http.ResponseWriter, _ *http.Request, s agentapi.Metadata) {
	writeText(w, s.UserData)
}

func (m *Metadata) getPasswordReset(w http.ResponseWriter, _ *http.Request, s agentapi.Metadata) {
	m.mu.RLock()
	confirmed := s.PasswordReset != nil && m.done[s.PasswordReset.ID]
	m.mu.RUnlock()
	if s.PasswordReset == nil || confirmed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, struct {
		ID          int64  `json:"id"`
		SHA512Crypt string `json:"sha512_crypt"`
	}{s.PasswordReset.ID, s.PasswordReset.SHA512Crypt})
}

// getVolumes answers the asking server's attached volumes with automount
// as {"volumes":[<id>,...]}, sorted, [] when none; the guest's volume hook
// mounts only these.
func (m *Metadata) getVolumes(w http.ResponseWriter, _ *http.Request, s agentapi.Metadata) {
	ids := append([]int64{}, s.Volumes...)
	slices.Sort(ids)
	writeJSON(w, struct {
		Volumes []int64 `json:"volumes"`
	}{ids})
}

// postPasswordResetDone accepts the confirmation only for the reset pending
// for the asking server.
func (m *Metadata) postPasswordResetDone(w http.ResponseWriter, r *http.Request, s agentapi.Metadata) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || s.PasswordReset == nil || s.PasswordReset.ID != id {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	m.done[id] = true
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// privateNetworksYAML writes the networks as a YAML list with Hetzner's
// field names; the MAC address is quoted, so YAML 1.1 parsers do not read
// an all-digit MAC as a number.
func privateNetworksYAML(nets []agentapi.MetadataNetwork) string {
	if len(nets) == 0 {
		return "[]\n"
	}
	var b strings.Builder
	for _, n := range nets {
		aliases := make([]string, 0, len(n.AliasIPs))
		for _, a := range n.AliasIPs {
			aliases = append(aliases, a.String())
		}
		b.WriteString("- ip: " + n.IP.String() + "\n")
		b.WriteString("  alias_ips: [" + strings.Join(aliases, ", ") + "]\n")
		if n.InterfaceNum > 0 {
			b.WriteString("  interface_num: " + strconv.Itoa(n.InterfaceNum) + "\n")
		}
		b.WriteString("  mac_address: " + strconv.Quote(n.MAC) + "\n")
		b.WriteString("  network_id: " + strconv.FormatInt(n.NetworkID, 10) + "\n")
		b.WriteString("  network_name: " + yamlString(n.NetworkName) + "\n")
		if n.Network.IsValid() {
			b.WriteString("  network: " + n.Network.String() + "\n")
		}
		b.WriteString("  subnet: " + n.Subnet.String() + "\n")
		b.WriteString("  gateway: " + n.Gateway.String() + "\n")
	}
	return b.String()
}

// yamlList writes items as a YAML block sequence of strings, [] if empty.
func yamlList(items []string) string {
	if len(items) == 0 {
		return "[]\n"
	}
	var b strings.Builder
	for _, it := range items {
		b.WriteString("- " + yamlString(it) + "\n")
	}
	return b.String()
}

// plainScalar matches strings that YAML reads back as the same string
// without quotes: a name starting with a letter, or a dotted address.
var plainScalar = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9._-]*|[0-9]+(?:\.[0-9]+){2,})$`)

// yamlString writes s as a YAML scalar: plain where that is unambiguous,
// double-quoted otherwise (Go's escapes are valid in YAML double quotes).
func yamlString(s string) string {
	switch strings.ToLower(s) {
	case "y", "n", "yes", "no", "true", "false", "on", "off", "null":
		return strconv.Quote(s)
	}
	if plainScalar.MatchString(s) {
		return s
	}
	return strconv.Quote(s)
}

func writeText(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
