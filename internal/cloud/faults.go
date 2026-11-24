//go:build windows

package cloud

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
	"github.com/webcodingcat/hlab/internal/resources/volumes"
	"github.com/webcodingcat/hlab/internal/store"
)

// newFaults sets a new fault manager for this cloud run; resources are
// the first path segments of the router's modules. The effects of the
// server and disk faults act through the run's servers and volumes
// modules, the net faults through a new NetEffects that pushes the agent
// document.
func (c *Cloud) newFaults(resources []string) {
	net := faults.NewNetEffects(c.PushAgent)
	effects := faults.Effects(c.srv, c.vol)
	maps.Copy(effects, net.Effects())
	c.netEffects.Store(net)
	c.faults.Store(faults.New(faults.Options{
		Lookup:    c.lookupTarget,
		Resources: resources,
		Effects:   effects,
	}))
}

// faultLayer is the fault layer of the API chain. The router is built
// before the manager, which needs the router's resources, so the layer
// runs the middleware of the run's manager per request; without one it
// passes the request on.
func (c *Cloud) faultLayer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if m := c.faults.Load(); m != nil {
			m.Middleware(next).ServeHTTP(w, req)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// endFaults ends the faults on a deleted target (servers, volumes and
// storage boxes call it); none without a manager.
func (c *Cloud) endFaults(ctx context.Context, project, targetType string, id int64) error {
	if m := c.faults.Load(); m != nil {
		return m.EndTarget(ctx, project, targetType, id)
	}
	return nil
}

// faultActive reports whether a fault of kind is active on the target;
// never without a manager.
func (c *Cloud) faultActive(project, kind, targetType string, id int64) bool {
	m := c.faults.Load()
	return m != nil && m.Active(project, kind, targetType, id)
}

// endFault ends the faults of kind on the target; none without a manager.
func (c *Cloud) endFault(ctx context.Context, project, kind, targetType string, id int64) error {
	if m := c.faults.Load(); m != nil {
		return m.EndKind(ctx, project, kind, targetType, id)
	}
	return nil
}

// lookupTarget checks that t exists in project: servers, volumes and
// storage boxes in the store (a volume_unavailable or disk_slow volume
// must be attached), locations in the catalog.
func (c *Cloud) lookupTarget(_ context.Context, project, kind string, t faults.Target) error {
	if t.Type == faults.TargetLocations {
		for _, l := range []string{t.From, t.To} {
			if _, ok := c.cat.Location(l); !ok {
				return fmt.Errorf("location %q: %w", l, faults.ErrNotFound)
			}
		}
		return nil
	}
	return c.st.Tx(func(tx store.Tx) error {
		p, err := store.ProjectByName(tx, project)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("project %s: %w", project, faults.ErrNotFound)
		}
		if err != nil {
			return err
		}
		switch t.Type {
		case faults.TargetServer:
			_, err = store.Get[servers.Record](tx, servers.Kind, p.ID, t.ID)
		case faults.TargetVolume:
			var v volumes.Record
			v, err = store.Get[volumes.Record](tx, volumes.Kind, p.ID, t.ID)
			if err == nil && (kind == faults.KindVolumeUnavailable || kind == faults.KindDiskSlow) && v.Server == nil {
				return &faults.InputError{Field: "target", Message: fmt.Sprintf("volume %d is not attached", t.ID)}
			}
		case faults.TargetStorageBox:
			_, err = storageboxes.Load(tx, p.ID, t.ID)
		}
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%s %d: %w", t.Type, t.ID, faults.ErrNotFound)
		}
		return err
	})
}

// netFaults turns the net faults into agent document entries. doc holds
// the modules' contributions already (servers, networks, floating IPs,
// storage boxes); locations targets take the project's servers per
// location from the store, a box target its IP from the storage box
// record. A fault whose target is gone is left out.
func netFaults(tx store.Tx, doc *agentapi.Config, fs []faults.Fault) ([]agentapi.NetFault, error) {
	if len(fs) == 0 {
		return nil, nil
	}
	entries := faultServers(doc)
	var out []agentapi.NetFault
	for _, f := range fs {
		if f.Target == nil {
			continue
		}
		nf := agentapi.NetFault{
			ID:          f.ID,
			Kind:        f.Kind,
			LossPercent: paramInt(f.Params, "loss_percent"),
			DelayMs:     paramInt(f.Params, "delay_ms"),
			JitterMs:    paramInt(f.Params, "jitter_ms"),
		}
		nf.Traffic, _ = f.Params["traffic"].(string)
		if f.Seed != nil {
			nf.Seed = *f.Seed
		}
		switch f.Target.Type {
		case faults.TargetServer:
			e, ok := entries[f.Target.ID]
			if !ok {
				continue
			}
			nf.Server = &e
		case faults.TargetLocations:
			from, to, ok, err := locationServers(tx, f.Project, f.Target.From, f.Target.To, entries)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			nf.From, nf.To = from, to
		case faults.TargetStorageBox:
			ip, ok, err := boxIP(tx, f.Project, f.Target.ID)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			nf.BoxIP = ip
		default:
			continue
		}
		out = append(out, nf)
	}
	return out, nil
}

// faultServers returns the fault entry of every server in doc: its public
// IPv4 and its addresses in the private networks.
func faultServers(doc *agentapi.Config) map[int64]agentapi.FaultServer {
	entries := make(map[int64]agentapi.FaultServer, len(doc.Servers))
	for _, s := range doc.Servers {
		entries[s.ID] = agentapi.FaultServer{ID: s.ID, PublicIPv4: s.PublicIPv4}
	}
	for _, n := range doc.Networks {
		for _, m := range n.Members {
			e, ok := entries[m.ServerID]
			if !ok {
				continue
			}
			e.Private = append(e.Private, agentapi.FaultPrivate{
				VLAN: n.VLAN, IP: m.IP, AliasIPs: slices.Clone(m.AliasIPs),
			})
			entries[m.ServerID] = e
		}
	}
	return entries
}

// locationServers returns the entries of the project's servers in from and
// to, sorted by ID; ok is false when the project is gone.
func locationServers(tx store.Tx, project, from, to string, entries map[int64]agentapi.FaultServer) (f, t []agentapi.FaultServer, ok bool, err error) {
	p, err := store.ProjectByName(tx, project)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	recs, err := store.List[servers.Record](tx, servers.Kind, p.ID)
	if err != nil {
		return nil, nil, false, err
	}
	slices.SortFunc(recs, func(a, b servers.Record) int { return cmp.Compare(a.ID, b.ID) })
	for _, r := range recs {
		e, inDoc := entries[r.ID]
		if !inDoc {
			continue
		}
		switch r.Hlab.Location {
		case from:
			f = append(f, e)
		case to:
			t = append(t, e)
		}
	}
	return f, t, true, nil
}

// boxIP returns the address of the project's storage box id; ok is false
// when the project or the box is gone.
func boxIP(tx store.Tx, project string, id int64) (netip.Addr, bool, error) {
	p, err := store.ProjectByName(tx, project)
	if errors.Is(err, store.ErrNotFound) {
		return netip.Addr{}, false, nil
	}
	if err != nil {
		return netip.Addr{}, false, err
	}
	r, err := storageboxes.Load(tx, p.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return netip.Addr{}, false, nil
	}
	if err != nil {
		return netip.Addr{}, false, err
	}
	ip, err := netip.ParseAddr(r.HLab.IP)
	if err != nil {
		return netip.Addr{}, false, nil
	}
	return ip, true, nil
}

// paramInt returns the integer parameter name of a fault, 0 without one.
func paramInt(params map[string]any, name string) int {
	switch n := params[name].(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}
