package certificates_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/ca"
)

// caIssuer is the fake Issuer: a ca.CA in a temp folder on the test clock,
// counting its calls.
type caIssuer struct {
	ca *ca.CA

	mu    sync.Mutex
	calls int
}

func newIssuer(t *testing.T, clock *testClock) *caIssuer {
	t.Helper()
	a, err := ca.Open(t.TempDir(), "hlab CA", clock.Now)
	if err != nil {
		t.Fatalf("ca.Open: %v", err)
	}
	return &caIssuer{ca: a}
}

func (i *caIssuer) Issue(domains []string) ([]byte, []byte, error) {
	i.mu.Lock()
	i.calls++
	i.mu.Unlock()
	return i.ca.Issue(domains)
}

func (i *caIssuer) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.calls
}

// checkIssued checks that the certificate field holds a leaf issued by the
// CA for domains.
func checkIssued(t *testing.T, i *caIssuer, c certView, domains []string, at time.Time) {
	t.Helper()
	if c.Certificate == nil {
		t.Fatalf("certificate %d has no certificate PEM", c.ID)
	}
	var chain []*x509.Certificate
	for rest := []byte(*c.Certificate); ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("certificate %d: parse PEM: %v", c.ID, err)
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 {
		t.Fatalf("certificate %d: no certificate in %q", c.ID, *c.Certificate)
	}
	rootBlock, _ := pem.Decode(i.ca.Certificate())
	root, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	if _, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, CurrentTime: at}); err != nil {
		t.Errorf("certificate %d is not issued by the hlab CA: %v", c.ID, err)
	}
	if !slices.Equal(chain[0].DNSNames, domains) {
		t.Errorf("certificate %d: SANs %v, want %v", c.ID, chain[0].DNSNames, domains)
	}
}

// createZoneAuth creates a zone with the token auth and waits for its action.
func createZoneAuth(t *testing.T, e *certEnv, auth string, zone map[string]any) int64 {
	t.Helper()
	resp := e.DoAuth(auth, http.MethodPost, "/zones", zone)
	data := body(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /zones %v: status %d, want 201 (%s)", zone, resp.StatusCode, data)
	}
	var b struct {
		Zone struct {
			ID int64 `json:"id"`
		} `json:"zone"`
		Action actionView `json:"action"`
	}
	unmarshal(t, data, &b)
	if got := e.For(t).WaitActionAuth(auth, b.Action.ID); got.Status != "success" {
		t.Fatalf("create zone %v ended %q %+v, want success", zone, got.Status, got.Error)
	}
	return b.Zone.ID
}

func createZone(t *testing.T, e *certEnv, name string) int64 {
	t.Helper()
	return createZoneAuth(t, e, "Bearer "+e.Token, map[string]any{"name": name, "mode": "primary"})
}

// createManaged posts a managed certificate, expects 201 with issuance
// pending, renewal unavailable and a create_certificate action, and
// returns both.
func createManaged(t *testing.T, e *certEnv, name string, domains ...string) (certView, actionView) {
	t.Helper()
	resp := e.Do(http.MethodPost, "/certificates", map[string]any{"name": name, "type": "managed", "domain_names": domains})
	data := body(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /certificates managed %v: status %d, want 201 (%s)", domains, resp.StatusCode, data)
	}
	var b struct {
		Certificate certView   `json:"certificate"`
		Action      actionView `json:"action"`
	}
	unmarshal(t, data, &b)
	if s := b.Certificate.Status; s == nil || s.Issuance != "pending" || s.Renewal != "unavailable" {
		t.Errorf("managed certificate %v at creation: status %+v, want issuance pending, renewal unavailable", domains, s)
	}
	if b.Action.Command != "create_certificate" {
		t.Errorf("managed certificate %v: action %q, want create_certificate", domains, b.Action.Command)
	}
	return b.Certificate, b.Action
}

