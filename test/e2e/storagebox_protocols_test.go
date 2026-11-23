//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// protocolSwitchTimeout is the wait until a changed access setting shows
	// in a protocol daemon.
	protocolSwitchTimeout = 2 * time.Minute
	// davRequestTimeout limits one WebDAV request from the host.
	davRequestTimeout = time.Minute
	// boxLockTimeout is the wait while another row's action locks the box.
	boxLockTimeout = 5 * time.Minute
)

// hetznerLocked is hetznerDo for a request that starts an action on a box
// other rows act on too: while the API answers 423 locked (a running action
// of the box), it tries again until boxLockTimeout.
func (e *env) hetznerLocked(method, path string, body, out any) (int, apiError, error) {
	var status int
	var aerr apiError
	var err error
	werr := waitFor(boxLockTimeout, 2*time.Second, func() (bool, error) {
		status, aerr, err = e.hetznerDo(context.Background(), method, path, body, out)
		return err != nil || status != http.StatusLocked, nil
	})
	if werr != nil && err == nil {
		err = fmt.Errorf("box still locked: %w", werr)
	}
	return status, aerr, err
}

// caFixture is the hlab CA certificate, exported once per run.
var caFixture struct {
	once sync.Once
	pem  []byte
	err  error
}

// hlabCA returns the PEM of the hlab CA through `hlab ca export`.
func hlabCA(t *testing.T) []byte {
	t.Helper()
	caFixture.once.Do(func() {
		file := filepath.Join(harness.tmp, "hlab-ca.pem")
		if r, err := runHlab(context.Background(), time.Minute, "ca", "export", file); err != nil {
			caFixture.err = fmt.Errorf("hlab ca export: %v\n%s", err, r)
			return
		}
		caFixture.pem, caFixture.err = os.ReadFile(file)
	})
	if caFixture.err != nil {
		t.Fatalf("hlab CA: %v", caFixture.err)
	}
	return caFixture.pem
}

// hostHTTPS is an HTTPS client of the host that trusts only the hlab CA.
// Every request opens its own connection: mpm-itk closes a connection that
// is reused for another virtual host.
func hostHTTPS(t *testing.T) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(hlabCA(t)) {
		t.Fatal("the exported hlab CA is no PEM certificate")
	}
	return &http.Client{
		Timeout: davRequestTimeout,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
		},
	}
}

// davDo sends one WebDAV request as account to https://<account.Server>/path
// and returns the status and the body.
func davDo(c *http.Client, account *storageBox, method, path string, body []byte, header map[string]string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), davRequestTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+account.Server+"/"+strings.TrimPrefix(path, "/"), rd)
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth(account.Username, account.Password)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// davOK reports whether status is a success of a WebDAV method.
func davOK(status int) bool {
	return status >= 200 && status <= 299
}

// wslCAFile writes the hlab CA into dir of the distro and returns its path.
func wslCAFile(t *testing.T, w *wslEnv, dir string) string {
	t.Helper()
	f := dir + "/hlab-ca.pem"
	script := "set -e\ncat > '" + f + "' <<'HLAB_CA_EOF'\n" +
		strings.TrimSpace(strings.ReplaceAll(string(hlabCA(t)), "\r", "")) + "\nHLAB_CA_EOF\n"
	if r, err := w.run(context.Background(), time.Minute, nil, script); err != nil {
		t.Fatalf("writing the hlab CA into %s: %v\n%s", w.distro, err, r)
	}
	return f
}

// wslData writes size random bytes into dir/name of the distro and returns
// their SHA-256.
func wslData(t *testing.T, w *wslEnv, dir, name string, size int) string {
	t.Helper()
	script := fmt.Sprintf("set -e\ncd '%s'\nhead -c %d /dev/urandom > '%s'\nsha256sum '%s' | cut -c1-64\n", dir, size, name, name)
	r, err := w.run(context.Background(), time.Minute, nil, script)
	sum := strings.TrimSpace(r.Stdout)
	if err != nil || len(sum) != 64 {
		t.Fatalf("writing %s in %s: %v\n%s", name, w.distro, err, r)
	}
	return sum
}

