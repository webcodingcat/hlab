//go:build windows

package cloud

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
	"github.com/webcodingcat/hlab/internal/store"
)

const (
	// storageTLSDomain is the wildcard name of the storage VM's WebDAV
	// and FTPS certificate; the services select the account by SNI.
	storageTLSDomain = "*.storagebox.hetzner.internal"
	// storageTLSCertKey and storageTLSKeyKey keep the certificate (leaf
	// plus CA chain) and its PKCS#8 key in kv.
	storageTLSCertKey = "storage.tls_cert"
	storageTLSKeyKey  = "storage.tls_key"
	// storageTLSRenewBefore renews the certificate this long before it
	// expires.
	storageTLSRenewBefore = 30 * 24 * time.Hour
	// storageTLSCheckEvery is how often the cloud checks whether the
	// certificate is due for renewal.
	storageTLSCheckEvery = 24 * time.Hour
)

// storageTLS returns the storage VM's wildcard certificate kept in kv. It
// issues a new one from the hlab CA when none is kept, the kept one does
// not parse, or it expires within storageTLSRenewBefore.
func (c *Cloud) storageTLS() (agentapi.TLS, error) {
	var out agentapi.TLS
	err := c.st.Tx(func(tx store.Tx) error {
		certPEM, err := getKVOptional(tx, storageTLSCertKey)
		if err != nil {
			return err
		}
		keyPEM, err := getKVOptional(tx, storageTLSKeyKey)
		if err != nil {
			return err
		}
		if storageTLSValid(certPEM, keyPEM, time.Now()) {
			out = agentapi.TLS{CertPEM: certPEM, KeyPEM: keyPEM}
			return nil
		}
		cert, key, err := c.ca.Issue([]string{storageTLSDomain})
		if err != nil {
			return err
		}
		if err := store.SetKV(tx, storageTLSCertKey, string(cert)); err != nil {
			return err
		}
		if err := store.SetKV(tx, storageTLSKeyKey, string(key)); err != nil {
			return err
		}
		out = agentapi.TLS{CertPEM: string(cert), KeyPEM: string(key)}
		return nil
	})
	if err != nil {
		return agentapi.TLS{}, fmt.Errorf("storage TLS certificate: %w", err)
	}
	return out, nil
}

// getKVOptional returns the kv value of key, "" when it is missing.
func getKVOptional(tx store.Tx, key string) (string, error) {
	v, err := store.GetKV(tx, key)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return v, err
}

// storageTLSValid reports whether the pair parses, matches and is valid
// for longer than storageTLSRenewBefore after now.
func storageTLSValid(certPEM, keyPEM string, now time.Time) bool {
	if certPEM == "" || keyPEM == "" {
		return false
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return false
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return false
	}
	return now.Add(storageTLSRenewBefore).Before(leaf.NotAfter)
}

// storageTLSDue reports whether storage boxes exist and storageTLS would
// issue a new certificate.
func (c *Cloud) storageTLSDue() (bool, error) {
	var due bool
	err := c.st.Tx(func(tx store.Tx) error {
		boxes, err := storageboxes.Boxes(tx)
		if err != nil || len(boxes) == 0 {
			return err
		}
		certPEM, err := getKVOptional(tx, storageTLSCertKey)
		if err != nil {
			return err
		}
		keyPEM, err := getKVOptional(tx, storageTLSKeyKey)
		if err != nil {
			return err
		}
		due = !storageTLSValid(certPEM, keyPEM, time.Now())
		return nil
	})
	return due, err
}

// renewStorageTLS pushes the storage document again when the certificate
// is due, so the renewed one reaches the storage VM; it does nothing
// otherwise.
func (c *Cloud) renewStorageTLS(ctx context.Context) {
	due, err := c.storageTLSDue()
	if err != nil {
		slog.Error("check the storage TLS certificate", "err", err)
		return
	}
	if !due {
		return
	}
	if err := c.applyStorage(ctx); err != nil {
		slog.Error("renew the storage TLS certificate", "err", err)
		return
	}
	slog.Info("storage TLS certificate renewed")
}