// wantFailed checks a failed issuance with code on the certificate and its
// failed action.
func wantFailed(t *testing.T, e *certEnv, c certView, a actionView, code, what string) {
	t.Helper()
	got := waitAction(t, e, a, "error", what)
	if got.Error == nil || got.Error.Code != code {
		t.Errorf("%s: action error %+v, want %s", what, got.Error, code)
	}
	v, _ := getCert(t, e, c.ID)
	if v.Status == nil || v.Status.Issuance != "failed" || v.Status.Error == nil || v.Status.Error.Code != code {
		t.Errorf("%s: status %+v, want issuance failed with %s", what, v.Status, code)
	}
}

func retry(e *certEnv, id int64) *http.Response {
	return e.Do(http.MethodPost, fmt.Sprintf("/certificates/%d/actions/retry", id), nil)
}

// runJobs runs the certificates module's jobs once.
func runJobs(t *testing.T, e *certEnv) {
	t.Helper()
	jobs := e.mod.Jobs()
	if len(jobs) == 0 {
		t.Fatal("certificates module has no job")
	}
	for _, j := range jobs {
		ctx, cancel := context.WithTimeout(context.Background(), apitest.ActionTimeout)
		j.Run(ctx)
		cancel()
	}
}

func testManaged(t *testing.T) {
	clock := newTestClock(start)
	issuer := newIssuer(t, clock)
	e := newCertEnv(t, issuer, clock)
	createZone(t, e, "example.test")

	// Success for the zone itself and a wildcard below it.
	domains := []string{"example.test", "*.www.example.test"}
	c, a := createManaged(t, e, "managed", domains...)
	waitAction(t, e, a, "success", "create managed certificate")
	v, _ := getCert(t, e, c.ID)
	if v.Status == nil || v.Status.Issuance != "completed" || v.Status.Renewal != "scheduled" {
		t.Errorf("issued managed certificate: status %+v, want issuance completed, renewal scheduled", v.Status)
	}
	checkIssued(t, issuer, v, domains, clock.Now())

	// No zone in the project; a zone of another project does not count.
	createZoneAuth(t, e, "Bearer "+e.NewToken("q", false), map[string]any{"name": "foreign.test", "mode": "primary"})
	c, a = createManaged(t, e, "foreign", "www.foreign.test")
	wantFailed(t, e, c, a, "dns_zone_not_found", "domain in a zone of another project")
	missing, a := createManaged(t, e, "missing", "www.missing.test")
	wantFailed(t, e, missing, a, "dns_zone_not_found", "domain without a zone")

	// A secondary zone.
	createZoneAuth(t, e, "Bearer "+e.Token, map[string]any{
		"name": "secondary.test", "mode": "secondary",
		"primary_nameservers": []map[string]any{{"address": "192.0.2.53"}},
	})
	c, a = createManaged(t, e, "secondary", "www.secondary.test")
	wantFailed(t, e, c, a, "dns_zone_is_secondary_zone", "domain in a secondary zone")

	// retry after creating the zone issues the certificate.
	createZone(t, e, "missing.test")
	resp := retry(e, missing.ID)
	data := body(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("retry after creating the zone: status %d, want 201 (%s)", resp.StatusCode, data)
	}
	var rb struct {
		Action actionView `json:"action"`
	}
	unmarshal(t, data, &rb)
	waitAction(t, e, rb.Action, "success", "retry after creating the zone")
	v, _ = getCert(t, e, missing.ID)
	if v.Status == nil || v.Status.Issuance != "completed" {
		t.Errorf("after retry: status %+v, want issuance completed", v.Status)
	}
	checkIssued(t, issuer, v, []string{"www.missing.test"}, clock.Now())

	// retry on a completed and on an uploaded certificate.
	wantError(t, retry(e, missing.ID), http.StatusUnprocessableEntity, "invalid_input", "retry on a completed certificate")
	root := makeCert(t, "Test Root", nil, true, nil)
	leaf := makeCert(t, "upload.test", []string{"upload.test"}, false, &root)
	resp = e.Do(http.MethodPost, "/certificates", map[string]any{
		"name": "uploaded", "type": "uploaded", "certificate": leaf.certPEM, "private_key": leaf.keyPEM,
	})
	data = body(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /certificates uploaded: status %d, want 201 (%s)", resp.StatusCode, data)
	}
	var ub struct {
		Certificate certView `json:"certificate"`
	}
	unmarshal(t, data, &ub)
	wantError(t, retry(e, ub.Certificate.ID), http.StatusUnprocessableEntity, "invalid_input", "retry on an uploaded certificate")
}

