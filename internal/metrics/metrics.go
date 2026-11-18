// Package metrics keeps the samples of each server in memory and derives
// Hetzner's metrics series (cpu, disk, network) from them.
package metrics

import (
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// Interval is the sampling interval of hlabd and the smallest step.
const Interval = 10 * time.Second

// Point limits of Hetzner's metrics endpoint.
const (
	defaultPoints = 200
	maxPoints     = 500
)

// seriesNames are the series each metric type answers.
var seriesNames = map[string][]string{
	"cpu":     {"cpu"},
	"disk":    {"disk.0.iops.read", "disk.0.iops.write", "disk.0.bandwidth.read", "disk.0.bandwidth.write"},
	"network": {"network.0.pps.in", "network.0.pps.out", "network.0.bandwidth.in", "network.0.bandwidth.out"},
}

// Store holds the samples of each server for the retention.
type Store struct {
	retention time.Duration
	mu        sync.Mutex
	samples   map[int64][]platform.Sample
}

// NewStore returns an empty store keeping samples for retention.
func NewStore(retention time.Duration) *Store {
	return &Store{retention: retention, samples: map[int64][]platform.Sample{}}
}

// Add appends a sample of a server and drops the samples older than the
// retention, counted back from this sample. Samples arrive in time order.
func (s *Store) Add(serverID int64, smp platform.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := append(s.samples[serverID], smp)
	cut := smp.Time.Add(-s.retention)
	i := 0
	for i < len(list) && list[i].Time.Before(cut) {
		i++
	}
	if i > 0 {
		list = slices.Clone(list[i:])
	}
	s.samples[serverID] = list
}

// Forget drops all samples of a server.
func (s *Store) Forget(serverID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.samples, serverID)
}

// Query is a parsed metrics request.
type Query struct {
	Types      []string // "cpu", "disk", "network"
	Start, End time.Time
	Step       int // seconds; 0 = automatic
}

// QueryError names the query parameter that is wrong.
type QueryError struct{ Field, Message string }

func (e *QueryError) Error() string { return e.Field + ": " + e.Message }

// ParseQuery reads type (repeated or comma separated), start, end (RFC 3339)
// and step (seconds, optional).
func ParseQuery(v url.Values) (Query, error) {
	var q Query
	for _, raw := range v["type"] {
		for t := range strings.SplitSeq(raw, ",") {
			t = strings.TrimSpace(t)
			if _, ok := seriesNames[t]; !ok {
				return Query{}, &QueryError{"type", "unknown metric type " + strconv.Quote(t) + "; allowed: cpu, disk, network"}
			}
			if !slices.Contains(q.Types, t) {
				q.Types = append(q.Types, t)
			}
		}
	}
	if len(q.Types) == 0 {
		return Query{}, &QueryError{"type", "is required"}
	}
	var err error
	if q.Start, err = parseTime(v, "start"); err != nil {
		return Query{}, err
	}
	if q.End, err = parseTime(v, "end"); err != nil {
		return Query{}, err
	}
	if q.End.Before(q.Start) {
		return Query{}, &QueryError{"end", "must not be before start"}
	}
	if raw := v.Get("step"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Query{}, &QueryError{"step", "must be a positive number of seconds"}
		}
		q.Step = n
	}
	return q, nil
}

func parseTime(v url.Values, field string) (time.Time, error) {
	raw := v.Get(field)
	if raw == "" {
		return time.Time{}, &QueryError{field, "is required"}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, &QueryError{field, "must be an RFC 3339 time"}
	}
	return t, nil
}

// TimeSeries is one series: pairs of [unix seconds, "value"].
type TimeSeries struct {
	Values [][2]any `json:"values"`
}

// Result is Hetzner's metrics object.
type Result struct {
	Start      time.Time             `json:"start"`
	End        time.Time             `json:"end"`
	Step       float64               `json:"step"`
	TimeSeries map[string]TimeSeries `json:"time_series"`
}

// Series answers the query for a server. The step is at least Interval;
// without a requested step it gives at most 200 points, and a requested
// step giving more than 500 points is raised. Each point is the average of
// the values within its step; rates come from counter deltas between
// neighbouring samples, a decrease being a counter reset.
func (s *Store) Series(serverID int64, q Query) Result {
	s.mu.Lock()
	list := slices.Clone(s.samples[serverID])
	s.mu.Unlock()

	span := q.End.Sub(q.Start).Seconds()
	step := q.Step
	if step == 0 {
		step = minStep(span, defaultPoints)
	} else if math.Floor(span/float64(step))+1 > maxPoints {
		step = minStep(span, maxPoints)
	}
	step = max(step, int(Interval/time.Second))

	// Per sample: the value of each series at its time.
	type value struct {
		at   time.Time
		vals map[string]float64
	}
	var values []value
	for i, cur := range list {
		if cur.Time.Before(q.Start) || cur.Time.After(q.End) {
			continue
		}
		vals := map[string]float64{"cpu": cur.CPUPercent}
		if i > 0 {
			prev := list[i-1]
			if dt := cur.Time.Sub(prev.Time).Seconds(); dt > 0 {
				rate := func(c, p uint64) float64 {
					if c < p { // counter reset: restart the delta at zero
						return float64(c) / dt
					}
					return float64(c-p) / dt
				}
				vals["disk.0.iops.read"] = rate(cur.DiskReadOps, prev.DiskReadOps)
				vals["disk.0.iops.write"] = rate(cur.DiskWriteOps, prev.DiskWriteOps)
				vals["disk.0.bandwidth.read"] = rate(cur.DiskReadBytes, prev.DiskReadBytes)
				vals["disk.0.bandwidth.write"] = rate(cur.DiskWriteBytes, prev.DiskWriteBytes)
				vals["network.0.pps.in"] = rate(cur.NetRxPackets, prev.NetRxPackets)
				vals["network.0.pps.out"] = rate(cur.NetTxPackets, prev.NetTxPackets)
				vals["network.0.bandwidth.in"] = rate(cur.NetRxBytes, prev.NetRxBytes)
				vals["network.0.bandwidth.out"] = rate(cur.NetTxBytes, prev.NetTxBytes)
			}
		}
		values = append(values, value{cur.Time, vals})
	}

	res := Result{Start: q.Start, End: q.End, Step: float64(step), TimeSeries: map[string]TimeSeries{}}
	stepDur := time.Duration(step) * time.Second
	for _, typ := range q.Types {
		for _, name := range seriesNames[typ] {
			points := [][2]any{}
			bucket, sum, n := int64(-1), 0.0, 0
			flush := func() {
				if n > 0 {
					ts := q.Start.Add(time.Duration(bucket) * stepDur)
					points = append(points, [2]any{float64(ts.UnixMilli()) / 1000, strconv.FormatFloat(sum/float64(n), 'f', -1, 64)})
				}
			}
			for _, v := range values {
				x, ok := v.vals[name]
				if !ok {
					continue
				}
				b := int64(v.at.Sub(q.Start) / stepDur)
				if b != bucket {
					flush()
					bucket, sum, n = b, 0, 0
				}
				sum += x
				n++
			}
			flush()
			res.TimeSeries[name] = TimeSeries{Values: points}
		}
	}
	return res
}

// minStep is the smallest step in whole seconds giving at most limit points
// over span seconds (both ends included).
func minStep(span float64, limit int) int {
	return int(math.Floor(span/float64(limit))) + 1
}
