package loadbalancers_test

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
)

// statusJob is the job that reads health and counters from the agent.
const statusJob = "lb-status"

// healthOf returns the status per listen port of a health_status list.
func healthOf(h []healthView) map[int]string {
	out := map[int]string{}
	for _, s := range h {
		out[s.ListenPort] = s.Status
	}
	return out
}

func sameHealth(a, b map[int]string) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// testHealthStatus: unknown before any status, then per target and port
// from the agent, also inside a label selector.
func testHealthStatus(t *testing.T) {
	e := newEnv(t)
	direct := createServer(t, e, "direct", "fsn1", nil)
	labeled := createServer(t, e, "labeled", "fsn1", map[string]any{"labels": map[string]string{"role": "web"}})
	lb := createLB(t, e, lbBody("health"))
	runLBAction(t, e, lb.ID, "add_service", tcpService(8080, 80))
	runLBAction(t, e, lb.ID, "add_service", tcpService(8000, 81))
	runLBAction(t, e, lb.ID, "add_target", serverTargetBody(direct.ID, false))
	runLBAction(t, e, lb.ID, "add_target", selectorTargetBody("role=web"))

	unknown := map[int]string{8080: "unknown", 8000: "unknown"}
	got := mustLB(t, e, lb.ID)
	if tg, ok := got.serverTarget(direct.ID); !ok || !sameHealth(healthOf(tg.HealthStatus), unknown) {
		t.Errorf("before any status: server target health %+v (found %v), want unknown on 8080 and 8000", tg.HealthStatus, ok)
	}

	key := func(id int64) string { return fmt.Sprintf("server:%d", id) }
	e.status.Store(&agentapi.Status{Targets: map[int64][]agentapi.TargetHealth{lb.ID: {
		{Key: key(direct.ID), ListenPort: 8080, Status: "healthy"},
		{Key: key(direct.ID), ListenPort: 8000, Status: "unhealthy"},
		{Key: key(labeled.ID), ListenPort: 8080, Status: "unhealthy"},
		{Key: key(labeled.ID), ListenPort: 8000, Status: "healthy"},
	}}})
	e.runJob(t, statusJob)

	got = mustLB(t, e, lb.ID)
	if tg, ok := got.serverTarget(direct.ID); !ok || !sameHealth(healthOf(tg.HealthStatus), map[int]string{8080: "healthy", 8000: "unhealthy"}) {
		t.Errorf("after the status: server target health %+v, want 8080 healthy, 8000 unhealthy", tg.HealthStatus)
	}
	sel, ok := got.selectorTarget("role=web")
	if !ok || len(sel.Targets) != 1 || sel.Targets[0].Server == nil || sel.Targets[0].Server.ID != labeled.ID {
		t.Fatalf("label selector target %+v (found %v), want server %d nested", sel, ok, labeled.ID)
	}
	if h := healthOf(sel.Targets[0].HealthStatus); !sameHealth(h, map[int]string{8080: "unhealthy", 8000: "healthy"}) {
		t.Errorf("nested target health %v, want 8080 unhealthy, 8000 healthy", h)
	}
}

type metricsBody struct {
	Metrics struct {
		Step       float64 `json:"step"`
		TimeSeries map[string]struct {
			Values [][2]any `json:"values"`
		} `json:"time_series"`
	} `json:"metrics"`
}

// seriesValues returns the values of a series as numbers.
func seriesValues(t *testing.T, b metricsBody, name string) []float64 {
	t.Helper()
	ts, ok := b.Metrics.TimeSeries[name]
	if !ok {
		t.Errorf("no series %q among %d series", name, len(b.Metrics.TimeSeries))
		return nil
	}
	var out []float64
	for _, v := range ts.Values {
		s, ok := v[1].(string)
		if !ok {
			t.Errorf("series %s value %v is not a string", name, v[1])
			continue
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Errorf("series %s value %q: %v", name, s, err)
			continue
		}
		out = append(out, f)
	}
	return out
}

