package main

import (
	"log/slog"

	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

// defaultDataDir is where the service keeps its data on Windows.
const defaultDataDir = labapi.WindowsDataDir

// nativeLifecycle returns this machine's life cycle without agents: the fake
// platform brings its own.
func nativeLifecycle() platform.Lifecycle {
	return windows.NewLifecycle(windows.MutexName, windows.InstanceFile(), nil)
}

// nativePlatform returns Windows with Hyper-V and a function that stops its
// PowerShell processes.
func nativePlatform(_ string, logger *slog.Logger) (platform.Platform, func() error, error) {
	parts := windows.New(logger)
	return parts.Platform, parts.Close, nil
}
