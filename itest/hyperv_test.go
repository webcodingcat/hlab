//go:build live && windows

package itest

import (
	"log/slog"
	"os"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/contract"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

// quietLogger logs the PowerShell processes' warnings only.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// TestHyperVKeepsTheContract runs the hypervisor's contract tests against
// Hyper-V. Its disks go to a temporary folder on the Windows disk: Hyper-V
// cannot use files in WSL2's file system.
func TestHyperVKeepsTheContract(t *testing.T) {
	hypervisor := windows.NewHypervisor(quietLogger())
	t.Cleanup(func() { _ = hypervisor.Close() })
	contract.Hypervisor(t, hypervisor, t.TempDir())
}
