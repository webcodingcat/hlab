package faults

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/api"
)

// defaultPerHour is Hetzner's default rate limit per project.
const defaultPerHour = 3600

// bucket is a token bucket of capacity per hour, refilled by
// capacity/3600 tokens per second, lazily on each use.
type bucket struct {
	capacity float64
	tokens   float64
	last     time.Time
}

// newBucket returns a full bucket of capacity at now.
func newBucket(capacity float64, now time.Time) *bucket {
	return &bucket{capacity: capacity, tokens: capacity, last: now}
}

// refill adds the tokens gained since the last use.
func (b *bucket) refill(now time.Time) {
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(b.capacity, b.tokens+b.capacity*elapsed/3600)
	}
	b.last = now
}

// setHeaders writes Hetzner's RateLimit-* headers of b at now.
func (b *bucket) setHeaders(h http.Header, now time.Time) {
	untilFull := math.Ceil((b.capacity - b.tokens) * 3600 / b.capacity)
	h.Set("RateLimit-Limit", strconv.FormatInt(int64(b.capacity), 10))
	h.Set("RateLimit-Remaining", strconv.FormatInt(int64(math.Floor(b.tokens)), 10))
	h.Set("RateLimit-Reset", strconv.FormatInt(now.Add(time.Duration(untilFull)*time.Second).Unix(), 10))
}

// Middleware is the fault layer of the api chain: for requests marked
// api.ByToken it applies the project's rate_limit (or counts against the
// default bucket, which never refuses) and api_error faults and sets the
// RateLimit-* headers of the project's bucket.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !api.ByToken(req.Context()) {
			next.ServeHTTP(w, req)
			return
		}
		p, _ := api.ProjectFrom(req.Context())
		refused, hit := m.admit(w.Header(), p.Name, req)
		switch {
		case refused:
			api.Error(w, "rate_limit_exceeded", "rate limit exceeded", nil)
		case hit != nil:
			code, _ := hit.Params["code"].(string)
			var details any
			if code == "invalid_input" {
				details = map[string]any{"fields": []any{}}
			}
			api.Error(w, code, fmt.Sprintf("%s injected by HLab fault %d", code, hit.ID), details)
		default:
			next.ServeHTTP(w, req)
		}
	})
}

// admit counts req against project's bucket and sets the headers; it
// reports whether the rate_limit fault refuses req and, if not, the
// api_error fault that hits it.
func (m *Manager) admit(h http.Header, project string, req *http.Request) (refused bool, hit *Fault) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	var limit, apiError *entry
	for _, e := range m.entries {
		if !e.applied || e.ending || e.fault.Project != project {
			continue
		}
		switch e.fault.Kind {
		case KindRateLimit:
			limit = e
		case KindAPIError:
			apiError = e
		}
	}

	if limit != nil {
		b := limit.bucket
		b.refill(now)
		if b.tokens < 1 {
			b.setHeaders(h, now)
			return true, nil
		}
		b.tokens--
		b.setHeaders(h, now)
	} else {
		b := m.defaults[project]
		if b == nil {
			b = newBucket(defaultPerHour, now)
			m.defaults[project] = b
		}
		b.refill(now)
		b.tokens = math.Max(0, b.tokens-1)
		b.setHeaders(h, now)
	}

	if apiError == nil || !matches(apiError.fault.Params, req) {
		return false, nil
	}
	percent, _ := apiError.fault.Params["percent"].(int64)
	if apiError.rng.IntN(100) < int(percent) {
		f := clone(apiError.fault)
		return false, &f
	}
	return false, nil
}

// matches reports whether req passes the only and resource filters of an
// api_error fault. The path is the one below the API prefix.
func matches(params map[string]any, req *http.Request) bool {
	switch only, _ := params["only"].(string); only {
	case "read":
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			return false
		}
	case "write":
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			return false
		}
	}
	if resource, ok := params["resource"].(string); ok {
		first, _, _ := strings.Cut(strings.TrimPrefix(req.URL.Path, "/"), "/")
		if first != resource {
			return false
		}
	}
	return true
}
