package certificates_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/certificates"
	"github.com/webcodingcat/hlab/internal/resources/zones"
	"github.com/webcodingcat/hlab/internal/store"
)

// certEnv mounts the certificates module next to the zones module, with a
// recording agent push, a settable UsedBy and a fake clock.
type certEnv struct {
	*apitest.Env
	mod   *certificates.Module
	clock *testClock

	mu     sync.Mutex
	pushes int
	usedBy map[int64][]int64 // certificate ID -> LB IDs
}

// newCertEnv builds the environment; issuer is the CA of managed
// certificates (nil: none).
func newCertEnv(t *testing.T, issuer certificates.Issuer, clock *testClock) *certEnv {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	// ServersDeps opens the store the next apitest.New uses.
	st := apitest.ServersDeps(t, cat, platformtest.NewFake()).Store
	e := &certEnv{clock: clock, usedBy: map[int64][]int64{}}
	zm := zones.New(zones.Deps{
		Store:     st,
		PushAgent: func(context.Context) error { return nil },
		SetZones:  func(context.Context, []string) error { return nil },
		Now:       clock.Now,
	})
	e.mod = certificates.New(certificates.Deps{
		Store:     st,
		CA:        issuer,
		PushAgent: e.push,
		UsedBy:    e.used,
		Now:       clock.Now,
		Clock:     clock,
	})
	e.Env = apitest.New(t, zm, e.mod, resactions.New())
	return e
}

func (e *certEnv) push(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pushes++
	return nil
}

func (e *certEnv) pushCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pushes
}

func (e *certEnv) used(_ store.Tx, _, certID int64) ([]int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.usedBy[certID]), nil
}

func (e *certEnv) setUsedBy(certID int64, lbs ...int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.usedBy[certID] = lbs
}

// start is the time the test clock starts at.
var start = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// testClock is the fake clock of the module: the test sets Now, and After
// never fires, so jobs run only when the test runs them.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock { return &testClock{now: now} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

type ref struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type actionView struct {
	ID      int64  `json:"id"`
	Command string `json:"command"`
	Status  string `json:"status"`
}

type statusView struct {
	Issuance string `json:"issuance"`
	Renewal  string `json:"renewal"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type certView struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	Labels         map[string]string `json:"labels"`
	Type           string            `json:"type"`
	Certificate    *string           `json:"certificate"`
	Fingerprint    *string           `json:"fingerprint"`
	DomainNames    []string          `json:"domain_names"`
	NotValidBefore *time.Time        `json:"not_valid_before"`
	NotValidAfter  *time.Time        `json:"not_valid_after"`
	Status         *statusView       `json:"status"`
	UsedBy         []ref             `json:"used_by"`
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"details"`
	} `json:"error"`
}

// body reads and closes the response body.
func body(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}

func unmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
}

// wantError checks status and error.code and returns the error body.
func wantError(t *testing.T, resp *http.Response, status int, code, what string) errorBody {
	t.Helper()
	var b errorBody
	unmarshal(t, body(t, resp), &b)
	if resp.StatusCode != status || b.Error.Code != code {
		t.Errorf("%s: status %d code %q (%s), want %d %s", what, resp.StatusCode, b.Error.Code, b.Error.Message, status, code)
	}
	return b
}

// wantFieldError checks a 422 invalid_input naming field.
func wantFieldError(t *testing.T, resp *http.Response, field, what string) {
	t.Helper()
	b := wantError(t, resp, http.StatusUnprocessableEntity, "invalid_input", what)
	var names []string
	for _, f := range b.Error.Details.Fields {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, field) {
		t.Errorf("%s: error on fields %v, want %s", what, names, field)
	}
}

func certPath(id int64) string { return fmt.Sprintf("/certificates/%d", id) }

// getCert returns GET /certificates/{id} and the raw body.
func getCert(t *testing.T, e *certEnv, id int64) (certView, []byte) {
	t.Helper()
	resp := e.Do(http.MethodGet, certPath(id), nil)
	data := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /certificates/%d: status %d, want 200 (%s)", id, resp.StatusCode, data)
	}
	var b struct {
		Certificate certView `json:"certificate"`
	}
	unmarshal(t, data, &b)
	return b.Certificate, data
}

// waitAction waits for action a and fails unless it ends with status.
func waitAction(t *testing.T, e *certEnv, a actionView, status, what string) apitest.Action {
	t.Helper()
	got := e.For(t).WaitAction(a.ID)
	if got.Status != status {
		t.Fatalf("%s: action %d %s ended %q %+v, want %s", what, a.ID, a.Command, got.Status, got.Error, status)
	}
	return got
}

// pemOf encodes der as a PEM block of typ.
func pemOf(typ string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}))
}

// issued is a certificate a test made, with its key.
type issued struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM string
	keyPEM  string
}

