package cloud

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's texts about firewalls (measured 2026-11-08). Their errors carry
// the details null.
const (
	firewallResource          = "firewall"
	labelSelectorResource     = "label_selector"
	firewallNotFound          = "firewall with ID '%d' not found" // GET, PUT, DELETE
	firewallNotFoundAction    = "firewall with ID %d not found"   // the actions, without quotes
	firewallNameTaken         = "name is already used"
	firewallInUse             = "firewall with ID %d is still in use"
	firewallLimit             = "firewall limit exceeded"
	firewallServerMissing     = "server with ID %d not found"
	firewallAlreadyApplied    = "firewall has already been applied to resource"
	firewallBySelector        = "firewall has been applied via label selector."
	firewallRemoveMissing     = "resource not found"
	firewallSelectorNotThere  = "firewall with ID %d cannot be removed from label_selector: resource not found"
	firewallServerNotThere    = "firewall with ID %d cannot be removed from server: resource not found" // not measured: follows the selector's
	firewallTooSmall          = "value required to be larger"
	firewallTooLarge          = "value required to be smaller"
	firewallSelectorMissing   = "parameter label_selector needs to be specified if type is label_selector"
	firewallSelectorInvalid   = "invalid label_selector: invalid syntax"
	firewallApplyToMissing    = "'apply_to' is required"
	firewallRemoveFromMissing = "'remove_from' is required"
	firewallNameMax           = 128
	firewallDescriptionMax    = 255
	firewallIPsMax            = 100 // per rule
	// FirewallsPerServer and the effective rules are Hetzner's limits:
	// 5 firewalls on a server, 500 effective rules in a firewall.
	firewallsPerServer    = 5
	firewallEffectiveMax  = 500
	firewallLimitServer   = "firewalls_per_server"
	firewallLimitRules    = "firewall_effective_rules"
	firewallLabelKey      = "invalid label value: key contains invalid characters or is malformed"
	firewallLabelValue    = "invalid label value: value contains invalid characters or is malformed" // not measured: follows the key's
	firewallStatusApplied = "applied"
	firewallStatusPending = "pending"
)

// firewallCreateRequest is the body of POST /firewalls. Rules and apply_to
// stay raw until checked: Hetzner answers an entry that is no object with
// json_error, not with "failed to parse json".
type firewallCreateRequest struct {
	Name    *string            `json:"name"`
	Labels  *map[string]string `json:"labels"`
	Rules   []json.RawMessage  `json:"rules"`
	ApplyTo []json.RawMessage  `json:"apply_to"`
}

// firewallUpdateRequest is the body of PUT /firewalls/{id}: only the name
// and the labels change; Hetzner ignores rules there.
type firewallUpdateRequest struct {
	Name   *string            `json:"name"`
	Labels *map[string]string `json:"labels"`
}

// firewallAnswer is a firewall as the API shows it. It is HLab's own
// type: schema.FirewallResource leaves out an empty applied_to_resources,
// which Hetzner sends as [] for a label selector.
type firewallAnswer struct {
	ID        int64                  `json:"id"`
	Name      string                 `json:"name"`
	Labels    map[string]string      `json:"labels"`
	Created   time.Time              `json:"created"`
	Rules     []schema.FirewallRule  `json:"rules"`
	AppliedTo []firewallTargetAnswer `json:"applied_to"`
}

// firewallTargetAnswer is one entry of a firewall's applied_to.
type firewallTargetAnswer struct {
	Type               string                                `json:"type"`
	Server             *schema.FirewallResourceServer        `json:"server,omitempty"`
	LabelSelector      *schema.FirewallResourceLabelSelector `json:"label_selector,omitempty"`
	AppliedToResources *[]schema.FirewallResource            `json:"applied_to_resources,omitempty"`
}

// firewallSortKeys are the sort values of GET /firewalls. Hetzner ignores
// an unknown one (measured 2026-11-08).
var firewallSortKeys = web.SortKeys[firewallAnswer]{
	"id":      func(a, b firewallAnswer) int { return cmp.Compare(a.ID, b.ID) },
	"name":    func(a, b firewallAnswer) int { return cmp.Compare(a.Name, b.Name) },
	"created": func(a, b firewallAnswer) int { return a.Created.Compare(b.Created) },
}

