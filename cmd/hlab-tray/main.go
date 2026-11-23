//go:build windows

// Command hlab-tray is the tray program of hlab: a tray icon with a menu
// and the console window.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/webcodingcat/hlab/internal/bridge"
	"github.com/webcodingcat/hlab/internal/mgmtclient"
	"github.com/webcodingcat/hlab/internal/profile"
	"github.com/webcodingcat/hlab/internal/sysint"
	"github.com/webcodingcat/hlab/internal/sysint/windows"
)

// pollInterval is how often the cloud state is polled and a broken log
// stream reconnects.
const pollInterval = 2 * time.Second

// logBatch is how long log lines are collected for one "logs" event.
const logBatch = 100 * time.Millisecond

func main() {
	paths, err := windows.Paths()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "hlab-tray:", err)
		os.Exit(1)
	}
	client := mgmtclient.New(windows.DialManagement)
	scm := windows.NewServiceControl(profile.Current().ServiceName)

	cloud := bridge.NewCloud(bridge.CloudDeps{
		Service:    scm,
		Client:     client,
		StatusFile: filepath.Join(paths.State, "status.json"),
		Interval:   pollInterval,
		OnChange:   tray.cloudChanged,
	})
	logs := bridge.NewLogs(bridge.LogsDeps{
		Client:   client,
		LogDir:   paths.Logs,
		Running:  func() bool { return cloud.Status().State == sysint.StateRunning },
		Emit:     tray.emitLogs,
		Interval: pollInterval,
		Batch:    logBatch,
	})
	svc := bridge.NewService(bridge.Deps{
		Client: client,
		Paths:  paths,
		Running: func(ctx context.Context) bool {
			st, err := scm.State(ctx)
			return err == nil && st == sysint.StateRunning
		},
	})

	console := bridge.NewConsole(bridge.ConsoleDeps{
		CA: func(ctx context.Context) ([]byte, error) {
			status, body, err := client.Do(ctx, http.MethodGet, "/ca.pem", nil)
			if err != nil {
				return nil, err
			}
			if status != http.StatusOK {
				return nil, mgmtclient.AnswerError(status, body)
			}
			return body, nil
		},
	})

	// A second instance exits inside application.New, before anything
	// else starts.
	app := application.New(appOptions(svc, cloud, logs, application.NewService(console)))
	consoleWindows.setApp(app)
	tray.setup(app, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go cloud.Run(ctx)
	})
	err = app.Run()
	cancel()
	logs.Follow(false)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "hlab-tray:", err)
		os.Exit(1)
	}
}
