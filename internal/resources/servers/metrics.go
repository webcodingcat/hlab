package servers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/metrics"
	"github.com/webcodingcat/hlab/internal/store"
)

// getMetrics answers GET /servers/{id}/metrics with the cpu, disk and
// network series derived from the samples of the server's VM.
func (m *Module) getMetrics(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		_, err := store.Get[Record](tx, Kind, p.ID, id)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	q, err := metrics.ParseQuery(req.URL.Query())
	if err != nil {
		var qerr *metrics.QueryError
		if errors.As(err, &qerr) {
			api.Fail(w, &api.FieldError{Field: qerr.Field, Message: qerr.Message})
			return
		}
		api.ServerError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"metrics": m.d.Metrics.Series(id, q)})
}

// sample stores one sample of the VM of every running server. A VM that
// cannot be read (stopped meanwhile, deleted) is skipped.
func (m *Module) sample(ctx context.Context) {
	var recs []Record
	if err := m.d.Store.Tx(func(tx store.Tx) error {
		var err error
		recs, err = store.ListAll[Record](tx, Kind)
		return err
	}); err != nil {
		slog.Error("server metrics sample", "err", err)
		return
	}
	for _, r := range recs {
		if r.Status != StatusRunning {
			continue
		}
		smp, err := m.d.Platform.Metrics(ctx, m.vmName(r.ID))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Debug("server metrics sample", "id", r.ID, "err", err)
			continue
		}
		m.d.Metrics.Add(r.ID, smp)
	}
}
