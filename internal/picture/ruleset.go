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
	Link, Public, Uplink string
	Host4, Host6         string
	Resolver4, Resolver6 string
	Metadata             string
	NoInternet           []string
}

// Ruleset returns the appliance's whole nftables ruleset.
func Ruleset(lab Lab) agentproto.Ruleset {
	resolvers := lab.resolvers()
	values := rulesetValues{
		Link:      DeviceLink,
		Public:    DevicePublic,
		Uplink:    DeviceUplink,
		Host4:     hostAddress(lab.HostLink4, hostNumber).String(),
		Host6:     hostAddress(lab.HostLink6, hostNumber).String(),
		Resolver4: resolvers[0].String(),
		Resolver6: resolvers[1].String(),
		Metadata:  MetadataAddress.String(),
	}
	for _, server := range lab.servers() {
		if !server.Internet && server.IPv4.IsValid() {
			values.NoInternet = append(values.NoInternet, server.IPv4.String())
		}
	}
	var text strings.Builder
	// the template and its values are fixed, so it cannot fail
	if err := rulesetTemplate.Execute(&text, values); err != nil {
		panic(err)
	}
	return agentproto.Ruleset{Generation: lab.Generation, Text: text.String()}
}
