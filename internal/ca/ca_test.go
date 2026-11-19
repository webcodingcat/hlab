package ca_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/ca"
)

// clock is a settable test clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)}
}

// certs decodes every CERTIFICATE block of data.
func certs(t *testing.T, data []byte) []*x509.Certificate {
	t.Helper()
	var out []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			t.Fatalf("PEM block %q, want CERTIFICATE", block.Type)
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parse certificate: %v", err)
		}
		out = append(out, c)
	}
	return out
}

func openCA(t *testing.T, dir string, c *clock) *ca.CA {
	t.Helper()
	a, err := ca.Open(dir, "hlab CA", c.Now)
	if err != nil {
		t.Fatalf("ca.Open(%s): %v", dir, err)
	}
	return a
}

func crl(t *testing.T, a *ca.CA) (*x509.RevocationList, []byte) {
	t.Helper()
	der, err := a.CRL()
	if err != nil {
		t.Fatalf("CRL: %v", err)
	}
	l, err := x509.ParseRevocationList(der)
	if err != nil {
		t.Fatalf("parse CRL: %v", err)
	}
	return l, der
}

func TestCA(t *testing.T) {
	t.Run("CERT-02", func(t *testing.T) {
		dir := t.TempDir()
		clk := newClock()
		now := clk.Now()
		a := openCA(t, dir, clk)
		for _, f := range []string{"ca.pem", "ca.key"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("after the first Open: %s: %v", f, err)
			}
		}
		again := openCA(t, dir, clk)
		if !bytes.Equal(a.Certificate(), again.Certificate()) {
			t.Errorf("a second Open on the same folder returned another CA certificate")
		}

		roots := certs(t, a.Certificate())
		if len(roots) != 1 {
			t.Fatalf("Certificate holds %d certificates, want 1", len(roots))
		}
		root := roots[0]
		if err := root.CheckSignatureFrom(root); err != nil {
			t.Errorf("CA certificate is not self-signed: %v", err)
		}
		if !root.BasicConstraintsValid || !root.IsCA {
			t.Errorf("CA certificate: basic constraints valid %t, CA %t; want CA:true", root.BasicConstraintsValid, root.IsCA)
		}
		if want := x509.KeyUsageCertSign | x509.KeyUsageCRLSign; root.KeyUsage&want != want {
			t.Errorf("CA key usage %b, want keyCertSign and cRLSign", root.KeyUsage)
		}
		if root.Subject.CommonName != "hlab CA" {
			t.Errorf("CA CN %q, want %q", root.Subject.CommonName, "hlab CA")
		}
		if len(root.CRLDistributionPoints) != 0 {
			t.Errorf("CA has CDP %v, want none", root.CRLDistributionPoints)
		}
		if root.NotBefore.After(now) {
			t.Errorf("CA not valid before %v, after now %v", root.NotBefore, now)
		}
		if d := root.NotAfter.Sub(now.Add(ca.CAValidity)); d < -time.Hour || d > time.Hour {
			t.Errorf("CA not valid after %v, want about now + 10 years (%v)", root.NotAfter, now.Add(ca.CAValidity))
		}

		domains := []string{"example.test", "*.example.test"}
		certPEM, keyPEM, err := a.Issue(domains)
		if err != nil {
			t.Fatalf("Issue(%v): %v", domains, err)
		}
		chain := certs(t, certPEM)
		if len(chain) != 2 {
			t.Fatalf("Issue returned %d certificates, want the leaf and the CA", len(chain))
		}
		leaf := chain[0]
		if !chain[1].Equal(root) {
			t.Errorf("the second certificate of the chain is not the CA")
		}
		pool := x509.NewCertPool()
		pool.AddCert(root)
		inter := x509.NewCertPool()
		for _, c := range chain[1:] {
			inter.AddCert(c)
		}
		for _, name := range []string{"example.test", "www.example.test"} {
			if _, err := leaf.Verify(x509.VerifyOptions{
				Roots: pool, Intermediates: inter, DNSName: name, CurrentTime: now,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}); err != nil {
				t.Errorf("leaf does not verify for %s against the CA: %v", name, err)
			}
		}
		if !slices.Equal(leaf.DNSNames, domains) {
			t.Errorf("leaf DNS SANs %v, want %v", leaf.DNSNames, domains)
		}
		if leaf.Subject.CommonName != "example.test" {
			t.Errorf("leaf CN %q, want example.test", leaf.Subject.CommonName)
		}
		if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
			t.Errorf("leaf extended key usage %v, want serverAuth", leaf.ExtKeyUsage)
		}
		if !slices.Equal(leaf.CRLDistributionPoints, []string{ca.CRLURL}) {
			t.Errorf("leaf CDP %v, want [%s]", leaf.CRLDistributionPoints, ca.CRLURL)
		}
		if !leaf.NotBefore.Equal(now.Add(-time.Hour)) || !leaf.NotAfter.Equal(now.Add(ca.LeafValidity)) {
			t.Errorf("leaf valid %v to %v, want %v to %v (90 days)", leaf.NotBefore, leaf.NotAfter, now.Add(-time.Hour), now.Add(ca.LeafValidity))
		}
		if ca.LeafValidity != 90*24*time.Hour {
			t.Errorf("LeafValidity %v, want 90 days", ca.LeafValidity)
		}
		pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
		if !ok || pub.Curve != elliptic.P256() {
			t.Fatalf("leaf key %T, want ECDSA P-256", leaf.PublicKey)
		}
		block, _ := pem.Decode(keyPEM)
		if block == nil {
			t.Fatalf("key is no PEM: %q", keyPEM)
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			t.Fatalf("key is no PKCS#8 key: %v", err)
		}
		priv, ok := k.(*ecdsa.PrivateKey)
		if !ok || !priv.PublicKey.Equal(pub) {
			t.Errorf("returned key %T does not match the leaf's public key", k)
		}
	})

	t.Run("CERT-04", func(t *testing.T) {
		dir := t.TempDir()
		clk := newClock()
		a := openCA(t, dir, clk)
		root := certs(t, a.Certificate())[0]

		first, _ := crl(t, a)
		if err := first.CheckSignatureFrom(root); err != nil {
			t.Errorf("CRL is not signed by the CA: %v", err)
		}
		if len(first.RevokedCertificateEntries) != 0 {
			t.Errorf("CRL lists %d revoked certificates, want none", len(first.RevokedCertificateEntries))
		}
		if d := first.NextUpdate.Sub(first.ThisUpdate); d != 8*24*time.Hour {
			t.Errorf("CRL nextUpdate - thisUpdate = %v, want 8 days", d)
		}

		clk.advance(7 * 24 * time.Hour)
		if err := a.RenewCRL(); err != nil {
			t.Fatalf("RenewCRL: %v", err)
		}
		renewed, der := crl(t, a)
		if first.Number == nil || renewed.Number == nil || renewed.Number.Cmp(first.Number) <= 0 {
			t.Errorf("CRL number after RenewCRL %v, want greater than %v", renewed.Number, first.Number)
		}
		if !renewed.ThisUpdate.Equal(clk.Now()) {
			t.Errorf("CRL thisUpdate after RenewCRL %v, want now %v", renewed.ThisUpdate, clk.Now())
		}
		if d := renewed.NextUpdate.Sub(renewed.ThisUpdate); d != 8*24*time.Hour {
			t.Errorf("renewed CRL nextUpdate - thisUpdate = %v, want 8 days", d)
		}

		clk.advance(time.Hour)
		again := openCA(t, dir, clk)
		_, stored := crl(t, again)
		if !bytes.Equal(stored, der) {
			t.Errorf("a CA opened again serves another CRL than the stored one")
		}
	})
}
