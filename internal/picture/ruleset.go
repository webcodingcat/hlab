package picture

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// rulesetText is the template of the ruleset. It holds the fixed rules
// before any firewall: the servers reach the host only on the APIs, the
// uplink only the Internet, and no rule depends on conntrack seeing a
// connection start.
//
//go:embed ruleset.nft.tmpl
var rulesetText string

var rulesetTemplate = template.Must(template.New("ruleset").
	Funcs(template.FuncMap{"join": strings.Join}).Parse(rulesetText))

// rulesetValues fills the template.
type rulesetValues struct {
	Link, Public, Private, Uplink string
	VRFs                          string // a pattern of the networks' VRFs
	Host4, Host6                  string
	Resolver4, Resolver6          string
	Metadata                      string
	ServiceRange4                 string
	Range4                        string
	NoInternet                    []string
	FirewallMaps                  []firewallMap
	// FirewallChains are the servers' chains, then the firewalls'.
	FirewallChains []chainValues
}

// firewallMap is one of the verdict maps that send a server's packets to
// its chain.
type firewallMap struct {
	Name, Type string
	Elements   []string
}

// Ruleset returns the appliance's whole nftables ruleset.
func Ruleset(lab Lab) agentproto.Ruleset {
	resolvers := lab.resolvers()
	values := rulesetValues{
		Link:          DeviceLink,
		Public:        DevicePublic,
		Private:       DevicePrivate,
		VRFs:          vrfPrefix + "*",
		Uplink:        DeviceUplink,
		Host4:         hostAddress(lab.HostLink4, hostNumber).String(),
		Host6:         hostAddress(lab.HostLink6, hostNumber).String(),
		Resolver4:     resolvers[0].String(),
		Resolver6:     resolvers[1].String(),
		Metadata:      MetadataAddress.String(),
		ServiceRange4: ServiceRange4.String(),
		Range4:        lab.Range4.String(),
	}
	for _, server := range lab.servers() {
		if !server.Internet && server.IPv4.IsValid() {
			values.NoInternet = append(values.NoInternet, server.IPv4.String())
		}
	}
	// a guest may send from its Floating IPs too
	for _, floating := range lab.floatingIPs() {
		if server, ok := lab.server(floating.ServerID); ok && !server.Internet && floating.Prefix.Addr().Is4() {
			values.NoInternet = append(values.NoInternet, floating.Prefix.Addr().String())
		}
	}
	firewalls := firewallParts(lab)
	values.FirewallMaps = []firewallMap{
		{Name: "firewall_in4", Type: "ipv4_addr", Elements: firewalls.In4},
		{Name: "firewall_in6", Type: "ipv6_addr", Elements: firewalls.In6},
		{Name: "firewall_out4", Type: "ipv4_addr", Elements: firewalls.Out4},
		{Name: "firewall_out6", Type: "ipv6_addr", Elements: firewalls.Out6},
	}
	values.FirewallChains = append(firewalls.Servers, firewalls.Firewalls...)
	var text strings.Builder
	// the template and its values are fixed, so it cannot fail
	if err := rulesetTemplate.Execute(&text, values); err != nil {
		panic(err)
	}
	return agentproto.Ruleset{Generation: lab.Generation, Text: text.String()}
}
