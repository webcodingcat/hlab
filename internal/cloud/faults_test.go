//go:build windows

package cloud

import (
	"cmp"
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
	"github.com/webcodingcat/hlab/internal/resources/volumes"
	"github.com/webcodingcat/hlab/internal/store"
)

// testFaults runs the tests of the fault target lookup of the cloud and of
// the net fault entries of the agent document.
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

	t.Run("FAULT-07", func(t *testing.T) {
		t.Run("agent_document", func(t *testing.T) {
			fx := newNetFixture(t)
			a := fx.addServer(t, "p", "a", "fsn1", "198.19.1.10")
			b := fx.addServer(t, "p", "b", "fsn1", "198.19.1.11")
			n := fx.addServer(t, "p", "no-public", "fsn1", "")
			fx.addNetwork(10,
				agentapi.Member{ServerID: a, IP: addr("10.0.0.2"), AliasIPs: []netip.Addr{addr("10.0.0.5"), addr("10.0.0.6")}},
				agentapi.Member{ServerID: b, IP: addr("10.0.0.3")},
				agentapi.Member{ServerID: n, IP: addr("10.0.0.4")})
			fx.addNetwork(11, agentapi.Member{ServerID: a, IP: addr("10.1.0.2")})

			loss := fx.create(t, "p", faults.Spec{
				Kind: faults.KindNetwork, Target: &faults.Target{Type: faults.TargetServer, ID: a},
				Params: map[string]any{"traffic": "private", "loss_percent": 30}, Seed: ptr(uint64(42)),
			})
			cut := fx.create(t, "p", faults.Spec{
				Kind: faults.KindNetwork, Target: &faults.Target{Type: faults.TargetServer, ID: n},
				Params: map[string]any{"traffic": "all", "loss_percent": 100},
			})
			gone := fx.create(t, "p", faults.Spec{
				Kind: faults.KindNetwork, Target: &faults.Target{Type: faults.TargetServer, ID: 999999},
				Params: map[string]any{"traffic": "public", "loss_percent": 100},
			})

			want := []agentapi.NetFault{
				{
					ID: loss.ID, Kind: faults.KindNetwork, Traffic: "private", LossPercent: 30, Seed: 42,
					Server: &agentapi.FaultServer{ID: a, PublicIPv4: addr("198.19.1.10"), Private: []agentapi.FaultPrivate{
						{VLAN: 10, IP: addr("10.0.0.2"), AliasIPs: []netip.Addr{addr("10.0.0.5"), addr("10.0.0.6")}},
						{VLAN: 11, IP: addr("10.1.0.2")},
					}},
				},
				{
					ID: cut.ID, Kind: faults.KindNetwork, Traffic: "all", LossPercent: 100,
					Server: &agentapi.FaultServer{ID: n, Private: []agentapi.FaultPrivate{{VLAN: 10, IP: addr("10.0.0.4")}}},
				},
			}
			checkNetFaults(t, fx.netFaults(t, loss, cut, gone), want)
		})
	})

	t.Run("FAULT-08", func(t *testing.T) {
		t.Run("agent_document", func(t *testing.T) {
			fx := newNetFixture(t)
			a := fx.addServer(t, "p", "a", "fsn1", "198.19.1.10")
			b := fx.addServer(t, "p", "b", "fsn1", "198.19.1.11")
			c := fx.addServer(t, "p", "c", "hel1", "198.19.1.12")
			fx.addServer(t, "q", "other-project", "fsn1", "198.19.1.13")
			fx.addServer(t, "p", "elsewhere", "nbg1", "198.19.1.14")
			fx.addNetwork(10,
				agentapi.Member{ServerID: a, IP: addr("10.0.0.2")},
				agentapi.Member{ServerID: c, IP: addr("10.0.0.3")})

			between := fx.create(t, "p", faults.Spec{
				Kind: faults.KindLatency, Target: &faults.Target{Type: faults.TargetLocations, From: "fsn1", To: "hel1"},
				Params: map[string]any{"delay_ms": 100, "jitter_ms": 20},
			})
			onServer := fx.create(t, "p", faults.Spec{
				Kind: faults.KindLatency, Target: &faults.Target{Type: faults.TargetServer, ID: c},
				Params: map[string]any{"delay_ms": 50, "jitter_ms": 5},
			})

			entryA := agentapi.FaultServer{ID: a, PublicIPv4: addr("198.19.1.10"), Private: []agentapi.FaultPrivate{{VLAN: 10, IP: addr("10.0.0.2")}}}
			entryB := agentapi.FaultServer{ID: b, PublicIPv4: addr("198.19.1.11")}
			entryC := agentapi.FaultServer{ID: c, PublicIPv4: addr("198.19.1.12"), Private: []agentapi.FaultPrivate{{VLAN: 10, IP: addr("10.0.0.3")}}}
			want := []agentapi.NetFault{
				{ID: between.ID, Kind: faults.KindLatency, From: []agentapi.FaultServer{entryA, entryB}, To: []agentapi.FaultServer{entryC}, DelayMs: 100, JitterMs: 20},
				{ID: onServer.ID, Kind: faults.KindLatency, Server: &entryC, DelayMs: 50, JitterMs: 5},
			}
			checkNetFaults(t, fx.netFaults(t, between, onServer), want)

			d := fx.addServer(t, "p", "d", "hel1", "198.19.1.15")
			want[0].To = append(want[0].To, agentapi.FaultServer{ID: d, PublicIPv4: addr("198.19.1.15")})
			checkNetFaults(t, fx.netFaults(t, between, onServer), want)
		})
	})

	t.Run("FAULT-11", func(t *testing.T) {
		t.Run("agent_document", func(t *testing.T) {
			fx := newNetFixture(t)
			box := fx.addBox(t, "p", "198.19.3.3")
			f := fx.create(t, "p", faults.Spec{
				Kind: faults.KindStorageBoxUnavailable, Target: &faults.Target{Type: faults.TargetStorageBox, ID: box},
				Params: map[string]any{},
			})
			want := []agentapi.NetFault{{ID: f.ID, Kind: faults.KindStorageBoxUnavailable, BoxIP: addr("198.19.3.3")}}
			checkNetFaults(t, fx.netFaults(t, f), want)
		})
	})
}

