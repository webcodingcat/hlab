//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// managementPipe is the management API of the dev profile.
const managementPipe = `\\.\pipe\hlab-dev`

// wslWorkDir creates a working directory for the row in the distro's
// temporary directory of the run; it is deleted when the row ends.
func wslWorkDir(t *testing.T, w *wslEnv) string {
	t.Helper()
	r, err := w.run(context.Background(), time.Minute, nil, "set -e\nmktemp -d '"+w.dir+"/sbox.XXXXXXXX'\n")
	d := strings.TrimSpace(r.Stdout)
	if err != nil || !strings.HasPrefix(d, "/") {
		t.Fatalf("mktemp in %s: %v\n%s", w.distro, err, r)
	}
	t.Cleanup(func() {
		if r, err := w.run(context.Background(), time.Minute, nil, "rm -rf -- '"+d+"'\n"); err != nil {
			t.Errorf("removing %s in %s: %v\n%s", d, w.distro, err, r)
		}
	})
	return d
}

// wslPassword runs script with bash in dir of the distro as a login with
// password. OpenSSH (ssh, sftp, scp and the ssh of rsync and borg) asks a
// temporary askpass script that prints the password from the environment
// handed over through WSLENV; the script never holds the password and is
// deleted when the script ends. The script finds the ssh options of a
// password login in $SSHOPTS and the account in $LOGIN.
func (w *wslEnv) wslPassword(dir string, account *storageBox, script string) (result, error) {
	var environ []string
	wslenv := "HLAB_E2E_PASSWORD/u"
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.EqualFold(k, "WSLENV"):
			if v != "" {
				wslenv = v + ":" + wslenv
			}
			continue
		case strings.EqualFold(k, "HLAB_E2E_PASSWORD"):
			continue
		}
		environ = append(environ, kv)
	}
	environ = append(environ, "WSLENV="+wslenv, "HLAB_E2E_PASSWORD="+account.Password)
	prelude := "set -e\ncd '" + dir + "'\n" +
		"a=$(mktemp '" + dir + "/askpass.XXXXXXXX')\ntrap 'rm -f -- \"$a\"' EXIT\n" +
		"cat > \"$a\" <<'HLAB_ASKPASS_EOF'\n#!/bin/sh\nprintf '%s\\n' \"$HLAB_E2E_PASSWORD\"\nHLAB_ASKPASS_EOF\n" +
		"chmod 700 \"$a\"\n" +
		"export SSH_ASKPASS=\"$a\" SSH_ASKPASS_REQUIRE=force DISPLAY=hlab-e2e:0\n" +
		"SSHOPTS='-o BatchMode=no -o PubkeyAuthentication=no -o IdentitiesOnly=yes" +
		" -o PreferredAuthentications=password,keyboard-interactive -o NumberOfPasswordPrompts=1" +
		" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o LogLevel=ERROR'\n" +
		"LOGIN='" + account.Username + "@" + account.Server + "'\n"
	return w.run(context.Background(), transferTimeout, environ, prelude+script)
}

