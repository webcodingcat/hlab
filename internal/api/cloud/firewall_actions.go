package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// firewallActions is the firewalls' action table. Its rows take no flag:
// Hetzner answers firewall actions at once, and no 423 was seen. The rows
// apply_server and apply_selector are both Hetzner's apply_firewall,
// remove_server and remove_selector its remove_firewall, as each entry of
// apply_to and remove_from gets an action of its own.
func firewallActions(l *lab.Lab) lab.ActionTable {
	return lab.ActionTable{
		ResourceType: firewallResource,
		Actions: map[string]lab.ActionSpec{
			"set_rules":       {Name: "set_firewall_rules", Plan: planFirewallRules, Unflagged: true},
			"apply_rules":     {Name: "apply_firewall", Plan: planFirewallApplyRules(l), Unflagged: true},
			"apply_server":    {Name: "apply_firewall", Plan: planFirewallApplyServer(l), Unflagged: true},
			"apply_selector":  {Name: "apply_firewall", Plan: planFirewallApplySelector(l), Unflagged: true},
			"unselect_server": {Name: "remove_firewall", Plan: planFirewallUnselectServer, Unflagged: true},
			"remove_server":   {Name: "remove_firewall", Plan: planFirewallRemoveServer(l), Unflagged: true},
			"remove_selector": {Name: "remove_firewall", Plan: planFirewallRemoveSelector(l), Unflagged: true},
			"apply_at_create": {Name: "apply_firewall", Plan: planFirewallApplyAtCreate(l), Unflagged: true},
		},
	}
}

// firewallRuleRequest is one rule of a request, with pointer fields to
// tell a missing field from an empty one.
type firewallRuleRequest struct {
	Description    *string  `json:"description"`
	Direction      *string  `json:"direction"`
	Protocol       *string  `json:"protocol"`
	Port           *string  `json:"port"`
	SourceIPs      []string `json:"source_ips"`
	DestinationIPs []string `json:"destination_ips"`
}

// firewallTargetRequest is one entry of apply_to or remove_from.
type firewallTargetRequest struct {
	Type     string
	ServerID *int64
	Selector *string
}

// firewallTargetJSON is how an entry of apply_to arrives.
type firewallTargetJSON struct {
	Type   string `json:"type"`
	Server *struct {
		ID *int64 `json:"id"`
	} `json:"server"`
	LabelSelector *struct {
		Selector *string `json:"selector"`
	} `json:"label_selector"`
}

