//go:build e2e

package e2e

import (
	"context"
	"crypto/sha1" //nolint:gosec // the Windows store names certificates by their SHA-1 thumbprint
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCERT04: after `hlab ca trust on` the CA is in LocalMachine\Root and
// in the WSL2 distro's /etc/ssl/certs, and a plain curl to the LB's managed
// certificate succeeds on the host (curl.exe, revocation check on) and in
// WSL2; after `hlab ca trust off` both entries are gone and the curl in
// WSL2 fails verification.
func testCERT04(t *testing.T) {
	lb := sharedLB(t)
	_, w := sharedWSL(t)
	url := "https://www." + lb.zone + "/"

	file := filepath.Join(t.TempDir(), "hlab-ca.pem")
	if r, err := runHlab(context.Background(), time.Minute, "ca", "export", file); err != nil {
		t.Fatalf("hlab ca export: %v\n%s", err, r)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read exported CA: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("exported CA is no PEM certificate: %q", data)
	}
	sum := sha1.Sum(block.Bytes) //nolint:gosec // thumbprint, not a security check
	thumbprint := strings.ToUpper(hex.EncodeToString(sum[:]))
	caBody := base64.StdEncoding.EncodeToString(block.Bytes)

	trust := func(state string) {
		t.Helper()
		if r, err := runHlab(context.Background(), 5*time.Minute, "ca", "trust", state); err != nil {
			t.Fatalf("hlab ca trust %s: %v\n%s", state, err, r)
		}
	}
	t.Cleanup(func() {
		if r, err := runHlab(context.Background(), 5*time.Minute, "ca", "trust", "off"); err != nil {
			t.Errorf("cleanup: hlab ca trust off: %v\n%s", err, r)
		}
	})

	inWindows := func() (bool, error) {
		var found bool
		err := powershellJSON(context.Background(),
			fmt.Sprintf(`Test-Path 'Cert:\LocalMachine\Root\%s' | ConvertTo-Json`, thumbprint), &found)
		return found, err
	}
	inWSL := func() (bool, error) {
		script := "tr -d '\\r\\n' < /etc/ssl/certs/ca-certificates.crt | grep -qF '" + caBody + "' && echo yes || echo no\n"
		r, err := w.run(context.Background(), time.Minute, nil, script)
		if err != nil {
			return false, fmt.Errorf("look for the CA in distro %s: %v\n%s", w.distro, err, r)
		}
		return strings.TrimSpace(r.Stdout) == "yes", nil
	}
	curlWSL := func() (result, error) {
		return w.run(context.Background(), time.Minute, nil, "curl -sS --max-time 20 -o /dev/null "+url+"\n")
	}
	wait := func(what string, want bool, cond func() (bool, error)) {
		t.Helper()
		err := waitFor(2*time.Minute, 2*time.Second, func() (bool, error) {
			got, err := cond()
			return got == want, err
		})
		if err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}

	trust("on")
	wait(`CA in LocalMachine\Root after trust on`, true, inWindows)
	wait("CA in "+w.distro+":/etc/ssl/certs after trust on", true, inWSL)
	if r, err := command(context.Background(), 2*time.Minute, nil, "", "curl.exe", "-sS", "--max-time", "60", "-o", "NUL", url); err != nil {
		t.Errorf("curl.exe %s on the host with trust on: %v\n%s", url, err, r)
	}
	if r, err := curlWSL(); err != nil {
		t.Errorf("curl %s in %s with trust on: %v\n%s", url, w.distro, err, r)
	}

	trust("off")
	wait(`CA gone from LocalMachine\Root after trust off`, false, inWindows)
	wait("CA gone from "+w.distro+":/etc/ssl/certs after trust off", false, inWSL)
	// Exit code 60: the peer certificate cannot be verified.
	if r, _ := curlWSL(); r.ExitCode != 60 {
		t.Errorf("curl %s in %s with trust off: exit %d, want 60 (verification failed)\n%s", url, w.distro, r.ExitCode, r)
	}
}