// makeCert signs a certificate for cn and dns with parent (nil: self-signed).
func makeCert(t *testing.T, cn string, dns []string, isCA bool, parent *issued) issued {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		DNSNames:              dns,
		NotBefore:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	if isCA {
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	parentCert, signer := tmpl, key
	if parent != nil {
		parentCert, signer = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, signer)
	if err != nil {
		t.Fatalf("create certificate %s: %v", cn, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate %s: %v", cn, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return issued{cert: cert, key: key, certPEM: pemOf("CERTIFICATE", der), keyPEM: pemOf("PRIVATE KEY", keyDER)}
}

// fingerprint is the colon-separated SHA-256 of the certificate.
func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, ":")
}

// leaksKey reports whether data holds the base64 body of keyPEM or any
// PEM private key.
func leaksKey(data []byte, keyPEM string) bool {
	lines := strings.Split(strings.TrimSpace(keyPEM), "\n")
	secret := strings.Join(lines[1:len(lines)-1], "")
	flat := strings.NewReplacer(`\n`, "", "\n", "").Replace(string(data))
	return strings.Contains(flat, secret) || strings.Contains(string(data), "PRIVATE KEY")
}

func TestCertificates(t *testing.T) {
	t.Run("CERT-01", func(t *testing.T) {
		e := newCertEnv(t, nil, newTestClock(start))
		root := makeCert(t, "Test Root", nil, true, nil)
		inter := makeCert(t, "Test Intermediate", nil, true, &root)
		leaf := makeCert(t, "upload.test", []string{"upload.test", "www.upload.test"}, false, &inter)
		chain := leaf.certPEM + inter.certPEM

		resp := e.Do(http.MethodPost, "/certificates", map[string]any{
			"name": "uploaded", "type": "uploaded", "certificate": chain, "private_key": leaf.keyPEM,
		})
		data := body(t, resp)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /certificates uploaded: status %d, want 201 (%s)", resp.StatusCode, data)
		}
		if leaksKey(data, leaf.keyPEM) {
			t.Errorf("POST /certificates answer contains the private key")
		}
		var created struct {
			Certificate certView        `json:"certificate"`
			Action      json.RawMessage `json:"action"`
		}
		unmarshal(t, data, &created)
		c := created.Certificate
		if string(created.Action) != "null" {
			t.Errorf("POST /certificates uploaded: action %s, want null", created.Action)
		}
		if c.Status != nil {
			t.Errorf("uploaded certificate: status %+v, want null", c.Status)
		}
		if c.Type != "uploaded" {
			t.Errorf("type %q, want uploaded", c.Type)
		}
		if c.Certificate == nil || *c.Certificate != chain {
			t.Errorf("certificate %v, want the chain as given", c.Certificate)
		}
		if c.Fingerprint == nil || !strings.EqualFold(*c.Fingerprint, fingerprint(leaf.cert)) {
			t.Errorf("fingerprint %v, want %s", c.Fingerprint, fingerprint(leaf.cert))
		}
		if want := []string{"upload.test", "www.upload.test"}; !slices.Equal(c.DomainNames, want) {
			t.Errorf("domain_names %v, want %v", c.DomainNames, want)
		}
		if c.NotValidBefore == nil || !c.NotValidBefore.Equal(leaf.cert.NotBefore) ||
			c.NotValidAfter == nil || !c.NotValidAfter.Equal(leaf.cert.NotAfter) {
			t.Errorf("valid %v to %v, want %v to %v", c.NotValidBefore, c.NotValidAfter, leaf.cert.NotBefore, leaf.cert.NotAfter)
		}

		// The key appears in no answer.
		if _, got := getCert(t, e, c.ID); leaksKey(got, leaf.keyPEM) {
			t.Errorf("GET /certificates/%d contains the private key", c.ID)
		}
		resp = e.Do(http.MethodGet, "/certificates", nil)
		if data := body(t, resp); resp.StatusCode != http.StatusOK || leaksKey(data, leaf.keyPEM) {
			t.Errorf("GET /certificates: status %d, or the answer contains the private key", resp.StatusCode)
		}

		// Bad input.
		other := makeCert(t, "other.test", []string{"other.test"}, false, &inter)
		wantFieldError(t, e.Do(http.MethodPost, "/certificates", map[string]any{
			"name": "wrong-key", "type": "uploaded", "certificate": chain, "private_key": other.keyPEM,
		}), "private_key", "POST /certificates with the key of another certificate")
		wantFieldError(t, e.Do(http.MethodPost, "/certificates", map[string]any{
			"name": "no-cert", "type": "uploaded", "certificate": leaf.keyPEM, "private_key": leaf.keyPEM,
		}), "certificate", "POST /certificates with a PEM that is no certificate")

		// used_by and DELETE while in use.
		e.setUsedBy(c.ID, 42)
		if v, _ := getCert(t, e, c.ID); !reflect.DeepEqual(v.UsedBy, []ref{{ID: 42, Type: "load_balancer"}}) {
			t.Errorf("used_by %+v, want load balancer 42", v.UsedBy)
		}
		wantError(t, e.Do(http.MethodDelete, certPath(c.ID), nil), http.StatusUnprocessableEntity, "resource_in_use",
			"DELETE a certificate a load balancer uses")
		getCert(t, e, c.ID)

		// PUT changes name and labels.
		resp = e.Do(http.MethodPut, certPath(c.ID), map[string]any{"name": "renamed", "labels": map[string]string{"env": "test"}})
		data = body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT /certificates/%d: status %d, want 200 (%s)", c.ID, resp.StatusCode, data)
		}
		if leaksKey(data, leaf.keyPEM) {
			t.Errorf("PUT /certificates/%d answer contains the private key", c.ID)
		}
		if v, _ := getCert(t, e, c.ID); v.Name != "renamed" || !reflect.DeepEqual(v.Labels, map[string]string{"env": "test"}) {
			t.Errorf("after PUT: name %q labels %v, want renamed and env=test", v.Name, v.Labels)
		}
	})

	t.Run("CERT-02", testManaged)
	t.Run("CERT-03", testRenewal)
}