func containsValue(vals []float64, want float64) bool {
	return slices.ContainsFunc(vals, func(v float64) bool { return math.Abs(v-want) < 1e-9 })
}

// testMetrics: rates from growing counters, a reset, the query rules and
// the point limit.
func testMetrics(t *testing.T) {
	e := newEnv(t)
	lb := createLB(t, e, lbBody("metrics"))
	t0 := e.clock.Now()

	tick := func(at time.Time, c agentapi.LBCounters) {
		t.Helper()
		e.clock.Set(at)
		e.status.Store(&agentapi.Status{LBMetrics: map[int64]agentapi.LBCounters{lb.ID: c}})
		e.runJob(t, statusJob)
	}
	tick(t0, agentapi.LBCounters{OpenConnections: 5, ConnectionsTotal: 100, RequestsTotal: 1000, BytesIn: 10000, BytesOut: 20000})
	tick(t0.Add(10*time.Second), agentapi.LBCounters{OpenConnections: 7, ConnectionsTotal: 200, RequestsTotal: 1500, BytesIn: 20000, BytesOut: 50000})
	// The connections counter drops: a reset, counted as 30 new ones.
	tick(t0.Add(20*time.Second), agentapi.LBCounters{OpenConnections: 7, ConnectionsTotal: 30, RequestsTotal: 2000, BytesIn: 30000, BytesOut: 80000})

	get := func(q url.Values) *http.Response {
		return e.Do(http.MethodGet, fmt.Sprintf("/load_balancers/%d/metrics?%s", lb.ID, q.Encode()), nil)
	}
	q := url.Values{
		"type":  {"open_connections,connections_per_second,requests_per_second,bandwidth"},
		"start": {t0.Format(time.RFC3339)},
		"end":   {t0.Add(20 * time.Second).Format(time.RFC3339)},
		"step":  {"10"},
	}
	resp := get(q)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET metrics: status %d (%s), want 200", resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b metricsBody
	apitest.Decode(t, resp, &b)
	if len(b.Metrics.TimeSeries) != 5 {
		t.Errorf("series %v, want five", b.Metrics.TimeSeries)
	}
	for _, c := range []struct {
		series string
		want   []float64
	}{
		{"open_connections", []float64{7}},
		{"connections_per_second", []float64{10, 3}},
		{"requests_per_second", []float64{50}},
		{"bandwidth.in", []float64{1000}},
		{"bandwidth.out", []float64{3000}},
	} {
		vals := seriesValues(t, b, c.series)
		for _, w := range c.want {
			if !containsValue(vals, w) {
				t.Errorf("series %s = %v, want a value %v", c.series, vals, w)
			}
		}
		for _, v := range vals {
			if v < 0 || v > 5000 {
				t.Errorf("series %s = %v holds %v, a counter reset counted as a drop", c.series, vals, v)
			}
		}
	}

	noStart := url.Values{"type": q["type"], "end": q["end"]}
	wantInvalid(t, get(noStart), "metrics without start")
	unknown := url.Values{"type": {"cpu"}, "start": q["start"], "end": q["end"]}
	wantInvalid(t, get(unknown), "metrics with an unknown type")

	start, end := t0.Add(-24*time.Hour), t0.Add(20*time.Second)
	long := url.Values{"type": {"open_connections"}, "start": {start.Format(time.RFC3339)}, "end": {end.Format(time.RFC3339)}, "step": {"1"}}
	resp = get(long)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET metrics over a day with step 1: status %d (%s), want 200", resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var lb2 metricsBody
	apitest.Decode(t, resp, &lb2)
	span := end.Sub(start).Seconds()
	if step := lb2.Metrics.Step; step <= 1 || math.Floor(span/step)+1 > 500 {
		t.Errorf("step 1 over a day answered step %v, want one giving at most 500 points", step)
	}
}
