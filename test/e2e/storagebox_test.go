//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// hetznerEndpoint is the Hetzner API (Storage Boxes).
	hetznerEndpoint = "http://api.hetzner.internal/hetzner/v1"
	// storageVM is the Hyper-V name of the storage VM of the dev profile.
	storageVM = vmPrefix + "-storage"
	// boxCreateTimeout covers the first box, which creates the storage VM.
	boxCreateTimeout = 15 * time.Minute
	boxActionTimeout = 5 * time.Minute
	// boxReadyTimeout is the wait until a new box answers on port 22.
	boxReadyTimeout = 3 * time.Minute
	// transferTimeout limits one sftp, scp, rsync or borg run.
	transferTimeout = 3 * time.Minute
)

// storageBox is a box or a subaccount as the API describes it, with the
// password the test gave it.
type storageBox struct {
	ID       int64
	Username string
	Server   string // u<n>[-sub<m>].storagebox.hetzner.internal
	Password string
}

// boxFixture is the one box SBOX-03, SBOX-04, SBOX-11, SBOX-12 and SBOX-13
// share: reachable_externally and ssh_enabled on, the run's SSH key given at
// creation.
var boxFixture struct {
	once sync.Once
	box  *storageBox
	err  error
}

// sharedBox creates the shared box once and waits until it answers on
// port 22 from the host.
func sharedBox(t *testing.T) (*env, *storageBox) {
	t.Helper()
	e := sharedEnv(t)
	boxFixture.once.Do(func() {
		key, err := os.ReadFile(e.pubKey)
		if err != nil {
			boxFixture.err = err
			return
		}
		boxFixture.box, boxFixture.err = e.createStorageBox(harness.runID+"-sbox", "bx11",
			map[string]bool{"reachable_externally": true, "ssh_enabled": true},
			[]string{strings.TrimSpace(string(key))})
		if boxFixture.err != nil {
			return
		}
		boxFixture.err = waitFor(boxReadyTimeout, 3*time.Second, func() (bool, error) {
			_, err := hostBanner(boxFixture.box.Server)
			return err == nil, nil
		})
		if boxFixture.err != nil {
			boxFixture.err = fmt.Errorf("box %s does not answer on port 22 from the host: %w", boxFixture.box.Server, boxFixture.err)
		}
	})
	if boxFixture.err != nil {
		t.Fatalf("shared storage box: %v", boxFixture.err)
	}
	return e, boxFixture.box
}

// releaseStorageBoxFixture deletes the shared box; the delete of the last
// box also removes the storage VM.
func releaseStorageBoxFixture(t *testing.T) {
	if boxFixture.box == nil {
		return
	}
	if err := envVal.deleteStorageBox(boxFixture.box.ID); err != nil {
		t.Errorf("deleting the shared storage box: %v", err)
	}
	boxFixture.box = nil
}

// hetznerDo is apiDo against the Hetzner API (Storage Boxes).
func (e *env) hetznerDo(ctx context.Context, method, path string, body, out any) (int, apiError, error) {
	return e.apiDoAt(ctx, hetznerEndpoint, method, path, body, out)
}

// boxPassword returns a fresh password following Hetzner's policy.
func boxPassword() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return "E2e-" + hex.EncodeToString(b) + "-Zq9"
}