// decodeFirewallRules decodes the rules of a request; Hetzner answers a
// rule that is no object with json_error (measured 2026-11-08).
func decodeFirewallRules(raw []json.RawMessage) ([]firewallRuleRequest, error) {
	rules := make([]firewallRuleRequest, 0, len(raw))
	for _, entry := range raw {
		var rule firewallRuleRequest
		if err := json.Unmarshal(entry, &rule); err != nil {
			return nil, web.JSONDocumentRequired()
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// decodeFirewallTargets decodes the entries of apply_to or remove_from.
func decodeFirewallTargets(raw []json.RawMessage) ([]firewallTargetRequest, error) {
	targets := make([]firewallTargetRequest, 0, len(raw))
	for _, entry := range raw {
		var target firewallTargetJSON
		if err := json.Unmarshal(entry, &target); err != nil {
			return nil, web.JSONDocumentRequired()
		}
		decoded := firewallTargetRequest{Type: target.Type}
		if target.Server != nil {
			decoded.ServerID = new(int64)
			if target.Server.ID != nil {
				*decoded.ServerID = *target.Server.ID
			}
		}
		if target.LabelSelector != nil {
			decoded.Selector = target.LabelSelector.Selector
		}
		targets = append(targets, decoded)
	}
	return targets, nil
}

// checkFirewallRuleFields checks what Hetzner checks of the rules first,
// with the rule's index in the field's name: the direction, the length of
// the description, the number of addresses (measured 2026-11-08).
func checkFirewallRuleFields(check *web.Check, rules []firewallRuleRequest) {
	for i, rule := range rules {
		field := fmt.Sprintf("rules[%d].", i)
		if rule.Direction == nil || (*rule.Direction != "in" && *rule.Direction != "out") {
			check.Fail(field+"direction", "'"+field+"direction' value not allowed")
		}
		if rule.Description != nil && len([]rune(*rule.Description)) > firewallDescriptionMax {
			check.Fail(field+"description", firewallTooLarge)
		}
		if len(rule.SourceIPs) > firewallIPsMax {
			check.Fail(field+"source_ips", firewallTooLarge)
		}
		if len(rule.DestinationIPs) > firewallIPsMax {
			check.Fail(field+"destination_ips", firewallTooLarge)
		}
	}
}

// checkFirewallTargetFields checks the entries of apply_to or remove_from:
// a known type, and a selector where the type names one. Hetzner reads a
// server of an entry without a valid type as the ID 0 (measured
// 2026-11-08: "value required to be larger" beside the type's error).
func checkFirewallTargetFields(check *web.Check, field string, targets []firewallTargetRequest) {
	for i, target := range targets {
		prefix := fmt.Sprintf("%s[%d].", field, i)
		switch target.Type {
		case "server":
			if target.ServerID == nil || *target.ServerID <= 0 {
				check.Fail(prefix+"server.id", firewallTooSmall)
			}
		case labelSelectorResource:
			if target.Selector == nil {
				check.Fail(prefix+"label_selector", firewallSelectorMissing)
			}
		default:
			if target.ServerID != nil {
				check.Fail(prefix+"server.id", firewallTooSmall)
			}
			check.Fail(prefix+"type", "'"+prefix+"type' value not allowed")
		}
	}
}

// checkFirewallSelectors refuses a label selector that does not parse,
// with Hetzner's text, which does not give the reason.
func checkFirewallSelectors(targets []firewallTargetRequest) error {
	for _, target := range targets {
		if target.Type != labelSelectorResource {
			continue
		}
		if _, err := web.ParseSelector(*target.Selector); err != nil {
			return web.InvalidInput("invalid input in field 'apply_to[].label_selector'",
				web.Field{Name: "apply_to[].label_selector", Messages: []string{firewallSelectorInvalid}})
		}
	}
	return nil
}

// portPattern is a port or a range in a rule: "80" or "80-85".
var portPattern = regexp.MustCompile(`^([0-9]+)(-([0-9]+))?$`)

// checkFirewallRules checks each rule's protocol, addresses and port, and
// the firewall's effective rules, after checkFirewallRuleFields, and
// returns the rules as Hetzner keeps them: tcp and udp without a port take
// "any", "80-80" is "80", the other protocols drop the port, a rule keeps
// only the addresses of its direction, IPv6 in its canonical form. Each
// error was measured alone (2026-11-08); a rule with several faults gets
// the first in this order.
func checkFirewallRules(rules []firewallRuleRequest) ([]state.FirewallRule, error) {
	checked := make([]state.FirewallRule, 0, len(rules))
	for i, request := range rules {
		rule := state.FirewallRule{Description: request.Description, Direction: *request.Direction, SourceIPs: []string{}, DestinationIPs: []string{}}
		if request.Protocol == nil || !slices.Contains([]string{"tcp", "udp", "icmp", "esp", "gre"}, *request.Protocol) {
			return nil, fieldsInvalid("rules[].protocol_l4", "field contains invalid value")
		}
		rule.Protocol = *request.Protocol
		addresses, field := request.SourceIPs, "source_ips"
		if rule.Direction == "out" {
			addresses, field = request.DestinationIPs, "destination_ips"
		}
		if len(addresses) == 0 {
			return nil, web.InvalidInput("invalid input in field 'rules'",
				web.Field{Name: fmt.Sprintf("rules[%d].%s", i, field), Messages: []string{field + " needs to have at least one entry"}})
		}
		if rule.Protocol == "tcp" || rule.Protocol == "udp" {
			port, err := checkFirewallPort(request.Port)
			if err != nil {
				return nil, err
			}
			rule.Port = port
		}
		canonical, err := checkFirewallAddresses(addresses, field)
		if err != nil {
			return nil, err
		}
		if rule.Direction == "in" {
			rule.SourceIPs = canonical
		} else {
			rule.DestinationIPs = canonical
		}
		checked = append(checked, rule)
	}
	if effectiveRules(checked) > firewallEffectiveMax {
		return nil, web.LimitExceeded(firewallLimit, firewallLimitRules)
	}
	return checked, nil
}

// checkFirewallPort checks a tcp or udp rule's port and returns it as
// Hetzner keeps it.
func checkFirewallPort(port *string) (string, error) {
	if port == nil || *port == "" || *port == "any" {
		return "any", nil
	}
	match := portPattern.FindStringSubmatch(*port)
	if match == nil {
		return "", fieldsInvalid("port", "invalid port")
	}
	first, err := strconv.ParseUint(match[1], 10, 16)
	last := first
	if err == nil && match[3] != "" {
		last, err = strconv.ParseUint(match[3], 10, 16)
	}
	if err != nil {
		return "", fieldsInvalid("port", "invalid port")
	}
	// a single port is a range from itself to itself: port 0 fails twice
	var outOfRange []web.Field
	for _, bound := range []uint64{first, last} {
		if bound == 0 {
			outOfRange = append(outOfRange, web.Field{Name: "rules[].port", Messages: []string{"value is out of range"}})
		}
	}
	if len(outOfRange) > 0 {
		return "", web.InvalidInput(invalidFieldsMessage(outOfRange), outOfRange...)
	}
	if first > last {
		return "", fieldsInvalid("rules[].port", "field value is too small")
	}
	if first == last {
		return strconv.FormatUint(first, 10), nil
	}
	return *port, nil
}

// invalidFieldsMessage returns the message of an invalid_input error that
// names fields, each as often as it is listed.
func invalidFieldsMessage(fields []web.Field) string {
	names := make([]string, len(fields))
	for i, field := range fields {
		names[i] = "'" + field.Name + "'"
	}
	if len(fields) == 1 {
		return "invalid input in field " + names[0]
	}
	return "invalid input in fields " + strings.Join(names, ", ")
}

// fieldsInvalid returns an invalid_input error for one field.
func fieldsInvalid(name, message string) error {
	return web.InvalidInput("invalid input in field '"+name+"'", web.Field{Name: name, Messages: []string{message}})
}

// checkFirewallAddresses checks a rule's CIDRs and returns them in their
// canonical form.
func checkFirewallAddresses(addresses []string, field string) ([]string, error) {
	canonical := make([]string, 0, len(addresses))
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address)
		if err != nil {
			return nil, fieldsInvalid("rules[]."+field, "IP not in cidr notation: "+address)
		}
		if prefix.Masked() != prefix {
			return nil, fieldsInvalid("rules[]."+field, fmt.Sprintf("IP has host bit(s) set, did you mean %s?", prefix.Masked()))
		}
		canonical = append(canonical, prefix.String())
	}
	return canonical, nil
}

// effectiveRules counts a firewall's effective rules as Hetzner does: each
// address of a direction once, however many rules name it with other ports
// or protocols, both directions together (measured 2026-11-08: 500 inbound
// pass, 300 inbound and 300 outbound do not, 60 rules on the same 10
// addresses do).
func effectiveRules(rules []state.FirewallRule) int {
	seen := map[string]bool{}
	for _, rule := range rules {
		for _, address := range append(append([]string{}, rule.SourceIPs...), rule.DestinationIPs...) {
			seen[rule.Direction+" "+address] = true
		}
	}
	return len(seen)
}

// planFirewallRules sets the rules of the body, which the handler checked;
// the action ends at once, as on Hetzner, and an apply_firewall after it
// sends them to the servers.
func planFirewallRules(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	firewall, err := callerFirewall(tx, req.ProjectID, req.ID, firewallNotFoundAction)
	if err != nil {
		return lab.Plan{}, err
	}
	var body struct {
		Rules *[]json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return lab.Plan{}, web.ParseFailed()
	}
	if body.Rules == nil {
		return lab.Plan{}, nil // a create's: its rules are in the record
	}
	rules, err := decodeFirewallRules(*body.Rules)
	if err != nil {
		return lab.Plan{}, err
	}
	checked, err := checkFirewallRules(rules)
	if err != nil {
		return lab.Plan{}, err
	}
	firewall.Rules = checked
	return lab.Plan{}, nil
}

// planFirewallApplyRules is the apply_firewall after set_rules: it names
// the servers the firewall applies to and sends them the new rules.
func planFirewallApplyRules(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		firewall, err := callerFirewall(tx, req.ProjectID, req.ID, firewallNotFoundAction)
		if err != nil {
			return lab.Plan{}, err
		}
		plan := lab.Plan{Steps: l.Machine().ChangeFirewall}
		for _, id := range lab.FirewallServers(firewall) {
			plan.Resources = append(plan.Resources, state.ResourceRef{Type: lab.ServerResource, ID: id})
		}
		return plan, nil
	}
}

