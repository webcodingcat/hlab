//go:build live && windows

package itest

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/contract"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

// quietLogger logs the PowerShell processes' warnings only.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// newParts returns the Windows platform for a test: with HLab's own mutex,
// so that no HLab runs alongside, but with an instance file in instanceDir,
// so that the test never names its data folder to the CLI.
func newParts(instanceDir string) *windows.Platform {
	parts := windows.New(quietLogger())
	parts.Lifecycle = windows.NewLifecycle(windows.MutexName, filepath.Join(instanceDir, "instance"),
		parts.Lifecycle.AgentDialer())
	return parts
}

// TestHyperVKeepsTheContract runs the hypervisor's contract tests against
// Hyper-V. Its disks go to a temporary folder on the Windows disk: Hyper-V
// cannot use files in WSL2's file system.
func TestHyperVKeepsTheContract(t *testing.T) {
	hypervisor := windows.NewHypervisor(quietLogger())
	t.Cleanup(func() { _ = hypervisor.Close() })
	contract.Hypervisor(t, hypervisor, t.TempDir())
}
