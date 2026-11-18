package metrics_test

import (
	"errors"
	"math"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/metrics"
	"github.com/webcodingcat/hlab/internal/platform"
)

// interval is the sampling interval of hlabd.
const interval = 10 * time.Second

// perInterval is how much each counter grows per interval in feed, and the
// rate per second each series must show.
var perInterval = platform.Sample{
	DiskReadBytes: 1000, DiskWriteBytes: 2000, DiskReadOps: 50, DiskWriteOps: 30,
	NetRxBytes: 5000, NetTxBytes: 4000, NetRxPackets: 100, NetTxPackets: 70,
}

// wantRates are the values of the nine series fed by feed.
var wantRates = map[string]float64{
	"cpu":                     150,
	"disk.0.bandwidth.read":   100,
	"disk.0.bandwidth.write":  200,
	"disk.0.iops.read":        5,
	"disk.0.iops.write":       3,
	"network.0.bandwidth.in":  500,
	"network.0.bandwidth.out": 400,
	"network.0.pps.in":        10,
	"network.0.pps.out":       7,
}

// feed adds a sample every interval from start to end (inclusive) with
// counters growing by perInterval and CPU at 150 percent. At resetAt (if
// not zero) the counters restart from zero as after a VM restart.
func feed(s *metrics.Store, id int64, start, end, resetAt time.Time) {
	var n uint64 = 1000 // counters do not start at zero
	for ts := start; !ts.After(end); ts = ts.Add(interval) {
		if !resetAt.IsZero() && ts.Equal(resetAt) {
			n = 1 // one interval's growth after the restart
		}
		s.Add(id, platform.Sample{
			Time:           ts,
			CPUPercent:     150,
			DiskReadBytes:  n * perInterval.DiskReadBytes,
			DiskWriteBytes: n * perInterval.DiskWriteBytes,
			DiskReadOps:    n * perInterval.DiskReadOps,
			DiskWriteOps:   n * perInterval.DiskWriteOps,
			NetRxBytes:     n * perInterval.NetRxBytes,
			NetTxBytes:     n * perInterval.NetTxBytes,
			NetRxPackets:   n * perInterval.NetRxPackets,
			NetTxPackets:   n * perInterval.NetTxPackets,
		})
		n++
	}
}

// point is one value of a series.
type point struct {
	ts    float64
	value float64
}

// points converts a series' values, failing on a malformed pair.
func points(t *testing.T, name string, values [][2]any) []point {
	t.Helper()
	out := make([]point, 0, len(values))
	for _, v := range values {
		var ts float64
		switch x := v[0].(type) {
		case float64:
			ts = x
		case int64:
			ts = float64(x)
		case int:
			ts = float64(x)
		default:
			t.Fatalf("%s: timestamp %#v is not a number", name, v[0])
		}
		s, ok := v[1].(string)
		if !ok {
			t.Fatalf("%s: value %#v is not a string", name, v[1])
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("%s: value %q: %v", name, s, err)
		}
		out = append(out, point{ts, f})
	}
	return out
}

