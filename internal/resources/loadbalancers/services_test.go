package loadbalancers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
)

func addService(e *lbEnv, id int64, body map[string]any) *http.Response {
	return lbAction(e, id, "add_service", body)
}

func runAddService(t *testing.T, e *lbEnv, id int64, body map[string]any) {
	t.Helper()
	runLBAction(t, e, id, "add_service", body)
}

func httpServiceBody(protocol string, listen, dest int, http map[string]any) map[string]any {
	b := tcpService(listen, dest)
	b["protocol"] = protocol
	if http != nil {
		b["http"] = http
	}
	return b
}

func mustService(t *testing.T, e *lbEnv, id int64, port int) serviceView {
	t.Helper()
	s, ok := mustLB(t, e, id).service(port)
	if !ok {
		t.Fatalf("load balancer %d has no service on port %d", id, port)
	}
	return s
}

// testServicesTCP: tcp services, their default health check, the listen
// port rule and the service limit.
func testServicesTCP(t *testing.T) {
	e := newEnv(t)
	cert, _, _ := uploadCert(t, e.Env, "tcp-cert")
	lb := createLB(t, e, lbBody("tcp"))

	runAddService(t, e, lb.ID, tcpService(8080, 80))
	s := mustService(t, e, lb.ID, 8080)
	hc := s.HealthCheck
	if s.Protocol != "tcp" || s.DestinationPort != 80 || hc.Protocol != "tcp" || hc.Port != 80 ||
		hc.Interval != 15 || hc.Timeout != 10 || hc.Retries != 3 {
		t.Errorf("tcp service without health check: %+v, want tcp to 80 with check tcp/80/15/10/3", s)
	}

	runLBAction(t, e, lb.ID, "update_service", map[string]any{"listen_port": 8080, "destination_port": 81})
	if s := mustService(t, e, lb.ID, 8080); s.DestinationPort != 81 {
		t.Errorf("after update_service: destination_port %d, want 81", s.DestinationPort)
	}

	wantError(t, addService(e, lb.ID, tcpService(8080, 90)),
		http.StatusPreconditionFailed, "source_port_already_used", "add_service on a used listen port")

	// Port 80 is used by a tcp service; turning on redirect_http of the
	// https service would take it as well.
	runAddService(t, e, lb.ID, tcpService(80, 80))
	runAddService(t, e, lb.ID, httpServiceBody("https", 443, 80, map[string]any{"certificates": []int64{cert}}))
	wantError(t, lbAction(e, lb.ID, "update_service", map[string]any{
		"listen_port": 443, "protocol": "https", "http": map[string]any{"redirect_http": true},
	}), http.StatusUnprocessableEntity, "source_port_already_used", "update_service taking a used listen port")

	runLBAction(t, e, lb.ID, "delete_service", map[string]any{"listen_port": 8080})
	if _, ok := mustLB(t, e, lb.ID).service(8080); ok {
		t.Errorf("service 8080 still there after delete_service")
	}

	// lb11 allows five services; 80 and 443 exist.
	for _, port := range []int{9001, 9002, 9003} {
		runAddService(t, e, lb.ID, tcpService(port, 80))
	}
	wantError(t, addService(e, lb.ID, tcpService(9004, 80)),
		http.StatusForbidden, "resource_limit_exceeded", "a sixth service on lb11")
}

