package system_test

import (
	"testing"

	"github.com/webcodingcat/hlab/internal/system"
	"github.com/webcodingcat/hlab/internal/vhdx"
)

// The guest's udev rule (internal/seed) reads the volume's ID and the
// automount flag from these WWNs.
func TestVolumeDiskIDShowsTheIDInTheWWN(t *testing.T) {
	for _, tc := range []struct {
		id        int64
		automount bool
		want      string
	}{
		{1000005, true, "0x60022480484c41420100000001000005"},
		{107411521, false, "0x60022480484c41420000000107411521"},
		{999999999999, false, "0x60022480484c41420000999999999999"},
	} {
		if got := vhdx.WWN(system.VolumeDiskID(tc.id, tc.automount)); got != tc.want {
			t.Errorf("the WWN of volume %d (automount %v) = %s, want %s", tc.id, tc.automount, got, tc.want)
		}
	}
}