func TestMetrics(t *testing.T) {
	t.Run("SRV-21", func(t *testing.T) {
		start := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
		end := start.Add(2 * time.Hour)
		rfc := func(tm time.Time) string { return tm.Format(time.RFC3339) }

		// ParseQuery.
		ok := []struct {
			query string
			types []string
			step  int
		}{
			{"type=cpu,disk&start=" + url.QueryEscape(rfc(start)) + "&end=" + url.QueryEscape(rfc(end)), []string{"cpu", "disk"}, 0},
			{"type=cpu&type=network&start=" + url.QueryEscape(rfc(start)) + "&end=" + url.QueryEscape(rfc(end)) + "&step=60", []string{"cpu", "network"}, 60},
		}
		for _, c := range ok {
			v, err := url.ParseQuery(c.query)
			if err != nil {
				t.Fatal(err)
			}
			q, err := metrics.ParseQuery(v)
			if err != nil {
				t.Errorf("ParseQuery(%s): %v", c.query, err)
				continue
			}
			if !slices.Equal(q.Types, c.types) || !q.Start.Equal(start) || !q.End.Equal(end) || q.Step != c.step {
				t.Errorf("ParseQuery(%s) = %+v, want types %v, %s to %s, step %d", c.query, q, c.types, start, end, c.step)
			}
		}
		good := url.Values{"type": {"cpu"}, "start": {rfc(start)}, "end": {rfc(end)}}
		bad := []struct {
			name   string
			change func(url.Values)
			fields []string
		}{
			{"unknown type", func(v url.Values) { v.Set("type", "cpu,memory") }, []string{"type"}},
			{"missing start", func(v url.Values) { v.Del("start") }, []string{"start"}},
			{"start not RFC 3339", func(v url.Values) { v.Set("start", "2026-11-18 10:00") }, []string{"start"}},
			{"missing end", func(v url.Values) { v.Del("end") }, []string{"end"}},
			{"end not RFC 3339", func(v url.Values) { v.Set("end", "1763460000") }, []string{"end"}},
			{"end before start", func(v url.Values) { v.Set("end", rfc(start.Add(-time.Minute))) }, []string{"end", "start"}},
		}
		for _, c := range bad {
			v := url.Values{}
			for k, vs := range good {
				v[k] = slices.Clone(vs)
			}
			c.change(v)
			_, err := metrics.ParseQuery(v)
			var qe *metrics.QueryError
			if !errors.As(err, &qe) {
				t.Errorf("ParseQuery with %s: err = %v, want a *QueryError", c.name, err)
				continue
			}
			if !slices.Contains(c.fields, qe.Field) {
				t.Errorf("ParseQuery with %s: field %q, want one of %v", c.name, qe.Field, c.fields)
			}
		}

		// Series from fed samples, with a counter reset in the middle.
		s := metrics.NewStore(24 * time.Hour * 365 * 10)
		feed(s, 1, start, end, start.Add(time.Hour))
		all := []string{"cpu", "disk", "network"}

		check := func(name string, q metrics.Query, maxPoints int) metrics.Result {
			t.Helper()
			r := s.Series(1, q)
			names := make([]string, 0, len(r.TimeSeries))
			for n := range r.TimeSeries {
				names = append(names, n)
			}
			slices.Sort(names)
			want := make([]string, 0, len(wantRates))
			for n := range wantRates {
				want = append(want, n)
			}
			slices.Sort(want)
			if !slices.Equal(names, want) {
				t.Errorf("%s: series %v, want %v", name, names, want)
			}
			for n, ts := range r.TimeSeries {
				ps := points(t, n, ts.Values)
				if len(ps) == 0 || len(ps) > maxPoints {
					t.Errorf("%s: %s has %d points, want 1 to %d", name, n, len(ps), maxPoints)
				}
				for _, p := range ps {
					if math.Abs(p.value-wantRates[n]) > 0.01*wantRates[n] {
						t.Errorf("%s: %s at %v = %v, want %v", name, n, p.ts, p.value, wantRates[n])
						break
					}
				}
			}
			return r
		}

		r := check("default step", metrics.Query{Types: all, Start: start, End: end}, 200)
		if r.Step < end.Sub(start).Seconds()/200 {
			t.Errorf("default step = %v, want at least %v", r.Step, end.Sub(start).Seconds()/200)
		}
		r = check("step 10", metrics.Query{Types: all, Start: start, End: end, Step: 10}, 500)
		if r.Step < end.Sub(start).Seconds()/500 {
			t.Errorf("step 10 over 2 h = %v, want raised to at least %v", r.Step, end.Sub(start).Seconds()/500)
		}
		r = check("step 60", metrics.Query{Types: all, Start: start, End: end, Step: 60}, 500)
		if r.Step != 60 {
			t.Errorf("step 60 = %v, want 60", r.Step)
		}

		// Forget empties the server.
		s.Forget(1)
		for n, ts := range s.Series(1, metrics.Query{Types: all, Start: start, End: end}).TimeSeries {
			if len(ts.Values) != 0 {
				t.Errorf("after Forget: %s has %d points, want none", n, len(ts.Values))
			}
		}

		// Retention: samples older than it are gone.
		now := time.Now().Truncate(time.Second)
		retention := time.Minute
		short := metrics.NewStore(retention)
		feed(short, 2, now.Add(-10*time.Minute), now, time.Time{})
		res := short.Series(2, metrics.Query{Types: all, Start: now.Add(-10 * time.Minute), End: now, Step: 10})
		oldest := float64(now.Add(-retention - 2*interval).Unix())
		for n, ts := range res.TimeSeries {
			ps := points(t, n, ts.Values)
			if len(ps) == 0 {
				t.Errorf("retention: %s has no points within the retention", n)
			}
			for _, p := range ps {
				if p.ts < oldest {
					t.Errorf("retention: %s has a point at %v, older than the retention of %s", n, time.Unix(int64(p.ts), 0), retention)
					break
				}
			}
		}
	})
}
