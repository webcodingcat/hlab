package zones

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strconv"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// tsigAlgorithms are the TSIG algorithms of secondary zones.
var tsigAlgorithms = []string{"hmac-md5", "hmac-sha1", "hmac-sha256"}

// ownCommands are the zone writes that change the state of the zone
// itself; they answer locked while an action holds the zone. All other
// writes add, change or remove RRSets and queue behind those actions.
var ownCommands = []string{"change_protection", "change_ttl", "change_primary_nameservers", "import_zonefile"}

// zoneRef is the zone in the resources of an action.
func zoneRef(id int64) actions.Ref { return actions.Ref{ID: id, Type: refType} }

// write runs one zone write: in a single transaction it loads the zone of
// the path, lets f validate and change it and starts the action command;
// the action publishes the zones. A command of ownCommands answers locked
// while an action holds the zone and stores its change at once. Any other
// command changes RRSets: it queues behind the running actions of the zone
// and its change takes effect only at its success (see queued). It returns
// the zone as f left it and the action.
func (m *Module) write(req *http.Request, command string, f func(r *Record) error) (Record, *actions.Action, error) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	own := slices.Contains(ownCommands, command)
	var r Record
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, req.PathValue("id")); err != nil {
			return err
		}
		if !own {
			r = cloneSets(r)
		}
		if err := f(&r); err != nil {
			return err
		}
		refs := []actions.Ref{zoneRef(r.ID)}
		if !own {
			action, err = acts.StartQueued(tx, p.ID, command, refs, nil)
			return err
		}
		if action, err = acts.StartQueued(tx, p.ID, command, refs, refs); err != nil {
			return err
		}
		return put(tx, r)
	})
	if err != nil {
		return r, nil, err
	}
	if own {
		m.publish(acts, action)
	} else {
		m.queued(acts, p.ID, r.ID, action.ID, f)
	}
	return r, action, nil
}

// queued runs the work of RRSet action actionID on zone id once its turn
// has come: f applied again to the zone as earlier actions left it is
// stored as the zone's pending RRSets, which only the agent document
// shows, and published; at success f is applied once more to the zone as
// it is then and committed, so GET shows the change together with the
// action's success. A failed publish drops the pending RRSets; Recover
// drops those of an action a stop interrupted.
func (m *Module) queued(acts *actions.Manager, projectID, id, actionID int64, f func(r *Record) error) {
	acts.Run(actionID, func(ctx context.Context, _ func(int)) error {
		err := m.d.Store.Tx(func(tx store.Tx) error {
			r, err := store.Get[Record](tx, Kind, projectID, id)
			if err != nil {
				return err
			}
			next := cloneSets(r)
			if err := f(&next); err != nil {
				return err
			}
			r.Hlab.Pending = &Pending{Action: actionID, RRSets: next.RRSets}
			return put(tx, r)
		})
		if err != nil {
			return workError(err)
		}
		pubErr := m.publishNow(ctx)
		var changeErr error
		err = m.d.Store.Tx(func(tx store.Tx) error {
			r, err := store.Get[Record](tx, Kind, projectID, id)
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if pd := r.Hlab.Pending; pd != nil && pd.Action == actionID {
				r.Hlab.Pending = nil
			}
			if pubErr == nil {
				next := cloneSets(r)
				if changeErr = f(&next); changeErr == nil {
					r = next
				}
			}
			return put(tx, r)
		})
		return errors.Join(pubErr, workError(changeErr), err)
	})
}

// cloneSets returns r with its own copies of the RRSets and their records,
// so f can change them without touching r.
func cloneSets(r Record) Record {
	r.RRSets = slices.Clone(r.RRSets)
	for i := range r.RRSets {
		r.RRSets[i].Records = slices.Clone(r.RRSets[i].Records)
	}
	return r
}

// workError turns an error of a check inside queued work into the action's
// error with the code the request would have answered.
func workError(err error) error {
	var fe *api.FieldError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return &actions.Error{Code: "not_found", Message: "the RRSet was not found"}
	case errors.As(err, &fe):
		code := fe.Code
		if code == "" {
			code = "invalid_input"
		}
		return &actions.Error{Code: code, Message: fe.Message}
	}
	return err
}