// netFixture is a store with projects "p" and "q", the agent document the
// modules would contribute for it, and a fault manager whose net kinds are
// active through a NetEffects whose push does nothing.
type netFixture struct {
	st       *store.Store
	projects map[string]int64
	doc      agentapi.Config
	m        *faults.Manager
}

func newNetFixture(t *testing.T) *netFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fx := &netFixture{st: st, projects: map[string]int64{}}
	err = st.Tx(func(tx store.Tx) error {
		for _, name := range []string{"p", "q"} {
			p, err := store.CreateProject(tx, name, name)
			if err != nil {
				return err
			}
			fx.projects[name] = p.ID
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create projects: %v", err)
	}
	net := faults.NewNetEffects(func(context.Context) error { return nil })
	fx.m = faults.New(faults.Options{Effects: net.Effects()})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = fx.m.EndAll(ctx)
	})
	return fx
}

// netCreated is the creation time of the fixture's records.
var netCreated = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

// addServer stores a server of project in location and adds its public side
// to the document; public "" is a server without a public IPv4.
func (fx *netFixture) addServer(t *testing.T, project, name, location, public string) int64 {
	t.Helper()
	var id int64
	err := fx.st.Tx(func(tx store.Tx) error {
		var err error
		if id, err = store.NextID(tx, servers.Kind); err != nil {
			return err
		}
		return putServer(tx, servers.Record{
			Server: servers.Server{
				ID: id, Name: name, Status: "running", Created: netCreated,
				Location: []byte(`{"name":"` + location + `"}`),
			},
			Hlab: servers.Internal{ProjectID: fx.projects[project], Location: location},
		})
	})
	if err != nil {
		t.Fatalf("store server %s: %v", name, err)
	}
	var ip netip.Addr
	if public != "" {
		ip = addr(public)
	}
	fx.doc.Servers = append(fx.doc.Servers, agentapi.Server{ID: id, Location: location, PublicIPv4: ip})
	return id
}

