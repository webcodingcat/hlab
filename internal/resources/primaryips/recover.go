package primaryips

import (
	"context"

	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Recover implements resource.Recoverer: every server whose public_net
// carries a primary IP ID without record (servers created before primary
// IPs had records) gets that record, assigned to it, with auto_delete.
// Records that exist stay as they are, so a second run changes nothing.
func (m *Module) Recover(_ context.Context) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		have := map[int64]bool{}
		for _, r := range recs {
			have[r.ID] = true
		}
		srvs, err := store.ListAll[servers.Record](tx, servers.Kind)
		if err != nil {
			return err
		}
		for _, s := range srvs {
			var missing []Record
			if v4 := s.PublicNet.IPv4; v4 != nil && v4.ID != 0 && !have[v4.ID] {
				missing = append(missing, recovered(s, v4.ID, typeIPv4, v4.IP))
			}
			if v6 := s.PublicNet.IPv6; v6 != nil && v6.ID != 0 && !have[v6.ID] {
				missing = append(missing, recovered(s, v6.ID, typeIPv6, v6.IP))
			}
			for _, r := range missing {
				if err := put(tx, r); err != nil {
					return err
				}
				have[r.ID] = true
			}
		}
		return nil
	})
}

// recovered is the record of primary IP id with ip of family typ, assigned
// to server s with auto_delete.
func recovered(s servers.Record, id int64, typ, ip string) Record {
	var r Record
	sid := s.ID
	r.PrimaryIP = PrimaryIP{
		ID:           id,
		Name:         defaultName(id),
		Labels:       map[string]string{},
		Created:      s.Created,
		Location:     s.Location,
		IP:           ip,
		DNSPtr:       []DNSPtr{},
		Type:         typ,
		AutoDelete:   true,
		AssigneeType: "server",
		AssigneeID:   &sid,
	}
	r.Hlab.ProjectID = s.Hlab.ProjectID
	r.Hlab.Location = s.Hlab.Location
	return r
}
