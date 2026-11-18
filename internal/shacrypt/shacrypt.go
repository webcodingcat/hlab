// Package shacrypt implements SHA-512 crypt ("$6$") as specified by Ulrich
// Drepper in "Unix crypt using SHA-256 and SHA-512"
// (https://www.akkadia.org/drepper/SHA-crypt.txt).
package shacrypt

import (
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"strconv"
	"strings"
)

const (
	prefix        = "$6$"
	roundsPrefix  = "rounds="
	defaultRounds = 5000
	minRounds     = 1000
	maxRounds     = 999999999
	maxSaltLen    = 16
	alphabet      = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// SHA512 hashes password with the setting "$6$[rounds=N$]salt". Rounds are
// clamped to 1000..999999999, the salt is truncated to 16 characters, and
// "rounds=N$" appears in the output only when the setting gives it.
func SHA512(password, setting string) (string, error) {
	rest, ok := strings.CutPrefix(setting, prefix)
	if !ok {
		return "", errors.New("shacrypt: setting does not start with $6$")
	}
	rounds := defaultRounds
	customRounds := false
	if after, ok := strings.CutPrefix(rest, roundsPrefix); ok {
		num, tail, found := strings.Cut(after, "$")
		if !found {
			return "", errors.New("shacrypt: rounds= without terminating $")
		}
		n, err := strconv.ParseUint(num, 10, 64)
		if err != nil {
			return "", errors.New("shacrypt: invalid rounds value")
		}
		rounds = int(min(max(n, minRounds), maxRounds))
		customRounds = true
		rest = tail
	}
	salt, _, _ := strings.Cut(rest, "$")
	if len(salt) > maxSaltLen {
		salt = salt[:maxSaltLen]
	}

	sum := digest([]byte(password), []byte(salt), rounds)

	var b strings.Builder
	b.WriteString(prefix)
	if customRounds {
		b.WriteString(roundsPrefix)
		b.WriteString(strconv.Itoa(rounds))
		b.WriteByte('$')
	}
	b.WriteString(salt)
	b.WriteByte('$')
	encode(&b, sum)
	return b.String(), nil
}

// NewSHA512 hashes password with a random 16-character salt and the default
// 5000 rounds.
func NewSHA512(password string) (string, error) {
	raw := make([]byte, maxSaltLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	salt := make([]byte, maxSaltLen)
	for i, c := range raw {
		salt[i] = alphabet[c&0x3f]
	}
	return SHA512(password, prefix+string(salt))
}

// digest runs steps 1 to 21 of Drepper's algorithm.
func digest(p, s []byte, rounds int) []byte {
	// Digest B: password, salt, password.
	h := sha512.New()
	h.Write(p)
	h.Write(s)
	h.Write(p)
	b := h.Sum(nil)

	// Digest A.
	h.Reset()
	h.Write(p)
	h.Write(s)
	h.Write(repeat(b, len(p)))
	for n := len(p); n > 0; n >>= 1 {
		if n&1 != 0 {
			h.Write(b)
		} else {
			h.Write(p)
		}
	}
	a := h.Sum(nil)

	// Sequence P from digest DP.
	h.Reset()
	for range len(p) {
		h.Write(p)
	}
	pSeq := repeat(h.Sum(nil), len(p))

	// Sequence S from digest DS.
	h.Reset()
	for range 16 + int(a[0]) {
		h.Write(s)
	}
	sSeq := repeat(h.Sum(nil), len(s))

	c := a
	for i := range rounds {
		h.Reset()
		if i&1 != 0 {
			h.Write(pSeq)
		} else {
			h.Write(c)
		}
		if i%3 != 0 {
			h.Write(sSeq)
		}
		if i%7 != 0 {
			h.Write(pSeq)
		}
		if i&1 != 0 {
			h.Write(c)
		} else {
			h.Write(pSeq)
		}
		c = h.Sum(nil)
	}
	return c
}

// repeat returns n bytes of src repeated.
func repeat(src []byte, n int) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i += len(src) {
		copy(out[i:], src)
	}
	return out
}

// byteOrder is Drepper's grouping of the 64 digest bytes (b2, b1, b0).
var byteOrder = [21][3]int{
	{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4},
	{47, 5, 26}, {6, 27, 48}, {28, 49, 7}, {50, 8, 29}, {9, 30, 51},
	{31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13}, {56, 14, 35},
	{15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19},
	{62, 20, 41},
}

func encode(b *strings.Builder, sum []byte) {
	put := func(b2, b1, b0 byte, n int) {
		w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for range n {
			b.WriteByte(alphabet[w&0x3f])
			w >>= 6
		}
	}
	for _, g := range byteOrder {
		put(sum[g[0]], sum[g[1]], sum[g[2]], 4)
	}
	put(0, 0, sum[63], 2)
}
