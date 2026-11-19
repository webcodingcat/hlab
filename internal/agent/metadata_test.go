package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/agentapi"
)

func TestAgent(t *testing.T) {
	t.Run("META-01", testMetadata)
	t.Run("META-01/private_vlan", testMetadataPrivateVLAN)
	t.Run("SRV-11", testRewriteIDs)
	t.Run("VOL-03", testMetadataVolumes)
	t.Run("FW-02", testCompileFirewallsIn)
	t.Run("FW-03", testCompileFirewallsOut)
	t.Run("FW-04", testCompileFirewallsPrivate)
	t.Run("DNS-02", testDNSZones)
	t.Run("LB-02", testHAProxyBalance)
	t.Run("LB-03", testHAProxyHTTP)
	t.Run("LB-04", testHAProxyTLS)
	t.Run("LB-05/check", testHAProxyChecks)
	t.Run("LB-05/stat", testParseStat)
	t.Run("LB-07", testHAProxyProxyProtocol)
}

// testMetadataPrivateVLAN: two servers with the same private IP in two
// VLANs each get their own metadata from the handler of their VLAN.
func testMetadataPrivateVLAN(t *testing.T) {
	ctx := context.Background()
	private := netip.MustParseAddr("10.0.0.2")
	x := agentapi.Metadata{
		VLAN:       2,
		IP:         private,
		ServerID:   60,
		Hostname:   "vlan-x",
		Region:     "eu-central",
		PublicIPv4: "198.19.0.30",
	}
	y := agentapi.Metadata{
		VLAN:       3,
		IP:         private,
		ServerID:   61,
		Hostname:   "vlan-y",
		Region:     "us-east",
		PublicIPv4: "198.19.0.31",
	}
	m := agent.NewMetadata()
	if err := m.Apply(ctx, agentapi.Config{Schema: agentapi.Schema, Generation: 1, Metadata: []agentapi.Metadata{x, y}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, s := range []agentapi.Metadata{x, y} {
		h := m.HandlerFor(s.VLAN)
		for key, want := range map[string]string{
			"hostname":    s.Hostname,
			"instance-id": strconv.FormatInt(s.ServerID, 10),
			"region":      s.Region,
		} {
			code, body := request(t, h, http.MethodGet, "/hetzner/v1/metadata/"+key, private.String())
			if code != http.StatusOK || strings.TrimSpace(body) != want {
				t.Errorf("VLAN %d from %s: GET %s = %d %q, want 200 %q", s.VLAN, private, key, code, body, want)
			}
		}
	}

	if code, body := request(t, m.HandlerFor(2), http.MethodGet, "/hetzner/v1/metadata/hostname", "10.0.0.9"); code != http.StatusNotFound {
		t.Errorf("unknown IP on VLAN 2: GET hostname = %d %q, want 404", code, body)
	}
}

func testMetadataVolumes(t *testing.T) {
	ctx := context.Background()
	a := agentapi.Metadata{
		IP:         netip.MustParseAddr("198.19.0.20"),
		ServerID:   50,
		Hostname:   "vol-a",
		PublicIPv4: "198.19.0.20",
		Volumes:    []int64{101, 103},
	}
	b := agentapi.Metadata{
		IP:         netip.MustParseAddr("198.19.0.21"),
		ServerID:   51,
		Hostname:   "vol-b",
		PublicIPv4: "198.19.0.21",
	}
	m := agent.NewMetadata()
	if err := m.Apply(ctx, agentapi.Config{Schema: agentapi.Schema, Generation: 1, Metadata: []agentapi.Metadata{a, b}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, c := range []struct {
		name string
		from string
		want []int64
	}{
		{"A", a.IP.String(), []int64{101, 103}},
		{"B", b.IP.String(), []int64{}},
	} {
		code, body := request(t, m, http.MethodGet, "/hlab/v1/volumes", c.from)
		if code != http.StatusOK {
			t.Errorf("%s: GET /hlab/v1/volumes = %d %q, want 200", c.name, code, body)
			continue
		}
		var doc struct {
			Volumes []int64 `json:"volumes"`
		}
		if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Volumes == nil {
			t.Errorf("%s: GET /hlab/v1/volumes body %q (%v), want {\"volumes\":[...]}", c.name, body, err)
			continue
		}
		if !slices.Equal(doc.Volumes, c.want) {
			t.Errorf("%s: volumes = %v, want %v", c.name, doc.Volumes, c.want)
		}
	}

	if code, body := request(t, m, http.MethodGet, "/hlab/v1/volumes", "198.19.0.99"); code != http.StatusNotFound {
		t.Errorf("unknown address: GET /hlab/v1/volumes = %d %q, want 404", code, body)
	}
}

func testMetadata(t *testing.T) {
	ctx := context.Background()
	a := agentapi.Metadata{
		VLAN:             0,
		IP:               netip.MustParseAddr("198.19.0.10"),
		ServerID:         42,
		Hostname:         "srv-a",
		Region:           "eu-central",
		AvailabilityZone: "fsn1-dc14",
		PublicIPv4:       "198.19.0.10",
		PublicKeys:       []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA alice@a", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB bob@b"},
		UserData:         "#cloud-config\nruncmd:\n  - echo a: b\n",
		PrivateNetworks: []agentapi.MetadataNetwork{{
			IP:          netip.MustParseAddr("10.0.0.2"),
			AliasIPs:    []netip.Addr{netip.MustParseAddr("10.0.0.3"), netip.MustParseAddr("10.0.0.4")},
			MAC:         "86:00:00:12:34:56",
			NetworkID:   1234,
			NetworkName: "nw-test1",
			Subnet:      netip.MustParsePrefix("10.0.0.0/24"),
			Gateway:     netip.MustParseAddr("10.0.0.1"),
		}},
	}
	b := agentapi.Metadata{
		VLAN:             0,
		IP:               netip.MustParseAddr("198.19.0.11"),
		ServerID:         43,
		Hostname:         "srv-b",
		Region:           "us-east",
		AvailabilityZone: "ash-dc1",
		PublicIPv4:       "198.19.0.11",
		PublicKeys:       []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC carol@c"},
		UserData:         "#!/bin/sh\necho b\n",
		PasswordReset:    &agentapi.PasswordReset{ID: 7, SHA512Crypt: "$6$saltsalt$hashhash"},
	}
	cfg := agentapi.Config{Schema: agentapi.Schema, Generation: 1, Metadata: []agentapi.Metadata{a, b}}

	m := agent.NewMetadata()
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, s := range []agentapi.Metadata{a, b} {
		from := s.IP.String()

		code, body := request(t, m, http.MethodGet, "/hetzner/v1/metadata", from)
		if code != http.StatusOK {
			t.Fatalf("%s: GET /hetzner/v1/metadata = %d, want 200", s.Hostname, code)
		}
		keys := topLevelKeys(body)
		if !slices.IsSorted(keys) {
			t.Errorf("%s: metadata keys %v are not sorted", s.Hostname, keys)
		}
		doc, ok := parseYAML(t, body).(map[string]any)
		if !ok {
			t.Fatalf("%s: metadata document is not a YAML mapping:\n%s", s.Hostname, body)
		}
		want := map[string]string{
			"availability-zone": s.AvailabilityZone,
			"hostname":          s.Hostname,
			"instance-id":       strconv.FormatInt(s.ServerID, 10),
			"public-ipv4":       s.PublicIPv4,
			"region":            s.Region,
		}
		for k, v := range want {
			if doc[k] != v {
				t.Errorf("%s: metadata %s = %#v, want %q", s.Hostname, k, doc[k], v)
			}
		}
		if got := strList(doc["public-keys"]); !slices.Equal(got, s.PublicKeys) {
			t.Errorf("%s: metadata public-keys = %#v, want %q", s.Hostname, doc["public-keys"], s.PublicKeys)
		}

		for k, v := range want {
			code, body := request(t, m, http.MethodGet, "/hetzner/v1/metadata/"+k, from)
			if code != http.StatusOK || strings.TrimRight(body, "\n") != v {
				t.Errorf("%s: GET /hetzner/v1/metadata/%s = %d %q, want 200 %q", s.Hostname, k, code, body, v)
			}
		}

		code, body = request(t, m, http.MethodGet, "/hetzner/v1/metadata/public-keys", from)
		if got := strList(parseYAML(t, body)); code != http.StatusOK || !slices.Equal(got, s.PublicKeys) {
			t.Errorf("%s: GET /hetzner/v1/metadata/public-keys = %d %q, want 200 list %q", s.Hostname, code, body, s.PublicKeys)
		}

		code, body = request(t, m, http.MethodGet, "/hetzner/v1/userdata", from)
		if code != http.StatusOK || body != s.UserData {
			t.Errorf("%s: GET /hetzner/v1/userdata = %d %q, want 200 %q", s.Hostname, code, body, s.UserData)
		}
	}

	// Private networks: a YAML list with Hetzner's field names, [] without networks.
	code, body := request(t, m, http.MethodGet, "/hetzner/v1/metadata/private-networks", a.IP.String())
	if code != http.StatusOK {
		t.Fatalf("A: GET private-networks = %d, want 200", code)
	}
	nets, ok := parseYAML(t, body).([]any)
	if !ok || len(nets) != 1 {
		t.Fatalf("A: private-networks is not a list of one network:\n%s", body)
	}
	n, ok := nets[0].(map[string]any)
	if !ok {
		t.Fatalf("A: private network is not a mapping:\n%s", body)
	}
	wantNet := map[string]string{
		"ip":           "10.0.0.2",
		"mac_address":  "86:00:00:12:34:56",
		"network_id":   "1234",
		"network_name": "nw-test1",
		"subnet":       "10.0.0.0/24",
		"gateway":      "10.0.0.1",
	}
	for k, v := range wantNet {
		if n[k] != v {
			t.Errorf("A: private network %s = %#v, want %q", k, n[k], v)
		}
	}
	if got := strList(n["alias_ips"]); !slices.Equal(got, []string{"10.0.0.3", "10.0.0.4"}) {
		t.Errorf("A: private network alias_ips = %#v, want [10.0.0.3 10.0.0.4]", n["alias_ips"])
	}
	if !strings.Contains(body, `"86:00:00:12:34:56"`) && !strings.Contains(body, `'86:00:00:12:34:56'`) {
		t.Errorf("A: mac_address is not quoted:\n%s", body)
	}
	code, body = request(t, m, http.MethodGet, "/hetzner/v1/metadata/private-networks", b.IP.String())
	if code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Errorf("B: GET private-networks = %d %q, want 200 []", code, body)
	}

	// Unknown source address.
	for _, p := range []string{"/hetzner/v1/metadata", "/hetzner/v1/metadata/hostname", "/hetzner/v1/userdata", "/hlab/v1/password-reset"} {
		if code, _ := request(t, m, http.MethodGet, p, "198.19.0.99"); code != http.StatusNotFound {
			t.Errorf("unknown address: GET %s = %d, want 404", p, code)
		}
	}

	// Password reset.
	if code, body := request(t, m, http.MethodGet, "/hlab/v1/password-reset", a.IP.String()); code != http.StatusNoContent {
		t.Errorf("A without reset: GET password-reset = %d %q, want 204", code, body)
	}
	code, body = request(t, m, http.MethodGet, "/hlab/v1/password-reset", b.IP.String())
	var reset struct {
		ID          int64  `json:"id"`
		SHA512Crypt string `json:"sha512_crypt"`
	}
	if code != http.StatusOK {
		t.Fatalf("B with reset: GET password-reset = %d %q, want 200", code, body)
	}
	if err := json.Unmarshal([]byte(body), &reset); err != nil || reset.ID != 7 || reset.SHA512Crypt != b.PasswordReset.SHA512Crypt {
		t.Errorf("B: password-reset body = %q (%v), want id 7 and sha512_crypt %q", body, err, b.PasswordReset.SHA512Crypt)
	}
	if got := m.Done(); len(got) != 0 {
		t.Errorf("Done() before confirmation = %v, want empty", got)
	}
	if code, body := request(t, m, http.MethodPost, "/hlab/v1/password-reset/7/done", b.IP.String()); code != http.StatusNoContent {
		t.Fatalf("B: POST password-reset/7/done = %d %q, want 204", code, body)
	}
	if got := m.Done(); !slices.Equal(got, []int64{7}) {
		t.Errorf("Done() after confirmation = %v, want [7]", got)
	}
	cfg.Generation = 2
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("Apply again: %v", err)
	}
	if got := m.Done(); !slices.Equal(got, []int64{7}) {
		t.Errorf("Done() while the reset is still in the config = %v, want [7]", got)
	}
	b.PasswordReset = nil
	cfg = agentapi.Config{Schema: agentapi.Schema, Generation: 3, Metadata: []agentapi.Metadata{a, b}}
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("Apply without reset: %v", err)
	}
	if got := m.Done(); len(got) != 0 {
		t.Errorf("Done() after the config dropped the reset = %v, want empty", got)
	}
}