// testServicesHTTP: http defaults, sticky sessions, the cookie lifetime on
// update and redirect_http.
func testServicesHTTP(t *testing.T) {
	e := newEnv(t)
	cert, _, _ := uploadCert(t, e.Env, "http-cert")
	lb := createLB(t, e, lbBody("http"))

	body := httpServiceBody("http", 8000, 80, nil)
	body["health_check"] = map[string]any{
		"protocol": "http", "port": 80, "interval": 15, "timeout": 10, "retries": 3,
		"http": map[string]any{"path": "/"},
	}
	runAddService(t, e, lb.ID, body)
	s := mustService(t, e, lb.ID, 8000)
	if s.HTTP == nil {
		t.Fatalf("http service without http object: %+v", s)
	}
	if s.HTTP.CookieName != "HCLBSTICKY" || s.HTTP.CookieLifetime != 300 {
		t.Errorf("http defaults: cookie %q lifetime %d, want HCLBSTICKY 300", s.HTTP.CookieName, s.HTTP.CookieLifetime)
	}
	if s.HTTP.StickySessions == nil || *s.HTTP.StickySessions || s.HTTP.RedirectHTTP == nil || *s.HTTP.RedirectHTTP {
		t.Errorf("http defaults: sticky_sessions %v redirect_http %v, want both false", s.HTTP.StickySessions, s.HTTP.RedirectHTTP)
	}
	if s.HealthCheck.HTTP == nil || s.HealthCheck.HTTP.TLS == nil || *s.HealthCheck.HTTP.TLS {
		t.Errorf("http health check: %+v, want tls false", s.HealthCheck.HTTP)
	}

	runAddService(t, e, lb.ID, httpServiceBody("http", 8001, 80, map[string]any{"sticky_sessions": true}))
	if s := mustService(t, e, lb.ID, 8001); s.HTTP == nil || s.HTTP.StickySessions == nil || !*s.HTTP.StickySessions {
		t.Errorf("service with sticky_sessions true: %+v", s.HTTP)
	}
	runLBAction(t, e, lb.ID, "update_service", map[string]any{
		"listen_port": 8001, "protocol": "http", "proxyprotocol": false, "http": map[string]any{"cookie_lifetime": 0},
	})
	if s := mustService(t, e, lb.ID, 8001); s.HTTP == nil || s.HTTP.CookieLifetime != 300 {
		t.Errorf("after update with cookie_lifetime 0: %+v, want lifetime 300 kept", s.HTTP)
	}

	wantInvalid(t, addService(e, lb.ID, httpServiceBody("http", 8002, 80, map[string]any{"redirect_http": true})),
		"redirect_http on an http service")

	runAddService(t, e, lb.ID, httpServiceBody("https", 443, 80, map[string]any{
		"certificates": []int64{cert}, "redirect_http": true,
	}))
	wantError(t, addService(e, lb.ID, httpServiceBody("http", 80, 80, nil)),
		http.StatusPreconditionFailed, "source_port_already_used", "a service on port 80 next to redirect_http")
}

// testServicesHTTPS: certificates of https services.
func testServicesHTTPS(t *testing.T) {
	e := newEnv(t)
	lb := createLB(t, e, lbBody("https"))
	c1, _, _ := uploadCert(t, e.Env, "c1")

	runAddService(t, e, lb.ID, httpServiceBody("https", 443, 80, map[string]any{"certificates": []int64{c1}}))
	if s := mustService(t, e, lb.ID, 443); s.HTTP == nil || !slices.Equal(s.HTTP.Certificates, []int64{c1}) {
		t.Errorf("https service certificates %+v, want [%d]", s.HTTP, c1)
	}
	resp := e.Do(http.MethodGet, fmt.Sprintf("/certificates/%d", c1), nil)
	var cb struct {
		Certificate struct {
			UsedBy []struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"used_by"`
		} `json:"certificate"`
	}
	apitest.Decode(t, resp, &cb)
	if len(cb.Certificate.UsedBy) != 1 || cb.Certificate.UsedBy[0].ID != lb.ID || cb.Certificate.UsedBy[0].Type != "load_balancer" {
		t.Errorf("certificate %d used_by %+v, want load balancer %d", c1, cb.Certificate.UsedBy, lb.ID)
	}

	wantInvalid(t, addService(e, lb.ID, httpServiceBody("https", 444, 80, map[string]any{"certificates": []int64{unknownID}})),
		"https service with an unknown certificate")
	q := e.For(t)
	q.Token = e.NewToken("q", false)
	foreign, _, _ := uploadCert(t, q, "foreign")
	wantInvalid(t, addService(e, lb.ID, httpServiceBody("https", 445, 80, map[string]any{"certificates": []int64{foreign}})),
		"https service with a certificate of another project")

	// lb11 allows ten assigned certificates.
	many := []int64{c1}
	for i := range 10 {
		id, _, _ := uploadCert(t, e.Env, fmt.Sprintf("many-%d", i))
		many = append(many, id)
	}
	wantError(t, addService(e, lb.ID, httpServiceBody("https", 446, 80, map[string]any{"certificates": many})),
		http.StatusForbidden, "resource_limit_exceeded", "eleven certificates on lb11")

	body := httpServiceBody("https", 8443, 443, map[string]any{"certificates": []int64{c1}})
	body["health_check"] = map[string]any{
		"protocol": "https", "port": 443, "interval": 15, "timeout": 10, "retries": 3,
		"http": map[string]any{"path": "/"},
	}
	runAddService(t, e, lb.ID, body)
}
