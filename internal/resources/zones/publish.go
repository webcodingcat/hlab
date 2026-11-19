package zones

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/dnszone"
	"github.com/webcodingcat/hlab/internal/store"
)

// publish runs action a as the publishing of all zones: the agent push,
// then the NRPT rules for all zone names. A failure ends the action error.
func (m *Module) publish(acts *actions.Manager, a *actions.Action) {
	acts.Run(a.ID, func(ctx context.Context, _ func(int)) error {
		if err := m.d.PushAgent(ctx); err != nil {
			return err
		}
		var names []string
		err := m.d.Store.Tx(func(tx store.Tx) error {
			var err error
			names, err = Names(tx)
			return err
		})
		if err != nil {
			return err
		}
		return m.d.SetZones(ctx, names)
	})
}

// Contribute implements resource.NetContributor: every primary zone with
// its RRSets, each TTL resolved against the zone's TTL.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	slices.SortFunc(recs, func(a, b Record) int { return cmp.Compare(a.Name, b.Name) })
	for _, r := range recs {
		if r.Mode != ModePrimary {
			continue
		}
		z := agentapi.Zone{Name: r.Name, TTL: r.TTL}
		for _, s := range r.RRSets {
			ttl := r.TTL
			if s.TTL != nil {
				ttl = *s.TTL
			}
			values := make([]string, len(s.Records))
			for i, rec := range s.Records {
				values[i] = rec.Value
			}
			z.RRSets = append(z.RRSets, agentapi.RRSet{Name: s.Name, Type: s.Type, TTL: ttl, Records: values})
		}
		doc.Zones = append(doc.Zones, z)
	}
	return nil
}

// bumpSerial sets the serial of the apex SOA of r to the next one for now.
func bumpSerial(r *Record, now time.Time) {
	i := slices.IndexFunc(r.RRSets, func(s RRSet) bool { return s.Name == "@" && s.Type == "SOA" })
	if i < 0 || len(r.RRSets[i].Records) == 0 {
		return
	}
	f := strings.Fields(r.RRSets[i].Records[0].Value)
	if len(f) < 3 {
		return
	}
	old, _ := strconv.ParseUint(f[2], 10, 32)
	f[2] = strconv.FormatUint(uint64(dnszone.NextSerial(uint32(old), now)), 10) //nolint:gosec // parsed with 32 bits
	r.RRSets[i].Records[0].Value = strings.Join(f, " ")
}
