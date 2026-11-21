//go:build windows

// Command hlabd is the hlab Windows service. Without arguments it runs the
// cloud under the service manager; `install [--dev]` and
// `uninstall-cleanup [--keep-data]` register and remove it (administrator
// rights needed); `check-prerequisites [--ui]` lists what the machine lacks
// (used by the installer before anything is installed).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	xwin "golang.org/x/sys/windows"

	"github.com/webcodingcat/hlab/internal/cloud"
	hnwin "github.com/webcodingcat/hlab/internal/hostnet/windows"
	"github.com/webcodingcat/hlab/internal/logfile"
	"github.com/webcodingcat/hlab/internal/prereq"
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
	case "check-prerequisites":
		fs := flag.NewFlagSet("check-prerequisites", flag.ContinueOnError)
		ui := fs.Bool("ui", false, "show the result in a message box")
		if fs.Parse(args[1:]) != nil || fs.NArg() > 0 {
			return 2
		}
		return checkPrerequisites(*ui)
	default:
		fmt.Fprintln(os.Stderr, "usage: hlabd [install [--dev] | uninstall-cleanup [--keep-data] | check-prerequisites [--ui]]")
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

// prereqLog is the file in %TEMP% that keeps the list of a failed check
// for the setup log.
const prereqLog = "hlab-setup-prerequisites.log"

// checkPrerequisites prints what the machine lacks, writes it to
// %TEMP%\hlab-setup-prerequisites.log and, with ui, shows it in a message
// box. It returns 1 when anything is missing or the facts cannot be read.
func checkPrerequisites(ui bool) int {
	var lines []string
	f, err := windows.Prerequisites(context.Background())
	if err != nil {
		lines = []string{"Could not check the prerequisites of hlab: " + err.Error()}
	} else {
		lines = prereq.Missing(f)
	}
	if len(lines) == 0 {
		fmt.Println("All prerequisites of hlab are present.")
		return 0
	}
	text := "hlab cannot be installed on this machine:\r\n\r\n- " + strings.Join(lines, "\r\n- ") + "\r\n"
	fmt.Print(text)
	if err := os.WriteFile(filepath.Join(os.TempDir(), prereqLog), []byte(text), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "hlabd:", err)
	}
	if ui {
		body, _ := xwin.UTF16PtrFromString(text)
		caption, _ := xwin.UTF16PtrFromString("HLab Setup")
		_, _ = xwin.MessageBox(0, body, caption, xwin.MB_OK|xwin.MB_ICONERROR|xwin.MB_TOPMOST|xwin.MB_SETFOREGROUND)
	}
	return 1
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
		func(ctx context.Context, params []string) error { return c.Start(ctx, params) },
		func(ctx context.Context, reason sysint.StopReason) error { return c.Stop(ctx, reason) },
	)
	if err != nil {
		slog.Error("service ended", "err", err)
		return 1
	}
	return 0
}
