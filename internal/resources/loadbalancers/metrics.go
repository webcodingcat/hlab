package loadbalancers

import (
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// Point limits of Hetzner's metrics endpoint, as for servers.
const (
	defaultPoints = 200
	maxPoints     = 500
)

// seriesNames are the series each metric type answers.
var seriesNames = map[string][]string{
	"open_connections":       {"open_connections"},
	"connections_per_second": {"connections_per_second"},
	"requests_per_second":    {"requests_per_second"},
	"bandwidth":              {"bandwidth.in", "bandwidth.out"},
}

// metricsQuery is a parsed metrics request.
type metricsQuery struct {
	types      []string
	start, end time.Time
	step       int // seconds; 0 = automatic
}

// timeSeries is one series: pairs of [unix seconds, "value"].
type timeSeries struct {
	Values [][2]any `json:"values"`
}

// metricsResult is Hetzner's metrics object.
type metricsResult struct {
	Start      time.Time             `json:"start"`
	End        time.Time             `json:"end"`
	Step       float64               `json:"step"`
	TimeSeries map[string]timeSeries `json:"time_series"`
}

// getMetrics answers GET /load_balancers/{id}/metrics from the counter
// samples of the status job.
func (m *Module) getMetrics(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		_, err := Load(tx, p.ID, id)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	q, ferr := parseMetricsQuery(req.URL.Query())
	if ferr != nil {
		api.Fail(w, ferr)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"metrics": series(m.samplesOf(id), q)})
}

// parseMetricsQuery reads type (repeated or comma separated), start, end
// (RFC 3339) and step (seconds, optional).
func parseMetricsQuery(v url.Values) (metricsQuery, *api.FieldError) {
	var q metricsQuery
	for _, raw := range v["type"] {
		for t := range strings.SplitSeq(raw, ",") {
			t = strings.TrimSpace(t)
			if _, ok := seriesNames[t]; !ok {
				return q, &api.FieldError{Field: "type", Message: "unknown metric type " + strconv.Quote(t) +
					"; allowed: open_connections, connections_per_second, requests_per_second, bandwidth"}
			}
			if !slices.Contains(q.types, t) {
				q.types = append(q.types, t)
			}
		}
	}
	if len(q.types) == 0 {
		return q, &api.FieldError{Field: "type", Message: "is required"}
	}
	var err *api.FieldError
	if q.start, err = parseTime(v, "start"); err != nil {
		return q, err
	}
	if q.end, err = parseTime(v, "end"); err != nil {
		return q, err
	}
	if q.end.Before(q.start) {
		return q, &api.FieldError{Field: "end", Message: "must not be before start"}
	}
	if raw := v.Get("step"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return q, &api.FieldError{Field: "step", Message: "must be a positive number of seconds"}
		}
		q.step = n
	}
	return q, nil
}

func parseTime(v url.Values, field string) (time.Time, *api.FieldError) {
	raw := v.Get(field)
	if raw == "" {
		return time.Time{}, &api.FieldError{Field: field, Message: "is required"}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, &api.FieldError{Field: field, Message: "must be an RFC 3339 time"}
	}
	return t, nil
}

// series answers q from samples with the step rule of the server metrics:
// at least the status interval, at most 200 points without a requested
// step, and a requested step giving more than 500 points is raised. Each
// point is the average of the values within its step; rates come from
// counter deltas between neighbouring samples, a decrease being a reset.
func series(list []sample, q metricsQuery) metricsResult {
	span := q.end.Sub(q.start).Seconds()
	step := q.step
	if step == 0 {
		step = minStep(span, defaultPoints)
	} else if math.Floor(span/float64(step))+1 > maxPoints {
		step = minStep(span, maxPoints)
	}
	step = max(step, int(statusInterval/time.Second))

	type value struct {
		at   time.Time
		vals map[string]float64
	}
	var values []value
	for i, cur := range list {
		if cur.at.Before(q.start) || cur.at.After(q.end) {
			continue
		}
		vals := map[string]float64{"open_connections": float64(cur.c.OpenConnections)}
		if i > 0 {
			prev := list[i-1]
			if dt := cur.at.Sub(prev.at).Seconds(); dt > 0 {
				rate := func(c, p uint64) float64 {
					if c < p { // counter reset: restart the delta at zero
						return float64(c) / dt
					}
					return float64(c-p) / dt
				}
				vals["connections_per_second"] = rate(cur.c.ConnectionsTotal, prev.c.ConnectionsTotal)
				vals["requests_per_second"] = rate(cur.c.RequestsTotal, prev.c.RequestsTotal)
				vals["bandwidth.in"] = rate(cur.c.BytesIn, prev.c.BytesIn)
				vals["bandwidth.out"] = rate(cur.c.BytesOut, prev.c.BytesOut)
			}
		}
		values = append(values, value{cur.at, vals})
	}

	res := metricsResult{Start: q.start, End: q.end, Step: float64(step), TimeSeries: map[string]timeSeries{}}
	stepDur := time.Duration(step) * time.Second
	for _, typ := range q.types {
		for _, name := range seriesNames[typ] {
			points := [][2]any{}
			bucket, sum, n := int64(-1), 0.0, 0
			flush := func() {
				if n > 0 {
					ts := q.start.Add(time.Duration(bucket) * stepDur)
					points = append(points, [2]any{float64(ts.UnixMilli()) / 1000, strconv.FormatFloat(sum/float64(n), 'f', -1, 64)})
				}
			}
			for _, v := range values {
				x, ok := v.vals[name]
				if !ok {
					continue
				}
				b := int64(v.at.Sub(q.start) / stepDur)
				if b != bucket {
					flush()
					bucket, sum, n = b, 0, 0
				}
				sum += x
				n++
			}
			flush()
			res.TimeSeries[name] = timeSeries{Values: points}
		}
	}
	return res
}

// minStep is the smallest step in whole seconds giving at most limit points
// over span seconds (both ends included).
func minStep(span float64, limit int) int {
	return int(math.Floor(span/float64(limit))) + 1
}
