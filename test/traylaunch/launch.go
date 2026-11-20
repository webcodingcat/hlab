// Package traylaunch builds and starts the e2e build of hlab-tray (tag
// e2e) with a remote debugging port, so Playwright can drive its window
// over CDP. The UI end-to-end tests (test/e2e) and the README screenshot
// tool (tools/screenshots) share it.
package traylaunch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Tray is a running e2e tray.
type Tray struct {
	Port int    // CDP port on 127.0.0.1
	Log  string // file with the tray's stdout and stderr

	cmd     *exec.Cmd
	exited  chan struct{}
	waitErr error
}

// Build builds cmd/hlab-tray of the repository at root with tag e2e and the
// dev profile into exe. The dev profile finds the shipped catalog next to
// bin\, so exe belongs into root\bin.
func Build(ctx context.Context, root, exe string) error {
	build := exec.CommandContext(ctx, "go", "build", "-tags", "e2e",
		"-ldflags", "-X github.com/webcodingcat/hlab/internal/profile.Name=dev -H windowsgui",
		"-o", exe, "./cmd/hlab-tray")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build -tags e2e ./cmd/hlab-tray: %w\n%s", err, out)
	}
	return nil
}

// Start starts exe (built by Build) with a free CDP port and the WebView
// user data folder webviewDir, the environment of this process plus env,
// and waits until the CDP endpoint answers or ctx ends. The tray's output
// goes to hlab-tray-e2e.log next to webviewDir.
func Start(ctx context.Context, exe, webviewDir string, env []string) (*Tray, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	t := &Tray{
		Port:   port,
		Log:    filepath.Join(filepath.Dir(webviewDir), "hlab-tray-e2e.log"),
		exited: make(chan struct{}),
	}
	logf, err := os.Create(t.Log)
	if err != nil {
		return nil, err
	}
	t.cmd = exec.Command(exe)
	t.cmd.Dir = filepath.Dir(filepath.Dir(exe))
	t.cmd.Stdout = logf
	t.cmd.Stderr = logf
	t.cmd.Env = append(append(os.Environ(), env...),
		"HLAB_E2E_CDP_PORT="+strconv.Itoa(port),
		"HLAB_E2E_WEBVIEW_DIR="+webviewDir,
	)
	if err := t.cmd.Start(); err != nil {
		_ = logf.Close()
		return nil, fmt.Errorf("start %s: %w", exe, err)
	}
	go func() {
		t.waitErr = t.cmd.Wait()
		_ = logf.Close()
		close(t.exited)
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	client := &http.Client{Timeout: 5 * time.Second}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-t.exited:
			return nil, fmt.Errorf("the e2e tray ended at start (%v); its log: %s", t.waitErr, t.Log)
		default:
		}
		if resp, err := client.Get(url); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return t, nil
			}
		}
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("CDP endpoint %s: %w", url, ctx.Err())
		case <-tick.C:
		}
	}
}

// Done is closed when the tray process has ended.
func (t *Tray) Done() <-chan struct{} { return t.exited }

// Err is how the tray process ended; valid once Done is closed.
func (t *Tray) Err() error { return t.waitErr }

// Stop ends the tray process and waits up to a minute for it.
func (t *Tray) Stop() {
	select {
	case <-t.exited:
		return
	default:
	}
	_ = t.cmd.Process.Kill()
	select {
	case <-t.exited:
	case <-time.After(time.Minute):
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
