package faults_test

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
)

func init() {
	register("FAULT-04", testFault04)
}

// rateHeaders returns RateLimit-Limit, RateLimit-Remaining and
// RateLimit-Reset of resp; a missing or non-integer header fails the test.
func rateHeaders(t *testing.T, what string, resp *http.Response) (limit, remaining, reset int64) {
	t.Helper()
	get := func(name string) int64 {
		v := resp.Header.Get(name)
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("%s: header %s = %q, want an integer", what, name, v)
		}
		return n
	}
	return get("RateLimit-Limit"), get("RateLimit-Remaining"), get("RateLimit-Reset")
}

// wantRate checks the three headers against limit, remaining and the reset
// time now + resetIn.
func (a *apiEnv) wantRate(t *testing.T, what string, resp *http.Response, limit, remaining int64, resetIn time.Duration) {
	t.Helper()
	l, r, reset := rateHeaders(t, what, resp)
	wantReset := a.Clock.Now().Add(resetIn).Unix()
	if l != limit || r != remaining || reset != wantReset {
		t.Errorf("%s: RateLimit limit %d remaining %d reset %d, want %d, %d, %d",
			what, l, r, reset, limit, remaining, wantReset)
	}
}

func testFault04(t *testing.T) {
	t.Run("default_headers", func(t *testing.T) {
		a := newAPIEnv(t)
		tok := a.API.Token

		for i := int64(1); i <= 3; i++ {
			resp := a.send(t, tok, http.MethodGet, "/v1/ssh_keys")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("request %d: status %d, want 200", i, resp.StatusCode)
			}
			a.wantRate(t, "request "+strconv.FormatInt(i, 10), resp, 3600, 3600-i, time.Duration(i)*time.Second)
		}

		// One token back per second on the test clock.
		a.Clock.Advance(time.Second)
		a.wantRate(t, "after 1 s", a.send(t, tok, http.MethodGet, "/v1/ssh_keys"), 3600, 3597, 3*time.Second)

		// A 401 carries the headers with 3600 remaining and does not count.
		resp := a.sendAuth(t, "Bearer "+apitest.RandomToken(), http.MethodGet, "/v1/ssh_keys")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unknown token: status %d, want 401", resp.StatusCode)
		}
		if l, r, _ := rateHeaders(t, "401", resp); l != 3600 || r != 3600 {
			t.Errorf("401: RateLimit limit %d remaining %d, want 3600, 3600", l, r)
		}

		// A proxy request carries the headers with 3600 remaining and does
		// not count.
		resp = a.proxy(t, http.MethodGet, "/v1/ssh_keys")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("proxy request: status %d, want 200", resp.StatusCode)
		}
		if l, r, _ := rateHeaders(t, "proxy", resp); l != 3600 || r != 3600 {
			t.Errorf("proxy: RateLimit limit %d remaining %d, want 3600, 3600", l, r)
		}

		// The Hetzner API shares the project's bucket.
		a.wantRate(t, "hetzner API", a.send(t, tok, http.MethodGet, "/hetzner/v1/storage_boxes"), 3600, 3596, 4*time.Second)

		// An unknown path carries the headers.
		resp = a.send(t, tok, http.MethodGet, "/v1/no_such_resource")
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("unknown path: status %d, want 404", resp.StatusCode)
		}
		if l, r, _ := rateHeaders(t, "unknown path", resp); l != 3600 || r < 0 || r > 3600 {
			t.Errorf("unknown path: RateLimit limit %d remaining %d, want 3600 and 0..3600", l, r)
		}

		// At 0 requests are never refused; Remaining stays at 0.
		var last *http.Response
		for i := range 3700 {
			last = a.send(t, tok, http.MethodGet, "/v1/ssh_keys")
			if last.StatusCode != http.StatusOK {
				t.Fatalf("request %d of 3700 without a rate_limit fault: status %d, want 200", i+1, last.StatusCode)
			}
		}
		a.wantRate(t, "after 3700 more", last, 3600, 0, 3600*time.Second)
	})

	t.Run("limit", func(t *testing.T) {
		a := newAPIEnv(t)
		tok := a.API.Token
		a.create(t, "p", rateLimitSpec(10))

		paths := []string{"/v1/ssh_keys", "/hetzner/v1/storage_boxes"}
		for i := int64(1); i <= 10; i++ {
			resp := a.send(t, tok, http.MethodGet, paths[i%2])
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("request %d of 10: status %d, want 200", i, resp.StatusCode)
			}
			a.wantRate(t, "request "+strconv.FormatInt(i, 10), resp, 10, 10-i, time.Duration(i)*360*time.Second)
		}
		// The eleventh is refused; Do validates the 429 against the description.
		resp := a.API.For(t).Do(http.MethodGet, "/ssh_keys", nil)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("request 11: status %d, want 429", resp.StatusCode)
		}
		a.wantRate(t, "request 11", resp, 10, 0, 3600*time.Second)
		if code := apitest.ErrorCode(t, resp); code != "rate_limit_exceeded" {
			t.Errorf("request 11: error code %q, want rate_limit_exceeded", code)
		}

		// Another project and proxy requests are not limited.
		qToken := a.API.NewToken("q", false)
		if resp := a.send(t, qToken, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusOK {
			t.Errorf("project q: status %d, want 200", resp.StatusCode)
		} else if l, _, _ := rateHeaders(t, "project q", resp); l != 3600 {
			t.Errorf("project q: RateLimit-Limit %d, want 3600", l)
		}
		if resp := a.proxy(t, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusOK {
			t.Errorf("proxy: status %d, want 200", resp.StatusCode)
		}

		// Refused requests do not count: after 360 s one more passes.
		for i := range 5 {
			if resp := a.send(t, tok, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("refused request %d: status %d, want 429", i, resp.StatusCode)
			}
		}
		a.Clock.Advance(360 * time.Second)
		resp = a.send(t, tok, http.MethodGet, "/v1/ssh_keys")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("after 360 s: status %d, want 200", resp.StatusCode)
		}
		a.wantRate(t, "after 360 s", resp, 10, 0, 3600*time.Second)
		if resp := a.send(t, tok, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("second after 360 s: status %d, want 429", resp.StatusCode)
		}

		t.Run("api_error_hit_counts", func(t *testing.T) {
			a := newAPIEnv(t)
			a.create(t, "p", rateLimitSpec(10))
			a.create(t, "p", apiErrorSpec(hitCode, 100))
			for i := int64(1); i <= 10; i++ {
				resp := a.send(t, a.API.Token, http.MethodGet, "/v1/ssh_keys")
				if !isHit(resp) {
					t.Fatalf("request %d: status %d, want the injected 409", i, resp.StatusCode)
				}
				a.wantRate(t, "hit "+strconv.FormatInt(i, 10), resp, 10, 10-i, time.Duration(i)*360*time.Second)
			}
			if resp := a.send(t, a.API.Token, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusTooManyRequests {
				t.Errorf("request 11: status %d, want 429", resp.StatusCode)
			}
		})

		t.Run("refused_draw_nothing", func(t *testing.T) {
			const s = 31337
			// Hits of the passing requests with a rate limit and refused
			// requests in between ...
			a := newAPIEnv(t)
			a.create(t, "p", rateLimitSpec(10))
			spec := apiErrorSpec(hitCode, 50)
			spec.Seed = seed(s)
			a.create(t, "p", spec)
			var limited []bool
			pass := func(n int) {
				for i := range n {
					resp := a.send(t, a.API.Token, http.MethodGet, "/v1/ssh_keys")
					if resp.StatusCode == http.StatusTooManyRequests {
						t.Fatalf("passing request %d refused", i)
					}
					limited = append(limited, isHit(resp))
				}
			}
			refuse := func(n int) {
				for i := range n {
					if resp := a.send(t, a.API.Token, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusTooManyRequests {
						t.Fatalf("refused request %d: status %d, want 429", i, resp.StatusCode)
					}
				}
			}
			pass(10)
			refuse(5)
			a.Clock.Advance(360 * time.Second)
			pass(1)
			refuse(3)
			a.Clock.Advance(720 * time.Second)
			pass(2)
			refuse(4)

			// ... equal those of the same seed without a rate limit.
			b := newAPIEnv(t)
			b.create(t, "p", spec)
			if plain := b.hits(t, len(limited)); !slices.Equal(limited, plain) {
				t.Errorf("refused requests changed the api_error draws:\nwith limit    %v\nwithout limit %v", limited, plain)
			}
		})
	})

	t.Run("end", func(t *testing.T) {
		a := newAPIEnv(t)
		tok := a.API.Token
		f := a.create(t, "p", rateLimitSpec(10))
		for i := range 10 {
			if resp := a.send(t, tok, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusOK {
				t.Fatalf("request %d: status %d, want 200", i+1, resp.StatusCode)
			}
		}
		if resp := a.send(t, tok, http.MethodGet, "/v1/ssh_keys"); resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("request 11: status %d, want 429", resp.StatusCode)
		}
		if err := a.M.End(t.Context(), "p", f.ID); err != nil {
			t.Fatalf("End: %v", err)
		}
		for i := range 20 {
			resp := a.send(t, tok, http.MethodGet, "/v1/ssh_keys")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("after End, request %d: status %d, want 200", i+1, resp.StatusCode)
			}
			if l, r, _ := rateHeaders(t, "after End", resp); l != 3600 || r < 3500 || r >= 3600 {
				t.Fatalf("after End, request %d: RateLimit limit %d remaining %d, want 3600 and the default bucket", i+1, l, r)
			}
		}

		a.create(t, "p", rateLimitSpec(10))
		a.wantRate(t, "first request of a new rate_limit", a.send(t, tok, http.MethodGet, "/v1/ssh_keys"), 10, 9, 360*time.Second)
	})
}
