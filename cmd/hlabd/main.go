//go:build windows

// Command hlabd is the hlab Windows service. Without arguments it runs the
// cloud under the service manager. The other commands are called by the
// MSI and scripts/dev-setup.ps1: `install [--dev]` registers the service,
// `uninstall-cleanup [--keep-data]` removes everything of the profile from
// the host, `stop-service` stops a running cloud before an upgrade (these
// need administrator rights), `remove-user-data` deletes the per-user
// folders of the calling user and `check-prerequisites [--ui]` lists what
// the machine lacks.
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
	"github.com/webcodingcat/hlab/internal/platform/hyperv"
	"github.com/webcodingcat/hlab/internal/prereq"
	"github.com/webcodingcat/hlab/internal/profile"
	"github.com/webcodingcat/hlab/internal/sysint"
	"github.com/webcodingcat/hlab/internal/sysint/windows"
	"github.com/webcodingcat/hlab/internal/uninstall"
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
		err = uninstallCleanup(*keep)
	case "stop-service":
		if len(args) > 1 {
			return 2
		}
		err = windows.StopService(context.Background())
	case "remove-user-data":
		if len(args) > 1 {
			return 2
		}
		err = removeUserData()
	case "check-prerequisites":
		fs := flag.NewFlagSet("check-prerequisites", flag.ContinueOnError)
		ui := fs.Bool("ui", false, "show the result in a message box")
		if fs.Parse(args[1:]) != nil || fs.NArg() > 0 {
			return 2
		}
		return checkPrerequisites(*ui)
	default:
		fmt.Fprintln(os.Stderr, "usage: hlabd [install [--dev] | uninstall-cleanup [--keep-data] | stop-service | remove-user-data | check-prerequisites [--ui]]")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hlabd:", err)
		return 1
	}
	return 0
}

// uninstallCleanup removes the service, the host settings, the CA trust,
// the VMs and the switch of the profile and, unless keepData, its files. It
// prints what was skipped; the error lists each object that could not be
// removed.
func uninstallCleanup(keepData bool) error {
	ctx := context.Background()
	tag := profile.Current().Tag
	hv, closeHV := hypervisor(ctx, tag)
	defer closeHV()
	res, err := uninstall.Run(ctx, uninstall.Options{
		Tag:        tag,
		DataDir:    windows.DataDir(),
		KeepData:   keepData,
		Service:    serviceRemover{},
		Host:       hnwin.New(hnwin.Options{StateDir: windows.DataDir()}),
		Hypervisor: hv,
	})
	for _, n := range res.Notes {
		fmt.Println("note:", n)
	}
	if err != nil {
		return fmt.Errorf("uninstall-cleanup could not remove:\n- %s", strings.ReplaceAll(err.Error(), "\n", "\n- "))
	}
	return nil
}

// serviceRemover is the uninstall.Service of the current profile.
type serviceRemover struct{}

func (serviceRemover) RemoveService(ctx context.Context, mode string) error {
	return windows.RemoveService(ctx, mode)
}

// hypervisor returns the Hyper-V platform of the profile. When Hyper-V
// cannot be loaded and its management service is not registered, nothing
// of hlab can be left in Hyper-V: the result lists no VMs and has no switch.
// Otherwise a Hyper-V that cannot be loaded reports the load error for the
// VMs and the switch, so the cleanup does every other step and names it.
func hypervisor(ctx context.Context, tag string) (uninstall.Hypervisor, func()) {
	p, err := hyperv.New(hyperv.Options{SwitchName: tag})
	if err == nil {
		return p, func() {
			if cl, ok := p.(interface{ Close() error }); ok {
				_ = cl.Close()
			}
		}
	}
	if f, ferr := windows.Prerequisites(ctx); ferr == nil && !f.VMMS {
		fmt.Println("note: Hyper-V is not installed; no VMs or switch to remove")
		return noHyperV{}, func() {}
	}
	return brokenHyperV{fmt.Errorf("load Hyper-V: %w", err)}, func() {}
}

// noHyperV stands in for a Hyper-V that is not installed.
type noHyperV struct{}

func (noHyperV) VMs(context.Context, string) ([]string, error) { return nil, nil }
func (noHyperV) DeleteVM(context.Context, string) error        { return nil }
func (noHyperV) DeleteSwitch(context.Context, string) error    { return nil }

// brokenHyperV stands in for a Hyper-V that is installed but cannot be
// loaded: listing the VMs and deleting the switch fail with err.
type brokenHyperV struct{ err error }

func (h brokenHyperV) VMs(context.Context, string) ([]string, error) { return nil, h.err }
func (h brokenHyperV) DeleteVM(context.Context, string) error        { return h.err }
func (h brokenHyperV) DeleteSwitch(context.Context, string) error    { return h.err }

// removeUserData deletes the per-user folders of the calling user below
// %AppData% and %LocalAppData%.
func removeUserData() error {
	appData, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	localAppData, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	return uninstall.RemoveUserData(appData, localAppData)
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
