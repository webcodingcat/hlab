//go:build windows

package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
	"github.com/webcodingcat/hlab/internal/resources/volumes"
	"github.com/webcodingcat/hlab/internal/store"
)

// newFaults sets a new fault manager for this cloud run; resources are
// the first path segments of the router's modules.
func (c *Cloud) newFaults(resources []string) {
	c.faults.Store(faults.New(faults.Options{Lookup: c.lookupTarget, Resources: resources}))
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

// lookupTarget checks that t exists in project: servers, volumes and
// storage boxes in the store (a volume_unavailable volume must be
// attached), locations in the catalog.
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
			if err == nil && kind == faults.KindVolumeUnavailable && v.Server == nil {
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
