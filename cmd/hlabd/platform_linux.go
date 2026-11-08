package main

import (
	"fmt"
	"log/slog"

	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/linux"
)

// defaultDataDir is where the service keeps its data on Linux.
const defaultDataDir = labapi.LinuxDataDir

// nativeLifecycle returns this machine's life cycle.
func nativeLifecycle() platform.Lifecycle {
	return linux.NewLifecycle(linux.LockFile)
}

// nativePlatform: the Linux platform comes with phase 8.
func nativePlatform(name string, _ *slog.Logger) (platform.Platform, func() error, error) {
	return platform.Platform{}, nil, fmt.Errorf("the platform %s is not yet implemented: use --platform %s", name, fakePlatform)
}
