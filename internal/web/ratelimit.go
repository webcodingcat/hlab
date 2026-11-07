package web

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// isActionsPath tells whether a path is one of Hetzner's actions endpoints:
// /actions…, /servers/actions…, /servers/{id}/actions…, likewise for every
// resource. They share a budget of their own, twice the size of the one of
// all other routes (measured 2026-11-07: 7200 against 3600 an hour, counted
// apart; /actions, /servers/actions, /servers/{id}/actions/… and
// /floating_ips/actions took from the same 7200). The route was checked
// before, so no other path has a segment "actions".
func isActionsPath(path string) bool {
	return strings.Contains(path+"/", "/actions/")
}

// rateLimiter is Hetzner's rate limit: requests per hour and project, given
// back gradually (one a second for 3600 an hour), with bursts allowed up to
// the whole budget. Each project has two budgets: one for the actions
// endpoints, one for all other routes.
type rateLimiter struct {
	perHour int // the budget of all other routes; 0: no limit
	now     func() time.Time

	mu      sync.Mutex
	buckets map[budget]bucket
	pruned  time.Time // when full buckets were last dropped
}

// budget names one budget of a project.
type budget struct {
	projectID int64
	actions   bool
}

// bucket is what a budget has left, as of a time.
type bucket struct {
	left float64
	at   time.Time
}

func newRateLimiter(perHour int, now func() time.Time) *rateLimiter {
	return &rateLimiter{perHour: perHour, now: now, buckets: map[budget]bucket{}}
}

// take counts a request of a project against the budget of its path and
// sets Hetzner's RateLimit headers; it tells whether the request is allowed.
func (r *rateLimiter) take(header http.Header, projectID int64, path string) bool {
	if r.perHour == 0 {
		return true
	}
	key := budget{projectID: projectID, actions: isActionsPath(path)}
	perHour := r.budgetPerHour(key)

	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.prune(now)
	perSecond := perHour / time.Hour.Seconds()
	current, found := r.buckets[key]
	if !found {
		current = bucket{left: perHour, at: now}
	}
	current.left = min(perHour, current.left+now.Sub(current.at).Seconds()*perSecond)
	current.at = now
	allowed := current.left >= 1
	if allowed {
		current.left--
	}
	r.buckets[key] = current

	untilFull := math.Ceil((perHour - current.left) / perSecond)
	header.Set("RateLimit-Limit", strconv.Itoa(int(perHour)))
	header.Set("RateLimit-Remaining", strconv.Itoa(int(math.Floor(current.left))))
	header.Set("RateLimit-Reset", strconv.FormatInt(now.Unix()+int64(untilFull), 10))
	return allowed
}

// budgetPerHour returns the size of a budget: the actions endpoints have
// twice that of the other routes.
func (r *rateLimiter) budgetPerHour(key budget) float64 {
	if key.actions {
		return 2 * float64(r.perHour)
	}
	return float64(r.perHour)
}

// prune drops, at most once an hour, the buckets that are full again: a
// missing bucket starts full, so nothing changes, and the buckets of deleted
// projects do not pile up. The caller holds the mutex.
func (r *rateLimiter) prune(now time.Time) {
	if now.Sub(r.pruned) < time.Hour {
		return
	}
	r.pruned = now
	for key, current := range r.buckets {
		perHour := r.budgetPerHour(key)
		refilled := current.left + now.Sub(current.at).Seconds()*perHour/time.Hour.Seconds()
		if refilled >= perHour {
			delete(r.buckets, key)
		}
	}
}
