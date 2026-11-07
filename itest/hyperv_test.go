//go:build live && windows

package itest

import (
	"log/slog"
	"os"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/contract"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

// TestHyperVKeepsTheContract runs the hypervisor's contract tests against
// Hyper-V. Its disks go to a temporary folder on the Windows disk: Hyper-V
// cannot use files in WSL2's file system.
func TestHyperVKeepsTheContract(t *testing.T) {
	hypervisor := windows.NewHypervisor(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { _ = hypervisor.Close() })
	contract.Hypervisor(t, hypervisor, t.TempDir())
}