// createStorageBox creates a box in serverLocation and waits for its
// create_storage_box action. Once the API accepted the create, the box is
// returned also with an error, so the caller can delete it.
func (e *env) createStorageBox(name, boxType string, access map[string]bool, sshKeys []string) (*storageBox, error) {
	pw := boxPassword()
	body := map[string]any{
		"name":             name,
		"location":         serverLocation,
		"storage_box_type": boxType,
		"password":         pw,
		"access_settings":  access,
	}
	if len(sshKeys) > 0 {
		body["ssh_keys"] = sshKeys
	}
	var created struct {
		StorageBox struct {
			ID int64 `json:"id"`
		} `json:"storage_box"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodPost, "/storage_boxes", body, &created)
	if err != nil {
		return nil, fmt.Errorf("POST /storage_boxes %s: %w", name, err)
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("POST /storage_boxes %s: status %d %s: %s", name, status, aerr.Code, aerr.Message)
	}
	// From here on the box exists, so the caller can delete it.
	b := &storageBox{ID: created.StorageBox.ID, Password: pw}
	if err := e.waitAction(created.Action.ID, boxCreateTimeout); err != nil {
		return b, fmt.Errorf("create_storage_box %s: %w", name, err)
	}
	var got struct {
		StorageBox struct {
			Username *string `json:"username"`
			Server   *string `json:"server"`
		} `json:"storage_box"`
	}
	path := fmt.Sprintf("/storage_boxes/%d", created.StorageBox.ID)
	status, aerr, err = e.hetznerDo(context.Background(), http.MethodGet, path, nil, &got)
	if err != nil || status != http.StatusOK {
		return b, fmt.Errorf("GET %s: status %d %s %v", path, status, aerr.Code, err)
	}
	if got.StorageBox.Username == nil || got.StorageBox.Server == nil {
		return b, fmt.Errorf("GET %s: username or server null after create_storage_box", path)
	}
	b.Username, b.Server = *got.StorageBox.Username, *got.StorageBox.Server
	return b, nil
}

// newTestBox creates a box of the row's own, deleted when the row ends.
func newTestBox(t *testing.T, e *env, suffix, boxType string, access map[string]bool) *storageBox {
	t.Helper()
	b, err := e.createStorageBox(harness.runID+"-"+suffix, boxType, access, nil)
	if b != nil {
		t.Cleanup(func() {
			if err := e.deleteStorageBox(b.ID); err != nil {
				t.Error(err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// deleteStorageBox deletes the box and waits for its action; a box already
// gone is fine.
func (e *env) deleteStorageBox(id int64) error {
	var body struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	path := fmt.Sprintf("/storage_boxes/%d", id)
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodDelete, path, nil, &body)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("DELETE %s: status %d %s: %s", path, status, aerr.Code, aerr.Message)
	}
	return e.waitAction(body.Action.ID, boxActionTimeout)
}

// hostBanner connects from the host to port 22 of host and returns the
// first bytes the server sends.
func hostBanner(host string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, "22"))
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 7)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	if s := string(buf[:n]); strings.HasPrefix(s, "SSH-") {
		return s, nil
	}
	return "", fmt.Errorf("port 22 of %s sent %q, not an SSH banner", host, buf[:n])
}

// login is the account a host sftp or scp uses: with the password, or with
// the run's key when key is set.
type login struct {
	user, server, password, key string
	port                        int
}

// passwordLogin logs in as b with its password on port.
func passwordLogin(b *storageBox, port int) login {
	return login{user: b.Username, server: b.Server, password: b.Password, port: port}
}

// remote is the scp spelling of path on the account.
func (l login) remote(path string) string {
	return l.user + "@" + l.server + ":" + path
}

// hostOpenSSH runs the Windows OpenSSH tool (sftp.exe or scp.exe) as l with
// args before the tool's own arguments; a password login answers through
// askpassEnviron, a key login offers only the key. Each run gets a fresh
// known_hosts file: every box shares the storage VM's host key.
func hostOpenSSH(l login, tool string, timeout time.Duration, args func(dir string) ([]string, error)) (result, error) {
	dir, err := os.MkdirTemp(harness.tmp, "sbox-")
	if err != nil {
		return result{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var environ []string
	opts := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + filepath.Join(dir, "known_hosts"),
		"-o", "ConnectTimeout=10",
		"-o", "LogLevel=ERROR",
		"-o", "IdentitiesOnly=yes",
		"-P", fmt.Sprint(l.port),
	}
	if l.key != "" {
		opts = append([]string{"-o", "BatchMode=yes", "-o", "PreferredAuthentications=publickey", "-i", l.key}, opts...)
	} else {
		if environ, err = askpassEnviron(dir, l.password); err != nil {
			return result{}, err
		}
		// BatchMode=no comes before sftp's own -b, which sets BatchMode=yes;
		// ssh keeps the first value.
		opts = append([]string{"-o", "BatchMode=no", "-o", "PubkeyAuthentication=no",
			"-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "NumberOfPasswordPrompts=1"}, opts...)
	}
	rest, err := args(dir)
	if err != nil {
		return result{}, err
	}
	return command(context.Background(), timeout, environ, "", systemTool(tool), append(opts, rest...)...)
}

// hostSFTP runs the sftp batch as l from the host; the batch stops at the
// first failing command not prefixed with "-".
func hostSFTP(l login, batch string) (result, error) {
	return hostOpenSSH(l, "sftp.exe", transferTimeout, func(dir string) ([]string, error) {
		f := filepath.Join(dir, "batch")
		if err := os.WriteFile(f, []byte(batch), 0o600); err != nil {
			return nil, err
		}
		return []string{"-b", f, l.user + "@" + l.server}, nil
	})
}

// hostSCP runs scp from the host as l with the given source and target.
func hostSCP(l login, args ...string) (result, error) {
	return hostOpenSSH(l, "scp.exe", transferTimeout, func(string) ([]string, error) { return args, nil })
}

// sftpPath quotes a local path for an sftp batch.
func sftpPath(p string) string {
	return `"` + filepath.ToSlash(p) + `"`
}

// randomFile writes size random bytes into a new file of dir.
func randomFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	b := make([]byte, size)
	_, _ = rand.Read(b)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// sameFile fails the test when the two files differ.
func sameFile(t *testing.T, want, got, what string) {
	t.Helper()
	a, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("%s: %d bytes read back differ from the %d bytes written", what, len(b), len(a))
	}
}

// testSBOX03: the first storage box creates the storage VM; on port 22 of
// u<n>.storagebox.hetzner.internal, sftp and scp from the host write and
// read the box's data with its password and with the key given at creation.
func testSBOX03(t *testing.T) {
	e, box := sharedBox(t)
	if ok, err := vmExists(storageVM); err != nil || !ok {
		t.Errorf("storage VM %s after the first box: exists %v, %v", storageVM, ok, err)
	}
	if want := box.Username + ".storagebox.hetzner.internal"; box.Server != want {
		t.Errorf("server %q, want %q", box.Server, want)
	}

	dir := t.TempDir()
	logins := []struct {
		name string
		l    login
	}{
		{"password", passwordLogin(box, 22)},
		{"key", login{user: box.Username, server: box.Server, key: e.key, port: 22}},
	}
	if r, err := hostSFTP(logins[0].l, "-mkdir sbox03\n"); err != nil {
		t.Fatalf("sftp mkdir sbox03: %v\n%s", err, r)
	}
	for _, lg := range logins {
		src := randomFile(t, dir, lg.name+".bin", 256<<10)

		back := filepath.Join(dir, lg.name+"-sftp-back.bin")
		batch := fmt.Sprintf("put %s sbox03/sftp-%s.bin\nget sbox03/sftp-%s.bin %s\n", sftpPath(src), lg.name, lg.name, sftpPath(back))
		if r, err := hostSFTP(lg.l, batch); err != nil {
			t.Errorf("sftp with the %s: %v\n%s", lg.name, err, r)
		} else {
			sameFile(t, src, back, "sftp with the "+lg.name)
		}

		back = filepath.Join(dir, lg.name+"-scp-back.bin")
		remote := lg.l.remote("sbox03/scp-" + lg.name + ".bin")
		if r, err := hostSCP(lg.l, src, remote); err != nil {
			t.Errorf("scp upload with the %s: %v\n%s", lg.name, err, r)
			continue
		}
		if r, err := hostSCP(lg.l, remote, back); err != nil {
			t.Errorf("scp download with the %s: %v\n%s", lg.name, err, r)
			continue
		}
		sameFile(t, src, back, "scp with the "+lg.name)
	}
}

// testSBOX04: the boxes are reachable from a server; from the host and WSL2
// a second box without reachable_externally is not reachable, the shared
// box with it is.
func testSBOX04(t *testing.T) {
	e, shared := sharedBox(t)
	_, srv := sharedServer(t)
	_, w := sharedWSL(t)
	private := newTestBox(t, e, "sbox04", "bx11", map[string]bool{"ssh_enabled": true})

	banner := func(host string) string {
		return "getent hosts " + host + " >/dev/null || exit 3\n" +
			"timeout 10 bash -c 'exec 3<>/dev/tcp/" + host + "/22; head -c 7 <&3'\n"
	}

	for _, b := range []*storageBox{shared, private} {
		var last result
		err := waitFor(boxReadyTimeout, 3*time.Second, func() (bool, error) {
			r, err := e.ssh(srv, time.Minute, "", strings.TrimSpace(strings.ReplaceAll(banner(b.Server), "\n", "; ")))
			last = r
			return err == nil && strings.HasPrefix(r.Stdout, "SSH-"), nil
		})
		if err != nil {
			t.Errorf("port 22 of %s from server %s: %v\n%s", b.Server, srv.Name, err, last)
		}
	}

	if _, err := hostBanner(shared.Server); err != nil {
		t.Errorf("port 22 of %s (reachable_externally) from the host: %v", shared.Server, err)
	}
	if r, err := w.run(context.Background(), time.Minute, nil, banner(shared.Server)); err != nil || !strings.HasPrefix(r.Stdout, "SSH-") {
		t.Errorf("port 22 of %s (reachable_externally) from %s: %v\n%s", shared.Server, w.distro, err, r)
	}

	if addrs, err := net.DefaultResolver.LookupHost(context.Background(), private.Server); err != nil || len(addrs) == 0 {
		t.Fatalf("%s does not resolve on the host: %v", private.Server, err)
	}
	if s, err := hostBanner(private.Server); err == nil {
		t.Errorf("port 22 of %s (not reachable_externally) from the host answered %q", private.Server, s)
	}
	r, err := w.run(context.Background(), time.Minute, nil, banner(private.Server))
	switch {
	case r.ExitCode == 3:
		t.Errorf("%s does not resolve in %s\n%s", private.Server, w.distro, r)
	case err == nil || strings.HasPrefix(r.Stdout, "SSH-"):
		t.Errorf("port 22 of %s (not reachable_externally) from %s answered\n%s", private.Server, w.distro, r)
	}
}

// testSBOX11: folders created over SFTP appear in
// GET /storage_boxes/{id}/folders, also below a path.
func testSBOX11(t *testing.T) {
	e, box := sharedBox(t)
	batch := "mkdir sbox11\nmkdir sbox11/beta\nmkdir sbox11/alpha\nmkdir sbox11/alpha/inner\n"
	if r, err := hostSFTP(passwordLogin(box, 22), batch); err != nil {
		t.Fatalf("sftp mkdir: %v\n%s", err, r)
	}

	folders := func(path string) []string {
		t.Helper()
		p := fmt.Sprintf("/storage_boxes/%d/folders", box.ID)
		if path != "" {
			p += "?path=" + url.QueryEscape(path)
		}
		var body struct {
			Folders []string `json:"folders"`
		}
		status, aerr, err := e.hetznerDo(context.Background(), http.MethodGet, p, nil, &body)
		if err != nil || status != http.StatusOK {
			t.Fatalf("GET %s: status %d %s %v", p, status, aerr.Code, err)
		}
		return body.Folders
	}
	if got := folders(""); !slices.Contains(got, "sbox11") {
		t.Errorf("folders of the box %v, want sbox11 among them", got)
	}
	if got, want := folders("sbox11"), []string{"alpha", "beta"}; !slices.Equal(got, want) {
		t.Errorf("folders below sbox11 %v, want %v", got, want)
	}
	if got, want := folders("sbox11/alpha"), []string{"inner"}; !slices.Equal(got, want) {
		t.Errorf("folders below sbox11/alpha %v, want %v", got, want)
	}
}