// request sends one request to h as if it came from the address from.
func request(t *testing.T, h http.Handler, method, path, from string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, "http://169.254.169.254"+path, nil)
	r.RemoteAddr = netip.AddrPortFrom(netip.MustParseAddr(from), 40000).String()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// topLevelKeys returns the keys of a YAML mapping's first level in order.
func topLevelKeys(doc string) []string {
	var keys []string
	for line := range strings.SplitSeq(doc, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '-' || line[0] == '#' {
			continue
		}
		if k, _, ok := strings.Cut(line, ":"); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// strList returns the strings of a parsed YAML sequence, nil otherwise.
func strList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

type yamlLine struct {
	indent int
	text   string
}

// parseYAML reads the block YAML subset the metadata service writes:
// mappings, block sequences (also of mappings), flow sequences of scalars
// and plain or quoted scalars. Every scalar stays a string.
func parseYAML(t *testing.T, doc string) any {
	t.Helper()
	var lines []yamlLine
	for l := range strings.SplitSeq(doc, "\n") {
		l = strings.TrimRight(l, " \r")
		trimmed := strings.TrimLeft(l, " ")
		if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, yamlLine{len(l) - len(trimmed), trimmed})
	}
	if len(lines) == 0 {
		return nil
	}
	v, next := parseNode(t, lines, 0)
	if next != len(lines) {
		t.Fatalf("YAML not fully read at line %q:\n%s", lines[next].text, doc)
	}
	return v
}

func parseNode(t *testing.T, lines []yamlLine, i int) (any, int) {
	t.Helper()
	indent, text := lines[i].indent, lines[i].text
	switch {
	case text == "-" || strings.HasPrefix(text, "- "):
		var seq []any
		for i < len(lines) && lines[i].indent == indent && (lines[i].text == "-" || strings.HasPrefix(lines[i].text, "- ")) {
			rest := strings.TrimSpace(strings.TrimPrefix(lines[i].text, "-"))
			switch {
			case rest == "":
				v, next := parseNode(t, lines, i+1)
				seq, i = append(seq, v), next
			case isMapLine(rest):
				lines[i] = yamlLine{indent + 2, rest}
				v, next := parseNode(t, lines, i)
				seq, i = append(seq, v), next
			default:
				seq, i = append(seq, scalar(t, rest)), i+1
			}
		}
		return seq, i
	case isMapLine(text):
		m := map[string]any{}
		for i < len(lines) && lines[i].indent == indent && isMapLine(lines[i].text) {
			k, v, _ := strings.Cut(lines[i].text, ":")
			v = strings.TrimSpace(v)
			i++
			if v != "" {
				m[k] = scalar(t, v)
				continue
			}
			if i < len(lines) && (lines[i].indent > indent || (lines[i].indent == indent && strings.HasPrefix(lines[i].text, "-"))) {
				m[k], i = parseNode(t, lines, i)
			} else {
				m[k] = nil
			}
		}
		return m, i
	default:
		return scalar(t, text), i + 1
	}
}

// isMapLine reports whether text is "key: value" or "key:".
func isMapLine(text string) bool {
	if text == "" || strings.ContainsAny(text[:1], `"'[{`) {
		return false
	}
	return strings.Contains(text, ": ") || strings.HasSuffix(text, ":")
}

func scalar(t *testing.T, v string) any {
	t.Helper()
	switch {
	case strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"):
		items := []any{}
		inner := strings.TrimSpace(v[1 : len(v)-1])
		if inner == "" {
			return items
		}
		for p := range strings.SplitSeq(inner, ",") {
			items = append(items, scalar(t, strings.TrimSpace(p)))
		}
		return items
	case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
		s, err := strconv.Unquote(v)
		if err != nil {
			t.Fatalf("bad double-quoted YAML scalar %s: %v", v, err)
		}
		return s
	case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}
