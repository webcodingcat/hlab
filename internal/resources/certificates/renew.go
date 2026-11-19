package certificates

import (
	"context"
	"log/slog"
	"time"

	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// renewBefore is how long before expiry a managed certificate is renewed.
const renewBefore = 30 * 24 * time.Hour

// Jobs implements resource.Scheduled: the hourly renewal of managed
// certificates.
func (m *Module) Jobs() []scheduler.Job {
	return []scheduler.Job{{Name: "certificate renewal", Every: time.Hour, Run: m.renew}}
}

// renew issues a new leaf for every managed, issued certificate with less
// than renewBefore left. Renewal goes pending, then scheduled or failed;
// a failed renewal keeps the old leaf and is tried again on the next run.
func (m *Module) renew(ctx context.Context) {
	now := m.clock().Now()
	var due []Record
	err := m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.Type != TypeManaged || r.Status.Issuance != IssuanceCompleted ||
				r.NotValidAfter == nil || r.NotValidAfter.Sub(now) >= renewBefore {
				continue
			}
			r.Status.Renewal = RenewalPending
			r.Status.Error = nil
			if err := put(tx, r); err != nil {
				return err
			}
			due = append(due, r)
		}
		return nil
	})
	if err != nil {
		slog.Error("certificate renewal", "err", err)
		return
	}
	for _, r := range due {
		if ctx.Err() != nil {
			return
		}
		if err := m.issue(ctx, m.d.Store, r.Hlab.ProjectID, r.ID, true); err != nil {
			slog.Warn("certificate renewal failed", "certificate", r.ID, "err", err)
		}
	}
}

func (m *Module) clock() scheduler.Clock {
	if m.d.Clock == nil {
		return scheduler.RealClock()
	}
	return m.d.Clock
}
