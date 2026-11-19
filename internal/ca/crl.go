package ca

import (
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
)

// loadCRL reads ca.crl when it exists; its CRL number is the base of the
// next RenewCRL.
func (c *CA) loadCRL() error {
	der, err := os.ReadFile(filepath.Join(c.dir, crlFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ca: read %s: %w", crlFile, err)
	}
	l, err := x509.ParseRevocationList(der)
	if err != nil {
		return fmt.Errorf("ca: parse %s: %w", crlFile, err)
	}
	c.crl, c.der = l, der
	return nil
}

// RenewCRL writes a new empty CRL (number +1, thisUpdate now, nextUpdate
// now+CRLValidity) to ca.crl.
func (c *CA) RenewCRL() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renewLocked()
}

func (c *CA) renewLocked() error {
	number := big.NewInt(1)
	if c.crl != nil && c.crl.Number != nil {
		number.Add(c.crl.Number, big.NewInt(1))
	}
	now := c.now()
	tmpl := &x509.RevocationList{
		Number:     number,
		ThisUpdate: now,
		NextUpdate: now.Add(CRLValidity),
	}
	der, err := x509.CreateRevocationList(rand.Reader, tmpl, c.cert, c.key)
	if err != nil {
		return fmt.Errorf("ca: create CRL: %w", err)
	}
	l, err := x509.ParseRevocationList(der)
	if err != nil {
		return fmt.Errorf("ca: parse CRL: %w", err)
	}
	if err := os.WriteFile(filepath.Join(c.dir, crlFile), der, 0o644); err != nil {
		return fmt.Errorf("ca: write %s: %w", crlFile, err)
	}
	c.crl, c.der = l, der
	return nil
}

// CRL returns the current CRL as DER (RenewCRL first if none exists yet).
func (c *CA) CRL() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.der == nil {
		if err := c.renewLocked(); err != nil {
			return nil, err
		}
	}
	return append([]byte(nil), c.der...), nil
}
