package ipam_test

import (
	"net/netip"
	"path/filepath"
	"slices"
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

	t.Run("HOST-02", func(t *testing.T) {
		st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })

		old := netip.MustParsePrefix("198.19.0.0/16")
		var inUse []netip.Addr
		if err := st.Tx(func(tx store.Tx) error {
			if err := ipam.RecordRange(tx, old); err != nil {
				return err
			}
			for range 3 {
				a, err := ipam.NextPublic(tx, old)
				if err != nil {
					return err
				}
				inUse = append(inUse, a)
			}
			return nil
		}); err != nil {
			t.Fatalf("record range and allocate: %v", err)
		}

		outside := func(r netip.Prefix) ([]netip.Addr, netip.Prefix) {
			t.Helper()
			var used []netip.Addr
			var recorded netip.Prefix
			if err := st.Tx(func(tx store.Tx) error {
				var err error
				used, recorded, err = ipam.Outside(tx, r)
				return err
			}); err != nil {
				t.Fatalf("Outside(%s): %v", r, err)
			}
			slices.SortFunc(used, netip.Addr.Compare)
			return used, recorded
		}

		// A changed range without the addresses in use reports them all.
		used, recorded := outside(netip.MustParsePrefix("203.0.113.0/24"))
		if !slices.Equal(used, inUse) {
			t.Errorf("Outside(203.0.113.0/24) used = %v, want %v", used, inUse)
		}
		if recorded != old {
			t.Errorf("Outside(203.0.113.0/24) recorded = %s, want %s", recorded, old)
		}

		// The same range and a changed range holding every address in use
		// report none.
		for _, r := range []string{"198.19.0.0/16", "198.19.0.0/24"} {
			if used, _ := outside(netip.MustParsePrefix(r)); len(used) != 0 {
				t.Errorf("Outside(%s) used = %v, want none", r, used)
			}
		}
	})
}