// firewallTargetBody is the body of a target's row.
type firewallTargetBody struct {
	Server   int64  `json:"server"`
	Selector string `json:"selector"`
}

// planFirewallApplyServer applies the firewall to a server of the project.
// A server that its label selector covers moves to a direct entry and has
// the firewall already, so the action ends at once.
func planFirewallApplyServer(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		firewall, body, err := firewallAndTarget(tx, req)
		if err != nil {
			return lab.Plan{}, err
		}
		server, err := state.Get(tx, state.Servers, body.Server)
		if err != nil || server.ProjectID != req.ProjectID {
			return lab.Plan{}, web.RefusalNull(http.StatusNotFound, "firewall_resource_not_found", fmt.Sprintf(firewallServerMissing, body.Server))
		}
		if slices.ContainsFunc(firewall.AppliedTo, func(target state.FirewallTarget) bool { return target.ServerID == server.ID }) {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "firewall_already_applied", firewallAlreadyApplied)
		}
		plan := lab.Plan{Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: server.ID}}}
		if selector := selectedBy(firewall, server.ID); selector != nil {
			selector.Servers = slices.DeleteFunc(selector.Servers, func(id int64) bool { return id == server.ID })
		} else {
			if firewallsOf(tx, server.ID) >= firewallsPerServer {
				return lab.Plan{}, web.LimitExceeded(firewallLimit, firewallLimitServer)
			}
			plan.Steps = l.Machine().ChangeFirewall
		}
		firewall.AppliedTo = append(firewall.AppliedTo, state.FirewallTarget{ServerID: server.ID})
		return plan, nil
	}
}

