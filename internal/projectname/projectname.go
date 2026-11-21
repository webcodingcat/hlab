// Package projectname derives a project name from its display name and
// validates display names. It is the one derivation, used by the management
// API and the tray bridge.
package projectname

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxLen is the limit of a display name in runes and of a derived name in
// bytes (a derived name is ASCII).
const MaxLen = 255

// umlauts maps the letters PROJ-06 transliterates; applied after lower-casing
// and before NFD, which would otherwise split ä into a and a mark.
var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")

// ValidateDisplay trims display and returns it, or an error whose message the
// API returns as is.
func ValidateDisplay(display string) (string, error) {
	d := strings.TrimSpace(display)
	if d == "" {
		return "", errors.New("the display name is empty")
	}
	if utf8.RuneCountInString(d) > MaxLen {
		return "", errors.New("the display name is longer than 255 characters")
	}
	for _, r := range d {
		if unicode.IsControl(r) {
			return "", errors.New("the display name contains a control character")
		}
	}
	return d, nil
}

// Derive returns the project name of display: lower case, ä ö ü ß as ae oe ue
// ss, other accents dropped, every other run of characters as one "-", cut to
// MaxLen, no "-" at either end. It returns "" when nothing is left.
func Derive(display string) string {
	s := norm.NFD.String(umlauts.Replace(strings.ToLower(strings.TrimSpace(display))))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := b.String()
	if len(out) > MaxLen {
		out = out[:MaxLen]
	}
	return strings.Trim(out, "-")
}
