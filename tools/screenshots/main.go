// Command screenshots renders the README screenshots of the tray window
// (`task docs:screenshots`, run from the repository root with the dev cloud
// running): the project list and the "API tokens" and "Storage Boxes"
// sections of a project console, each in light and dark, into docs/images.
//
// It creates the sample projects demo-web, demo-staging and demo-ci and a
// token "ci" in demo-web through bin\hlab.exe, a storage box "backups" in
// demo-web through the Hetzner API with a token that lives only for that
// step, starts the e2e build of the tray (test/traylaunch) and runs the
// Playwright project "screenshots" against its window over CDP. Afterwards
// it deletes only the projects, the token and the box it created; a token
// value is never printed or shown.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/test/traylaunch"
)

// samples are the projects shown on the screenshots; the spec filters the
// list by their common prefix.
var samples = []string{"demo-web", "demo-staging", "demo-ci"}

const (
	tokenProject = "demo-web"
	tokenName    = "ci"
	// boxName is the storage box in tokenProject; tempToken is the token
	// that creates or deletes it and is revoked right after.
	boxName   = "backups"
	tempToken = "screenshots"
	// hetznerEndpoint is hlab's Hetzner API (storage boxes), apiEndpoint
	// its Cloud API, whose /actions also hold storage box actions.
	hetznerEndpoint = "http://api.hetzner.internal/hetzner/v1"
	apiEndpoint     = "http://api.hetzner.internal/v1"
	boxLocation     = "fsn1"
	boxType         = "bx11"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "screenshots:", err)
		os.Exit(1)
	}
}

func run() (err error) {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return errors.New("run from the repository root (task docs:screenshots)")
	}
	hlab := filepath.Join(root, "bin", "hlab.exe")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	var status struct {
		State string `json:"state"`
	}
	if err := hlabJSON(ctx, hlab, &status, "cloud", "status"); err != nil {
		return err
	}
	if status.State != "running" {
		return fmt.Errorf("the dev cloud is %s; start it first (bin\\hlab.exe cloud start)", status.State)
	}

	created, err := createSamples(ctx, hlab)
	defer func() {
		if cerr := cleanup(hlab, created); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "hlab-screenshots-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	exe := filepath.Join(root, "bin", "hlab-tray-e2e.exe")
	fmt.Println("building the e2e tray")
	if err := traylaunch.Build(ctx, root, exe); err != nil {
		return err
	}
	startCtx, startCancel := context.WithTimeout(ctx, 2*time.Minute)
	tray, err := traylaunch.Start(startCtx, exe, filepath.Join(tmp, "webview"))
	startCancel()
	if err != nil {
		return err
	}
	defer tray.Stop()

	frontend := filepath.Join(root, "ui", "frontend")
	out := filepath.Join(root, "docs", "images")
	cmd := exec.CommandContext(ctx, "node",
		filepath.Join(frontend, "node_modules", "@playwright", "test", "cli.js"), "test",
		"--config", filepath.Join(frontend, "playwright.config.ts"),
		"--project", "screenshots", "--reporter", "line")
	cmd.Dir = frontend
	cmd.Env = append(os.Environ(),
		"HLAB_E2E_CDP_PORT="+strconv.Itoa(tray.Port),
		"HLAB_SCREENSHOTS_DIR="+out,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("playwright test --project screenshots: %w (tray log: %s)", err, tray.Log)
	}
	fmt.Println("screenshots written to", out)
	return nil
}

// created is what this run added and removes again.
type created struct {
	projects []string
	token    bool  // the token in tokenProject, when that project existed before
	box      int64 // the storage box in tokenProject, when that project existed before
}

// createSamples creates the sample projects that do not exist yet and the
// token in tokenProject.
func createSamples(ctx context.Context, hlab string) (created, error) {
	var c created
	var list struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := hlabJSON(ctx, hlab, &list, "project", "list"); err != nil {
		return c, err
	}
	existing := map[string]bool{}
	for _, p := range list.Projects {
		existing[p.Name] = true
	}
	for _, name := range samples {
		if existing[name] {
			continue
		}
		if err := hlabJSON(ctx, hlab, nil, "project", "create", name); err != nil {
			return c, err
		}
		c.projects = append(c.projects, name)
	}

	var tokens struct {
		Tokens []struct {
			Name string `json:"name"`
		} `json:"tokens"`
	}
	if err := hlabJSON(ctx, hlab, &tokens, "token", "list", "--project", tokenProject); err != nil {
		return c, err
	}
	hasToken := false
	for _, t := range tokens.Tokens {
		hasToken = hasToken || t.Name == tokenName
	}
	if !hasToken {
		// The output holds the token value; hlabJSON never shows it.
		if err := hlabJSON(ctx, hlab, nil, "token", "create", "--project", tokenProject, "--name", tokenName); err != nil {
			return c, err
		}
		c.token = !slices.Contains(c.projects, tokenProject)
	}
	return c, createBox(ctx, hlab, &c)
}