func testRenewal(t *testing.T) {
	clock := newTestClock(start)
	issuer := newIssuer(t, clock)
	e := newCertEnv(t, issuer, clock)
	zoneID := createZone(t, e, "renew.test")
	c, a := createManaged(t, e, "renew", "www.renew.test")
	waitAction(t, e, a, "success", "create managed certificate")
	e.setUsedBy(c.ID, 7)
	first, _ := getCert(t, e, c.ID)
	if first.NotValidAfter == nil {
		t.Fatalf("issued certificate has no not_valid_after")
	}
	expiry := *first.NotValidAfter
	day := 24 * time.Hour

	// 31 days left: left alone.
	clock.Set(expiry.Add(-31 * day))
	calls, pushes := issuer.count(), e.pushCount()
	runJobs(t, e)
	v, _ := getCert(t, e, c.ID)
	if issuer.count() != calls || v.NotValidAfter == nil || !v.NotValidAfter.Equal(expiry) || *v.Certificate != *first.Certificate {
		t.Errorf("renewal job at 31 days left: issued %d times, not_valid_after %v; want the certificate left alone (%v)",
			issuer.count()-calls, v.NotValidAfter, expiry)
	}
	if v.Status == nil || v.Status.Renewal != "scheduled" {
		t.Errorf("renewal job at 31 days left: status %+v, want renewal scheduled", v.Status)
	}

	// 29 days left: a new leaf, renewal scheduled, agent pushed.
	clock.Set(expiry.Add(-29 * day))
	runJobs(t, e)
	v, _ = getCert(t, e, c.ID)
	if v.NotValidAfter == nil || !v.NotValidAfter.After(expiry) {
		t.Fatalf("renewal job at 29 days left: not_valid_after %v, want a new one after %v", v.NotValidAfter, expiry)
	}
	if v.Status == nil || v.Status.Renewal != "scheduled" {
		t.Errorf("after renewal: status %+v, want renewal scheduled", v.Status)
	}
	if e.pushCount() == pushes {
		t.Errorf("renewal pushed no agent document")
	}
	checkIssued(t, issuer, v, []string{"www.renew.test"}, clock.Now())
	renewed := v

	// Zone deleted: renewal failed, the old leaf kept.
	resp := e.Do(http.MethodDelete, fmt.Sprintf("/zones/%d", zoneID), nil)
	data := body(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("DELETE /zones/%d: status %d, want 201 (%s)", zoneID, resp.StatusCode, data)
	}
	var db struct {
		Action actionView `json:"action"`
	}
	unmarshal(t, data, &db)
	waitAction(t, e, db.Action, "success", "delete zone")
	clock.Set(renewed.NotValidAfter.Add(-29 * day))
	runJobs(t, e)
	v, _ = getCert(t, e, c.ID)
	if v.Status == nil || v.Status.Renewal != "failed" || v.Status.Error == nil || v.Status.Error.Code != "dns_zone_not_found" {
		t.Errorf("renewal without the zone: status %+v, want renewal failed with dns_zone_not_found", v.Status)
	}
	if v.NotValidAfter == nil || !v.NotValidAfter.Equal(*renewed.NotValidAfter) || *v.Certificate != *renewed.Certificate {
		t.Errorf("renewal without the zone: not_valid_after %v, want the old leaf kept (%v)", v.NotValidAfter, renewed.NotValidAfter)
	}
}
