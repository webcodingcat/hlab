//go:build windows

// Command hlab is the management command of hlab.
package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"

	"golang.org/x/term"

	"github.com/webcodingcat/hlab/internal/cli"
	"github.com/webcodingcat/hlab/internal/profile"
	"github.com/webcodingcat/hlab/internal/sysint/windows"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.IO{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Dial:       windows.DialManagement,
		Service:    windows.NewServiceControl(profile.Current().ServiceName),
		IsTerminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		StatusFile: filepath.Join(windows.DataDir(), "status.json"),
	})
	stop()
	os.Exit(code)
}
