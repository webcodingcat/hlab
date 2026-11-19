// Package ca is the hlab CA that signs the leaf certificates of managed
// certificates (decisions/managed-certificates.md).
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CRLURL is the CRL distribution point of every leaf (Q12).
const CRLURL = "http://api.hetzner.internal/hlab/v1/ca.crl"

const (
	LeafValidity = 90 * 24 * time.Hour
	CAValidity   = 10 * 365 * 24 * time.Hour
	CRLValidity  = 8 * 24 * time.Hour // nextUpdate - thisUpdate
)

const (
	certFile = "ca.pem"
	keyFile  = "ca.key"
	crlFile  = "ca.crl"
)

// CA holds the CA key pair and its current CRL.
type CA struct {
	dir     string
	now     func() time.Time
	key     *ecdsa.PrivateKey
	cert    *x509.Certificate
	certPEM []byte

	mu  sync.Mutex
	crl *x509.RevocationList // nil until the first RenewCRL
	der []byte
}

// Open loads ca.pem, ca.key and ca.crl from dir. If ca.pem or ca.key is
// missing it creates a new CA named name. now nil means time.Now.
func Open(dir, name string, now func() time.Time) (*CA, error) {
	if now == nil {
		now = time.Now
	}
	c := &CA{dir: dir, now: now}
	certPEM, certErr := os.ReadFile(filepath.Join(dir, certFile))
	keyPEM, keyErr := os.ReadFile(filepath.Join(dir, keyFile))
	switch {
	case errors.Is(certErr, fs.ErrNotExist) || errors.Is(keyErr, fs.ErrNotExist):
		if err := c.create(name); err != nil {
			return nil, err
		}
	case certErr != nil:
		return nil, fmt.Errorf("ca: read %s: %w", certFile, certErr)
	case keyErr != nil:
		return nil, fmt.Errorf("ca: read %s: %w", keyFile, keyErr)
	default:
		if err := c.load(certPEM, keyPEM); err != nil {
			return nil, err
		}
	}
	if err := c.loadCRL(); err != nil {
		return nil, err
	}
	return c, nil
}

// create makes a new CA key pair and writes ca.key (0600) and ca.pem.
func (c *CA) create(name string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("ca: generate key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return err
	}
	now := c.now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(CAValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("ca: create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("ca: marshal key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return fmt.Errorf("ca: create %s: %w", c.dir, err)
	}
	if err := os.WriteFile(filepath.Join(c.dir, keyFile), keyPEM, 0o600); err != nil {
		return fmt.Errorf("ca: write %s: %w", keyFile, err)
	}
	if err := os.WriteFile(filepath.Join(c.dir, certFile), certPEM, 0o644); err != nil {
		return fmt.Errorf("ca: write %s: %w", certFile, err)
	}
	return c.load(certPEM, keyPEM)
}

// load parses the CA certificate and key.
func (c *CA) load(certPEM, keyPEM []byte) error {
	cb, _ := pem.Decode(certPEM)
	if cb == nil || cb.Type != "CERTIFICATE" {
		return fmt.Errorf("ca: %s holds no certificate", certFile)
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return fmt.Errorf("ca: parse %s: %w", certFile, err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return fmt.Errorf("ca: %s holds no key", keyFile)
	}
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return fmt.Errorf("ca: parse %s: %w", keyFile, err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || !key.PublicKey.Equal(cert.PublicKey) {
		return fmt.Errorf("ca: %s does not match %s", keyFile, certFile)
	}
	c.cert, c.key = cert, key
	c.certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return nil
}

// Certificate returns the CA certificate as PEM.
func (c *CA) Certificate() []byte {
	return append([]byte(nil), c.certPEM...)
}

// Issue signs a leaf for domains. It returns the leaf PEM followed by the CA
// PEM (the chain), and the key as PKCS#8 PEM.
func (c *CA) Issue(domains []string) (certPEM, keyPEM []byte, err error) {
	if len(domains) == 0 {
		return nil, nil, errors.New("ca: no domains")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: generate key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, nil, err
	}
	now := c.now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domains[0]},
		DNSNames:              append([]string(nil), domains...),
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(LeafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		CRLDistributionPoints: []string{CRLURL},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: issue: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: marshal key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPEM = append(certPEM, c.certPEM...)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// serialNumber returns a random positive serial number of at most 128 bits.
func serialNumber() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("ca: serial number: %w", err)
	}
	return n.Add(n, big.NewInt(1)), nil
}