// hostSum reads remote over SFTP from the host as l and returns its
// SHA-256; it fails the test when the read fails.
func hostSum(t *testing.T, l login, remote string) string {
	t.Helper()
	back := filepath.Join(t.TempDir(), "back.bin")
	if r, err := hostSFTP(l, fmt.Sprintf("get %s %s\n", remote, sftpPath(back))); err != nil {
		t.Errorf("sftp get %s as %s: %v\n%s", remote, l.user, err, r)
		return ""
	}
	b, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// smbclient runs smbclient commands from dir of the distro as account on
// //<account.Server>/<share>. The credentials go into a 0600 file from the
// environment, never into argv. An NT_STATUS in the output is a failure.
func (w *wslEnv) smbclient(dir string, account *storageBox, share, commands string) (result, error) {
	auth := fmt.Sprintf("umask 077\nauth=$(mktemp '%s/smbauth.XXXXXXXX')\ntrap 'rm -f -- \"$a\" \"$auth\"' EXIT\n"+
		"printf 'username=%%s\\npassword=%%s\\n' '%s' \"$HLAB_E2E_PASSWORD\" > \"$auth\"\n", dir, account.Username)
	script := auth + "cat > smb.cmds <<'HLAB_SMB_EOF'\n" + commands + "\nHLAB_SMB_EOF\n" +
		"timeout 120 smbclient '//" + account.Server + "/" + share + "' -A \"$auth\" -m SMB3 < smb.cmds 2>&1\n"
	r, err := w.wslPassword(dir, account, script)
	if err == nil && strings.Contains(r.Stdout+r.Stderr, "NT_STATUS_") {
		err = fmt.Errorf("smbclient reported an NT_STATUS")
	}
	return r, err
}

// lftp runs lftp commands from dir of the distro as account on
// ftp://<account.Server> in passive mode; with caFile set over explicit
// FTPS (control and data) verified against it, else over plain FTP. The
// password goes into a 0600 script file from the environment, never into
// argv. The first failing command ends the run with an error.
func (w *wslEnv) lftp(dir string, account *storageBox, caFile, commands string) (result, error) {
	settings := "set cmd:fail-exit yes\nset net:max-retries 1\nset net:timeout 20\nset ftp:passive-mode yes\n"
	if caFile != "" {
		settings += "set ftp:ssl-force yes\nset ftp:ssl-protect-data yes\nset ssl:verify-certificate yes\nset ssl:ca-file " + caFile + "\n"
	} else {
		settings += "set ftp:ssl-allow no\n"
	}
	script := fmt.Sprintf("umask 077\nf=$(mktemp '%s/lftp.XXXXXXXX')\ntrap 'rm -f -- \"$a\" \"$f\"' EXIT\n", dir) +
		"cat > \"$f\" <<'HLAB_LFTP_EOF'\n" + settings + "HLAB_LFTP_EOF\n" +
		fmt.Sprintf("printf 'open -u %%s,%%s ftp://%%s\\n' '%s' \"$HLAB_E2E_PASSWORD\" '%s' >> \"$f\"\n", account.Username, account.Server) +
		"cat >> \"$f\" <<'HLAB_LFTP_EOF'\n" + commands + "\nbye\nHLAB_LFTP_EOF\n" +
		"timeout 150 lftp -f \"$f\" 2>&1\n"
	return w.wslPassword(dir, account, script)
}

// serverCurl runs curl with args on the server as account; the credentials
// reach curl as a netrc file through stdin, never through argv.
func (e *env) serverCurl(srv *server, account *storageBox, args string) (result, error) {
	netrc := fmt.Sprintf("machine %s login %s password %s\n", account.Server, account.Username, account.Password)
	remote := "umask 077; f=$(mktemp); cat > \"$f\"; curl -sS --max-time 30 --netrc-file \"$f\" " + args + "; rc=$?; rm -f \"$f\"; exit $rc"
	return e.ssh(srv, 2*time.Minute, netrc, remote)
}

// updateAccess sets the access settings at path (a box or a subaccount)
// and waits for the action.
func (e *env) updateAccess(t *testing.T, path string, access map[string]bool) {
	t.Helper()
	var body struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	path += "/actions/update_access_settings"
	status, aerr, err := e.hetznerLocked(http.MethodPost, path, access, &body)
	if err != nil || status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST %s: status %d %s %s %v", path, status, aerr.Code, aerr.Message, err)
	}
	if err := e.waitAction(body.Action.ID, boxActionTimeout); err != nil {
		t.Fatalf("update_access_settings %s: %v", path, err)
	}
}

// boxPath and subPath are the API paths of a box and of its subaccount.
func boxPath(box *storageBox) string { return fmt.Sprintf("/storage_boxes/%d", box.ID) }
func subPath(box, sub *storageBox) string {
	return fmt.Sprintf("/storage_boxes/%d/subaccounts/%d", box.ID, sub.ID)
}

