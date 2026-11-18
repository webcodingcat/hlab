package shacrypt_test

import (
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/shacrypt"
)

// drepperVectors are the SHA-512 test vectors (tests2) of Ulrich Drepper's
// "Unix crypt using SHA-256 and SHA-512", checked against
// https://www.akkadia.org/drepper/SHA-crypt.txt.
var drepperVectors = []struct {
	setting, password, want string
}{
	{
		"$6$saltstring", "Hello world!",
		"$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJu" +
			"esI68u4OTLiBFdcbYEdFCoEOfaS35inz1",
	},
	{
		"$6$rounds=10000$saltstringsaltstring", "Hello world!",
		"$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sb" +
			"HbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v.",
	},
	{
		"$6$rounds=5000$toolongsaltstring", "This is just a test",
		"$6$rounds=5000$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQ" +
			"zQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0",
	},
	{
		"$6$rounds=1400$anotherlongsaltstring",
		"a very much longer text to encrypt.  This one even stretches over more" +
			"than one line.",
		"$6$rounds=1400$anotherlongsalts$POfYwTEok97VWcjxIiSOjiykti.o/pQs.wP" +
			"vMxQ6Fm7I6IoYN3CmLs66x9t0oSwbtEW7o7UmJEiDwGqd8p4ur1",
	},
	{
		"$6$rounds=77777$short",
		"we have a short salt string but not a short password",
		"$6$rounds=77777$short$WuQyW2YR.hBNpjjRhpYD/ifIw05xdfeEyQoMxIXbkvr0g" +
			"ge1a1x3yRULJ5CCaUeOxFmtlcGZelFl5CxtgfiAc0",
	},
	{
		"$6$rounds=123456$asaltof16chars..", "a short string",
		"$6$rounds=123456$asaltof16chars..$BtCwjqMJGx5hrJhZywWvt0RLE8uZ4oPwc" +
			"elCjmw2kSYu.Ec6ycULevoBK25fs2xXgMNrCzIMVcgEJAstJeonj1",
	},
	{
		"$6$rounds=10$roundstoolow", "the minimum number is still observed",
		"$6$rounds=1000$roundstoolow$kUMsbe306n21p9R.FRkW3IGn.S9NPN0x50YhH1x" +
			"hLsPuWGsUSklZt58jaTfF4ZEQpyUNGc0dqbpBYYBaHHrsX.",
	},
}

func TestShaCrypt(t *testing.T) {
	t.Run("SRV-02", func(t *testing.T) {
		for i, v := range drepperVectors {
			got, err := shacrypt.SHA512(v.password, v.setting)
			if err != nil {
				t.Errorf("vector %d: SHA512(%q, %q): %v", i+1, v.password, v.setting, err)
				continue
			}
			if got != v.want {
				t.Errorf("vector %d: SHA512(%q, %q)\n got %s\nwant %s", i+1, v.password, v.setting, got, v.want)
			}
		}

		const password = "correct horse battery staple"
		first, err := shacrypt.NewSHA512(password)
		if err != nil {
			t.Fatalf("NewSHA512: %v", err)
		}
		second, err := shacrypt.NewSHA512(password)
		if err != nil {
			t.Fatalf("NewSHA512: %v", err)
		}
		if first == second {
			t.Errorf("two NewSHA512 calls gave the same hash %s", first)
		}
		for _, h := range []string{first, second} {
			cut := strings.LastIndex(h, "$")
			if !strings.HasPrefix(h, "$6$") || cut < len("$6$") {
				t.Errorf("NewSHA512 = %q, want $6$<salt>$<hash>", h)
				continue
			}
			setting := h[:cut]
			again, err := shacrypt.SHA512(password, setting)
			if err != nil {
				t.Errorf("SHA512 with setting %q of NewSHA512: %v", setting, err)
				continue
			}
			if again != h {
				t.Errorf("SHA512(password, %q) = %s, want the NewSHA512 output %s", setting, again, h)
			}
		}
	})
}
