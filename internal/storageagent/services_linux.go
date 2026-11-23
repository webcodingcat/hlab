//go:build linux

package storageagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// tlsDir holds the wildcard certificate of WebDAV and FTPS:
// storagebox.crt and storagebox.key (0600, root only).
const tlsDir = "/etc/hlab-agent/tls"

// service is a protocol daemon configured at every apply after the
// accounts and passwords.
type service interface {
	Name() string
	Apply(ctx context.Context, doc agentapi.Storage, skipped map[string]bool) error
}

// services are the registered protocol daemons in registration order.
var services []service

// registerService adds s to the services of every apply; each service
// file calls it in its init.
func registerService(s service) {
	services = append(services, s)
}

// applyServices writes the TLS files and runs every registered service; a
// failing service is logged and joined into the error, the others still run.
func applyServices(ctx context.Context, doc agentapi.Storage, skipped map[string]bool) error {
	var errs []error
	if err := writeTLS(doc.TLS); err != nil {
		slog.Error("storage TLS files failed", "err", err)
		errs = append(errs, err)
	}
	for _, s := range services {
		if err := s.Apply(ctx, doc, skipped); err != nil {
			slog.Error("storage service failed", "service", s.Name(), "err", err)
			errs = append(errs, fmt.Errorf("service %s: %w", s.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// writeTLS writes the certificate and the key to tlsDir when they differ
// from the files there. The key is never logged.
func writeTLS(t agentapi.TLS) error {
	if err := os.MkdirAll(tlsDir, 0o755); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(tlsDir, "storagebox.key"), []byte(t.KeyPEM), 0o600); err != nil {
		return fmt.Errorf("write TLS key: %w", err)
	}
	return writeIfChanged(filepath.Join(tlsDir, "storagebox.crt"), []byte(t.CertPEM), 0o644)
}

// writeIfChanged replaces file atomically with data and mode perm when its
// content or mode differs.
func writeIfChanged(file string, data []byte, perm fs.FileMode) error {
	old, err := os.ReadFile(file)
	if err == nil && bytes.Equal(old, data) {
		if fi, err := os.Stat(file); err == nil && fi.Mode().Perm() == perm {
			return nil
		}
	}
	tmp := file + ".hlab"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, file)
}