// planFirewallApplyAtCreate is the apply_firewall of a firewall that a
// create request names for its server (req.Created): createServer has
// checked it and applied it in the record. The create's steps send the
// pictures with it before the server first starts, so these steps only
// wait Hetzner's time; they call l.Sleep themselves, as they change
// nothing on the machine. Hetzner's apply ends a few
// seconds before the create (measured 2026-11-09), as an apply's time
// does before the create's.
func planFirewallApplyAtCreate(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		if _, err := callerFirewall(tx, req.ProjectID, req.ID, firewallNotFoundAction); err != nil {
			return lab.Plan{}, err
		}
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: req.Created}},
			Steps:     func(ctx context.Context) error { return l.Sleep(ctx, lab.FirewallChange) },
		}, nil
	}
}

// planFirewallUnselectServer is the remove_firewall that comes before an
// apply_firewall to a server that the firewall's label selector covers:
// it names the selector; the apply moves the server.
func planFirewallUnselectServer(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	firewall, body, err := firewallAndTarget(tx, req)
	if err != nil {
		return lab.Plan{}, err
	}
	plan := lab.Plan{Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: body.Server}}}
	if selector := selectedBy(firewall, body.Server); selector != nil {
		plan.Resources = append(plan.Resources, state.ResourceRef{Type: labelSelectorResource, ID: selector.ID})
	}
	return plan, nil
}

// planFirewallApplySelector applies the firewall to a label selector: to
// the servers of the project that match it now and later, as far as each
// has room for another firewall.
func planFirewallApplySelector(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		firewall, body, err := firewallAndTarget(tx, req)
		if err != nil {
			return lab.Plan{}, err
		}
		if slices.ContainsFunc(firewall.AppliedTo, func(target state.FirewallTarget) bool {
			return target.ServerID == 0 && target.Selector == body.Selector
		}) {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "firewall_already_applied", firewallAlreadyApplied)
		}
		// a selector's ID comes from the firewalls' counter: Hetzner gives
		// it an ID of its own, and the lab has no kind for selectors
		target := state.FirewallTarget{ID: state.NewID(tx, state.Firewalls), Selector: body.Selector}
		firewall.AppliedTo = append(firewall.AppliedTo, target)
		refreshFirewallSelectors(l, tx, req.ProjectID)
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: labelSelectorResource, ID: target.ID}},
			Steps:     l.Machine().ChangeFirewallSelector,
		}, nil
	}
}

