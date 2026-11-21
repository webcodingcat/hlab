//go:build windows

package windows

import (
	"context"
	"crypto/sha1" //nolint:gosec // the store finds certificates by their SHA-1 thumbprint
	"encoding/pem"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TrustCA adds (on) or removes (off) the CA certificate in pemData to the
// LocalMachine Trusted Root store, then to the WSL2 distros.
func (h *HostNet) TrustCA(ctx context.Context, pemData []byte, on bool) error {
	der, err := certDER(pemData)
	if err != nil {
		return fmt.Errorf("hostnet: trust CA: %w", err)
	}
	if on {
		err = addRoot(der)
	} else {
		_, err = removeRoot(der)
	}
	if err != nil {
		return fmt.Errorf("hostnet: trust CA: %w", err)
	}
	return h.trustWSL(ctx, pemData, on)
}

// RemoveRootTrust removes the CA certificate in pemData from the
// LocalMachine Trusted Root store and reports whether it was there; an
// absent certificate is no error.
func (h *HostNet) RemoveRootTrust(pemData []byte) (had bool, err error) {
	der, err := certDER(pemData)
	if err != nil {
		return false, fmt.Errorf("hostnet: remove CA trust: %w", err)
	}
	had, err = removeRoot(der)
	if err != nil {
		return had, fmt.Errorf("hostnet: remove CA trust: %w", err)
	}
	return had, nil
}

// certDER returns the DER bytes of the PEM certificate in pemData.
func certDER(pemData []byte) ([]byte, error) {
	block, _ := pem.Decode(pemData)
	if block == nil || block.Type != "CERTIFICATE" || len(block.Bytes) == 0 {
		return nil, errors.New("no PEM certificate")
	}
	return block.Bytes, nil
}

// openRoot opens LocalMachine\ROOT.
func openRoot() (windows.Handle, error) {
	store, err := windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM, 0, 0,
		windows.CERT_SYSTEM_STORE_LOCAL_MACHINE, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("ROOT"))))
	if err != nil {
		return 0, fmt.Errorf("open LocalMachine\\ROOT: %w", err)
	}
	return store, nil
}

// addRoot adds the DER certificate to LocalMachine\ROOT.
func addRoot(der []byte) error {
	store, err := openRoot()
	if err != nil {
		return err
	}
	defer windows.CertCloseStore(store, 0) //nolint:errcheck // nothing to do on a failed close

	cert, err := windows.CertCreateCertificateContext(windows.X509_ASN_ENCODING, &der[0], uint32(len(der)))
	if err != nil {
		return fmt.Errorf("parse certificate: %w", err)
	}
	defer windows.CertFreeCertificateContext(cert) //nolint:errcheck // nothing to do on a failed free
	if err := windows.CertAddCertificateContextToStore(store, cert, windows.CERT_STORE_ADD_REPLACE_EXISTING, nil); err != nil {
		return fmt.Errorf("add to LocalMachine\\ROOT: %w", err)
	}
	return nil
}

// removeRoot removes the DER certificate from LocalMachine\ROOT by its
// thumbprint and reports whether it was there.
func removeRoot(der []byte) (had bool, err error) {
	store, err := openRoot()
	if err != nil {
		return false, err
	}
	defer windows.CertCloseStore(store, 0) //nolint:errcheck // nothing to do on a failed close

	sum := sha1.Sum(der) //nolint:gosec // thumbprint lookup, not a security hash
	blob := windows.CryptHashBlob{Size: uint32(len(sum)), Data: &sum[0]}
	cert, err := windows.CertFindCertificateInStore(store, windows.X509_ASN_ENCODING, 0,
		windows.CERT_FIND_SHA1_HASH, unsafe.Pointer(&blob), nil)
	if err != nil {
		if errors.Is(err, windows.Errno(windows.CRYPT_E_NOT_FOUND)) {
			return false, nil
		}
		return false, fmt.Errorf("find in LocalMachine\\ROOT: %w", err)
	}
	// CertDeleteCertificateFromStore frees cert, also on failure.
	if err := windows.CertDeleteCertificateFromStore(cert); err != nil {
		return true, fmt.Errorf("remove from LocalMachine\\ROOT: %w", err)
	}
	return true, nil
}