// eventually retries f until it succeeds within boxReadyTimeout; a new box
// or subaccount may need a moment until its daemons serve it.
func eventually(t *testing.T, what string, f func() (result, error)) bool {
	t.Helper()
	var last result
	var lastErr error
	err := waitFor(boxReadyTimeout, 3*time.Second, func() (bool, error) {
		last, lastErr = f()
		return lastErr == nil, nil
	})
	if err != nil {
		t.Errorf("%s: %v (last: %v)\n%s", what, err, lastErr, last)
		return false
	}
	return true
}

// refusedWithin waits until f fails, within protocolSwitchTimeout.
func refusedWithin(t *testing.T, what string, f func() (result, error)) {
	t.Helper()
	var last result
	err := waitFor(protocolSwitchTimeout, 3*time.Second, func() (bool, error) {
		r, err := f()
		last = r
		return err != nil, nil
	})
	if err != nil {
		t.Errorf("%s is still served: %v\n%s", what, err, last)
	}
}

// seedHome creates home in box with seed.txt and returns the local seed.
func seedHome(t *testing.T, box *storageBox, home string) string {
	t.Helper()
	seed := randomFile(t, t.TempDir(), "seed.txt", 4096)
	if r, err := hostSFTP(passwordLogin(box, 22), fmt.Sprintf("mkdir %s\nput %s %s/seed.txt\n", home, sftpPath(seed), home)); err != nil {
		t.Fatalf("box writes %s/seed.txt: %v\n%s", home, err, r)
	}
	return seed
}

// noWrites fails the test when home of box holds an entry starting with
// "ro-" (written by a read-only subaccount) or lacks seed.txt.
func noWrites(t *testing.T, box *storageBox, home string) {
	t.Helper()
	r, err := hostSFTP(passwordLogin(box, 22), "ls -1a "+home+"\n")
	if err != nil {
		t.Fatalf("box lists %s: %v\n%s", home, err, r)
	}
	listing := strings.Fields(sftpListing(r.Stdout))
	seen := false
	for _, f := range listing {
		switch base := filepath.Base(f); {
		case strings.HasPrefix(base, "ro-"):
			t.Errorf("%s holds %s written by the read-only subaccount\n%s", home, f, r)
		case base == "seed.txt":
			seen = true
		}
	}
	if !seen {
		t.Errorf("%s lost seed.txt\n%s", home, r)
	}
}

// testSBOX14: from WSL2 with smbclient, data written to share backup of the
// box and to a subaccount's share named after it are read back over SFTP
// from the host; with samba_enabled off the box's share and then the
// subaccount's share refuse.
func testSBOX14(t *testing.T) {
	e := sharedEnv(t)
	_, w := sharedWSL(t)
	dir := wslWorkDir(t, w)
	box := newTestBox(t, e, "sbox14", "bx11", map[string]bool{"reachable_externally": true, "samba_enabled": true})
	subAccess := map[string]bool{"reachable_externally": true, "samba_enabled": true}
	sub := e.createSubaccount(t, box, "sbox14-sub", subAccess)
	owner := passwordLogin(box, 22)

	sum := wslData(t, w, dir, "box.bin", 1<<20)
	if eventually(t, "smbclient put on //"+box.Server+"/backup", func() (result, error) {
		return w.smbclient(dir, box, "backup", "put box.bin sbox14-box.bin")
	}) {
		if got := hostSum(t, owner, "sbox14-box.bin"); got != sum {
			t.Errorf("sbox14-box.bin over SFTP has SHA-256 %s, written over SMB %s", got, sum)
		}
	}

	subSum := wslData(t, w, dir, "sub.bin", 1<<20)
	if eventually(t, "smbclient put on //"+sub.Server+"/"+sub.Username, func() (result, error) {
		return w.smbclient(dir, sub, sub.Username, "put sub.bin sbox14-sub.bin")
	}) {
		if got := hostSum(t, owner, "sbox14-sub/sbox14-sub.bin"); got != subSum {
			t.Errorf("sbox14-sub/sbox14-sub.bin over SFTP has SHA-256 %s, written over SMB %s", got, subSum)
		}
	}

	// The box and its subaccounts share one UID: after the subaccount's
	// share was used, the box still writes on backup.
	againSum := wslData(t, w, dir, "again.bin", 1<<20)
	if r, err := w.smbclient(dir, box, "backup", "put again.bin sbox14-again.bin"); err != nil {
		t.Errorf("smbclient put on //%s/backup after the subaccount's share was used: %v\n%s", box.Server, err, r)
	} else if got := hostSum(t, owner, "sbox14-again.bin"); got != againSum {
		t.Errorf("sbox14-again.bin over SFTP has SHA-256 %s, written over SMB %s", got, againSum)
	}

	e.updateAccess(t, boxPath(box), map[string]bool{"reachable_externally": true, "samba_enabled": false,
		"ssh_enabled": false, "webdav_enabled": false, "zfs_enabled": false})
	refusedWithin(t, "share backup of "+box.Server+" with samba_enabled off", func() (result, error) {
		return w.smbclient(dir, box, "backup", "ls")
	})

	subAccess["samba_enabled"] = false
	e.updateAccess(t, subPath(box, sub), subAccess)
	refusedWithin(t, "share "+sub.Username+" with samba_enabled off", func() (result, error) {
		return w.smbclient(dir, sub, sub.Username, "ls")
	})
}

