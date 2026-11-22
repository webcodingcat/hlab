// Package storagepass checks Storage Box passwords against the policy of the
// Hetzner API description and derives the hashes hlab keeps instead of the
// plain password.
package storagepass

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/nthash"
	"github.com/webcodingcat/hlab/internal/shacrypt"
)

const (
	minLen  = 12
	maxLen  = 128
	upper   = "ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÜ"
	lower   = "abcdefghijklmnopqrstuvwxyzäöüß"
	digits  = "0123456789"
	special = "^°!§$%/()=?+#-.,;:~*@{}_&"
)

// Check returns nil when pw meets the Storage Box password policy of the
// frozen description; the error text is the details.fields message. The
// breach check of the real API is not done.
func Check(pw string) error {
	if n := utf8.RuneCountInString(pw); n < minLen || n > maxLen {
		return errors.New("password must be 12 to 128 characters long")
	}
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range pw {
		switch {
		case strings.ContainsRune(upper, r):
			hasUpper = true
		case strings.ContainsRune(lower, r):
			hasLower = true
		case strings.ContainsRune(digits, r):
			hasDigit = true
		case strings.ContainsRune(special, r):
			hasSpecial = true
		default:
			return errors.New("password contains characters that are not allowed")
		}
	}
	if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
		return errors.New("password must contain at least one upper case letter, one lower case letter, one digit and one special character")
	}
	return nil
}

// Hashes are the forms of a password hlab keeps: sha512-crypt for SSH, FTP
// and WebDAV, the NT hash for Samba.
type Hashes struct {
	Crypt string `json:"crypt"` // sha512-crypt ($6$) by shacrypt.NewSHA512
	NT    string `json:"nt"`    // nthash.Hex
}

// Hash returns the hashes of pw.
func Hash(pw string) (Hashes, error) {
	crypt, err := shacrypt.NewSHA512(pw)
	if err != nil {
		return Hashes{}, err
	}
	return Hashes{Crypt: crypt, NT: nthash.Hex(pw)}, nil
}
