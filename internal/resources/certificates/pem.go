package certificates

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/api"
)

// leafInfo is what the API shows of a certificate, read from its leaf.
type leafInfo struct {
	Fingerprint string
	DomainNames []string
	NotBefore   time.Time
	NotAfter    time.Time
}

// parseUpload checks an uploaded chain and key: every PEM block of
// certPEM must be a certificate, the first one is the leaf, and keyPEM must
// be the PKCS#1, PKCS#8 or EC key of that leaf.
func parseUpload(certPEM, keyPEM string) (leafInfo, error) {
	chain, err := parseChain([]byte(certPEM))
	if err != nil {
		return leafInfo{}, &api.FieldError{Field: "certificate", Message: err.Error()}
	}
	key, err := parseKey([]byte(keyPEM))
	if err != nil {
		return leafInfo{}, &api.FieldError{Field: "private_key", Message: err.Error()}
	}
	pub, ok := key.Public().(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !pub.Equal(chain[0].PublicKey) {
		return leafInfo{}, &api.FieldError{Field: "private_key", Message: "private key does not match the certificate"}
	}
	return readLeaf(chain[0]), nil
}

// parseChain decodes the PEM certificates of data in order.
func parseChain(data []byte) ([]*x509.Certificate, error) {
	var chain []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("PEM block %q is no certificate", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("invalid certificate: %v", err)
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 || len(bytes.TrimSpace(rest)) > 0 {
		return nil, errors.New("certificate must be a PEM certificate chain")
	}
	return chain, nil
}

// parseKey decodes a PKCS#1, PKCS#8 or EC private key in PEM.
func parseKey(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("private_key must be a PEM private key")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if s, ok := k.(crypto.Signer); ok {
			return s, nil
		}
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("private_key is no PKCS#1, PKCS#8 or EC private key")
}

// readLeaf returns the colon-separated SHA-256 fingerprint, the domain
// names (the DNS SANs, else the CN) and the validity of cert.
func readLeaf(cert *x509.Certificate) leafInfo {
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	domains := cert.DNSNames
	if len(domains) == 0 && cert.Subject.CommonName != "" {
		domains = []string{cert.Subject.CommonName}
	}
	if domains == nil {
		domains = []string{}
	}
	return leafInfo{
		Fingerprint: strings.Join(parts, ":"),
		DomainNames: domains,
		NotBefore:   cert.NotBefore.UTC(),
		NotAfter:    cert.NotAfter.UTC(),
	}
}
