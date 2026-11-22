package firewalls

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/labels"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Error codes of the firewall actions.
const (
	codeResourceNotFound = "firewall_resource_not_found"
	codeAlreadyApplied   = "firewall_already_applied"
	codeBySelector       = "firewall_managed_by_label_selector"
	codePrivateOnly      = "private_net_only_server"
	codeAlreadyAdded     = "server_already_added"
	codeLimit            = "resource_limit_exceeded"
)

// Target is an entry of apply_to and remove_from.
type Target struct {
	Type          string         `json:"type"`
	Server        *ServerRef     `json:"server"`
	LabelSelector *LabelSelector `json:"label_selector"`
}

// fwRef is the firewall in the resources of an action.
func fwRef(id int64) actions.Ref { return actions.Ref{ID: id, Type: refType} }

// serverSpec is an action naming server and firewall.
func serverSpec(command string, server, fw int64) actions.Spec {
	return actions.Spec{Command: command, Resources: []actions.Ref{{ID: server, Type: typeServer}, fwRef(fw)}}
}

// applyTargets adds the targets to firewall r and returns the specs of the
// apply actions: one per server naming server and firewall, one per
// selector naming only the firewall. res is the resolution before the
// change.
func applyTargets(tx store.Tx, r *Record, res resolution, targets []Target, field string) ([]actions.Spec, error) {
	var specs []actions.Spec
	seen := map[int64]bool{}
	for i, t := range targets {
		f := fmt.Sprintf("%s[%d]", field, i)
		switch {
		case t.Type == typeServer && t.Server != nil:
			id := t.Server.ID
			s, err := store.Get[servers.Record](tx, servers.Kind, r.Hlab.ProjectID, id)
			if errors.Is(err, store.ErrNotFound) || err == nil && s.Status == servers.StatusDeleting {
				return nil, &api.FieldError{Code: codeResourceNotFound, Message: fmt.Sprintf("server %d not found", id)}
			}
			if err != nil {
				return nil, err
			}
			if seen[id] {
				return nil, &api.FieldError{Code: codeAlreadyAdded, Message: fmt.Sprintf("server %d is given more than once", id)}
			}
			seen[id] = true
			if slices.ContainsFunc(r.AppliedTo, func(a Applied) bool {
				return a.Type == typeServer && a.Server != nil && a.Server.ID == id
			}) {
				return nil, &api.FieldError{Code: codeAlreadyApplied, Message: fmt.Sprintf("firewall %d is already applied to server %d", r.ID, id)}
			}
			if !s.IPv4().IsValid() {
				return nil, &api.FieldError{Code: codePrivateOnly, Message: fmt.Sprintf("server %d has no public IPv4", id)}
			}
			if !res.reaches(r.ID, id) && len(res.firewallsOf(id)) >= maxPerServer {
				return nil, &api.FieldError{Code: codeLimit, Message: fmt.Sprintf("server %d already has %d firewalls", id, maxPerServer)}
			}
			r.AppliedTo = append(r.AppliedTo, Applied{Type: typeServer, Server: &ServerRef{ID: id}})
			specs = append(specs, serverSpec("apply_firewall", id, r.ID))
		case t.Type == typeSelector && t.LabelSelector != nil:
			sel := t.LabelSelector.Selector
			if _, err := labels.ParseSelector(sel); err != nil || sel == "" {
				return nil, &api.FieldError{Field: f + ".label_selector.selector", Message: "invalid label selector"}
			}
			if slices.ContainsFunc(r.AppliedTo, func(a Applied) bool {
				return a.Type == typeSelector && a.LabelSelector != nil && a.LabelSelector.Selector == sel
			}) {
				return nil, &api.FieldError{Code: codeAlreadyApplied, Message: fmt.Sprintf("firewall %d is already applied to label selector %q", r.ID, sel)}
			}
			r.AppliedTo = append(r.AppliedTo, Applied{Type: typeSelector, LabelSelector: &LabelSelector{Selector: sel}})
			specs = append(specs, actions.Spec{Command: "apply_firewall", Resources: []actions.Ref{fwRef(r.ID)}})
		default:
			return nil, &api.FieldError{Field: f + ".type", Message: "type must be server or label_selector with its object"}
		}
	}
	return specs, nil
}

