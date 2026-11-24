//go:build windows

package cloud

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/volumes"
	"github.com/webcodingcat/hlab/internal/store"
)

// testFaults runs the tests of the fault target lookup of the cloud.
func testFaults(t *testing.T) {
	t.Run("FAULT-10", func(t *testing.T) {
		t.Run("unattached_volume", func(t *testing.T) {
			c, _ := reloadCloud(t, "info", "[]")
			st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			c.st = st

			var serverID, attachedID, unattachedID int64
			created := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
			err = st.Tx(func(tx store.Tx) error {
				p, err := store.CreateProject(tx, "p", "p")
				if err != nil {
					return err
				}
				if serverID, err = store.NextID(tx, servers.Kind); err != nil {
					return err
				}
				srv := servers.Record{
					Server: servers.Server{ID: serverID, Name: "slow", Status: "running", Created: created},
					Hlab:   servers.Internal{ProjectID: p.ID},
				}
				if err := putServer(tx, srv); err != nil {
					return err
				}
				putVolume := func(name string, server *int64) (int64, error) {
					id, err := store.NextID(tx, volumes.Kind)
					if err != nil {
						return 0, err
					}
					v := volumes.Record{
						Volume: volumes.Volume{
							ID: id, Created: created, Name: name, Server: server, Location: []byte(`{"name":"fsn1"}`),
							Size: 10, Status: "available", Labels: map[string]string{},
						},
						Hlab: volumes.Internal{ProjectID: p.ID, Location: "fsn1"},
					}
					return id, store.Put(tx, store.Meta{
						Kind: volumes.Kind, ID: id, ProjectID: p.ID, Name: name, Labels: v.Labels, Created: created,
					}, v)
				}
				if attachedID, err = putVolume("attached", &serverID); err != nil {
					return err
				}
				unattachedID, err = putVolume("unattached", nil)
				return err
			})
			if err != nil {
				t.Fatalf("store a server and two volumes: %v", err)
			}

			ctx := context.Background()
			err = c.lookupTarget(ctx, "p", faults.KindDiskSlow, faults.Target{Type: faults.TargetVolume, ID: unattachedID})
			var ie *faults.InputError
			if !errors.As(err, &ie) {
				t.Errorf("disk_slow on the unattached volume: %v (%T), want *faults.InputError", err, err)
			}
			for _, tg := range []faults.Target{
				{Type: faults.TargetVolume, ID: attachedID},
				{Type: faults.TargetServer, ID: serverID},
			} {
				if err := c.lookupTarget(ctx, "p", faults.KindDiskSlow, tg); err != nil {
					t.Errorf("disk_slow on %s %d: %v, want nil", tg.Type, tg.ID, err)
				}
			}
		})
	})
}
