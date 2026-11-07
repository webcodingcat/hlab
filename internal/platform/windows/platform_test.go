//go:build windows

package windows_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

func TestThePartsWithoutHyperVReportNotImplemented(t *testing.T) {
	parts := windows.New(slog.New(slog.DiscardHandler))
	ctx := t.Context()
	_, dialErr := parts.Lifecycle.AgentDialer().Dial(ctx, "appliance", 1)
	calls := map[string]error{
		"HostNet.SetUp":    parts.HostNet.SetUp(ctx, platform.LinkSpec{}),
		"HostDNS.Apply":    parts.HostDNS.Apply(ctx, nil),
		"AgentDialer.Dial": dialErr,
	}
	for name, err := range calls {
		if !errors.Is(err, platform.ErrNotImplemented) {
			t.Errorf("%s: got %v, want platform.ErrNotImplemented", name, err)
		}
	}
}
