package storageagent_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/storageagent"
)

// ed25519Blob returns the SSH wire form of an ed25519 public key whose
// key bytes are all b.
func ed25519Blob(b byte) []byte {
	key := make([]byte, ed25519.PublicKeySize)
	for i := range key {
		key[i] = b
	}
	var out []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), key} {
		out = binary.BigEndian.AppendUint32(out, uint32(len(part)))
		out = append(out, part...)
	}
	return out
}

// rfc4716 renders blob as an RFC4716 block with its base64 wrapped at 70
// characters, as ssh-keygen -e writes it.
func rfc4716(blob []byte, comment string) string {
	enc := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	b.WriteString("---- BEGIN SSH2 PUBLIC KEY ----\n")
	b.WriteString("Comment: \"" + comment + "\"\n")
	for len(enc) > 70 {
		b.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	b.WriteString(enc + "\n")
	b.WriteString("---- END SSH2 PUBLIC KEY ----")
	return b.String()
}

func testAuthorizedKeys(t *testing.T) {
	line := base64.StdEncoding.EncodeToString(ed25519Blob(1))
	block := base64.StdEncoding.EncodeToString(ed25519Blob(2))
	input := strings.Join([]string{
		"# keys of the box",
		"",
		"ssh-ed25519 " + line + " alice@laptop",
		"not a key at all",
		rfc4716(ed25519Blob(2), "256-bit ED25519, bob@desk"),
		"---- BEGIN SSH2 PUBLIC KEY ----",
		"!!! no base64 !!!",
		"---- END SSH2 PUBLIC KEY ----",
	}, "\n") + "\n"

	got := storageagent.AuthorizedKeys(strings.NewReader(input))

	want := []string{"ssh-ed25519 " + line, "ssh-ed25519 " + block}
	if len(got) != len(want) {
		t.Fatalf("AuthorizedKeys = %q, want the keys %q", got, want)
	}
	for i, l := range got {
		f := strings.Fields(l)
		if len(f) < 2 || f[0]+" "+f[1] != want[i] {
			t.Errorf("key %d = %q, want an OpenSSH line starting %q", i, l, want[i])
		}
	}
}
