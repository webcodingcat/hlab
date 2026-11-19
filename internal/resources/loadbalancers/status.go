package loadbalancers

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/scheduler"
)

// statusInterval is the interval of the status job and the smallest
// metrics step.
const statusInterval = 10 * time.Second

// retention is how long counter samples are kept.
const retention = 24 * time.Hour

// sample is the counters of one load balancer at one time.
type sample struct {
	at time.Time
	c  agentapi.LBCounters
}

// lbStatus holds the health and counters read from the agent.
type lbStatus struct {
	mu      sync.Mutex
	health  map[int64]map[string]string // per LB, by healthKey
	samples map[int64][]sample          // per LB, in time order
}

// statusRoutes registers the metrics endpoint.
func (m *Module) statusRoutes(r resource.Registrar) {
	r.Handle("GET /load_balancers/{id}/metrics", http.HandlerFunc(m.getMetrics))
}

// Jobs implements resource.Scheduled: the status job reading health and
// counters from the agent.
func (m *Module) Jobs() []scheduler.Job {
	return []scheduler.Job{{Name: "lb-status", Every: statusInterval, Run: m.readStatus}}
}

// readStatus replaces the health cache with the agent's report and adds a
// counter sample per load balancer; samples older than the retention go.
func (m *Module) readStatus(ctx context.Context) {
	if m.d.AgentStatus == nil {
		return
	}
	st, err := m.d.AgentStatus(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("load balancer status", "err", err)
		}
		return
	}
	now := m.d.Clock.Now()
	health := map[int64]map[string]string{}
	for lbID, list := range st.Targets {
		h := map[string]string{}
		for _, t := range list {
			h[healthKey(t.Key, t.ListenPort)] = t.Status
		}
		health[lbID] = h
	}

	s := &m.stat
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = health
	if s.samples == nil {
		s.samples = map[int64][]sample{}
	}
	for lbID, c := range st.LBMetrics {
		s.samples[lbID] = append(s.samples[lbID], sample{at: now, c: c})
	}
	cut := now.Add(-retention)
	for lbID, list := range s.samples {
		i := 0
		for i < len(list) && list[i].at.Before(cut) {
			i++
		}
		switch {
		case i == len(list):
			delete(s.samples, lbID)
		case i > 0:
			s.samples[lbID] = slices.Clone(list[i:])
		}
	}
}

func healthKey(key string, port int) string { return key + "/" + strconv.Itoa(port) }

// health is the health of target key on listen port of load balancer
// lbID: "healthy", "unhealthy" or "unknown".
func (m *Module) health(lbID int64, key string, port int) string { //nolint:unused // used by viewTargets (plan 08-16)
	m.stat.mu.Lock()
	defer m.stat.mu.Unlock()
	if s, ok := m.stat.health[lbID][healthKey(key, port)]; ok {
		return s
	}
	return "unknown"
}

// samplesOf returns a copy of the counter samples of lbID.
func (m *Module) samplesOf(lbID int64) []sample {
	m.stat.mu.Lock()
	defer m.stat.mu.Unlock()
	return slices.Clone(m.stat.samples[lbID])
}
