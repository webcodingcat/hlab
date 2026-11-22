package agent

import (
	"errors"
	"testing"
)

// A netlink dump interrupted by a concurrent change is listed again; a
// dump that stays interrupted or fails otherwise returns its error.
func TestRetryDump(t *testing.T) {
	errIntr := errors.New("dump interrupted")
	errOther := errors.New("other")

	t.Run("interrupted then complete", func(t *testing.T) {
		calls := 0
		got, err := retryDump(errIntr, func() ([]string, error) {
			calls++
			if calls < 3 {
				return []string{"partial"}, errIntr
			}
			return []string{"a", "b"}, nil
		})
		if err != nil || len(got) != 2 || calls != 3 {
			t.Fatalf("got %v, %v after %d calls; want [a b], nil after 3", got, err, calls)
		}
	})

	t.Run("always interrupted", func(t *testing.T) {
		calls := 0
		_, err := retryDump(errIntr, func() ([]string, error) {
			calls++
			return nil, errIntr
		})
		if !errors.Is(err, errIntr) || calls != dumpAttempts {
			t.Fatalf("got %v after %d calls; want %v after %d", err, calls, errIntr, dumpAttempts)
		}
	})

	t.Run("other error", func(t *testing.T) {
		calls := 0
		_, err := retryDump(errIntr, func() ([]string, error) {
			calls++
			return nil, errOther
		})
		if !errors.Is(err, errOther) || calls != 1 {
			t.Fatalf("got %v after %d calls; want %v after 1", err, calls, errOther)
		}
	})
}
