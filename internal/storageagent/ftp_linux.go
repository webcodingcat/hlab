//go:build linux

package storageagent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

func init() { registerService(&ftp{}) }

// vsftpdPAM is vsftpd's PAM service; pamAccessLine is the rule added to it.
const (
	vsftpdPAM     = "/etc/pam.d/vsftpd"
	pamAccessLine = "account required pam_access.so accessfile=" + vsftpdAccessCfg
)

// ftp configures vsftpd: the configuration, the per-account files, the
// pam_access rules and a reload.
type ftp struct {
	started bool   // vsftpd was enabled and (re)started by this agent
	tls     string // the certificate and key vsftpd last loaded
}

func (*ftp) Name() string { return "ftp" }

// Apply writes the files, adds pam_access to vsftpd's PAM service and
// reloads vsftpd (SIGHUP) when its configuration or the certificate
// changed. The per-account files and the pam_access rules are read at
// every login and need no reload.
func (f *ftp) Apply(ctx context.Context, doc agentapi.Storage, skipped map[string]bool) error {
	files := FTPConfig(doc, skipped, tlsDir)
	if err := writeFTPUsers(files.Users); err != nil {
		return err
	}
	if err := writeIfChanged(vsftpdAccessCfg, []byte(files.Access), 0o644); err != nil {
		return err
	}
	if err := addPAMAccess(); err != nil {
		return err
	}
	old, err := os.ReadFile(vsftpdConf)
	changed := err != nil || string(old) != files.Conf
	if err := writeIfChanged(vsftpdConf, []byte(files.Conf), 0o644); err != nil {
		return err
	}
	tls := doc.TLS.CertPEM + doc.TLS.KeyPEM
	switch {
	case !f.started:
		if _, err := run(ctx, "systemctl", "enable", "vsftpd.service"); err != nil {
			return err
		}
		if _, err := run(ctx, "systemctl", "restart", "vsftpd.service"); err != nil {
			return err
		}
		f.started = true
	case changed || tls != f.tls:
		if _, err := run(ctx, "systemctl", "reload", "vsftpd.service"); err != nil {
			return err
		}
	}
	f.tls = tls
	return nil
}

// writeFTPUsers writes one user_config_dir file per account and removes
// the files of other accounts.
func writeFTPUsers(users map[string]string) error {
	if err := os.MkdirAll(vsftpdUsersDir, 0o755); err != nil {
		return err
	}
	for user, content := range users {
		if err := writeIfChanged(filepath.Join(vsftpdUsersDir, user), []byte(content), 0o644); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(vsftpdUsersDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, ok := users[e.Name()]; !ok {
			if err := os.Remove(filepath.Join(vsftpdUsersDir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// addPAMAccess puts pamAccessLine into vsftpd's PAM service before the
// common-account include, once; without that include it goes last.
func addPAMAccess() error {
	data, err := os.ReadFile(vsftpdPAM)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var out []string
	added := false
	for _, l := range lines {
		if strings.TrimSpace(l) == pamAccessLine {
			return nil
		}
		if !added && strings.HasPrefix(strings.TrimSpace(l), "@include common-account") {
			out = append(out, pamAccessLine)
			added = true
		}
		out = append(out, l)
	}
	if !added {
		out = append(out, pamAccessLine)
	}
	return writeIfChanged(vsftpdPAM, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}
