//go:build linux

package storageagent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

func init() { registerService(&webdav{}) }

// webdavSite is the one Apache site of the storage VM.
const webdavSite = "/etc/apache2/sites-enabled/hlab-storage.conf"

// listenDeadline bounds the wait for Apache to listen on the box IPs
// after a reload.
const listenDeadline = 15 * time.Second

// webdav configures Apache with mpm-itk: the site, the passwd files, the
// lock folders and a graceful reload.
type webdav struct {
	started bool   // apache2 was enabled and (re)started by this agent
	tls     string // the certificate and key Apache last loaded
}

func (*webdav) Name() string { return "webdav" }

// Apply writes the passwd files and the site, then reloads Apache when the
// site or the certificate changed and checks that every box IP listens.
// Apache refuses to start without a Listen, so without a box IP it is
// stopped.
func (w *webdav) Apply(ctx context.Context, doc agentapi.Storage, skipped map[string]bool) error {
	files := WebDAVConfig(doc, skipped, tlsDir)
	var ips []string
	gids := map[string]int{}
	boxes := map[string]bool{}
	for _, b := range doc.Boxes {
		if skipped[b.User] {
			continue
		}
		n, err := boxUID(b.User)
		if err != nil {
			continue
		}
		ips = append(ips, b.IP.String())
		boxes[b.User] = true
		gids[b.User] = n
		for _, s := range b.Subaccounts {
			gids[s.User] = n
		}
	}
	if err := writePasswdFiles(files.Passwd, gids); err != nil {
		return err
	}
	if err := ensureLockDirs(boxes); err != nil {
		return err
	}
	changed, err := writeSite(ctx, files.Site, len(ips) > 0)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		w.started = false
		_, err := run(ctx, "systemctl", "disable", "--now", "apache2.service")
		return err
	}
	tls := doc.TLS.CertPEM + doc.TLS.KeyPEM
	switch {
	case !w.started:
		if _, err := run(ctx, "systemctl", "enable", "apache2.service"); err != nil {
			return err
		}
		if _, err := run(ctx, "systemctl", "restart", "apache2.service"); err != nil {
			return err
		}
		w.started = true
	case changed || tls != w.tls:
		if _, err := run(ctx, "systemctl", "reload", "apache2.service"); err != nil {
			return err
		}
	}
	w.tls = tls
	if listening(ctx, ips) {
		return nil
	}
	// A graceful reload may keep the old listeners; a restart binds anew.
	if _, err := run(ctx, "systemctl", "restart", "apache2.service"); err != nil {
		return err
	}
	if !listening(ctx, ips) {
		return fmt.Errorf("apache2 does not listen on 443 of every box IP %v", ips)
	}
	return nil
}

// writePasswdFiles writes one htpasswd file per account, 0640 root and the
// box's group: mpm-itk authenticates as the box user. Files of other
// accounts are removed.
func writePasswdFiles(passwd map[string]string, gids map[string]int) error {
	if err := os.MkdirAll(webdavPasswdDir, 0o755); err != nil {
		return err
	}
	for user, content := range passwd {
		file := filepath.Join(webdavPasswdDir, user)
		if err := writeIfChanged(file, []byte(content), 0o640); err != nil {
			return fmt.Errorf("webdav passwd file of %s: %w", user, err)
		}
		if err := os.Chown(file, 0, gids[user]); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(webdavPasswdDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, ok := passwd[e.Name()]; !ok {
			if err := os.Remove(filepath.Join(webdavPasswdDir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureLockDirs gives every box a lock folder owned by the box user and
// removes the folders of other boxes.
func ensureLockDirs(boxes map[string]bool) error {
	if err := os.MkdirAll(davLockDir, 0o755); err != nil {
		return err
	}
	for box := range boxes {
		n, err := boxUID(box)
		if err != nil {
			return err
		}
		dir := filepath.Join(davLockDir, box)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := os.Chown(dir, n, n); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(davLockDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !boxes[e.Name()] {
			if err := os.RemoveAll(filepath.Join(davLockDir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeSite replaces the site with site when it differs and, with check,
// runs apachectl configtest; a refused site is put back to the old one.
// It reports whether the site changed.
func writeSite(ctx context.Context, site string, check bool) (bool, error) {
	old, err := os.ReadFile(webdavSite)
	if err == nil && string(old) == site {
		return false, nil
	}
	had := err == nil
	if err := writeIfChanged(webdavSite, []byte(site), 0o644); err != nil {
		return false, err
	}
	if !check {
		return true, nil
	}
	if _, err := run(ctx, "apachectl", "configtest"); err != nil {
		if had {
			_ = writeIfChanged(webdavSite, old, 0o644)
		} else {
			_ = os.Remove(webdavSite)
		}
		return false, err
	}
	return true, nil
}

// listening reports whether port 443 of every ip is listening, waiting up
// to listenDeadline.
func listening(ctx context.Context, ips []string) bool {
	deadline := time.Now().Add(listenDeadline)
	for {
		if out, err := run(ctx, "ss", "-ltnH"); err == nil {
			have := map[string]bool{}
			for _, l := range strings.Split(out, "\n") {
				if f := strings.Fields(l); len(f) >= 4 {
					have[f[3]] = true
				}
			}
			all := true
			for _, ip := range ips {
				all = all && have[ip+":443"]
			}
			if all {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
}
