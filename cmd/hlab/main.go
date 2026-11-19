//go:build windows

// Command hlab is the management command of hlab.
package main

import (
	"context"
	"fmt"
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
	var logDir string
	if paths, err := windows.Paths(); err == nil {
		logDir = paths.Logs
	}
	code := cli.Run(ctx, os.Args[1:], cli.IO{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Dial:       windows.DialManagement,
		Service:    windows.NewServiceControl(profile.Current().ServiceName),
		IsTerminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		StatusFile: filepath.Join(windows.DataDir(), "status.json"),
		Getenv:     os.Getenv,
		ReadSecret: readSecret,
		LogDir:     logDir,
	})
	stop()
	os.Exit(code)
}

// readSecret shows prompt on stderr and reads a line from the console
// without echo.
func readSecret(prompt string) (string, error) {
	_, _ = fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	_, _ = fmt.Fprintln(os.Stderr)
	return string(b), err
}
