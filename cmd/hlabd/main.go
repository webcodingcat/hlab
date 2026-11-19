//go:build windows

// Command hlabd is the hlab Windows service. Without arguments it runs the
// cloud under the service manager; `install [--dev]` and
// `uninstall-cleanup [--keep-data]` register and remove it (administrator
// rights needed).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/webcodingcat/hlab/internal/cloud"
	"github.com/webcodingcat/hlab/internal/logfile"
	hnwin "github.com/webcodingcat/hlab/internal/hostnet/windows"
	"github.com/webcodingcat/hlab/internal/sysint"
	"github.com/webcodingcat/hlab/internal/sysint/windows"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		return service()
	}
	var err error
	switch args[0] {
	case "install":
		fs := flag.NewFlagSet("install", flag.ContinueOnError)
		dev := fs.Bool("dev", false, "called by scripts/dev-setup.ps1")
		if fs.Parse(args[1:]) != nil || fs.NArg() > 0 {
			return 2
		}
		err = windows.NewInstaller().Install(*dev)
	case "uninstall-cleanup":
		fs := flag.NewFlagSet("uninstall-cleanup", flag.ContinueOnError)
		keep := fs.Bool("keep-data", false, "keep the data folder")
		if fs.Parse(args[1:]) != nil || fs.NArg() > 0 {
			return 2
		}
		err = uninstall(*keep)
	default:
		fmt.Fprintln(os.Stderr, "usage: hlabd [install [--dev] | uninstall-cleanup [--keep-data]]")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hlabd:", err)
		return 1
	}
	return 0
}

// uninstall stops and removes the service, removes the host networking of
// the profile (restoring the recorded host settings, which live in the data
// folder) and then, unless keepData, the data folder.
func uninstall(keepData bool) error {
	inst := windows.NewInstaller()
	if err := inst.Uninstall(true); err != nil {
		return err
	}
	if err := hnwin.New(hnwin.Options{StateDir: windows.DataDir()}).Down(context.Background()); err != nil {
		return err
	}
	if keepData {
		return nil
	}
	return inst.Uninstall(false)
}

// service runs the cloud under the service manager, logging JSON lines to
// hlabd.log in the profile's log folder (rotated at logfile.MaxBytes).
func service() int {
	paths, err := windows.Paths()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hlabd:", err)
		return 1
	}
	var out io.Writer = os.Stderr
	if w, err := logfile.NewWriter(paths.Logs, "hlabd.log", logfile.MaxBytes, logfile.Keep); err == nil {
		defer func() { _ = w.Close() }()
		out = w
	}
	level := new(slog.LevelVar)
	slog.SetDefault(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})))

	c := cloud.New(cloud.Deps{Paths: paths, Level: level})
	err = windows.NewServiceHost().Run(
		func(ctx context.Context, _ []string) error { return c.Start(ctx) },
		func(ctx context.Context, reason sysint.StopReason) error { return c.Stop(ctx, reason) },
	)
	if err != nil {
		slog.Error("service ended", "err", err)
		return 1
	}
	return 0
}
