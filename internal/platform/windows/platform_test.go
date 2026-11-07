//go:build windows

package windows_test

import (
	"errors"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

func TestEveryPartReportsNotImplemented(t *testing.T) {
	parts := windows.New()
	ctx := t.Context()
	_, lockErr := parts.Lifecycle.Lock(t.TempDir())
	_, dialErr := parts.Lifecycle.AgentDialer().Dial(ctx, "appliance", 1)
	_, stateErr := parts.Hypervisor.VMState(ctx, "web")
	calls := map[string]error{
		"Hypervisor.StartVM":   parts.Hypervisor.StartVM(ctx, "web"),
		"Hypervisor.VMState":   stateErr,
		"HostNet.SetUp":        parts.HostNet.SetUp(ctx, platform.LinkSpec{}),
		"HostDNS.Apply":        parts.HostDNS.Apply(ctx, nil),
		"Lifecycle.Lock":       lockErr,
		"Lifecycle.WriteToken": parts.Lifecycle.WriteToken("token", nil),
		"AgentDialer.Dial":     dialErr,
	}
	for name, err := range calls {
		if !errors.Is(err, platform.ErrNotImplemented) {
			t.Errorf("%s: got %v, want platform.ErrNotImplemented", name, err)
		}
	}
}