// testSBOX12SMB: a read-only subaccount reads over SMB from WSL2 and writes
// nothing.
func testSBOX12SMB(t *testing.T) {
	e, box := sharedBox(t)
	_, w := sharedWSL(t)
	dir := wslWorkDir(t, w)
	seed := seedHome(t, box, "sbox12-smb-ro")
	ro := e.createSubaccount(t, box, "sbox12-smb-ro",
		map[string]bool{"reachable_externally": true, "samba_enabled": true, "readonly": true})

	want, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	if eventually(t, "read-only "+ro.Username+" reads seed.txt over SMB", func() (result, error) {
		return w.smbclient(dir, ro, ro.Username, "get seed.txt seed.txt")
	}) {
		r, err := w.run(context.Background(), time.Minute, nil, "cd '"+dir+"' && sha256sum seed.txt | cut -c1-64\n")
		s := sha256.Sum256(want)
		if got := strings.TrimSpace(r.Stdout); err != nil || got != hex.EncodeToString(s[:]) {
			t.Errorf("seed.txt read over SMB has SHA-256 %q, want %x: %v\n%s", got, s, err, r)
		}
	}

	_ = wslData(t, w, dir, "ro.bin", 4096)
	for what, cmd := range map[string]string{"put": "put ro.bin ro-put.bin", "mkdir": "mkdir ro-dir"} {
		if r, err := w.smbclient(dir, ro, ro.Username, cmd); err == nil {
			t.Errorf("read-only %s: smbclient %s succeeded\n%s", ro.Username, what, r)
		}
	}
	noWrites(t, box, "sbox12-smb-ro")
}

// testSBOX04SMB: over SMB from WSL2 a subaccount without
// reachable_externally is refused and one with it is served.
func testSBOX04SMB(t *testing.T) {
	e, box := sharedBox(t)
	_, w := sharedWSL(t)
	dir := wslWorkDir(t, w)
	open := e.createSubaccount(t, box, "sbox04-smb-open", map[string]bool{"reachable_externally": true, "samba_enabled": true})
	closed := e.createSubaccount(t, box, "sbox04-smb-closed", map[string]bool{"samba_enabled": true})

	if !eventually(t, "share of "+open.Username+" (reachable_externally) from "+w.distro, func() (result, error) {
		return w.smbclient(dir, open, open.Username, "ls")
	}) {
		return
	}
	if r, err := w.smbclient(dir, closed, closed.Username, "ls"); err == nil {
		t.Errorf("share of %s (not reachable_externally) from %s was served\n%s", closed.Username, w.distro, r)
	}
}

