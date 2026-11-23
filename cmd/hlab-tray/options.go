package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/webcodingcat/hlab/internal/bridge"
	"github.com/webcodingcat/hlab/internal/profile"
	"github.com/webcodingcat/hlab/ui"
)

// e2eOptions changes the options of the e2e build (tag e2e); nil in every
// other build.
var e2eOptions func(*application.Options)

// appOptions returns the application options: the three bridge services,
// the file dialogs, the window service, the extra services (the console
// proxy), the embedded frontend and one tray per user and profile. A
// second start shows and focuses the window of the first and exits.
func appOptions(svc *bridge.Service, cloud *bridge.Cloud, logs *bridge.Logs, extra ...application.Service) application.Options {
	opts := application.Options{
		Name:        "HLab",
		Description: "Local Hetzner-compatible cloud",
		Services: []application.Service{
			application.NewService(svc),
			application.NewService(cloud),
			application.NewService(logs),
			application.NewService(&Files{}),
			application.NewService(consoleWindows),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(ui.Dist),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.webcodingcat.hlab-tray." + profile.Current().Name,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				tray.showWindow()
			},
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		OnShutdown: func() { tray.quitting.Store(true) },
	}
	opts.Services = append(opts.Services, extra...)
	if e2eOptions != nil {
		e2eOptions(&opts)
	}
	return opts
}
