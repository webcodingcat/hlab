package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/cli"
	"github.com/webcodingcat/hlab/internal/client"
	"github.com/webcodingcat/hlab/internal/config"
	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/platform/fake"
)

// startService starts hlabd on the fake platform, on free ports of
// 127.0.0.1, and points the CLI at it.
func startService(t *testing.T) *service {
	t.Helper()
	dataDir := t.TempDir()
	ports := "ports: {cloud: 0, hetzner: 0, control: 0}\ntime_scale: 0\n"
	if err := os.WriteFile(filepath.Join(dataDir, config.File), []byte(ports), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := start(serviceOptions{
		DataDir:  dataDir,
		Platform: fakePlatform,
		Parts:    fake.New().Platform(),
		Host:     "127.0.0.1",
		Version:  "test",
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(client.ControlVar, svc.status.Endpoints.WebUI)
	t.Setenv(client.TokenFileVar, svc.tokenFile)
	return svc
}

// hlab runs the CLI and returns its output; it fails the test if the
// command fails.
func hlab(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := cli.Execute(args, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("hlab %s: exit code %d: %s", strings.Join(args, " "), code, stderr.String())
	}
	return stdout.String()
}

// stopService stops the service through the CLI and waits until it has
// stopped.
func stopService(t *testing.T, svc *service) {
	t.Helper()
	stopped := make(chan error, 1)
	go func() { stopped <- svc.wait() }()
	hlab(t, "stop")
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hlabd did not stop")
	}
}

func TestANewLabHasTheDefaultProject(t *testing.T) {
	svc := startService(t)
	defer stopService(t, svc)
	if out := hlab(t, "project", "list"); !strings.Contains(out, " default ") {
		t.Errorf("got %q, want the project default", out)
	}
	if out := hlab(t, "status"); !strings.Contains(out, "running") || !strings.Contains(out, "projects     1") {
		t.Errorf("got %q, want a running lab with one project", out)
	}
}

func TestTheCLISaysWhenTheTokenDoesNotFit(t *testing.T) {
	svc := startService(t)
	defer stopService(t, svc)
	wrong := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(wrong, []byte("not-the-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(client.TokenFileVar, wrong)

	var stdout, stderr bytes.Buffer
	code := cli.Execute([]string{"status"}, &stdout, &stderr, "test")

	if code != 1 || !strings.Contains(stderr.String(), "does not fit this hlabd") {
		t.Errorf("exit code %d, stderr %q; want 1 and the hint", code, stderr.String())
	}
	t.Setenv(client.TokenFileVar, svc.tokenFile) // for stopService
}

func TestTheCLIManagesProjectsAndTokens(t *testing.T) {
	svc := startService(t)
	defer stopService(t, svc)
	hlab(t, "project", "create", "web")
	secret := strings.TrimSpace(hlab(t, "token", "create", "--project", "web", "--permission", "read", "ci"))
	if len(secret) != 64 {
		t.Errorf("got the token %q, want 64 characters", secret)
	}
	if out := hlab(t, "token", "list", "--project", "web"); !strings.Contains(out, "ci") || !strings.Contains(out, secret[:4]+"…") {
		t.Errorf("got %q, want the token ci starting with %s", out, secret[:4])
	}
	hlab(t, "token", "delete", "--project", "web", "ci")
	if out := hlab(t, "token", "list", "--project", "web"); strings.Contains(out, "ci") {
		t.Errorf("got %q after the delete", out)
	}
	hlab(t, "project", "delete", "web")
	if out := hlab(t, "project", "list"); strings.Contains(out, "web") {
		t.Errorf("got %q after the delete", out)
	}
}

func TestEnvPointsTheToolsAtTheLab(t *testing.T) {
	svc := startService(t)
	defer stopService(t, svc)
	variables := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(hlab(t, "env", "--shell", "sh")), "\n") {
		name, value, _ := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		variables[name] = strings.Trim(value, "'")
	}
	if variables["HCLOUD_ENDPOINT"] != svc.status.Endpoints.Cloud || variables["HLAB_UI"] != svc.status.Endpoints.WebUI {
		t.Errorf("got %v, want the lab's endpoints", variables)
	}
	// what the hcloud CLI does with them
	request, _ := http.NewRequest(http.MethodGet, variables["HCLOUD_ENDPOINT"]+"/server_types", nil)
	request.Header.Set("Authorization", "Bearer "+variables["HCLOUD_TOKEN"])
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var answer struct {
		ServerTypes []json.RawMessage `json:"server_types"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil || response.StatusCode != http.StatusOK || len(answer.ServerTypes) == 0 {
		t.Errorf("GET /server_types with the env token: %d, %v, %d types", response.StatusCode, err, len(answer.ServerTypes))
	}
	// a second call, as from another shell, gives the same token
	if out := hlab(t, "env", "--shell", "sh"); !strings.Contains(out, "HCLOUD_TOKEN='"+variables["HCLOUD_TOKEN"]+"'") {
		t.Errorf("the second call gave another token: %q", out)
	}
	if out := hlab(t, "token", "list"); strings.Count(out, "hlab-env") != 1 {
		t.Errorf("got %q, want one hlab-env token", out)
	}
}

func TestEnvWritesPowerShell(t *testing.T) {
	svc := startService(t)
	defer stopService(t, svc)
	out := hlab(t, "env", "--shell", "powershell")
	if !strings.Contains(out, "$env:HCLOUD_ENDPOINT = '"+svc.status.Endpoints.Cloud+"'") {
		t.Errorf("got %q, want PowerShell assignments", out)
	}
}

func TestStopEndsTheLabAndTheCLISaysSo(t *testing.T) {
	svc := startService(t)
	stopService(t, svc)
	if _, err := os.Stat(svc.tokenFile); !os.IsNotExist(err) {
		t.Errorf("the control token is still there: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := cli.Execute([]string{"status"}, &stdout, &stderr, "test"); code == 0 || !strings.Contains(stderr.String(), "not running") {
		t.Errorf("got exit code %d and %q, want HLab is not running", code, stderr.String())
	}
}

func TestTheWebUIIsOnTheControlPort(t *testing.T) {
	svc := startService(t)
	defer stopService(t, svc)
	response, err := http.Get(svc.status.Endpoints.WebUI)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 {
		t.Fatalf("got %d with %d cookies, want the page with its cookie", response.StatusCode, len(response.Cookies()))
	}
	request, _ := http.NewRequest(http.MethodGet, svc.status.Endpoints.Control+"/projects", nil)
	request.AddCookie(response.Cookies()[0])
	projects, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = projects.Body.Close() }()
	if projects.StatusCode != http.StatusOK {
		t.Errorf("GET %s/projects with the page's cookie: %d, want 200", labapi.Prefix, projects.StatusCode)
	}
}