// registerFirewalls adds the routes of the firewalls and their actions.
func registerFirewalls(api *web.API, l *lab.Lab) {
	table := firewallActions(l)
	api.Handle("GET /firewalls", func(req *web.Request) (int, any, error) {
		selector, err := web.ParseSelector(req.URL.Query().Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		var firewalls []firewallAnswer
		_ = l.View(func(tx *state.Tx) error {
			for _, firewall := range state.All(tx, state.Firewalls) {
				if firewall.ProjectID == req.Caller.ProjectID && selector.Matches(firewall.Labels) {
					firewalls = append(firewalls, firewallJSON(firewall))
				}
			}
			return nil
		})
		firewalls = byName(req, firewalls, func(firewall firewallAnswer) string { return firewall.Name })
		if err := web.Sort(req, firewalls, firewallSortKeys); err != nil {
			slices.SortFunc(firewalls, firewallSortKeys["id"]) // an unknown sort value
		}
		return listJSON(req, "firewalls", firewalls)
	})

	api.Handle("POST /firewalls", func(req *web.Request) (int, any, error) {
		return createFirewall(req, l, table)
	})

	api.Handle("GET /firewalls/{id}", func(req *web.Request) (int, any, error) {
		var out firewallAnswer
		err := l.View(func(tx *state.Tx) error {
			firewall, err := callerFirewall(tx, req.Caller.ProjectID, web.PathID(req, "id"), firewallNotFound)
			if err == nil {
				out = firewallJSON(firewall)
			}
			return err
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]firewallAnswer{"firewall": out}, nil
	})

	api.Handle("PUT /firewalls/{id}", func(req *web.Request) (int, any, error) {
		return updateFirewall(req, l)
	})

	// Hetzner deletes a firewall that applies only through label selectors,
	// and refuses one that applies to a server directly (measured
	// 2026-11-08)
	api.Handle("DELETE /firewalls/{id}", func(req *web.Request) (int, any, error) {
		id := web.PathID(req, "id")
		changed := false
		err := l.Change(func(tx *state.Tx) error {
			firewall, err := callerFirewall(tx, req.Caller.ProjectID, id, firewallNotFound)
			if err != nil {
				return err
			}
			if slices.ContainsFunc(firewall.AppliedTo, func(target state.FirewallTarget) bool { return target.ServerID != 0 }) {
				return web.RefusalNull(http.StatusUnprocessableEntity, "resource_in_use", fmt.Sprintf(firewallInUse, id))
			}
			changed = len(lab.FirewallServers(firewall)) > 0
			state.Delete(tx, state.Firewalls, id)
			// a server it held a place on may now take another selector's
			changed = refreshFirewallSelectors(l, tx, req.Caller.ProjectID) || changed
			return nil
		})
		if err != nil {
			return 0, nil, err
		}
		if changed {
			l.Machine().UpdatePicturesLater()
		}
		return http.StatusNoContent, nil, nil
	})

	api.Handle("POST /firewalls/{id}/actions/set_rules", func(req *web.Request) (int, any, error) {
		return setFirewallRules(req, l, table)
	})
	api.Handle("POST /firewalls/{id}/actions/apply_to_resources", func(req *web.Request) (int, any, error) {
		return changeFirewallTargets(req, l, table, "apply_to", firewallApplyToMissing, true)
	})
	api.Handle("POST /firewalls/{id}/actions/remove_from_resources", func(req *web.Request) (int, any, error) {
		return changeFirewallTargets(req, l, table, "remove_from", firewallRemoveFromMissing, false)
	})
	registerResourceActions(api, l, "firewalls", firewallResource)
}

// createFirewall creates a firewall with its rules and the resources it
// applies to. Its answer lists set_firewall_rules, finished at once, and
// an apply_firewall for each entry of apply_to (measured 2026-11-08).
func createFirewall(req *web.Request, l *lab.Lab, table lab.ActionTable) (int, any, error) {
	var body firewallCreateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	rules, err := decodeFirewallRules(body.Rules)
	if err != nil {
		return 0, nil, err
	}
	targets, err := decodeFirewallTargets(body.ApplyTo)
	if err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	checkFirewallName(&check, body.Name)
	if body.Labels != nil {
		check.LabelsWith(*body.Labels, firewallLabelKey, firewallLabelValue)
	}
	checkFirewallRuleFields(&check, rules)
	checkFirewallTargetFields(&check, "apply_to", targets)
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	checked, err := checkFirewallRules(rules)
	if err != nil {
		return 0, nil, err
	}
	if err := checkFirewallSelectors(targets); err != nil {
		return 0, nil, err
	}
	firewall := &state.Firewall{
		ProjectID: req.Caller.ProjectID,
		Name:      *body.Name,
		Labels:    map[string]string{},
		Created:   l.Now(),
		Rules:     checked,
	}
	if body.Labels != nil {
		firewall.Labels = *body.Labels
	}
	rows := []lab.Row{{Command: "set_rules", Body: []byte("{}")}}
	for _, target := range targets {
		rows = append(rows, targetRow(target, true))
	}
	var id int64
	actions, err := l.Create(table, lab.Request{ProjectID: firewall.ProjectID}, func(tx *state.Tx) (int64, error) {
		if _, taken := state.ByName(tx, state.Firewalls, firewall.ProjectID, firewall.Name); taken {
			return 0, web.RefusalNull(http.StatusConflict, "uniqueness_error", firewallNameTaken)
		}
		firewall.ID = state.NewID(tx, state.Firewalls)
		state.Put(tx, state.Firewalls, firewall)
		id = firewall.ID
		return firewall.ID, nil
	}, rows...)
	if err != nil {
		return 0, nil, actError(err)
	}
	var out firewallAnswer
	_ = l.View(func(tx *state.Tx) error {
		if current, err := state.Get(tx, state.Firewalls, id); err == nil {
			out = firewallJSON(current)
		}
		return nil
	})
	return http.StatusCreated, map[string]any{"firewall": out, "actions": actionsJSON(actions)}, nil
}

// updateFirewall changes a firewall's name and labels. Unlike a Floating
// IP's, an empty name is refused (measured 2026-11-08).
func updateFirewall(req *web.Request, l *lab.Lab) (int, any, error) {
	var body firewallUpdateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Update: true}
	if body.Name != nil {
		checkFirewallName(&check, body.Name)
	}
	if body.Labels != nil {
		check.LabelsWith(*body.Labels, firewallLabelKey, firewallLabelValue)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	var out firewallAnswer
	err := l.Change(func(tx *state.Tx) error {
		firewall, err := callerFirewall(tx, req.Caller.ProjectID, web.PathID(req, "id"), firewallNotFound)
		if err != nil {
			return err
		}
		if body.Name != nil && *body.Name != firewall.Name {
			if _, taken := state.ByName(tx, state.Firewalls, firewall.ProjectID, *body.Name); taken {
				return web.RefusalNull(http.StatusConflict, "uniqueness_error", firewallNameTaken)
			}
			firewall.Name = *body.Name
		}
		if body.Labels != nil {
			firewall.Labels = *body.Labels
		}
		out = firewallJSON(firewall)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]firewallAnswer{"firewall": out}, nil
}

// setFirewallRules replaces a firewall's rules: set_firewall_rules,
// finished at once, and an apply_firewall naming the servers it applies
// to, if any (measured 2026-11-08). A missing rules field clears them.
func setFirewallRules(req *web.Request, l *lab.Lab, table lab.ActionTable) (int, any, error) {
	var body firewallCreateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	rules, err := decodeFirewallRules(body.Rules)
	if err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	checkFirewallRuleFields(&check, rules)
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	if _, err := checkFirewallRules(rules); err != nil {
		return 0, nil, err
	}
	// the row's body always has rules, unlike a create's
	raw, _ := json.Marshal(map[string][]json.RawMessage{"rules": append([]json.RawMessage{}, body.Rules...)})
	id := web.PathID(req, "id")
	rows := []lab.Row{{Command: "set_rules", Body: raw}}
	// read before the change: a server that goes meanwhile leaves an
	// apply_firewall that names only the firewall, which does no harm
	_ = l.View(func(tx *state.Tx) error {
		if firewall, err := state.Get(tx, state.Firewalls, id); err == nil && len(lab.FirewallServers(firewall)) > 0 {
			rows = append(rows, lab.Row{Command: "apply_rules"})
		}
		return nil
	})
	actions, err := l.ActAll(table, lab.Request{ProjectID: req.Caller.ProjectID, ID: id}, rows...)
	if err != nil {
		return 0, nil, actError(err)
	}
	return http.StatusCreated, map[string]any{"actions": actionsJSON(actions)}, nil
}

// changeFirewallTargets applies a firewall to the resources of the body's
// field (apply_to) or removes it from them (remove_from): one action per
// entry. A server that the firewall reaches through a label selector moves
// to a direct entry: a remove_firewall naming the selector comes first,
// both finished at once (measured 2026-11-08).
func changeFirewallTargets(req *web.Request, l *lab.Lab, table lab.ActionTable, field, missing string, apply bool) (int, any, error) {
	var body map[string]json.RawMessage
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	raw, given := body[field]
	var entries []json.RawMessage
	if given && string(raw) != "null" {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return 0, nil, web.ParseFailed()
		}
	}
	if entries == nil {
		check := web.Check{Singular: true}
		check.Fail(field, missing)
		return 0, nil, check.Err()
	}
	targets, err := decodeFirewallTargets(entries)
	if err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	checkFirewallTargetFields(&check, field, targets)
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	if err := checkFirewallSelectors(targets); err != nil {
		return 0, nil, err
	}
	id := web.PathID(req, "id")
	var rows []lab.Row
	_ = l.View(func(tx *state.Tx) error {
		firewall, _ := state.Get(tx, state.Firewalls, id)
		for _, target := range targets {
			if apply && target.Type == "server" && firewall != nil && selectedBy(firewall, *target.ServerID) != nil {
				rows = append(rows, lab.Row{Command: "unselect_server", Body: targetBody(target)})
			}
			rows = append(rows, targetRow(target, apply))
		}
		return nil
	})
	actions, err := l.ActAll(table, lab.Request{ProjectID: req.Caller.ProjectID, ID: id}, rows...)
	if err != nil {
		return 0, nil, actError(err)
	}
	return http.StatusCreated, map[string]any{"actions": actionsJSON(actions)}, nil
}

// targetRow returns the action table's row that applies a firewall to a
// target, or removes it.
func targetRow(target firewallTargetRequest, apply bool) lab.Row {
	var command string
	switch {
	case apply && target.Type == "server":
		command = "apply_server"
	case apply:
		command = "apply_selector"
	case target.Type == "server":
		command = "remove_server"
	default:
		command = "remove_selector"
	}
	return lab.Row{Command: command, Body: targetBody(target)}
}

// targetBody returns the body of a target's row: {"server": id} or
// {"selector": text}.
func targetBody(target firewallTargetRequest) []byte {
	if target.Type == "server" {
		raw, _ := json.Marshal(map[string]int64{"server": *target.ServerID})
		return raw
	}
	raw, _ := json.Marshal(map[string]string{"selector": *target.Selector})
	return raw
}

// checkFirewallName checks a firewall's name: 1 to 128 characters.
func checkFirewallName(check *web.Check, name *string) {
	switch {
	case name == nil || *name == "":
		check.Fail("name", firewallTooSmall)
	case len([]rune(*name)) > firewallNameMax:
		check.Fail("name", firewallTooLarge)
	}
}

// callerFirewall returns the firewall of an ID if it belongs to the
// project, with Hetzner's 404 otherwise; notFound is the text, which
// differs between the endpoints and the actions.
func callerFirewall(tx *state.Tx, projectID, id int64, notFound string) (*state.Firewall, error) {
	firewall, err := state.Get(tx, state.Firewalls, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && firewall.ProjectID != projectID) {
		return nil, web.NotFoundNull(fmt.Sprintf(notFound, id))
	}
	return firewall, err
}

// firewallJSON returns a firewall as the API shows it.
func firewallJSON(firewall *state.Firewall) firewallAnswer {
	out := firewallAnswer{
		ID:        firewall.ID,
		Name:      firewall.Name,
		Labels:    firewall.Labels,
		Created:   firewall.Created,
		Rules:     []schema.FirewallRule{},
		AppliedTo: []firewallTargetAnswer{},
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	for _, rule := range firewall.Rules {
		answer := schema.FirewallRule{
			Direction:      rule.Direction,
			Protocol:       rule.Protocol,
			Description:    rule.Description,
			SourceIPs:      append([]string{}, rule.SourceIPs...),
			DestinationIPs: append([]string{}, rule.DestinationIPs...),
		}
		if rule.Port != "" {
			answer.Port = new(rule.Port)
		}
		out.Rules = append(out.Rules, answer)
	}
	for _, target := range firewall.AppliedTo {
		if target.ServerID != 0 {
			out.AppliedTo = append(out.AppliedTo, firewallTargetAnswer{
				Type: "server", Server: &schema.FirewallResourceServer{ID: target.ServerID},
			})
			continue
		}
		servers := []schema.FirewallResource{}
		for _, id := range target.Servers {
			servers = append(servers, schema.FirewallResource{Type: "server", Server: &schema.FirewallResourceServer{ID: id}})
		}
		out.AppliedTo = append(out.AppliedTo, firewallTargetAnswer{
			Type:               labelSelectorResource,
			LabelSelector:      &schema.FirewallResourceLabelSelector{Selector: target.Selector},
			AppliedToResources: &servers,
		})
	}
	return out
}

// serverFirewallsJSON returns a server's public_net.firewalls: each
// firewall that applies to it, pending while an action that names both
// runs, applied after.
func serverFirewallsJSON(tx *state.Tx, server *state.Server) []schema.ServerFirewall {
	out := []schema.ServerFirewall{}
	for _, id := range state.IDs(tx, state.Firewalls, func(firewall *state.Firewall) bool {
		return firewall.ProjectID == server.ProjectID && slices.Contains(lab.FirewallServers(firewall), server.ID)
	}) {
		status := firewallStatusApplied
		running := state.IDs(tx, state.Actions, func(action *state.Action) bool {
			return action.Status == state.ActionRunning && action.ProjectID == server.ProjectID &&
				namesResource(action, firewallResource, id) && namesResource(action, lab.ServerResource, server.ID)
		})
		if len(running) > 0 {
			status = firewallStatusPending
		}
		out = append(out, schema.ServerFirewall{ID: id, Status: status})
	}
	return out
}
