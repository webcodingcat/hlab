package loadbalancers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/store"
)

// change runs one load balancer action: in a single transaction it loads
// load balancer id, applies f (which validates and changes the record and
// returns further action resources), starts the action command and stores
// the record; the action's work pushes the agent document.
func (m *Module) change(w http.ResponseWriter, req *http.Request, id int64, command string, f func(tx store.Tx, r *Record) ([]actions.Ref, error)) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		more, err := f(tx, &r)
		if err != nil {
			return err
		}
		refs := append([]actions.Ref{{ID: id, Type: refType}}, more...)
		if action, err = acts.Start(tx, p.ID, command, refs); err != nil {
			return err
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.d.PushAgent(ctx) })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// queued runs a load balancer action that queues behind the running actions
// on its resources: target, service and network actions. The request
// checks f against the committed state and starts the action command on
// the load balancer and the refs f returns; it refuses with locked only
// when refuseLB is set and the load balancer has a running action. The work
// waits its turn, applies f again to the state the earlier actions left,
// pushes that state to the agent as pending and commits it at success, so
// GET shows the change together with the action's success.
func (m *Module) queued(w http.ResponseWriter, req *http.Request, id int64, command string, refuseLB bool, f func(tx store.Tx, r *Record) ([]actions.Ref, error)) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	st := api.StoreFrom(req.Context())
	lbRef := actions.Ref{ID: id, Type: refType}
	var action *actions.Action
	err := st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		more, err := f(tx, &r)
		if err != nil {
			return err
		}
		var refuse []actions.Ref
		if refuseLB {
			refuse = []actions.Ref{lbRef}
		}
		action, err = acts.StartQueued(tx, p.ID, command, append([]actions.Ref{lbRef}, more...), refuse)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	actionID := action.ID
	acts.Run(actionID, func(ctx context.Context, _ func(int)) error {
		err := st.Tx(func(tx store.Tx) error {
			r, err := Load(tx, p.ID, id)
			if err != nil {
				return err
			}
			next := r
			next.Services = slices.Clone(r.Services)
			next.Targets = slices.Clone(r.Targets)
			next.PrivateNet = slices.Clone(r.PrivateNet)
			if _, err := f(tx, &next); err != nil {
				return err
			}
			r.Hlab.Pending = &Pending{
				Action: actionID, Services: next.Services, Targets: next.Targets,
				PrivateNet: next.PrivateNet, VLAN: next.Hlab.VLAN,
			}
			return put(tx, r)
		})
		if err != nil {
			return actionError(err)
		}
		pushErr := m.d.PushAgent(ctx)
		err = st.Tx(func(tx store.Tx) error {
			r, err := Load(tx, p.ID, id)
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if pd := r.Hlab.Pending; pushErr == nil && pd != nil && pd.Action == actionID {
				r = pd.apply(r)
			}
			r.Hlab.Pending = nil
			return put(tx, r)
		})
		return errors.Join(pushErr, err)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// actionError turns an error of a validation inside an action's work into
// the action's error object with the code the request would have answered.
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

// decodeAction reads the path ID and the body of a load balancer action;
// an empty body is no error.
func decodeAction(w http.ResponseWriter, req *http.Request, body any) (int64, bool) {
	id, ok := api.PathID(w, req)
	if !ok {
		return 0, false
	}
	if req.ContentLength == 0 {
		return id, true
	}
	return id, api.Decode(w, req, body)
}

// changeAlgorithm sets the balancing algorithm.
func (m *Module) changeAlgorithm(w http.ResponseWriter, req *http.Request) {
	var body Algorithm
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "change_algorithm", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		if err := checkAlgorithm("type", body.Type); err != nil {
			return nil, err
		}
		r.Algorithm = body.Type
		return nil, nil
	})
}

// changeType moves the load balancer to another type whose limits hold its
// services, targets and certificates.
func (m *Module) changeType(w http.ResponseWriter, req *http.Request) {
	var body struct {
		LoadBalancerType json.RawMessage `json:"load_balancer_type"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "change_type", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		ref := refString(body.LoadBalancerType)
		typ, ok := m.lbType(ref)
		if !ok {
			return nil, &api.FieldError{Field: "load_balancer_type", Message: "load balancer type " + ref + " not found"}
		}
		if err := fits(*r, typ); err != nil {
			return nil, err
		}
		r.Hlab.Type = typ.Name
		return nil, nil
	})
}

// fits refuses typ when r has more services, targets or certificates than
// typ allows.
func fits(r Record, typ Type) error {
	certs := map[int64]bool{}
	for _, s := range r.Services {
		if s.HTTP != nil {
			for _, c := range s.HTTP.Certificates {
				certs[c] = true
			}
		}
	}
	var over string
	switch {
	case len(r.Services) > typ.MaxServices:
		over = fmt.Sprintf("%d services, the type allows %d", len(r.Services), typ.MaxServices)
	case len(r.Targets) > typ.MaxTargets:
		over = fmt.Sprintf("%d targets, the type allows %d", len(r.Targets), typ.MaxTargets)
	case len(certs) > typ.MaxAssignedCertificates:
		over = fmt.Sprintf("%d certificates, the type allows %d", len(certs), typ.MaxAssignedCertificates)
	default:
		return nil
	}
	return &api.FieldError{Code: "invalid_load_balancer_type", Message: "the load balancer has " + over}
}

// changeProtection sets the delete protection present in the body.
func (m *Module) changeProtection(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Delete *bool `json:"delete"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "change_protection", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		if body.Delete != nil {
			r.Protection.Delete = *body.Delete
		}
		return nil, nil
	})
}

// delete removes a load balancer without delete protection and starts a
// delete_load_balancer action that ends after the agent push.
func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		if r.Protection.Delete {
			return actions.ErrProtected
		}
		if action, err = acts.Start(tx, p.ID, "delete_load_balancer", []actions.Ref{{ID: id, Type: refType}}); err != nil {
			return err
		}
		return m.remove(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.d.PushAgent(ctx) })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// remove deletes the record of r, releases its system IPv4 and hands its
// primary IPs back to the primary IPs module.
func (m *Module) remove(tx store.Tx, r Record) error {
	if err := store.Delete(tx, Kind, r.Hlab.ProjectID, r.ID); err != nil {
		return err
	}
	if r.Hlab.IPv4ID == nil {
		if err := ipam.Release(tx, r.IPv4()); err != nil {
			return err
		}
	}
	if (r.Hlab.IPv4ID != nil || r.Hlab.IPv6ID != nil) && m.d.PublicIPs != nil {
		return m.d.PublicIPs.LoadBalancerDeleted(tx, r.ID)
	}
	return nil
}

// Delete implements resource.Deleter for project deletion: it removes load
// balancer id of any project with its addresses. One already gone is no
// error.
func (m *Module) Delete(_ context.Context, id int64) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.ID == id {
				return m.remove(tx, r)
			}
		}
		return nil
	})
}