// change runs one zone action through write and answers status {action}.
func (m *Module) change(w http.ResponseWriter, req *http.Request, command string, status int, f func(r *Record) error) {
	if _, action, err := m.write(req, command, f); err != nil {
		api.Fail(w, err)
	} else {
		api.JSON(w, status, map[string]any{"action": action})
	}
}

func (m *Module) changeProtection(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Delete *bool `json:"delete"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	m.change(w, req, "change_protection", http.StatusCreated, func(r *Record) error {
		if body.Delete != nil {
			r.Protection.Delete = *body.Delete
		}
		return nil
	})
}

// changeTTL sets the default TTL of a primary zone.
func (m *Module) changeTTL(w http.ResponseWriter, req *http.Request) {
	var body struct {
		TTL *int `json:"ttl"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	m.change(w, req, "change_ttl", http.StatusCreated, func(r *Record) error {
		if r.Mode != ModePrimary {
			return errMode
		}
		if body.TTL == nil {
			return &api.FieldError{Field: "ttl", Message: "ttl is required"}
		}
		if err := checkTTL(*body.TTL); err != nil {
			return err
		}
		r.TTL = *body.TTL
		bumpSerial(r, m.d.Now())
		return nil
	})
}

// changePrimaryNameservers replaces the primary nameservers of a secondary
// zone.
func (m *Module) changePrimaryNameservers(w http.ResponseWriter, req *http.Request) {
	var body struct {
		PrimaryNameservers []PrimaryNameserver `json:"primary_nameservers"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	m.change(w, req, "change_primary_nameservers", http.StatusCreated, func(r *Record) error {
		if r.Mode != ModeSecondary {
			return errMode
		}
		ns, err := checkNameservers(body.PrimaryNameservers)
		if err != nil {
			return err
		}
		r.PrimaryNameservers = ns
		return nil
	})
}

// checkNameservers validates the primary nameservers of a secondary zone
// and returns them with the default port 53 filled in. Nothing is checked
// for reachability.
func checkNameservers(ns []PrimaryNameserver) ([]PrimaryNameserver, error) {
	if len(ns) == 0 {
		return nil, &api.FieldError{Field: "primary_nameservers", Message: "a secondary zone needs primary nameservers"}
	}
	out := make([]PrimaryNameserver, len(ns))
	seen := map[netip.Addr]bool{}
	for i, n := range ns {
		f := fmt.Sprintf("primary_nameservers[%d].", i)
		addr, err := netip.ParseAddr(n.Address)
		if err != nil || addr.Zone() != "" {
			return nil, &api.FieldError{Field: f + "address", Message: "address must be an IP address"}
		}
		if seen[addr] {
			return nil, &api.FieldError{Field: f + "address", Message: "address is given twice"}
		}
		seen[addr] = true
		if n.Port == 0 {
			n.Port = 53
		}
		if n.Port < 1 || n.Port > 65535 {
			return nil, &api.FieldError{Field: f + "port", Message: "port must be between 1 and 65535"}
		}
		if n.TSIGAlgorithm != "" && !slices.Contains(tsigAlgorithms, n.TSIGAlgorithm) {
			return nil, &api.FieldError{Field: f + "tsig_algorithm", Message: "tsig_algorithm must be one of hmac-md5, hmac-sha1, hmac-sha256"}
		}
		if _, err := base64.StdEncoding.DecodeString(n.TSIGKey); err != nil {
			return nil, &api.FieldError{Field: f + "tsig_key", Message: "tsig_key must be base64"}
		}
		out[i] = n
	}
	return out, nil
}

// zoneAction answers GET /zones/actions/{id}: an action of a zone of the
// project. Other paths of two segments below /zones are not found.
func (m *Module) zoneAction(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(req.PathValue("sub"), 10, 64)
	if req.PathValue("id") != "actions" || err != nil {
		api.NotFound(w)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	a, err := api.ActionsFrom(req.Context()).Get(p.ID, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && !slices.ContainsFunc(a.Resources, func(r actions.Ref) bool { return r.Type == refType }) {
		api.NotFound(w)
		return
	}
	if err != nil {
		api.ServerError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"action": a})
}
