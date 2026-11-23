package storageboxes

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/store"
	"github.com/webcodingcat/hlab/internal/transit"
)

// Boxes returns the boxes of all projects as the storage VM's document
// holds them, ordered by ID; boxes marked deleting are left out.
func Boxes(tx store.Tx) ([]agentapi.Box, error) {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return nil, err
	}
	subs, err := subsByBox(tx)
	if err != nil {
		return nil, err
	}
	snaps, err := snapsByBox(tx)
	if err != nil {
		return nil, err
	}
	out := []agentapi.Box{}
	for _, r := range recs {
		if r.HLab.Deleting {
			continue
		}
		b, err := docBox(r, subs[r.ID], snaps[r.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// subsByBox returns the subaccounts of all projects by box ID, each list
// ordered by ID.
func subsByBox(tx store.Tx) (map[int64][]SubaccountRecord, error) {
	all, err := store.ListAll[SubaccountRecord](tx, SubaccountKind)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(all, func(a, b SubaccountRecord) int { return cmp.Compare(a.ID, b.ID) })
	by := map[int64][]SubaccountRecord{}
	for _, s := range all {
		by[s.StorageBox] = append(by[s.StorageBox], s)
	}
	return by, nil
}

// docBox is stored box r with its subaccounts subs and snapshots snaps in
// the document's shape; every snapshot record is listed, one still
// creating included.
func docBox(r Record, subs []SubaccountRecord, snaps []SnapshotRecord) (agentapi.Box, error) {
	ip, err := netip.ParseAddr(r.HLab.IP)
	if err != nil {
		return agentapi.Box{}, fmt.Errorf("storage box %d: ip: %w", r.ID, err)
	}
	var t struct {
		Size int64 `json:"size"`
	}
	if err := json.Unmarshal(r.StorageBoxType, &t); err != nil {
		return agentapi.Box{}, fmt.Errorf("storage box %d: type: %w", r.ID, err)
	}
	a := r.AccessSettings
	b := agentapi.Box{
		ID:         r.ID,
		User:       userName(r.HLab.N),
		IP:         ip,
		QuotaBytes: t.Size,
		Hashes:     agentapi.Hashes{Crypt: r.HLab.Hashes.Crypt, NT: r.HLab.Hashes.NT},
		SSHKeys:    keyLines(r.HLab.SSHKeys),
		Access: agentapi.Access{SSH: a.SSHEnabled, Samba: a.SambaEnabled, WebDAV: a.WebDAVEnabled,
			ZFS: a.ZFSEnabled, ReachableExternally: a.ReachableExternally},
	}
	for _, s := range snaps {
		b.Snapshots = append(b.Snapshots, s.Name)
	}
	for _, s := range subs {
		sa := s.AccessSettings
		b.Subaccounts = append(b.Subaccounts, agentapi.Subaccount{
			User:    s.Username,
			HomeDir: s.HomeDirectory,
			Hashes:  agentapi.Hashes{Crypt: s.HLab.Hashes.Crypt, NT: s.HLab.Hashes.NT},
			Access: agentapi.Access{SSH: sa.SSHEnabled, Samba: sa.SambaEnabled, WebDAV: sa.WebDAVEnabled,
				ReadOnly: sa.Readonly, ReachableExternally: sa.ReachableExternally},
		})
	}
	return b, nil
}

// keyLines returns keys as authorized_keys lines: trimmed, and without a
// key that still spans lines, which the storage agent would refuse for the
// whole document. Such keys come only from boxes stored before create
// trimmed them.
func keyLines(keys []string) []string {
	var out []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" && !strings.ContainsAny(k, "\r\n") {
			out = append(out, k)
		}
	}
	return out
}

// Contribute implements resource.NetContributor: the storage VM when any
// box exists and a route per box with its account names and whether the
// box or any of its subaccounts is reachable externally. Boxes marked
// deleting are left out, as in the storage VM's document.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	all, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	recs := slices.DeleteFunc(all, func(r Record) bool { return r.HLab.Deleting })
	if len(recs) == 0 {
		return nil
	}
	subs, err := subsByBox(tx)
	if err != nil {
		return err
	}
	doc.Storage = agentapi.StorageVM{Addr: transit.For(m.d.PublicRange).Storage, MAC: StorageMAC}
	for _, r := range recs {
		ip, err := netip.ParseAddr(r.HLab.IP)
		if err != nil {
			return fmt.Errorf("storage box %d: ip: %w", r.ID, err)
		}
		route := agentapi.StorageBoxRoute{
			IP:                  ip,
			Names:               []string{userName(r.HLab.N)},
			ReachableExternally: r.AccessSettings.ReachableExternally,
		}
		for _, s := range subs[r.ID] {
			route.Names = append(route.Names, s.Username)
			route.ReachableExternally = route.ReachableExternally || s.AccessSettings.ReachableExternally
		}
		doc.StorageBoxes = append(doc.StorageBoxes, route)
	}
	return nil
}