// testSBOX13: from WSL2, port 23 serves sftp, scp, rsync and borg (init,
// create, list) on data written over port 22; with ssh_enabled off port 23
// refuses.
func testSBOX13(t *testing.T) {
	e, box := sharedBox(t)
	_, w := sharedWSL(t)
	dir := wslWorkDir(t, w)

	steps := []struct{ name, script string }{
		{"sftp on port 22 writes", "head -c 1048576 /dev/urandom > data.bin\n" +
			"printf -- '-mkdir sbox13\\nput data.bin sbox13/data.bin\\n' > b22\n" +
			"sftp $SSHOPTS -P 22 -b b22 \"$LOGIN\"\n"},
		{"sftp on port 23", "printf 'get sbox13/data.bin sftp23.bin\\n' > b23\n" +
			"sftp $SSHOPTS -P 23 -b b23 \"$LOGIN\"\ncmp data.bin sftp23.bin\n"},
		{"scp on port 23", "scp $SSHOPTS -O -P 23 \"$LOGIN:sbox13/data.bin\" scp23.bin\ncmp data.bin scp23.bin\n" +
			"scp $SSHOPTS -O -P 23 data.bin \"$LOGIN:sbox13/scp-up.bin\"\n" +
			"scp $SSHOPTS -O -P 23 \"$LOGIN:sbox13/scp-up.bin\" scp23-back.bin\ncmp data.bin scp23-back.bin\n"},
		{"rsync on port 23", "rsync -a -e \"ssh $SSHOPTS -p 23\" \"$LOGIN:sbox13/\" rsync23/\ncmp data.bin rsync23/data.bin\n"},
		{"borg on port 23", "export BORG_RSH=\"ssh $SSHOPTS\" BORG_BASE_DIR=\"$PWD/borg-base\"" +
			" BORG_UNKNOWN_UNENCRYPTED_REPO_ACCESS_IS_OK=yes BORG_RELOCATED_REPO_ACCESS_IS_OK=yes\n" +
			"repo=\"ssh://$LOGIN:23/./sbox13/borg\"\n" +
			"borg init --encryption=none \"$repo\"\n" +
			"borg create \"$repo::hlab-e2e\" rsync23\n" +
			"borg list --short \"$repo\"\n"},
	}
	for _, s := range steps {
		r, err := w.wslPassword(dir, box, s.script)
		if err != nil {
			t.Fatalf("%s: %v\n%s", s.name, err, r)
		}
		if s.name == "borg on port 23" && !slices.Contains(strings.Fields(r.Stdout), "hlab-e2e") {
			t.Errorf("borg list printed %q, want the archive hlab-e2e", r.Stdout)
		}
	}

	off := newTestBox(t, e, "sbox13-off", "bx11", map[string]bool{"reachable_externally": true, "ssh_enabled": false})
	ls := "printf 'ls\\n' > bls\nsftp $SSHOPTS -P %d -b bls \"$LOGIN\"\n"
	var last result
	err := waitFor(boxReadyTimeout, 3*time.Second, func() (bool, error) {
		r, err := w.wslPassword(dir, off, fmt.Sprintf(ls, 22))
		last = r
		return err == nil, nil
	})
	if err != nil {
		t.Fatalf("sftp on port 22 of %s (ssh_enabled off): %v\n%s", off.Server, err, last)
	}
	if r, err := w.wslPassword(dir, off, fmt.Sprintf(ls, 23)); err == nil {
		t.Errorf("sftp on port 23 of %s with ssh_enabled off succeeded\n%s", off.Server, r)
	}
}

