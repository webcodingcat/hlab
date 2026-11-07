package web

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimiter is Hetzner's rate limit: perHour requests per hour and
// project, given back gradually (one a second for 3600 an hour), with
// bursts allowed up to the whole budget.
type rateLimiter struct {
	perHour int // 0: no limit
	now     func() time.Time

	mu      sync.Mutex
	buckets map[int64]bucket // by project ID
}

// bucket is what a project has left, as of a time.
type bucket struct {
	left float64
	at   time.Time
}

func newRateLimiter(perHour int, now func() time.Time) *rateLimiter {
	return &rateLimiter{perHour: perHour, now: now, buckets: map[int64]bucket{}}
}

// take counts a request of a project and sets Hetzner's RateLimit headers;
// it tells whether the request is allowed.
func (r *rateLimiter) take(header http.Header, projectID int64) bool {
	if r.perHour == 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	perSecond := float64(r.perHour) / time.Hour.Seconds()
	current, found := r.buckets[projectID]
	if !found {
		current = bucket{left: float64(r.perHour), at: now}
	}
	current.left = min(float64(r.perHour), current.left+now.Sub(current.at).Seconds()*perSecond)
	current.at = now
	allowed := current.left >= 1
	if allowed {
		current.left--
	}
	r.buckets[projectID] = current

	untilFull := math.Ceil((float64(r.perHour) - current.left) / perSecond)
	header.Set("RateLimit-Limit", strconv.Itoa(r.perHour))
	header.Set("RateLimit-Remaining", strconv.Itoa(int(math.Floor(current.left))))
	header.Set("RateLimit-Reset", strconv.FormatInt(now.Unix()+int64(untilFull), 10))
	return allowed
}
