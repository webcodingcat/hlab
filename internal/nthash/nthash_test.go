package nthash_test

import (
	"testing"

	"github.com/webcodingcat/hlab/internal/nthash"
)

// asUTF16LE returns the string whose UTF-16LE encoding is exactly b: each
// byte pair (low, high) becomes one BMP rune. b has an even length and no
// pair falls into the surrogate range, so the RFC 1320 vectors over these
// bytes test the MD4 of Hex directly.
func asUTF16LE(t *testing.T, b string) string {
	t.Helper()
	if len(b)%2 != 0 {
		t.Fatalf("vector %q has an odd length", b)
	}
	runes := make([]rune, 0, len(b)/2)
	for i := 0; i < len(b); i += 2 {
		r := rune(b[i]) | rune(b[i+1])<<8
		if r >= 0xD800 && r <= 0xDFFF {
			t.Fatalf("vector %q has a surrogate pair at %d", b, i)
		}
		runes = append(runes, r)
	}
	return string(runes)
}

func TestNTHash(t *testing.T) {
	t.Run("SBOX-09", func(t *testing.T) {
		// RFC 1320 appendix A.5; "a" and "abc" have an odd length and no
		// UTF-16LE form, so they are left out.
		rfc := []struct{ input, want string }{
			{"", "31d6cfe0d16ae931b73c59d7e0c089c0"},
			{"message digest", "d9130a8164549fe818874806e1c7014b"},
			{"abcdefghijklmnopqrstuvwxyz", "d79e1c308aa5bbcdeea8ed63df412da9"},
			{"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", "043f8582f241db351ce627e153e7f0e4"},
			{"12345678901234567890123456789012345678901234567890123456789012345678901234567890", "e33b4ddc9c38f2199c3e7b164fcc0536"},
		}
		for _, v := range rfc {
			if got := nthash.Hex(asUTF16LE(t, v.input)); got != v.want {
				t.Errorf("Hex over the UTF-16LE bytes %q = %s, want MD4 %s", v.input, got, v.want)
			}
		}

		known := []struct{ password, want string }{
			{"password", "8846f7eaee8fb117ad06bdd830b7586c"},
			{"Pässwort§123", "6aab15fef821cc77d55b5dc1591c8636"},
		}
		for _, k := range known {
			if got := nthash.Hex(k.password); got != k.want {
				t.Errorf("Hex(%q) = %s, want %s", k.password, got, k.want)
			}
		}
	})
}
