//go:build e2einstall

package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fixture is what the rows need in an installed release cloud: a project,
// a read/write token, one server, CA trust on with the CA in one WSL
// distro. It lives as long as the release data; a default uninstall drops it.
type fixture struct {
	project    string
	token      string
	serverID   int64
	serverName string
	vm         string // Hyper-V name of the server
}

var fix *fixture

// ensureFixture starts the release cloud and creates the fixture once per
// release data folder.
func ensureFixture(ctx context.Context) (*fixture, error) {
	if err := startReleaseCloud(ctx); err != nil {
		return nil, err
	}
	if fix != nil {
		return fix, nil
	}
	f := &fixture{project: "inst-" + time.Now().UTC().Format("20060102-150405")}
	logf("creating the fixture in project %s", f.project)
	if r, err := runRelease(ctx, 2*time.Minute, "project", "create", f.project); err != nil {
		return nil, fmt.Errorf("hlab project create: %v\n%s", err, r)
	}
	if ok, err := hasProject(ctx, f.project); err != nil || !ok {
		return nil, fmt.Errorf("project %s not in hlab project list (%v)", f.project, err)
	}
	r, err := runRelease(ctx, 2*time.Minute, "token", "create", "--project", f.project, "--name", "inst", "--output", "json")
	if err != nil {
		return nil, fmt.Errorf("hlab token create: %v (exit %d)\nstderr: %s", err, r.ExitCode, r.Stderr)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &tok); err != nil || tok.Token == "" {
		return nil, errors.New("hlab token create --output json printed no token")
	}
	f.token = tok.Token

	if err := f.createServer(ctx, f.project+"-srv"); err != nil {
		return nil, err
	}

	logf("turning CA trust on")
	if r, err := runRelease(ctx, 5*time.Minute, "ca", "trust", "on"); err != nil {
		return nil, fmt.Errorf("hlab ca trust on: %v\n%s", err, r)
	}
	thumb, err := caThumbprint(filepath.Join(releaseDataDir(), "ca", "ca.pem"))
	if err != nil {
		return nil, err
	}
	if err := waitFor(ctx, 3*time.Minute, 2*time.Second, func() (bool, error) { return rootHas(ctx, thumb) }); err != nil {
		return nil, fmt.Errorf(`precondition: the hlab CA %s is not in LocalMachine\Root after trust on: %w`, thumb, err)
	}
	if err := waitFor(ctx, 3*time.Minute, 2*time.Second, func() (bool, error) { return wslHasCAFile(ctx) }); err != nil {
		return nil, fmt.Errorf("precondition: %s is not in distro %s after trust on: %w", wslCAFile, harness.distro, err)
	}
	fix = f
	return f, nil
}

func hasProject(ctx context.Context, name string) (bool, error) {
	r, err := runRelease(ctx, 2*time.Minute, "project", "list", "--output", "json")
	if err != nil {
		return false, fmt.Errorf("hlab project list: %v\n%s", err, r)
	}
	var body struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &body); err != nil {
		return false, fmt.Errorf("hlab project list --output json: %w\n%s", err, r.Stdout)
	}
	for _, p := range body.Projects {
		if p.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// createServer creates one Ubuntu server through the Hetzner API and waits
// for its create action.
func (f *fixture) createServer(ctx context.Context, name string) error {
	logf("creating server %s", name)
	var created struct {
		Server struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"server"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	body := map[string]any{"name": name, "server_type": serverType, "location": serverLocation, "image": serverImage}
	code, err := f.api(ctx, http.MethodPost, "/servers", body, &created)
	if err != nil || code != http.StatusCreated {
		return fmt.Errorf("POST /servers: status %d: %v", code, err)
	}
	f.serverID, f.serverName = created.Server.ID, created.Server.Name
	f.vm = fmt.Sprintf("%s-server-%d", releaseTag, f.serverID)
	var last string
	err = waitFor(ctx, 30*time.Minute, 5*time.Second, func() (bool, error) {
		var a struct {
			Action struct {
				Status string `json:"status"`
			} `json:"action"`
		}
		code, err := f.api(ctx, http.MethodGet, fmt.Sprintf("/actions/%d", created.Action.ID), nil, &a)
		if err != nil || code != http.StatusOK {
			return false, nil
		}
		last = a.Action.Status
		if last == "error" {
			return false, fmt.Errorf("create action of server %s failed", name)
		}
		return last == "success", nil
	})
	if err != nil {
		return fmt.Errorf("server %s (last action status %q): %w", name, last, err)
	}
	if ok, err := vmExists(ctx, f.vm); err != nil || !ok {
		return fmt.Errorf("precondition: VM %s of server %s does not exist (%v)", f.vm, name, err)
	}
	return nil
}

// api sends method path with the fixture's token; a 2xx body is decoded into out.
func (f *fixture) api(ctx context.Context, method, path string, body, out any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiEndpoint+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%s %s: %s", method, path, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: %w\n%s", method, path, err, data)
		}
	}
	return resp.StatusCode, nil
}

// userFolders are the uninstalling user's folders of the tray.
func userFolders() []string {
	return []string{
		filepath.Join(os.Getenv("AppData"), "hlab-tray.exe"),
		filepath.Join(os.Getenv("LocalAppData"), "hlab"),
	}
}

// startTray starts the installed tray as the logged-on user (explorer.exe
// starts it without elevation) and waits until it runs. The tray's WebView2
// folder appears once its window was created; a folder that does not appear
// within the wait gets a marker, as does %LocalAppData%\hlab, which no
// product code writes, so the uninstall must delete both.
func startTray(ctx context.Context) error {
	tray := filepath.Join(releaseDir(), "hlab-tray.exe")
	_, _ = command(ctx, time.Minute, "", "explorer.exe", tray) // explorer.exe exits 1 after handing the start on
	if err := waitFor(ctx, time.Minute, time.Second, func() (bool, error) { return trayRunning(ctx) }); err != nil {
		return fmt.Errorf("precondition: hlab-tray.exe does not run: %w", err)
	}
	folders := userFolders()
	if err := waitFor(ctx, time.Minute, time.Second, func() (bool, error) { return !notExist(folders[0]), nil }); err != nil {
		logf("%s did not appear; a marker stands for it", folders[0])
	}
	for _, d := range folders {
		if notExist(d) {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(d, "hlab-e2einstall-marker"), []byte("marker\n"), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// defaultUninstall is the one default quiet uninstall INST-04,
// INST-04/dev-profile and INST-05 share: release installed, fixture with a
// running cloud, the tray running.
var defaultUninstall struct {
	done       bool
	gen        int // harness.installGen of the install it removed
	err        error
	thumbprint string // the hlab CA before the uninstall
}

func sharedDefaultUninstall(ctx context.Context) error {
	du := &defaultUninstall
	if du.done && du.gen == harness.installGen {
		return du.err
	}
	du.done = true
	du.err = func() error {
		if harness.distroErr != nil {
			return harness.distroErr
		}
		if err := ensureInstalled(ctx, newVersion); err != nil {
			return err
		}
		if _, err := ensureFixture(ctx); err != nil {
			return err
		}
		thumb, err := caThumbprint(filepath.Join(releaseDataDir(), "ca", "ca.pem"))
		if err != nil {
			return err
		}
		du.thumbprint = thumb
		if start, desk := shortcuts(); len(start) == 0 || len(desk) == 0 {
			return fmt.Errorf("precondition: shortcuts HLab missing after the install (start menu %v, desktop %v)", start, desk)
		}
		if err := startTray(ctx); err != nil {
			return err
		}
		return uninstall(ctx, false)
	}()
	du.gen = harness.installGen
	return du.err
}