// planFirewallRemoveServer removes the firewall from a server it applies
// to directly; Hetzner refuses a server it reaches through a selector.
func planFirewallRemoveServer(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		firewall, body, err := firewallAndTarget(tx, req)
		if err != nil {
			return lab.Plan{}, err
		}
		if server, err := state.Get(tx, state.Servers, body.Server); err != nil || server.ProjectID != req.ProjectID {
			return lab.Plan{}, web.RefusalNull(http.StatusNotFound, "firewall_resource_not_found", firewallRemoveMissing)
		}
		index := slices.IndexFunc(firewall.AppliedTo, func(target state.FirewallTarget) bool { return target.ServerID == body.Server })
		switch {
		case index < 0 && selectedBy(firewall, body.Server) != nil:
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "firewall_managed_by_label_selector", firewallBySelector)
		case index < 0:
			// a server the firewall does not reach at all: the text of a
			// missing one (measured 2026-11-09)
			return lab.Plan{}, web.RefusalNull(http.StatusNotFound, "firewall_resource_not_found", firewallRemoveMissing)
		}
		firewall.AppliedTo = slices.Delete(firewall.AppliedTo, index, index+1)
		refreshFirewallSelectors(l, tx, req.ProjectID)
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: lab.ServerResource, ID: body.Server}},
			Steps:     l.Machine().ChangeFirewall,
		}, nil
	}
}

// planFirewallRemoveSelector removes the firewall from a label selector
// and so from the servers it reached through it.
func planFirewallRemoveSelector(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		firewall, body, err := firewallAndTarget(tx, req)
		if err != nil {
			return lab.Plan{}, err
		}
		index := slices.IndexFunc(firewall.AppliedTo, func(target state.FirewallTarget) bool {
			return target.ServerID == 0 && target.Selector == body.Selector
		})
		if index < 0 {
			return lab.Plan{}, web.RefusalNull(http.StatusNotFound, "firewall_resource_not_found", fmt.Sprintf(firewallSelectorNotThere, firewall.ID))
		}
		target := firewall.AppliedTo[index]
		firewall.AppliedTo = slices.Delete(firewall.AppliedTo, index, index+1)
		refreshFirewallSelectors(l, tx, req.ProjectID)
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: labelSelectorResource, ID: target.ID}},
			Steps:     l.Machine().ChangeFirewallSelector,
		}, nil
	}
}

// firewallAndTarget returns the firewall of a target's row and the row's
// body.
func firewallAndTarget(tx *state.Tx, req lab.Request) (*state.Firewall, firewallTargetBody, error) {
	var body firewallTargetBody
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, body, web.ParseFailed()
	}
	firewall, err := callerFirewall(tx, req.ProjectID, req.ID, firewallNotFoundAction)
	return firewall, body, err
}

// selectedBy returns the firewall's label selector entry that holds a
// server, or nil.
func selectedBy(firewall *state.Firewall, serverID int64) *state.FirewallTarget {
	for i := range firewall.AppliedTo {
		if target := &firewall.AppliedTo[i]; target.ServerID == 0 && slices.Contains(target.Servers, serverID) {
			return target
		}
	}
	return nil
}

// firewallsOf counts the firewalls that apply to a server.
func firewallsOf(tx *state.Tx, serverID int64) int {
	return len(state.IDs(tx, state.Firewalls, func(firewall *state.Firewall) bool {
		return slices.Contains(lab.FirewallServers(firewall), serverID)
	}))
}

