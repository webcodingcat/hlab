package main

import (
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