// removeTargets removes the targets from firewall r and returns the specs
// of the remove actions.
func removeTargets(r *Record, res resolution, targets []Target) ([]actions.Spec, error) {
	var specs []actions.Spec
	for i, t := range targets {
		f := fmt.Sprintf("remove_from[%d]", i)
		switch {
		case t.Type == typeServer && t.Server != nil:
			id := t.Server.ID
			n := len(r.AppliedTo)
			r.AppliedTo = slices.DeleteFunc(r.AppliedTo, func(a Applied) bool {
				return a.Type == typeServer && a.Server != nil && a.Server.ID == id
			})
			if len(r.AppliedTo) == n {
				if res.reaches(r.ID, id) {
					return nil, &api.FieldError{Code: codeBySelector, Message: fmt.Sprintf("server %d is applied by a label selector of firewall %d", id, r.ID)}
				}
				return nil, &api.FieldError{Code: codeResourceNotFound, Message: fmt.Sprintf("firewall %d is not applied to server %d", r.ID, id)}
			}
			specs = append(specs, serverSpec("remove_firewall", id, r.ID))
		case t.Type == typeSelector && t.LabelSelector != nil:
			sel := t.LabelSelector.Selector
			n := len(r.AppliedTo)
			r.AppliedTo = slices.DeleteFunc(r.AppliedTo, func(a Applied) bool {
				return a.Type == typeSelector && a.LabelSelector != nil && a.LabelSelector.Selector == sel
			})
			if len(r.AppliedTo) == n {
				return nil, &api.FieldError{Code: codeResourceNotFound, Message: fmt.Sprintf("firewall %d is not applied to label selector %q", r.ID, sel)}
			}
			specs = append(specs, actions.Spec{Command: "remove_firewall", Resources: []actions.Ref{fwRef(r.ID)}})
		default:
			return nil, &api.FieldError{Field: f + ".type", Message: "type must be server or label_selector with its object"}
		}
	}
	return specs, nil
}

// runPush runs each action as an agent push: success after the push, error
// when it fails.
func (m *Module) runPush(acts *actions.Manager, started []*actions.Action) {
	for _, a := range started {
		acts.Run(a.ID, func(ctx context.Context, _ func(int)) error { return m.d.PushAgent(ctx) })
	}
}

// change loads firewall id of the request's project, lets f change it and
// return the specs of its actions, stores it and starts the actions with a
// lock on the firewall; after the commit the actions run and the answer is
// 201 {actions}. A firewall applied nowhere filters nothing, so its actions
// succeed at once. set_rules uses it.
func (m *Module) change(w http.ResponseWriter, req *http.Request, f func(tx store.Tx, r *Record, res resolution) ([]actions.Spec, error)) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var started, done []*actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		res, err := resolve(tx, p.ID)
		if err != nil {
			return err
		}
		specs, err := f(tx, &r, res)
		if err != nil {
			return err
		}
		if err := put(tx, r); err != nil {
			return err
		}
		started, err = acts.StartAll(tx, p.ID, []actions.Ref{fwRef(id)}, specs)
		if err != nil || len(r.AppliedTo) > 0 {
			return err
		}
		err = acts.Succeed(tx, started)
		started, done = nil, started
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.runPush(acts, started)
	started = append(started, done...)
	api.JSON(w, http.StatusCreated, map[string]any{"actions": started})
}

func (m *Module) applyToResources(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ApplyTo []Target `json:"apply_to"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	m.changeTargets(w, req, body.ApplyTo, func(tx store.Tx, r *Record, res resolution, ts []Target) ([]actions.Spec, error) {
		return applyTargets(tx, r, res, ts, "apply_to")
	})
}

func (m *Module) removeFromResources(w http.ResponseWriter, req *http.Request) {
	var body struct {
		RemoveFrom []Target `json:"remove_from"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	m.changeTargets(w, req, body.RemoveFrom, func(_ store.Tx, r *Record, res resolution, ts []Target) ([]actions.Spec, error) {
		return removeTargets(r, res, ts)
	})
}

// targetsFunc adds or removes targets on firewall r and returns one action
// spec per target, in the order of targets.
type targetsFunc func(tx store.Tx, r *Record, res resolution, targets []Target) ([]actions.Spec, error)

// changeTargets runs apply_to_resources or remove_from_resources of the
// firewall in the path: the request checks all targets with f against the
// committed state and starts one action per target, refusing with locked
// only while the firewall has a running action; the actions queue behind
// the running actions of their servers. The work of each action applies its
// own target to the state the earlier actions left, pushes that applied_to
// to the agent as pending and, at success, applies the target once more to
// the record as it is then and commits it, so GET shows the change together
// with the action's success and an error or a stop leaves the old state.
func (m *Module) changeTargets(w http.ResponseWriter, req *http.Request, targets []Target, f targetsFunc) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	st := api.StoreFrom(req.Context())
	var started []*actions.Action
	err := st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		res, err := resolve(tx, p.ID)
		if err != nil {
			return err
		}
		specs, err := f(tx, &r, res, targets)
		if err != nil {
			return err
		}
		started, err = acts.StartAll(tx, p.ID, []actions.Ref{fwRef(id)}, specs)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	for i, a := range started {
		actionID, target := a.ID, targets[i:i+1]
		acts.Run(actionID, func(ctx context.Context, _ func(int)) error {
			return m.commitTarget(ctx, st, p.ID, id, actionID, target, f)
		})
	}
	api.JSON(w, http.StatusCreated, map[string]any{"actions": started})
}

