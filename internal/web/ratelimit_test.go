package web_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/web"
)

// testTime is a clock that the test moves on.
type testTime struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testTime) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testTime) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Hetzner's rate limit: a budget per hour and project, given back gradually,
// one request a second at 3600 an hour.
func TestRateLimit(t *testing.T) {
	clock := &testTime{now: time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)}
	client, _ := newAPI(t, web.Options{RateLimit: 3, Now: clock.Now})

	for want := 2; want >= 0; want-- {
		response := client.Get("/v1/actions/1000000")
		if response.Status != 200 {
			t.Fatalf("request %d answered %d", 3-want, response.Status)
		}
		if got := response.Header.Get("RateLimit-Remaining"); got != strconv.Itoa(want) {
			t.Errorf("RateLimit-Remaining is %s, want %d", got, want)
		}
	}
	refused := client.Get("/v1/actions/1000000")
	if code, _, _ := errorOf(t, refused); refused.Status != 429 || code != "rate_limit_exceeded" {
		t.Fatalf("the fourth request answered %d %s", refused.Status, refused.Body)
	}
	if refused.Header.Get("RateLimit-Limit") != "3" {
		t.Errorf("RateLimit-Limit is %q", refused.Header.Get("RateLimit-Limit"))
	}
	// 3 an hour is one every 20 minutes: the budget is full again in an hour
	wantReset := clock.Now().Add(time.Hour).Unix()
	if reset := refused.Header.Get("RateLimit-Reset"); reset != strconv.FormatInt(wantReset, 10) {
		t.Errorf("RateLimit-Reset is %s, want %d", reset, wantReset)
	}

	clock.Advance(20 * time.Minute)
	if again := client.Get("/v1/actions/1000000"); again.Status != 200 {
		t.Errorf("after 20 minutes the request answered %d", again.Status)
	}
}

func TestRateLimitZeroIsNoLimit(t *testing.T) {
	client, _ := newAPI(t, web.Options{})
	response := client.Get("/v1/actions/1000000")
	if response.Status != 200 || response.Header.Get("RateLimit-Limit") != "" {
		t.Errorf("answered %d with RateLimit-Limit %q", response.Status, response.Header.Get("RateLimit-Limit"))
	}
}