// testSBOX15: from the host over HTTPS with the hlab CA trusted, WebDAV PUT,
// MKCOL and PROPFIND work on the box's name and on the subaccount's name,
// the data are read back over SFTP; with webdav_enabled off the box's name
// refuses.
func testSBOX15(t *testing.T) {
	e := sharedEnv(t)
	c := hostHTTPS(t)
	box := newTestBox(t, e, "sbox15", "bx11", map[string]bool{"reachable_externally": true, "webdav_enabled": true})
	sub := e.createSubaccount(t, box, "sbox15-sub", map[string]bool{"reachable_externally": true, "webdav_enabled": true})
	owner := passwordLogin(box, 22)
	dir := t.TempDir()

	for _, a := range []struct {
		name    string
		account *storageBox
		home    string // the account's data below the box's
	}{{"box", box, ""}, {"subaccount", sub, "sbox15-sub/"}} {
		src := randomFile(t, dir, a.name+".bin", 256<<10)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		var status int
		if !eventually(t, "MKCOL /dav/ on "+a.account.Server, func() (result, error) {
			var err error
			status, _, err = davDo(c, a.account, "MKCOL", "dav/", nil, nil)
			if err == nil && !davOK(status) {
				err = fmt.Errorf("status %d", status)
			}
			return result{}, err
		}) {
			continue
		}
		if status, _, err := davDo(c, a.account, http.MethodPut, "dav/"+a.name+".bin", data, nil); err != nil || !davOK(status) {
			t.Errorf("PUT /dav/%s.bin on %s: status %d %v", a.name, a.account.Server, status, err)
			continue
		}
		status, body, err := davDo(c, a.account, "PROPFIND", "dav/", nil, map[string]string{"Depth": "1"})
		if err != nil || status != http.StatusMultiStatus || !strings.Contains(string(body), a.name+".bin") {
			t.Errorf("PROPFIND /dav/ on %s: status %d %v, want 207 listing %s.bin\n%s", a.account.Server, status, err, a.name, body)
		}
		back := filepath.Join(dir, a.name+"-back.bin")
		if r, err := hostSFTP(owner, fmt.Sprintf("get %sdav/%s.bin %s\n", a.home, a.name, sftpPath(back))); err != nil {
			t.Errorf("box reads %sdav/%s.bin over SFTP: %v\n%s", a.home, a.name, err, r)
		} else {
			sameFile(t, src, back, "file written over WebDAV on the "+a.name+"'s name")
		}
	}

	e.updateAccess(t, boxPath(box), map[string]bool{"reachable_externally": true, "samba_enabled": false,
		"ssh_enabled": false, "webdav_enabled": false, "zfs_enabled": false})
	refusedWithin(t, "WebDAV on "+box.Server+" with webdav_enabled off", func() (result, error) {
		status, body, err := davDo(c, box, "PROPFIND", "", nil, map[string]string{"Depth": "1"})
		if err == nil && davOK(status) {
			return result{Stdout: string(body)}, nil
		}
		return result{}, fmt.Errorf("status %d %v", status, err)
	})
}

// testSBOX12WebDAV: a read-only subaccount reads over WebDAV and every write
// (PUT, MKCOL, DELETE) is refused.
func testSBOX12WebDAV(t *testing.T) {
	e, box := sharedBox(t)
	c := hostHTTPS(t)
	seed := seedHome(t, box, "sbox12-dav-ro")
	ro := e.createSubaccount(t, box, "sbox12-dav-ro",
		map[string]bool{"reachable_externally": true, "webdav_enabled": true, "readonly": true})
	want, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}

	var got []byte
	if !eventually(t, "read-only "+ro.Username+" GET /seed.txt over WebDAV", func() (result, error) {
		status, body, err := davDo(c, ro, http.MethodGet, "seed.txt", nil, nil)
		if err == nil && status != http.StatusOK {
			err = fmt.Errorf("status %d", status)
		}
		got = body
		return result{}, err
	}) {
		return
	}
	if !bytes.Equal(got, want) {
		t.Errorf("seed.txt over WebDAV: %d bytes differ from the %d written", len(got), len(want))
	}
	if status, _, err := davDo(c, ro, "PROPFIND", "", nil, map[string]string{"Depth": "1"}); err != nil || status != http.StatusMultiStatus {
		t.Errorf("PROPFIND / as read-only %s: status %d %v, want 207", ro.Username, status, err)
	}

	for _, w := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPut, "ro-put.txt", []byte("written by a read-only subaccount")},
		{"MKCOL", "ro-dir/", nil},
		{http.MethodDelete, "seed.txt", nil},
	} {
		if status, _, err := davDo(c, ro, w.method, w.path, w.body, nil); err == nil && davOK(status) {
			t.Errorf("read-only %s: %s /%s answered %d", ro.Username, w.method, w.path, status)
		}
	}
	noWrites(t, box, "sbox12-dav-ro")
}

