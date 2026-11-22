package storagepass_test

import (
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/shacrypt"
	"github.com/webcodingcat/hlab/internal/storagepass"
)

func TestStoragePass(t *testing.T) {
	t.Run("SBOX-09", func(t *testing.T) {
		cases := []struct {
			name string
			pw   string
			ok   bool
		}{
			{"11 runes", "Abcdefgh1!x", false},
			{"129 runes", "Aa1!" + strings.Repeat("ä", 125), false},
			{"12 runes with umlauts", "Ääöü1!abcdef", true},
			{"128 runes with umlauts", "Aa1!" + strings.Repeat("ä", 124), true},
			{"character outside the set", "Abcdefgh1!xé", false},
			{"no upper case", "abcdefgh1!xy", false},
			{"no lower case", "ABCDEFGH1!XY", false},
			{"no digit", "Abcdefgh!!xy", false},
			{"no special character", "Abcdefgh12xy", false},
			{"umlauts, ß, § and °", "ÄÖÜäöüß§°123", true},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				err := storagepass.Check(c.pw)
				if c.ok && err != nil {
					t.Errorf("Check(%q) = %v, want accepted", c.pw, err)
				}
				if !c.ok {
					if err == nil {
						t.Errorf("Check(%q) = nil, want refused", c.pw)
					} else if err.Error() == "" {
						t.Errorf("Check(%q) refused with an empty message", c.pw)
					}
				}
			})
		}

		t.Run("hash", func(t *testing.T) {
			const pw = "Pässwort§123"
			h, err := storagepass.Hash(pw)
			if err != nil {
				t.Fatalf("Hash: %v", err)
			}
			cut := strings.LastIndex(h.Crypt, "$")
			if !strings.HasPrefix(h.Crypt, "$6$") || cut < len("$6$") {
				t.Fatalf("Crypt = %q, want $6$<salt>$<hash>", h.Crypt)
			}
			again, err := shacrypt.SHA512(pw, h.Crypt[:cut])
			if err != nil || again != h.Crypt {
				t.Errorf("Crypt %q does not verify: SHA512 with its setting = %q, %v", h.Crypt, again, err)
			}
			// The NT hash of pw, as in the nthash test.
			if want := "6aab15fef821cc77d55b5dc1591c8636"; h.NT != want {
				t.Errorf("NT = %q, want %q", h.NT, want)
			}
		})
	})
}
