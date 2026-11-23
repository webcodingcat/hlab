//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	// netUseTimeout limits one net.exe call.
	netUseTimeout = 2 * time.Minute
	// uncIOTimeout limits one file read or write through a UNC path.
	uncIOTimeout = 2 * time.Minute
)

// hostNet runs net.exe directly (no shell, so the leading \\ of a UNC path
// reaches it intact) with args. The returned result has secret replaced
// and the error never names the arguments, so the password stays out of
// the test output.
func hostNet(secret string, args ...string) (result, error) {
	r, err := command(context.Background(), netUseTimeout, nil, "", "net", args...)
	redact := func(s string) string {
		if secret == "" {
			return s
		}
		return strings.ReplaceAll(s, secret, "***")
	}
	r.Stdout, r.Stderr = redact(r.Stdout), redact(r.Stderr)
	if err == nil && r.ExitCode != 0 {
		err = fmt.Errorf("exit code %d", r.ExitCode)
	}
	if err != nil {
		return r, fmt.Errorf("net %s %s: %s", args[0], args[1], redact(err.Error()))
	}
	return r, nil
}

// netDelete removes every connection of this logon session to target
// (\\server or \\server\share); none being there is fine.
func netDelete(target string) {
	_, _ = hostNet("", "use", target, "/delete", "/y")
}

// netUse maps \\<account.Server>\<share> as account without a drive letter
// and returns the UNC path. Leftover connections to the server name go
// first; the first mapping is retried until boxReadyTimeout; the mapping
// is deleted when the test ends, also on failure.
func netUse(t *testing.T, account *storageBox, share string) (string, bool) {
	t.Helper()
	server := `\\` + account.Server
	unc := server + `\` + share
	netDelete(unc)
	netDelete(server)
	t.Cleanup(func() { netDelete(unc) })
	ok := eventually(t, "net use "+unc+" /user:"+account.Username, func() (result, error) {
		return hostNet(account.Password, "use", unc, account.Password, "/user:"+account.Username, "/persistent:no")
	})
	return unc, ok
}

// withDeadline runs f and gives up after timeout.
func withDeadline(timeout time.Duration, what string, f func() error) error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("%s did not finish within %s", what, timeout)
	}
}

// uncRoundTrip writes a file through unc and reads it back over SFTP as
// owner under home, then writes a file over SFTP under home and reads it
// back through unc; both must keep their SHA-256.
func uncRoundTrip(t *testing.T, owner login, unc, home string) {
	t.Helper()
	dir := t.TempDir()
	local := randomFile(t, dir, "smb.bin", 1<<20)
	data, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if err := withDeadline(uncIOTimeout, "write to "+unc, func() error {
		return os.WriteFile(unc+`\sbox22-smb.bin`, data, 0o600)
	}); err != nil {
		t.Errorf("write %s\\sbox22-smb.bin: %v", unc, err)
	} else if got := hostSum(t, owner, home+"sbox22-smb.bin"); got != hex.EncodeToString(sum[:]) {
		t.Errorf("%ssbox22-smb.bin over SFTP has SHA-256 %s, written through %s %x", home, got, unc, sum)
	}

	remote := home + "sbox22-sftp.bin"
	src := randomFile(t, dir, "sftp.bin", 1<<20)
	want, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := hostSFTP(owner, fmt.Sprintf("put %s %s\n", sftpPath(src), remote)); err != nil {
		t.Errorf("sftp put %s: %v\n%s", remote, err, r)
		return
	}
	var back []byte
	if err := withDeadline(uncIOTimeout, "read from "+unc, func() error {
		var err error
		back, err = os.ReadFile(unc + `\sbox22-sftp.bin`)
		return err
	}); err != nil {
		t.Errorf("read %s\\sbox22-sftp.bin: %v", unc, err)
		return
	}
	if got, want := sha256.Sum256(back), sha256.Sum256(want); got != want {
		t.Errorf("%ssbox22-sftp.bin through %s has SHA-256 %x, written over SFTP %x", home, unc, got, want)
	}
}

// testSBOX22: from the Windows host without elevation, net.exe maps share
// backup of a box and a subaccount's share named after it under their own
// server names; files written through the UNC paths read back over SFTP
// and files written over SFTP read back through the UNC paths.
func testSBOX22(t *testing.T) {
	e := sharedEnv(t)
	access := map[string]bool{"reachable_externally": true, "samba_enabled": true}
	box := newTestBox(t, e, "sbox22", "bx11", access)
	sub := e.createSubaccount(t, box, "sbox22-sub", access)
	owner := passwordLogin(box, 22)

	if unc, ok := netUse(t, box, "backup"); ok {
		uncRoundTrip(t, owner, unc, "")
	}
	if unc, ok := netUse(t, sub, sub.Username); ok {
		uncRoundTrip(t, owner, unc, "sbox22-sub/")
	}
}