// testSBOX04WebDAV: over WebDAV a subaccount without reachable_externally
// is refused from the host and served from a server.
func testSBOX04WebDAV(t *testing.T) {
	e, box := sharedBox(t)
	_, srv := sharedServer(t)
	c := hostHTTPS(t)
	closed := e.createSubaccount(t, box, "sbox04-dav-closed", map[string]bool{"webdav_enabled": true})

	propfind := "-k -X PROPFIND -H 'Depth: 0' -o /dev/null -w '%{http_code}' https://" + closed.Server + "/"
	if !eventually(t, "WebDAV of "+closed.Username+" from server "+srv.Name, func() (result, error) {
		r, err := e.serverCurl(srv, closed, propfind)
		if err == nil && strings.TrimSpace(r.Stdout) != "207" {
			err = fmt.Errorf("status %s, want 207", r.Stdout)
		}
		return r, err
	}) {
		return
	}
	if status, _, err := davDo(c, closed, "PROPFIND", "", nil, map[string]string{"Depth": "0"}); err == nil && davOK(status) {
		t.Errorf("WebDAV of %s (not reachable_externally) from the host answered %d", closed.Username, status)
	}
}

// testSBOX16: from WSL2 with lftp, plain FTP and explicit FTPS (passive)
// upload and download a box's data, read back over SFTP from the host.
func testSBOX16(t *testing.T) {
	_, box := sharedBox(t)
	_, w := sharedWSL(t)
	dir := wslWorkDir(t, w)
	ca := wslCAFile(t, w, dir)

	for _, m := range []struct{ name, ca string }{{"ftp", ""}, {"ftps", ca}} {
		sum := wslData(t, w, dir, m.name+".bin", 1<<20)
		remote := "sbox16-" + m.name + ".bin"
		cmds := fmt.Sprintf("put %s.bin -o %s\nget %s -o %s-back.bin\n!cmp %s.bin %s-back.bin && echo HLAB_SAME\n",
			m.name, remote, remote, m.name, m.name, m.name)
		r, err := w.lftp(dir, box, m.ca, cmds)
		if err != nil {
			t.Errorf("%s upload and download as %s: %v\n%s", m.name, box.Username, err, r)
			continue
		}
		if !strings.Contains(r.Stdout, "HLAB_SAME") {
			t.Errorf("%s: the file downloaded differs from the one uploaded\n%s", m.name, r)
		}
		if got := hostSum(t, passwordLogin(box, 22), remote); got != sum {
			t.Errorf("%s over SFTP has SHA-256 %s, written over %s %s", remote, got, m.name, sum)
		}
	}
}

// testSBOX12FTP: a read-only subaccount downloads over FTP and every upload
// is refused.
func testSBOX12FTP(t *testing.T) {
	e, box := sharedBox(t)
	_, w := sharedWSL(t)
	dir := wslWorkDir(t, w)
	seed := seedHome(t, box, "sbox12-ftp-ro")
	ro := e.createSubaccount(t, box, "sbox12-ftp-ro", map[string]bool{"reachable_externally": true, "readonly": true})
	want, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}

	var r result
	if !eventually(t, "read-only "+ro.Username+" downloads seed.txt over FTP", func() (result, error) {
		var err error
		r, err = w.lftp(dir, ro, "", "get seed.txt -o seed.txt\n!sha256sum seed.txt\n")
		return r, err
	}) {
		return
	}
	if s := sha256.Sum256(want); !strings.Contains(r.Stdout, hex.EncodeToString(s[:])) {
		t.Errorf("seed.txt over FTP differs from the one written, want SHA-256 %x\n%s", s, r)
	}

	_ = wslData(t, w, dir, "ro.bin", 4096)
	if r, err := w.lftp(dir, ro, "", "put ro.bin -o ro-put.bin\n"); err == nil {
		t.Errorf("read-only %s: FTP upload succeeded\n%s", ro.Username, r)
	}
	noWrites(t, box, "sbox12-ftp-ro")
}

// testSBOX04FTP: over FTP a subaccount without reachable_externally gets 530
// from WSL2 and logs in from a server.
func testSBOX04FTP(t *testing.T) {
	e, box := sharedBox(t)
	_, srv := sharedServer(t)
	_, w := sharedWSL(t)
	dir := wslWorkDir(t, w)
	closed := e.createSubaccount(t, box, "sbox04-ftp-closed", map[string]bool{})

	if !eventually(t, "FTP login of "+closed.Username+" from server "+srv.Name, func() (result, error) {
		return e.serverCurl(srv, closed, "--list-only ftp://"+closed.Server+"/")
	}) {
		return
	}
	r, err := w.lftp(dir, closed, "", "ls\n")
	if err == nil {
		t.Errorf("FTP login of %s (not reachable_externally) from %s succeeded\n%s", closed.Username, w.distro, r)
	} else if !strings.Contains(r.Stdout+r.Stderr, "530") {
		t.Errorf("FTP login of %s from %s failed without 530: %v\n%s", closed.Username, w.distro, err, r)
	}
}