// createSubaccount creates a subaccount of box with its own password and
// waits for its create_subaccount action.
func (e *env) createSubaccount(t *testing.T, box *storageBox, home string, access map[string]bool) *storageBox {
	t.Helper()
	pw := boxPassword()
	var created struct {
		Subaccount struct {
			ID int64 `json:"id"`
		} `json:"subaccount"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	path := fmt.Sprintf("/storage_boxes/%d/subaccounts", box.ID)
	body := map[string]any{"home_directory": home, "password": pw, "access_settings": access}
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodPost, path, body, &created)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("POST %s: status %d %s %s %v", path, status, aerr.Code, aerr.Message, err)
	}
	if err := e.waitAction(created.Action.ID, boxActionTimeout); err != nil {
		t.Fatalf("create_subaccount %s: %v", home, err)
	}
	var got struct {
		Subaccount struct {
			Username string `json:"username"`
			Server   string `json:"server"`
		} `json:"subaccount"`
	}
	path = fmt.Sprintf("%s/%d", path, created.Subaccount.ID)
	if status, aerr, err := e.hetznerDo(context.Background(), http.MethodGet, path, nil, &got); err != nil || status != http.StatusOK {
		t.Fatalf("GET %s: status %d %s %v", path, status, aerr.Code, err)
	}
	return &storageBox{ID: created.Subaccount.ID, Username: got.Subaccount.Username, Server: got.Subaccount.Server, Password: pw}
}

// testSBOX12: a subaccount logs in with its own name and password on ports
// 22 and 23 and sees only its home directory; a read-only subaccount reads
// but writes nothing over either port.
func testSBOX12(t *testing.T) {
	e, box := sharedBox(t)
	dir := t.TempDir()
	owner := passwordLogin(box, 22)

	outside := randomFile(t, dir, "sbox12-outside.txt", 64)
	seed := randomFile(t, dir, "seed.txt", 4096)
	if r, err := hostSFTP(owner, fmt.Sprintf("put %s sbox12-outside.txt\nmkdir sbox12-ro\nput %s sbox12-ro/seed.txt\n",
		sftpPath(outside), sftpPath(seed))); err != nil {
		t.Fatalf("box writes the seed: %v\n%s", err, r)
	}

	access := map[string]bool{"ssh_enabled": true, "reachable_externally": true}
	rw := e.createSubaccount(t, box, "sbox12-rw", access)
	access["readonly"] = true
	ro := e.createSubaccount(t, box, "sbox12-ro", access)

	for _, port := range []int{22, 23} {
		name := fmt.Sprintf("rw%d.txt", port)
		src := randomFile(t, dir, name, 4096)
		batch := fmt.Sprintf("put %s %s\nls -1a .\n-ls -1a ..\n-ls -1a /\n", sftpPath(src), name)
		r, err := hostSFTP(passwordLogin(rw, port), batch)
		if err != nil {
			t.Errorf("subaccount %s on port %d: %v\n%s", rw.Username, port, err, r)
			continue
		}
		listing := sftpListing(r.Stdout)
		if !strings.Contains(listing, name) {
			t.Errorf("subaccount %s on port %d: listing does not show %s\n%s", rw.Username, port, name, r)
		}
		for _, foreign := range []string{"sbox12-outside.txt", "sbox12-ro"} {
			if strings.Contains(listing, foreign) {
				t.Errorf("subaccount %s on port %d sees %s outside its home\n%s", rw.Username, port, foreign, r)
			}
		}
		back := filepath.Join(dir, "box-"+name)
		if r, err := hostSFTP(owner, fmt.Sprintf("get sbox12-rw/%s %s\n", name, sftpPath(back))); err != nil {
			t.Errorf("box reads sbox12-rw/%s: %v\n%s", name, err, r)
		} else {
			sameFile(t, src, back, "file of the subaccount in sbox12-rw")
		}
	}

	attempt := randomFile(t, dir, "ro-put.txt", 64)
	for _, port := range []int{22, 23} {
		l := passwordLogin(ro, port)
		back := filepath.Join(dir, fmt.Sprintf("ro%d-seed.txt", port))
		if r, err := hostSFTP(l, fmt.Sprintf("get seed.txt %s\n", sftpPath(back))); err != nil {
			t.Errorf("read-only %s reads seed.txt on port %d: %v\n%s", ro.Username, port, err, r)
		} else {
			sameFile(t, seed, back, fmt.Sprintf("seed.txt read by the read-only subaccount on port %d", port))
		}
		writes := map[string]func() (result, error){
			"sftp put": func() (result, error) {
				return hostSFTP(l, fmt.Sprintf("put %s ro-put-%d.txt\n", sftpPath(attempt), port))
			},
			"sftp mkdir": func() (result, error) { return hostSFTP(l, fmt.Sprintf("mkdir ro-dir-%d\n", port)) },
		}
		if port == 23 {
			writes["scp upload"] = func() (result, error) { return hostSCP(l, "-O", attempt, l.remote("ro-scp-23.txt")) }
		}
		for what, write := range writes {
			if r, err := write(); err == nil {
				t.Errorf("read-only %s: %s on port %d succeeded\n%s", ro.Username, what, port, r)
			}
		}
	}
	r, err := hostSFTP(owner, "ls -1a sbox12-ro\n")
	if err != nil {
		t.Fatalf("box lists sbox12-ro: %v\n%s", err, r)
	}
	for _, f := range strings.Fields(sftpListing(r.Stdout)) {
		if strings.HasPrefix(filepath.Base(f), "ro-") {
			t.Errorf("sbox12-ro holds %s written by the read-only subaccount\n%s", f, r)
		}
	}
}

// sftpListing is the output of an sftp batch without the echoed commands.
func sftpListing(stdout string) string {
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "sftp>") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// ownStorageBoxTypes is the user's own types file of the dev profile.
func ownStorageBoxTypes() string {
	return filepath.Join(os.Getenv("ProgramData"), "hlab-dev", "storage-box-types.custom.json")
}

// reloadSettings sends POST /mgmt/v1/settings/reload through the pipe.
func reloadSettings() error {
	client := &http.Client{
		Timeout: time.Minute,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return winio.DialPipeContext(ctx, managementPipe)
		}},
	}
	defer client.CloseIdleConnections()
	resp, err := client.Post("http://hlab/mgmt/v1/settings/reload", "application/json", nil)
	if err != nil {
		return fmt.Errorf("POST /mgmt/v1/settings/reload: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /mgmt/v1/settings/reload: status %d", resp.StatusCode)
	}
	return nil
}

// ownTypesFile serializes the rows that add own types: each adds its own
// types and removes only those at cleanup; the last one restores the file
// as found before the first.
var ownTypesFile struct {
	mu      sync.Mutex
	users   int
	existed bool
	orig    []byte
}

// writeOwnTypes rewrites the own types file without the types named in
// drop and with add appended; an invalid file serves no own types, so its
// content is dropped. The caller holds ownTypesFile.mu.
func writeOwnTypes(drop map[string]int64, add func(types []map[string]any) []map[string]any) error {
	file := ownStorageBoxTypes()
	var types []map[string]any
	if b, err := os.ReadFile(file); err == nil {
		if json.Unmarshal(b, &types) != nil {
			types = nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	types = slices.DeleteFunc(types, func(m map[string]any) bool {
		_, mine := drop[fmt.Sprint(m["name"])]
		return mine
	})
	if add != nil {
		types = add(types)
	}
	b, err := json.MarshalIndent(types, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o600)
}

// addOwnStorageBoxTypes adds the given types (name and size) with
// snapshot_limit snapshotLimit to the own types file as the interactive
// user, reloads, and waits until the API lists them. Parallel callers each
// add their own types; at cleanup a caller removes only its own, and the
// last restores the file as found.
func addOwnStorageBoxTypes(t *testing.T, e *env, sizes map[string]int64, snapshotLimit int) {
	t.Helper()
	file := ownStorageBoxTypes()
	names := make([]string, 0, len(sizes))
	for name := range sizes {
		names = append(names, name)
	}
	slices.Sort(names)

	ownTypesFile.mu.Lock()
	if ownTypesFile.users == 0 {
		orig, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			ownTypesFile.mu.Unlock()
			t.Fatal(err)
		}
		ownTypesFile.existed, ownTypesFile.orig = err == nil, orig
	}
	ownTypesFile.users++
	t.Cleanup(func() {
		ownTypesFile.mu.Lock()
		defer ownTypesFile.mu.Unlock()
		ownTypesFile.users--
		var err error
		switch {
		case ownTypesFile.users > 0:
			err = writeOwnTypes(sizes, nil)
		case ownTypesFile.existed:
			err = os.WriteFile(file, ownTypesFile.orig, 0o600)
		default:
			// Absent before and absent now is the restored state.
			if err = os.Remove(file); errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
		}
		if err != nil {
			t.Errorf("removing own types %v from %s: %v", names, file, err)
		}
		if err := reloadSettings(); err != nil {
			t.Errorf("reload after removing own types %v: %v", names, err)
		}
	})

	zero := map[string]string{"gross": "0.0000", "net": "0.0000"}
	err := writeOwnTypes(sizes, func(types []map[string]any) []map[string]any {
		next := int64(100000)
		for _, m := range types {
			if id, ok := m["id"].(float64); ok && int64(id) >= next {
				next = int64(id) + 1
			}
		}
		for _, name := range names {
			types = append(types, map[string]any{
				"id": next, "name": name, "description": name,
				"snapshot_limit": snapshotLimit, "automatic_snapshot_limit": 10, "subaccounts_limit": 100,
				"size": sizes[name],
				"prices": []map[string]any{{
					"location": serverLocation, "price_hourly": zero, "price_monthly": zero, "setup_fee": zero,
				}},
				"deprecation": nil,
			})
			next++
		}
		return types
	})
	if err == nil {
		err = reloadSettings()
	}
	ownTypesFile.mu.Unlock()
	if err != nil {
		t.Fatalf("adding own types %v to %s as the interactive user: %v", names, file, err)
	}

	var listed []string
	err = waitFor(time.Minute, 2*time.Second, func() (bool, error) {
		var body struct {
			Types []struct {
				Name string `json:"name"`
			} `json:"storage_box_types"`
		}
		status, _, err := e.hetznerDo(context.Background(), http.MethodGet, "/storage_box_types", nil, &body)
		if err != nil || status != http.StatusOK {
			return false, nil
		}
		listed = listed[:0]
		for _, st := range body.Types {
			listed = append(listed, st.Name)
		}
		for _, name := range names {
			if !slices.Contains(listed, name) {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("own types %v not listed in /storage_box_types (%v): %v", names, listed, err)
	}
}

// testSBOX06: with own types of 64 MiB and 128 MiB, writing over 64 MiB
// into the small box fails; on the 128 MiB box with more than 64 MiB
// stored, change_type to the 64 MiB type is refused.
func testSBOX06(t *testing.T) {
	const small, large = "e2e-64m", "e2e-128m"
	e := sharedEnv(t)
	addOwnStorageBoxTypes(t, e, map[string]int64{small: 64 << 20, large: 128 << 20}, 10)
	access := map[string]bool{"reachable_externally": true}
	smallBox := newTestBox(t, e, "sbox06-small", small, access)
	largeBox := newTestBox(t, e, "sbox06-large", large, access)

	dir := t.TempDir()
	fits := randomFile(t, dir, "fits.bin", 1<<20)
	over := randomFile(t, dir, "over.bin", 80<<20)
	stored := randomFile(t, dir, "stored.bin", 96<<20)

	sl := passwordLogin(smallBox, 22)
	if r, err := hostSFTP(sl, fmt.Sprintf("put %s fits.bin\n", sftpPath(fits))); err != nil {
		t.Fatalf("writing 1 MiB into the %s box: %v\n%s", small, err, r)
	}
	if r, err := hostSFTP(sl, fmt.Sprintf("put %s over.bin\n", sftpPath(over))); err == nil {
		t.Errorf("writing 80 MiB into the %s box succeeded\n%s", small, r)
	}

	if r, err := hostSFTP(passwordLogin(largeBox, 22), fmt.Sprintf("put %s stored.bin\n", sftpPath(stored))); err != nil {
		t.Fatalf("writing 96 MiB into the %s box: %v\n%s", large, err, r)
	}
	path := fmt.Sprintf("/storage_boxes/%d/actions/change_type", largeBox.ID)
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodPost, path, map[string]string{"storage_box_type": small}, nil)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	if status != http.StatusUnprocessableEntity || aerr.Code != "invalid_input" {
		t.Errorf("change_type to %s with 96 MiB stored: status %d %s, want 422 invalid_input", small, status, aerr.Code)
	}
	var got struct {
		StorageBox struct {
			StorageBoxType struct {
				Name string `json:"name"`
			} `json:"storage_box_type"`
		} `json:"storage_box"`
	}
	p := fmt.Sprintf("/storage_boxes/%d", largeBox.ID)
	if status, _, err := e.hetznerDo(context.Background(), http.MethodGet, p, nil, &got); err != nil || status != http.StatusOK {
		t.Fatalf("GET %s: status %d %v", p, status, err)
	}
	if got.StorageBox.StorageBoxType.Name != large {
		t.Errorf("after the refused change_type: type %q, want %s", got.StorageBox.StorageBoxType.Name, large)
	}
}