// addNetwork adds a private network with its members to the document.
func (fx *netFixture) addNetwork(vlan int, members ...agentapi.Member) {
	fx.doc.Networks = append(fx.doc.Networks, agentapi.Network{ID: int64(len(fx.doc.Networks) + 1), VLAN: vlan, Members: members})
}

// addBox stores an active storage box of project with address ip.
func (fx *netFixture) addBox(t *testing.T, project, ip string) int64 {
	t.Helper()
	var id int64
	err := fx.st.Tx(func(tx store.Tx) error {
		var err error
		if id, err = store.NextID(tx, storageboxes.Kind); err != nil {
			return err
		}
		r := storageboxes.Record{
			Box: storageboxes.Box{
				ID: id, Name: "box", Location: []byte(`{"name":"fsn1"}`), Labels: map[string]string{},
				Status: storageboxes.StatusActive, Created: netCreated,
			},
			HLab: storageboxes.HLab{IP: ip},
		}
		return store.Put(tx, store.Meta{
			Kind: storageboxes.Kind, ID: id, ProjectID: fx.projects[project], Name: r.Name, Labels: r.Labels, Created: netCreated,
		}, r)
	})
	if err != nil {
		t.Fatalf("store storage box: %v", err)
	}
	return id
}

// create creates a fault through the manager, so it has the manager's shape.
func (fx *netFixture) create(t *testing.T, project string, s faults.Spec) faults.Fault {
	t.Helper()
	f, err := fx.m.Create(t.Context(), project, s)
	if err != nil {
		t.Fatalf("Create(%s, %s): %v", project, s.Kind, err)
	}
	return f
}

// netFaults runs netFaults on a copy of the fixture's document in a store
// transaction.
func (fx *netFixture) netFaults(t *testing.T, fs ...faults.Fault) []agentapi.NetFault {
	t.Helper()
	var got []agentapi.NetFault
	err := fx.st.Tx(func(tx store.Tx) error {
		doc := fx.doc
		var err error
		got, err = netFaults(tx, &doc, fs)
		return err
	})
	if err != nil {
		t.Fatalf("netFaults: %v", err)
	}
	return got
}

// checkNetFaults compares entries regardless of the order of entries,
// servers, private networks and alias IPs, and of nil against empty lists.
func checkNetFaults(t *testing.T, got, want []agentapi.NetFault) {
	t.Helper()
	g, w := normNetFaults(got), normNetFaults(want)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("netFaults\n got %+v\nwant %+v", g, w)
	}
}

func normNetFaults(in []agentapi.NetFault) []agentapi.NetFault {
	out := make([]agentapi.NetFault, 0, len(in))
	for _, f := range in {
		if f.Server != nil {
			s := normFaultServer(*f.Server)
			f.Server = &s
		}
		f.From, f.To = normFaultServers(f.From), normFaultServers(f.To)
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b agentapi.NetFault) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func normFaultServers(in []agentapi.FaultServer) []agentapi.FaultServer {
	if len(in) == 0 {
		return nil
	}
	out := make([]agentapi.FaultServer, 0, len(in))
	for _, s := range in {
		out = append(out, normFaultServer(s))
	}
	slices.SortFunc(out, func(a, b agentapi.FaultServer) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func normFaultServer(s agentapi.FaultServer) agentapi.FaultServer {
	if len(s.Private) == 0 {
		s.Private = nil
		return s
	}
	ps := make([]agentapi.FaultPrivate, 0, len(s.Private))
	for _, p := range s.Private {
		if len(p.AliasIPs) == 0 {
			p.AliasIPs = nil
		} else {
			p.AliasIPs = slices.Clone(p.AliasIPs)
			slices.SortFunc(p.AliasIPs, netip.Addr.Compare)
		}
		ps = append(ps, p)
	}
	slices.SortFunc(ps, func(a, b agentapi.FaultPrivate) int { return cmp.Compare(a.VLAN, b.VLAN) })
	s.Private = ps
	return s
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func ptr[T any](v T) *T { return &v }
