package main

import (
	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

// defaultDataDir is where the service keeps its data on Windows.
const defaultDataDir = labapi.WindowsDataDir

// nativeLifecycle returns this machine's life cycle.
func nativeLifecycle() platform.Lifecycle {
	return windows.NewLifecycle(windows.MutexName, windows.InstanceFile())
}
