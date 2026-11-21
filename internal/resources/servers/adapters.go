package servers

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"

	"github.com/webcodingcat/hlab/internal/cidata"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// SyncAdapters makes the VM's private adapters match the record of server
// id (hot add on the network's VLAN, hot remove), sets the deny lists of
// every member of each touched network to the MACs of the other members,
// rewrites the seed (eject slot 0, write, insert) and pushes the agent
// document. A server without VM only gets its seed rewritten.
func (m *Module) SyncAdapters(ctx context.Context, stor *store.Store, id int64, touched []int64) error {
	rec, all, err := loadWithAll(stor, id)
	if err != nil {
		return err
	}
	p, name := m.d.Platform, m.vmName(id)
	hasVM, err := m.hasVM(ctx, id)
	if err != nil {
		return err
	}
	if hasVM {
		want := map[string]bool{}
		for _, pn := range rec.PrivateNet {
			want[macKey(pn.MACAddress)] = true
		}
		have := map[string]bool{}
		for _, mac := range rec.Hlab.Adapters {
			have[macKey(mac)] = true
			if want[macKey(mac)] {
				continue
			}
			if err := p.DetachNIC(ctx, name, mac); err != nil && !isNotFound(err) {
				return err
			}
		}
		if err := stor.Tx(func(tx store.Tx) error {
			return Update(tx, id, func(r *Record) error {
				r.Hlab.Adapters = slices.DeleteFunc(r.Hlab.Adapters, func(mac string) bool { return !want[macKey(mac)] })
				return nil
			})
		}); err != nil {
			return err
		}
		for _, nic := range privateNICs(rec, all) {
			if have[macKey(nic.MAC.String())] {
				continue
			}
			if err := p.AttachNIC(ctx, name, nic); err != nil {
				return err
			}
			if err := stor.Tx(func(tx store.Tx) error {
				return Update(tx, id, func(r *Record) error {
					r.Hlab.Adapters = append(r.Hlab.Adapters, nic.MAC.String())
					return nil
				})
			}); err != nil {
				return err
			}
		}
	}
	if err := m.syncDeny(ctx, all, touched); err != nil {
		return err
	}
	if err := m.rewriteSeed(ctx, rec, hasVM); err != nil {
		return err
	}
	return m.d.PushAgent(ctx)
}

// RewriteSeed rebuilds the seed of server id from its stored record
// without touching adapters or the agent, for a Recover before the agent
// is ready.
func (m *Module) RewriteSeed(ctx context.Context, stor *store.Store, id int64) error {
	var rec Record
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		rec, err = Load(tx, id)
		return err
	}); err != nil {
		return err
	}
	hasVM, err := m.hasVM(ctx, id)
	if err != nil {
		return err
	}
	return m.rewriteSeed(ctx, rec, hasVM)
}

// hasVM reports whether the VM of server id exists.
func (m *Module) hasVM(ctx context.Context, id int64) (bool, error) {
	if _, err := m.d.Platform.State(ctx, m.vmName(id)); err != nil {
		if !isNotFound(err) {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// loadWithAll returns server id and all server records.
func loadWithAll(stor *store.Store, id int64) (Record, []Record, error) {
	var rec Record
	var all []Record
	err := stor.Tx(func(tx store.Tx) error {
		var err error
		if all, err = store.ListAll[Record](tx, Kind); err != nil {
			return err
		}
		rec, err = Load(tx, id)
		return err
	})
	return rec, all, err
}

// macKey is the canonical form of a MAC for comparisons.
func macKey(mac string) string {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return mac
	}
	return hw.String()
}

// members returns per server not being deleted its adapter MAC in
// network netID.
func members(all []Record, netID int64) map[int64]net.HardwareAddr {
	out := map[int64]net.HardwareAddr{}
	for _, r := range all {
		if r.Status == StatusDeleting {
			continue
		}
		for _, pn := range r.PrivateNet {
			if pn.Network != netID {
				continue
			}
			if hw, err := net.ParseMAC(pn.MACAddress); err == nil {
				out[r.ID] = hw
			}
		}
	}
	return out
}

// denyFor returns the MACs of the members other than self, sorted.
func denyFor(mem map[int64]net.HardwareAddr, self int64) []net.HardwareAddr {
	var out []net.HardwareAddr
	for sid, mac := range mem {
		if sid != self {
			out = append(out, mac)
		}
	}
	slices.SortFunc(out, func(a, b net.HardwareAddr) int { return slices.Compare(a, b) })
	return out
}

// privateNICs are the private adapters of rec, each on its network's VLAN
// with the MACs of the network's other members as deny list.
func privateNICs(rec Record, all []Record) []platform.NICSpec {
	var out []platform.NICSpec
	for _, pn := range rec.PrivateNet {
		mac, err := net.ParseMAC(pn.MACAddress)
		if err != nil {
			continue
		}
		out = append(out, platform.NICSpec{
			MAC:      mac,
			Segment:  platform.Segment{VLAN: rec.Hlab.Networks[pn.Network].VLAN},
			DenyMACs: denyFor(members(all, pn.Network), rec.ID),
		})
	}
	return out
}

// syncDeny sets the deny list of every member of the networks nets to the
// MACs of the other members; a member without VM or adapter is skipped.
func (m *Module) syncDeny(ctx context.Context, all []Record, nets []int64) error {
	for _, n := range nets {
		mem := members(all, n)
		for sid, mac := range mem {
			err := m.d.Platform.SetNICDeny(ctx, m.vmName(sid), mac.String(), denyFor(mem, sid))
			if err != nil && !isNotFound(err) {
				return err
			}
		}
	}
	return nil
}

// rewriteSeed builds the seed of rec again. With a VM it ejects slot 0
// first and inserts the new seed afterwards, unless rescue holds slot 0
// (ending rescue inserts the server's seed).
func (m *Module) rewriteSeed(ctx context.Context, rec Record, hasVM bool) error {
	p, name, seed := m.d.Platform, m.vmName(rec.ID), m.seedPath(rec.ID)
	insert := hasVM && !rescueOn(rec)
	if insert {
		if err := p.SetDVD(ctx, name, 0, ""); err != nil {
			return err
		}
	}
	if err := os.Remove(seed); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(seed), 0o750); err != nil {
		return err
	}
	if err := cidata.BuildFile(m.seed(rec), seed); err != nil {
		return err
	}
	if insert {
		return p.SetDVD(ctx, name, 0, seed)
	}
	return nil
}
