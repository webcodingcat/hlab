package sshkey

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicReadsTheKeyThatNewMade(t *testing.T) {
	private, public, err := New("hlab")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(public, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA") || !strings.HasSuffix(public, " hlab") {
		t.Errorf("the public key is %q, want an ssh-ed25519 line with the comment", public)
	}
	again, err := Public(private, "hlab")
	if err != nil {
		t.Fatal(err)
	}
	if again != public {
		t.Errorf("Public returns %q, want %q", again, public)
	}
	if _, err := Public([]byte("-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n"), "hlab"); err == nil {
		t.Error("Public took a broken key")
	}
}

// ssh-keygen derives the public key only from a private key it can read, so
// this checks that OpenSSH takes the format.
func TestOpenSSHReadsTheKey(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	private, public, err := New("hlab")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lab-key")
	if err := os.WriteFile(path, private, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(keygen, "-y", "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen -y: %v: %s", err, out)
	}
	want, _, _ := strings.Cut(public, " hlab")
	if got := strings.TrimSpace(string(out)); !strings.HasPrefix(got, want) {
		t.Errorf("ssh-keygen derives %q, want %q", got, want)
	}
}
