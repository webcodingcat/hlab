package projectname_test

import (
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/projectname"
)

func TestProjectName(t *testing.T) {
	t.Run("PROJ-06_derive", func(t *testing.T) {
		cases := []struct {
			name, display, want string
		}{
			{"words", "Web Coding Cat", "web-coding-cat"},
			{"umlauts and sharp s", "Größe Übung", "groesse-uebung"},
			{"lower-case umlauts", "ä ö ü ß", "ae-oe-ue-ss"},
			{"upper-case umlauts", "Ä Ö Ü ẞ", "ae-oe-ue-ss"},
			{"other accents", "Café Señor Žluťoučký", "cafe-senor-zlutoucky"},
			{"letter without decomposition", "Ørsted", "rsted"},
			{"only a letter without decomposition", "Æ", ""},
			{"runs of other characters", "a  --__!!b", "a-b"},
			{"trimmed at both ends", "  --Lab--  ", "lab"},
			{"cut to 255", strings.Repeat("a", 255) + "b", strings.Repeat("a", 255)},
			{"dash at the cut trimmed", strings.Repeat("a", 254) + " b", strings.Repeat("a", 254)},
			{"only symbols", "!!! ???", ""},
			{"other script", "東京", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := projectname.Derive(tc.display); got != tc.want {
					t.Errorf("Derive(%q) = %q, want %q", tc.display, got, tc.want)
				}
			})
		}
	})

	t.Run("PROJ-06_display", func(t *testing.T) {
		cases := []struct {
			name, display string
			want          string // the returned display name when valid
			valid         bool
		}{
			{"trimmed, inner spaces kept", "  Web  Coding Cat \n", "Web  Coding Cat", true},
			{"1 rune", "a", "a", true},
			{"255 runes", strings.Repeat("a", 255), strings.Repeat("a", 255), true},
			{"255 multi-byte runes", strings.Repeat("ä", 255), strings.Repeat("ä", 255), true},
			{"empty", "", "", false},
			{"only whitespace", " \t\n ", "", false},
			{"256 runes", strings.Repeat("a", 256), "", false},
			{"tab", "a\tb", "", false},
			{"newline", "a\nb", "", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := projectname.ValidateDisplay(tc.display)
				if !tc.valid {
					if err == nil {
						t.Errorf("ValidateDisplay(%q) = %q, nil; want an error", tc.display, got)
					}
					return
				}
				if err != nil {
					t.Fatalf("ValidateDisplay(%q): %v, want accepted", tc.display, err)
				}
				if got != tc.want {
					t.Errorf("ValidateDisplay(%q) = %q, want %q", tc.display, got, tc.want)
				}
			})
		}
	})
}
