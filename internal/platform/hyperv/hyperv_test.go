package hyperv

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// TestGuestRequest: a guest that is still booting refuses the request of
// the Shutdown integration service (32768) until its service is up; a
// reboot right after poweron must still succeed.
func TestGuestRequest(t *testing.T) {
	t.Run("SRV-07", func(t *testing.T) {
		calls := 0
		ask := func(context.Context) (bool, int64, error) {
			calls++
			if calls < 3 {
				return false, 32768, nil
			}
			return true, 0, nil
		}
		ok, rv, err := untilAccepted(context.Background(), time.Second, time.Millisecond, ask)
		if err != nil || !ok || rv != 0 || calls != 3 {
			t.Fatalf("untilAccepted = %v, %d, %v after %d calls; want accepted after 3", ok, rv, err, calls)
		}

		refuse := func(context.Context) (bool, int64, error) { return false, 32768, nil }
		ok, rv, err = untilAccepted(context.Background(), 20*time.Millisecond, time.Millisecond, refuse)
		if err != nil || ok || rv != 32768 {
			t.Fatalf("untilAccepted of a guest that always refuses = %v, %d, %v; want refused with 32768", ok, rv, err)
		}
	})

	t.Run("SRV-07/shutdown", func(t *testing.T) {
		calls := 0
		ask := func(context.Context) (bool, int64, error) {
			calls++
			if calls < 3 {
				return false, 32768, nil
			}
			return true, 0, nil
		}
		if err := shutdown(context.Background(), "vm", time.Second, time.Millisecond, ask); err != nil || calls != 3 {
			t.Fatalf("shutdown of a booting guest = %v after %d calls; want success after 3", err, calls)
		}

		refuse := func(context.Context) (bool, int64, error) { return false, 32768, nil }
		err := shutdown(context.Background(), "vm", 20*time.Millisecond, time.Millisecond, refuse)
		var pe *platform.Error
		if !errors.As(err, &pe) || pe.Kind != platform.InvalidState {
			t.Fatalf("shutdown of a guest that refuses until the deadline = %v; want a platform error of kind InvalidState", err)
		}
	})
}