// commitTarget is the work of action actionID of changeTargets on firewall
// id with its target.
func (m *Module) commitTarget(ctx context.Context, st *store.Store, projectID, id, actionID int64, target []Target, f targetsFunc) error {
	step := func(tx store.Tx, r Record) (Record, error) {
		res, err := resolve(tx, projectID)
		if err != nil {
			return r, err
		}
		next := r
		next.AppliedTo = slices.Clone(r.AppliedTo)
		_, err = f(tx, &next, res, target)
		return next, err
	}
	err := st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, projectID, id)
		if err != nil {
			return err
		}
		next, err := step(tx, r)
		if err != nil {
			return err
		}
		r.Hlab.Pending = &Pending{Action: actionID, AppliedTo: next.AppliedTo}
		return put(tx, r)
	})
	if err != nil {
		return actionError(err)
	}
	pushErr := m.d.PushAgent(ctx)
	var changeErr error
	err = st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, projectID, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if pd := r.Hlab.Pending; pd != nil && pd.Action == actionID {
			r.Hlab.Pending = nil
		}
		if pushErr == nil {
			// The change again, on the record as it is now, so a server
			// deleted while the action ran stays gone.
			var next Record
			if next, changeErr = step(tx, r); changeErr == nil {
				r = next
			}
		}
		return put(tx, r)
	})
	return errors.Join(pushErr, actionError(changeErr), err)
}

// actionError turns a validation error inside an action's work into the
// action's error.
func actionError(err error) error {
	var fe *api.FieldError
	if errors.As(err, &fe) {
		code := fe.Code
		if code == "" {
			code = "invalid_input"
		}
		return &actions.Error{Code: code, Message: fe.Message}
	}
	return err
}

// Recover implements resource.Recoverer: an apply or remove action
// interrupted by a stop has been marked error, so every firewall drops the
// applied_to it left pending; the next agent push follows the committed
// state.
func (m *Module) Recover(_ context.Context) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		fws, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, f := range fws {
			if f.Hlab.Pending == nil {
				continue
			}
			f.Hlab.Pending = nil
			if err := put(tx, f); err != nil {
				return err
			}
		}
		return nil
	})
}

// setRules replaces the rules; the actions are set_firewall_rules plus one
// apply_firewall per directly applied server.
func (m *Module) setRules(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Rules []Rule `json:"rules"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	m.change(w, req, func(_ store.Tx, r *Record, _ resolution) ([]actions.Spec, error) {
		rules, err := CheckRules(body.Rules)
		if err != nil {
			return nil, err
		}
		r.Rules = rules
		specs := []actions.Spec{{Command: "set_firewall_rules", Resources: []actions.Ref{fwRef(r.ID)}}}
		for _, a := range r.AppliedTo {
			if a.Type == typeServer && a.Server != nil {
				specs = append(specs, serverSpec("apply_firewall", a.Server.ID, r.ID))
			}
		}
		return specs, nil
	})
}

// AttachAtCreate implements servers.Firewalls: it applies firewalls ids to
// the new server serverID inside the create transaction, starts one
// apply_firewall action per firewall and runs them; they refuse on nothing
// and queue behind the running actions of the firewalls (the next actions
// of other server creates included), and their pushes wait for the commit
// of tx.
func (m *Module) AttachAtCreate(tx store.Tx, acts *actions.Manager, projectID, serverID int64, hasIPv4 bool, ids []int64) ([]*actions.Action, error) {
	res, err := resolve(tx, projectID)
	if err != nil {
		return nil, err
	}
	var recs []Record
	for _, id := range ids {
		if slices.ContainsFunc(recs, func(r Record) bool { return r.ID == id }) {
			continue
		}
		r, err := Load(tx, projectID, id)
		if err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}
	if !hasIPv4 {
		return nil, &api.FieldError{Code: codePrivateOnly, Message: fmt.Sprintf("server %d has no public IPv4", serverID)}
	}
	var specs []actions.Spec
	have := len(res.firewallsOf(serverID))
	for _, r := range recs {
		if !res.reaches(r.ID, serverID) {
			if have >= maxPerServer {
				return nil, &api.FieldError{Code: codeLimit, Message: fmt.Sprintf("a server can have at most %d firewalls", maxPerServer)}
			}
			have++
		}
		r.AppliedTo = append(r.AppliedTo, Applied{Type: typeServer, Server: &ServerRef{ID: serverID}})
		if err := put(tx, r); err != nil {
			return nil, err
		}
		specs = append(specs, serverSpec("apply_firewall", serverID, r.ID))
	}
	started, err := acts.StartAll(tx, projectID, nil, specs)
	if err != nil {
		return nil, err
	}
	m.runPush(acts, started)
	return started, nil
}