// refreshFirewallSelectors brings the servers that the project's label
// selectors hold up to date, after servers, their labels or the
// firewalls changed: a server that no longer matches, or is gone, leaves;
// a matching server comes under the firewall if it has room for another
// (Hetzner's limit of 5 per server holds for selectors too: the sixth is
// not applied, measured 2026-11-08). Firewalls with the lower ID come
// first. Each server that comes or leaves shows the change as Hetzner does
// (noteSelectorChange); one that is gone leaves without. It tells
// whether anything changed.
func refreshFirewallSelectors(l *lab.Lab, tx *state.Tx, projectID int64) bool {
	ids := state.IDs(tx, state.Firewalls, func(firewall *state.Firewall) bool {
		return firewall.ProjectID == projectID && slices.ContainsFunc(firewall.AppliedTo, func(target state.FirewallTarget) bool { return target.ServerID == 0 })
	})
	servers := map[int64]*state.Server{}
	var serverIDs []int64
	for _, id := range state.IDs(tx, state.Servers, func(server *state.Server) bool { return server.ProjectID == projectID }) {
		if server, err := state.Get(tx, state.Servers, id); err == nil {
			servers[id] = server
			serverIDs = append(serverIDs, id)
		}
	}
	changed := false
	var firewalls []*state.Firewall
	for _, id := range ids {
		firewall, err := state.Get(tx, state.Firewalls, id)
		if err != nil {
			continue
		}
		firewalls = append(firewalls, firewall)
		for i := range firewall.AppliedTo {
			target := &firewall.AppliedTo[i]
			if target.ServerID != 0 {
				continue
			}
			selector, _ := web.ParseSelector(target.Selector)
			var kept []int64
			for _, id := range target.Servers {
				server, there := servers[id]
				switch {
				case there && selector.Matches(server.Labels):
					kept = append(kept, id)
				case there:
					noteSelectorChange(l, server, firewall.ID, false)
				}
			}
			if len(kept) != len(target.Servers) {
				changed = true
			}
			target.Servers = kept
		}
	}
	for _, firewall := range firewalls {
		for i := range firewall.AppliedTo {
			target := &firewall.AppliedTo[i]
			if target.ServerID != 0 {
				continue
			}
			selector, _ := web.ParseSelector(target.Selector)
			for _, id := range serverIDs {
				if !selector.Matches(servers[id].Labels) || slices.Contains(lab.FirewallServers(firewall), id) || firewallsOf(tx, id) >= firewallsPerServer {
					continue
				}
				target.Servers = append(target.Servers, id)
				noteSelectorChange(l, servers[id], firewall.ID, true)
				changed = true
			}
		}
	}
	return changed
}

// serverFirewallNote is the kind of the notes about a firewall on a
// server that a label selector takes or lets go, before its action
// appears; serverFirewallPending of those while the action runs.
const (
	serverFirewallNote    = "server_firewall"
	serverFirewallPending = "server_firewall_pending"
)

// noteSelectorChange shows a server coming under a firewall's label
// selector, or leaving it, as Hetzner does (measured 2026-11-09): for a
// while nothing changes on the server (a firewall that comes is not
// listed yet, one that goes is still applied), then an apply_firewall or
// remove_firewall naming the firewall and the server runs, and the server
// shows the firewall pending as long. A server whose create runs gets its
// selectors' firewalls once its create is done, as on Hetzner. The data
// plane has the change at once.
func noteSelectorChange(l *lab.Lab, server *state.Server, firewallID int64, join bool) {
	command, before, after, lasting := "apply_firewall", "", lab.FirewallJoin, lab.FirewallJoined
	if !join {
		command, before, after, lasting = "remove_firewall", firewallStatusApplied, lab.FirewallLeave, lab.FirewallLeft
	}
	if server.Status == state.ServerInitializing {
		after = lab.ServerCreate
	}
	key := lab.NoteKey{Kind: serverFirewallNote, Owner: server.ID, Subject: firewallID}
	l.AddNote(key, lab.Note{Status: before}, 0, after)
	key.Kind = serverFirewallPending
	l.AddNote(key, lab.Note{Status: firewallStatusPending}, after, lasting)
	l.RecordActionLater(server.ProjectID, command, []state.ResourceRef{
		{Type: firewallResource, ID: firewallID}, {Type: lab.ServerResource, ID: server.ID},
	}, after, lasting)
}

// releaseFirewalls takes a server that goes out of the firewalls: its
// direct entries go, and the selectors no longer hold it; Hetzner records
// no action for that (measured 2026-11-08). No other server gains room by
// it, so the selectors need no refresh.
func releaseFirewalls(tx *state.Tx, serverID int64) {
	for _, id := range state.IDs(tx, state.Firewalls, func(firewall *state.Firewall) bool {
		return slices.Contains(lab.FirewallServers(firewall), serverID)
	}) {
		if firewall, err := state.Get(tx, state.Firewalls, id); err == nil {
			firewall.AppliedTo = slices.DeleteFunc(firewall.AppliedTo, func(target state.FirewallTarget) bool { return target.ServerID == serverID })
			for i := range firewall.AppliedTo {
				firewall.AppliedTo[i].Servers = slices.DeleteFunc(firewall.AppliedTo[i].Servers, func(id int64) bool { return id == serverID })
			}
		}
	}
}
