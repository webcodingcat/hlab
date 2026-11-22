// Package nthash computes the NT hash of a password: MD4 (RFC 1320) over its
// UTF-16LE encoding, as Samba stores it.
package nthash

import (
	"encoding/binary"
	"encoding/hex"
	"math/bits"
	"unicode/utf16"
)

// Hex returns the lower-case hex of MD4 over the UTF-16LE encoding of
// password.
func Hex(password string) string {
	units := utf16.Encode([]rune(password))
	b := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[2*i:], u)
	}
	sum := md4(b)
	return hex.EncodeToString(sum[:])
}

// md4 returns the MD4 digest of msg as specified in RFC 1320.
func md4(msg []byte) [16]byte {
	// Padding: 0x80, zeros up to 56 mod 64, then the bit length (64-bit LE).
	n := len(msg)
	padLen := 56 - (n+1)%64
	if padLen < 0 {
		padLen += 64
	}
	data := make([]byte, n+1+padLen+8)
	copy(data, msg)
	data[n] = 0x80
	binary.LittleEndian.PutUint64(data[len(data)-8:], uint64(n)*8)

	a, b, c, d := uint32(0x67452301), uint32(0xefcdab89), uint32(0x98badcfe), uint32(0x10325476)
	var x [16]uint32
	for off := 0; off < len(data); off += 64 {
		for i := range x {
			x[i] = binary.LittleEndian.Uint32(data[off+4*i:])
		}
		aa, bb, cc, dd := a, b, c, d

		// Round 1.
		for _, i := range [4]int{0, 4, 8, 12} {
			a = bits.RotateLeft32(a+(b&c|^b&d)+x[i], 3)
			d = bits.RotateLeft32(d+(a&b|^a&c)+x[i+1], 7)
			c = bits.RotateLeft32(c+(d&a|^d&b)+x[i+2], 11)
			b = bits.RotateLeft32(b+(c&d|^c&a)+x[i+3], 19)
		}
		// Round 2.
		const k2 = 0x5a827999
		for _, i := range [4]int{0, 1, 2, 3} {
			a = bits.RotateLeft32(a+(b&c|b&d|c&d)+x[i]+k2, 3)
			d = bits.RotateLeft32(d+(a&b|a&c|b&c)+x[i+4]+k2, 5)
			c = bits.RotateLeft32(c+(d&a|d&b|a&b)+x[i+8]+k2, 9)
			b = bits.RotateLeft32(b+(c&d|c&a|d&a)+x[i+12]+k2, 13)
		}
		// Round 3.
		const k3 = 0x6ed9eba1
		for _, i := range [4]int{0, 2, 1, 3} {
			a = bits.RotateLeft32(a+(b^c^d)+x[i]+k3, 3)
			d = bits.RotateLeft32(d+(a^b^c)+x[i+8]+k3, 9)
			c = bits.RotateLeft32(c+(d^a^b)+x[i+4]+k3, 11)
			b = bits.RotateLeft32(b+(c^d^a)+x[i+12]+k3, 15)
		}

		a += aa
		b += bb
		c += cc
		d += dd
	}

	var out [16]byte
	binary.LittleEndian.PutUint32(out[0:], a)
	binary.LittleEndian.PutUint32(out[4:], b)
	binary.LittleEndian.PutUint32(out[8:], c)
	binary.LittleEndian.PutUint32(out[12:], d)
	return out
}
