package rdns_test

import (
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"testing"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/rdns"
)

func ptr(s string) *string { return &s }

// wantFieldError fails unless err is an invalid_input *api.FieldError on
// field (any field when field is "").
func wantFieldError(t *testing.T, err error, field string) {
	t.Helper()
	var fe *api.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error %v, want *api.FieldError", err)
	}
	if fe.Code != "" && fe.Code != "invalid_input" {
		t.Errorf("code %q, want invalid_input", fe.Code)
	}
	if field != "" && fe.Field != field {
		t.Errorf("field %q, want %q", fe.Field, field)
	}
}

func TestRDNS(t *testing.T) {
	t.Run("RDNS-01", func(t *testing.T) {
		v4 := netip.MustParsePrefix("203.0.113.1/32")
		v6 := netip.MustParsePrefix("2001:db8:1:2::/64")

		t.Run("default", func(t *testing.T) {
			got := rdns.Default(netip.MustParseAddr("203.0.113.1"))
			if want := "static.1.113.0.203.clients.your-server.de"; got != want {
				t.Errorf("Default(203.0.113.1) = %q, want %q", got, want)
			}
		})

		t.Run("set replace remove IPv4", func(t *testing.T) {
			entries, err := rdns.Change(nil, v4, "203.0.113.1", ptr("a.example.com"))
			if err != nil {
				t.Fatalf("set: %v", err)
			}
			want := []rdns.Entry{{IP: "203.0.113.1", DNSPtr: "a.example.com"}}
			if !reflect.DeepEqual(entries, want) {
				t.Fatalf("after set: %v, want %v", entries, want)
			}
			entries, err = rdns.Change(entries, v4, "203.0.113.1", ptr("b.example.com"))
			if err != nil {
				t.Fatalf("replace: %v", err)
			}
			want = []rdns.Entry{{IP: "203.0.113.1", DNSPtr: "b.example.com"}}
			if !reflect.DeepEqual(entries, want) {
				t.Fatalf("after replace: %v, want %v", entries, want)
			}
			entries, err = rdns.Change(entries, v4, "203.0.113.1", nil)
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("after remove: %v, want no entries", entries)
			}
		})

		t.Run("set replace remove IPv6", func(t *testing.T) {
			entries, err := rdns.Change(nil, v6, "2001:db8:1:2::1", ptr("a.example.com"))
			if err != nil {
				t.Fatalf("set first: %v", err)
			}
			entries, err = rdns.Change(entries, v6, "2001:db8:1:2::2", ptr("b.example.com"))
			if err != nil {
				t.Fatalf("set second: %v", err)
			}
			entries, err = rdns.Change(entries, v6, "2001:db8:1:2::1", ptr("c.example.com"))
			if err != nil {
				t.Fatalf("replace: %v", err)
			}
			want := []rdns.Entry{
				{IP: "2001:db8:1:2::1", DNSPtr: "c.example.com"},
				{IP: "2001:db8:1:2::2", DNSPtr: "b.example.com"},
			}
			if !sameEntries(entries, want) {
				t.Fatalf("after replace: %v, want %v", entries, want)
			}
			entries, err = rdns.Change(entries, v6, "2001:db8:1:2::1", nil)
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			want = []rdns.Entry{{IP: "2001:db8:1:2::2", DNSPtr: "b.example.com"}}
			if !reflect.DeepEqual(entries, want) {
				t.Fatalf("after remove: %v, want %v", entries, want)
			}
		})

		t.Run("invalid ip", func(t *testing.T) {
			cases := []struct {
				name  string
				owner netip.Prefix
				ip    string
			}{
				{"other IPv4", v4, "203.0.113.2"},
				{"IPv6 outside the /64", v6, "2001:db8:1:3::1"},
				{"malformed", v4, "203.0.113"},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					_, err := rdns.Change(nil, c.owner, c.ip, ptr("a.example.com"))
					wantFieldError(t, err, "ip")
				})
			}
		})

		t.Run("invalid dns_ptr", func(t *testing.T) {
			cases := []struct {
				name string
				ptr  string
			}{
				{"trailing dot", "a.example.com."},
				{"without dot", "localhost"},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					_, err := rdns.Change(nil, v4, "203.0.113.1", ptr(c.ptr))
					wantFieldError(t, err, "dns_ptr")
				})
			}
		})

		t.Run("at most 100 IPv6 entries", func(t *testing.T) {
			var entries []rdns.Entry
			for i := 1; i <= 100; i++ {
				var err error
				entries, err = rdns.Change(entries, v6, fmt.Sprintf("2001:db8:1:2::%x", i), ptr("a.example.com"))
				if err != nil {
					t.Fatalf("entry %d: %v", i, err)
				}
			}
			_, err := rdns.Change(entries, v6, "2001:db8:1:2::ffff", ptr("a.example.com"))
			wantFieldError(t, err, "")
		})

		t.Run("show adds IPv4 default", func(t *testing.T) {
			got := rdns.Show(nil, v4)
			want := []rdns.Entry{{IP: "203.0.113.1", DNSPtr: "static.1.113.0.203.clients.your-server.de"}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Show(nil, IPv4) = %v, want %v", got, want)
			}
			set := []rdns.Entry{{IP: "203.0.113.1", DNSPtr: "a.example.com"}}
			if got := rdns.Show(set, v4); !reflect.DeepEqual(got, set) {
				t.Errorf("Show(set, IPv4) = %v, want %v", got, set)
			}
			if got := rdns.Show(nil, v6); len(got) != 0 {
				t.Errorf("Show(nil, IPv6) = %v, want no entries", got)
			}
		})
	})
}

// sameEntries compares entries regardless of order.
func sameEntries(got, want []rdns.Entry) bool {
	if len(got) != len(want) {
		return false
	}
	m := map[rdns.Entry]int{}
	for _, e := range got {
		m[e]++
	}
	for _, e := range want {
		if m[e] == 0 {
			return false
		}
		m[e]--
	}
	return true
}
