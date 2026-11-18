package ipam_test

import (
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/store"
)

func TestIPAM(t *testing.T) {
	t.Run("SRV-03", func(t *testing.T) {
		st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })

		r := netip.MustParsePrefix("198.19.0.0/28")
		next := func() (netip.Addr, error) {
			var a netip.Addr
			err := st.Tx(func(tx store.Tx) error {
				var err error
				a, err = ipam.NextPublic(tx, r)
				return err
			})
			return a, err
		}
		mustNext := func(want string) netip.Addr {
			t.Helper()
			a, err := next()
			if err != nil {
				t.Fatalf("NextPublic: %v, want %s", err, want)
			}
			if want != "" && a != netip.MustParseAddr(want) {
				t.Fatalf("NextPublic = %s, want %s", a, want)
			}
			return a
		}

		// Allocation starts at .4.
		mustNext("198.19.0.4")
		mustNext("198.19.0.5")
		mustNext("198.19.0.6")

		// A released lower address is not handed out again at once.
		if err := st.Tx(func(tx store.Tx) error {
			return ipam.Release(tx, netip.MustParseAddr("198.19.0.5"))
		}); err != nil {
			t.Fatalf("Release: %v", err)
		}
		last := mustNext("198.19.0.7")

		// Continue to the end of the range; then it wraps to .4, skips the
		// used .4 and returns the released .5.
		for {
			a := mustNext("")
			if a.Less(last) {
				if a != netip.MustParseAddr("198.19.0.5") {
					t.Fatalf("after wrapping NextPublic = %s, want 198.19.0.5", a)
				}
				break
			}
			if a != last.Next() {
				t.Fatalf("NextPublic = %s after %s, want %s", a, last, last.Next())
			}
			if !r.Contains(a) {
				t.Fatalf("NextPublic = %s outside %s", a, r)
			}
			last = a
		}

		// Every address is in use now.
		if a, err := next(); err == nil {
			t.Fatalf("NextPublic on a full range = %s, want an error", a)
		}
	})
}
