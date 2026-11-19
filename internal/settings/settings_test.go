package settings_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/settings"
)

func TestSettings(t *testing.T) {
	t.Run("HOST-02", func(t *testing.T) {
		cases := []struct {
			name  string
			value string
			want  string // normalised range; "" means refused
		}{
			{name: "default range", value: "198.19.0.0/16", want: "198.19.0.0/16"},
			{name: "slash 8", value: "11.0.0.0/8", want: "11.0.0.0/8"},
			{name: "slash 28", value: "198.51.100.16/28", want: "198.51.100.16/28"},
			{name: "normalised to network address", value: "203.0.113.77/24", want: "203.0.113.0/24"},
			{name: "next to 100.64 slash 10", value: "100.128.0.0/16", want: "100.128.0.0/16"},
			{name: "next to 172.16 slash 12", value: "172.32.0.0/16", want: "172.32.0.0/16"},
			{name: "next to the resolvers", value: "185.12.65.0/24", want: "185.12.65.0/24"},
			{name: "IPv6", value: "2001:db8::/48"},
			{name: "slash 7", value: "198.0.0.0/7"},
			{name: "slash 29", value: "198.19.0.0/29"},
			{name: "slash 32", value: "198.19.0.4/32"},
			{name: "0 slash 8", value: "0.1.0.0/16"},
			{name: "10 slash 8", value: "10.1.0.0/16"},
			{name: "100.64 slash 10", value: "100.64.0.0/16"},
			{name: "100.64 slash 10 at its end", value: "100.127.0.0/16"},
			{name: "containing 100.64 slash 10", value: "100.0.0.0/8"},
			{name: "127 slash 8", value: "127.0.0.0/24"},
			{name: "169.254 slash 16", value: "169.254.0.0/16"},
			{name: "172.16 slash 12", value: "172.20.0.0/16"},
			{name: "172.31.1.1", value: "172.31.0.0/16"},
			{name: "192.168 slash 16", value: "192.168.1.0/24"},
			{name: "224 slash 4", value: "224.0.0.0/8"},
			{name: "240 slash 4", value: "240.0.0.0/8"},
			{name: "185.12.64.1", value: "185.12.64.0/28"},
			{name: "185.12.64.2", value: "185.12.0.0/16"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "settings.toml")
				content := "[network]\npublic_range = \"" + tc.value + "\"\n"
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				s, err := settings.Load(path)
				if tc.want == "" {
					if err == nil {
						t.Fatalf("Load with public_range %s = %s, want an error", tc.value, s.Network.PublicRange)
					}
					if !strings.Contains(err.Error(), "network.public_range") {
						t.Errorf("error %q does not name network.public_range", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Load with public_range %s: %v", tc.value, err)
				}
				if want := netip.MustParsePrefix(tc.want); s.Network.PublicRange != want {
					t.Errorf("public_range %s loaded as %s, want %s", tc.value, s.Network.PublicRange, want)
				}
			})
		}
	})
}