// createBox creates the storage box boxName in tokenProject unless it
// exists and waits until its creation (which may boot the storage VM) is
// done.
func createBox(ctx context.Context, hlab string, c *created) error {
	return withToken(ctx, hlab, func(token string) error {
		var boxes struct {
			StorageBoxes []struct {
				ID int64 `json:"id"`
			} `json:"storage_boxes"`
		}
		if err := request(ctx, token, http.MethodGet, hetznerEndpoint+"/storage_boxes?name="+boxName, nil, &boxes); err != nil {
			return err
		}
		if len(boxes.StorageBoxes) > 0 {
			return nil
		}
		var resp struct {
			StorageBox struct {
				ID int64 `json:"id"`
			} `json:"storage_box"`
			Action struct {
				ID int64 `json:"id"`
			} `json:"action"`
		}
		body := map[string]any{
			"name":             boxName,
			"location":         boxLocation,
			"storage_box_type": boxType,
			// Upper and lower case, a digit and a special character.
			"password": "Shot-" + rand.Text()[:12] + "-9z",
		}
		fmt.Println("creating the storage box", boxName)
		if err := request(ctx, token, http.MethodPost, hetznerEndpoint+"/storage_boxes", body, &resp); err != nil {
			return err
		}
		if !slices.Contains(c.projects, tokenProject) {
			c.box = resp.StorageBox.ID
		}
		return poll(ctx, 20*time.Minute, func() (bool, error) {
			var a struct {
				Action struct {
					Status string `json:"status"`
				} `json:"action"`
			}
			err := request(ctx, token, http.MethodGet, fmt.Sprintf("%s/actions/%d", apiEndpoint, resp.Action.ID), nil, &a)
			if err == nil && a.Action.Status == "error" {
				err = errors.New("creating the storage box failed")
			}
			return a.Action.Status == "success", err
		})
	})
}

// deleteBox deletes the storage box id of tokenProject and waits until it
// is gone.
func deleteBox(ctx context.Context, hlab string, id int64) error {
	return withToken(ctx, hlab, func(token string) error {
		url := fmt.Sprintf("%s/storage_boxes/%d", hetznerEndpoint, id)
		return poll(ctx, 10*time.Minute, func() (bool, error) {
			var se statusError
			err := request(ctx, token, http.MethodDelete, url, nil, nil)
			switch {
			case errors.As(err, &se) && se == http.StatusNotFound:
				return true, nil
			case errors.As(err, &se) && se == http.StatusLocked:
				return false, nil // an action of the box still runs
			case err != nil:
				return false, err
			}
			err = request(ctx, token, http.MethodGet, url, nil, nil)
			if errors.As(err, &se) && se == http.StatusNotFound {
				return true, nil
			}
			return false, err
		})
	})
}

// withToken creates the token tempToken in tokenProject, calls fn with its
// value and revokes the token again.
func withToken(ctx context.Context, hlab string, fn func(token string) error) (err error) {
	var t struct {
		Token string `json:"token"`
	}
	if err := hlabJSON(ctx, hlab, &t, "token", "create", "--project", tokenProject, "--name", tempToken); err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, hlabJSON(ctx, hlab, nil, "token", "revoke", tempToken, "--project", tokenProject))
	}()
	return fn(t.Token)
}

// statusError is an HTTP status other than 2xx.
type statusError int

func (e statusError) Error() string { return fmt.Sprintf("status %d", int(e)) }

// request sends one request with the token and an optional JSON body and
// decodes a JSON answer into v (nil: discarded).
func request(ctx context.Context, token, method, url string, body, v any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %w", method, url, statusError(resp.StatusCode))
	}
	if v == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// poll calls done every two seconds until it reports true, fails or the
// limit passes.
func poll(ctx context.Context, limit time.Duration, done func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		ok, err := done()
		if ok || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("still waiting after %s: %w", limit, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// cleanup deletes the storage box, revokes the token and deletes the
// projects this run created; a project takes its storage box with it.
func cleanup(hlab string, c created) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var errs []error
	if c.box != 0 {
		errs = append(errs, deleteBox(ctx, hlab, c.box))
	}
	if c.token {
		errs = append(errs, hlabJSON(ctx, hlab, nil, "token", "revoke", tokenName, "--project", tokenProject))
	}
	for _, name := range c.projects {
		errs = append(errs, hlabJSON(ctx, hlab, nil, "project", "delete", name))
	}
	return errors.Join(errs...)
}

// hlabJSON runs hlab.exe with --output json and decodes stdout into v (nil:
// discarded). Errors carry only stderr, never stdout, which may hold a
// token.
func hlabJSON(ctx context.Context, hlab string, v any, args ...string) error {
	cmd := exec.CommandContext(ctx, hlab, append(args, "--output", "json")...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("hlab %v: %w: %s", args[:2], err, stderr.String())
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(stdout, v); err != nil {
		return fmt.Errorf("hlab %v: %w", args[:2], err)
	}
	return nil
}
