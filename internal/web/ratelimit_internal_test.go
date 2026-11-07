package web

import (
	"net/http"
	"testing"
	"time"
)

// The buckets of projects that stopped calling (deleted ones among them) go
// once they are full again; a bucket in use stays.
func TestRateLimiterDropsBucketsThatAreFullAgain(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	limiter := newRateLimiter(3, func() time.Time { return now })
	limiter.take(http.Header{}, 1, "/v1/ssh_keys")

	now = now.Add(2 * time.Hour)
	limiter.take(http.Header{}, 2, "/v1/ssh_keys")

	if _, found := limiter.buckets[budget{projectID: 1}]; found {
		t.Error("the bucket of project 1 is still there, though full again")
	}
	if _, found := limiter.buckets[budget{projectID: 2}]; !found {
		t.Error("the bucket of project 2, just used, is gone")
	}
}
