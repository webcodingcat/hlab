//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// The harness runs against the dev profile: service hlab-dev, data folder
// %ProgramData%\hlab-dev, binaries in bin\ of this working copy.
const (
	devService     = "hlab-dev"
	releaseService = "hlab"
	vmPrefix       = "hlab-dev"
	apiEndpoint    = "http://api.hetzner.internal/v1"
	serverType     = "cx23"
	serverLocation = "fsn1"
	serverImage    = "ubuntu-24.04"
	userDataMarker = "/var/lib/hlab-e2e-marker"
	userDataValue  = "hlab-e2e-user-data"
)

// harness holds what TestMain prepares once for every subtest.
var harness struct {
	root         string // repository root
	hlab         string // bin\hlab.exe
	hcloud       string // hcloud on PATH
	ssh          string // system ssh.exe
	sshKeygen    string // system ssh-keygen.exe
	tmp          string // temporary folder of this run
	startedCloud bool   // the harness started the dev cloud and stops it at the end
	runID        string // e2e-<time>, the project name
}

// env is the shared project, token, hcloud configuration and SSH key,
// created lazily by the first subtest that needs it.
type env struct {
	project   string
	token     string
	hcloudEnv []string
	key       string // private key file
	pubKey    string // public key file
	sshKey    string // name of the SSH key in the API
	known     string // known_hosts file of this run
	userData  string // user_data file with the marker
}

var (
	envOnce sync.Once
	envVal  *env
	envErr  error

	serverOnce sync.Once
	serverVal  *server
	serverErr  error
)

// server is a server created through hcloud.
type server struct {
	ID   int64
	Name string
	IPv4 string
}

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func run(m *testing.M) (code int, err error) {
	if err := setup(); err != nil {
		return 1, err
	}
	defer func() {
		if terr := teardown(); terr != nil && err == nil {
			err = terr
		}
	}()
	return m.Run(), nil
}

func setup() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	harness.root = root
	harness.hlab = filepath.Join(root, "bin", "hlab.exe")
	harness.runID = "e2e-" + time.Now().UTC().Format("20060102-150405")

	tmp, err := os.MkdirTemp("", "hlab-e2e-")
	if err != nil {
		return err
	}
	harness.tmp = tmp

	if harness.hcloud, err = exec.LookPath("hcloud"); err != nil {
		return fmt.Errorf("hcloud not found on PATH: %w", err)
	}
	harness.ssh = systemTool("ssh.exe")
	harness.sshKeygen = systemTool("ssh-keygen.exe")

	elevated, err := isElevated()
	if err != nil {
		return err
	}
	if elevated {
		return errors.New("the end-to-end tests must run without elevated rights; start the terminal normally")
	}

	if err := buildAll(); err != nil {
		return err
	}
	if err := guardProfiles(); err != nil {
		return err
	}

	state, err := cloudState()
	if err != nil {
		return err
	}
	if state != "running" {
		if out, err := runHlab(context.Background(), 10*time.Minute, "cloud", "start"); err != nil {
			return fmt.Errorf("hlab cloud start: %w\n%s", err, out)
		}
		harness.startedCloud = true
		if err := waitCloudState("running", 15*time.Minute); err != nil {
			return err
		}
	}
	return nil
}

func teardown() error {
	var errs []error
	if err := removeWSLDir(); err != nil {
		errs = append(errs, err)
	}
	if envVal != nil {
		if err := deleteAllServers(); err != nil {
			errs = append(errs, err)
		}
	}
	if harness.startedCloud {
		if out, err := runHlab(context.Background(), 10*time.Minute, "cloud", "stop"); err != nil {
			errs = append(errs, fmt.Errorf("hlab cloud stop: %w\n%s", err, out))
		} else if err := waitCloudState("stopped", 10*time.Minute); err != nil {
			errs = append(errs, err)
		}
	}
	if harness.tmp != "" {
		_ = os.RemoveAll(harness.tmp)
	}
	return errors.Join(errs...)
}

// repoRoot walks up from the working directory to the folder with go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root (go.mod) not found")
		}
		dir = parent
	}
}

// systemTool prefers the Windows OpenSSH tools over others on PATH.
func systemTool(name string) string {
	p := filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

func buildAll() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "task", "build")
	cmd.Dir = harness.root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("task build: %w", err)
	}
	return nil
}

// serviceInfo is a Windows service as Win32_Service reports it (not localized).
type serviceInfo struct {
	Name     string
	State    string // "Running", "Stopped", ...
	PathName string
}

func services(names ...string) (map[string]serviceInfo, error) {
	var filters []string
	for _, n := range names {
		filters = append(filters, fmt.Sprintf("Name='%s'", n))
	}
	script := fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-CimInstance -ClassName Win32_Service -Filter "%s" | ForEach-Object { [pscustomobject]@{ Name = $_.Name; State = [string]$_.State; PathName = [string]$_.PathName } })`,
		strings.Join(filters, " OR "))
	var list []serviceInfo
	if err := powershellJSON(context.Background(), script, &list); err != nil {
		return nil, err
	}
	m := map[string]serviceInfo{}
	for _, s := range list {
		m[strings.ToLower(s.Name)] = s
	}
	return m, nil
}

func guardProfiles() error {
	svcs, err := services(releaseService, devService)
	if err != nil {
		return err
	}
	if rel, ok := svcs[releaseService]; ok && rel.State != "Stopped" {
		return fmt.Errorf("the release cloud (service %q) is %s; stop it before running the end-to-end tests", releaseService, rel.State)
	}
	dev, ok := svcs[devService]
	if !ok {
		return fmt.Errorf("service %q is not installed; run scripts\\dev-setup.ps1 once in an administrator terminal", devService)
	}
	want := filepath.Join(harness.root, "bin", "hlabd.exe")
	got := serviceExe(dev.PathName)
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(want)) {
		return fmt.Errorf("service %q runs %q, not %q of this working copy; run scripts\\dev-setup.ps1 from this working copy", devService, got, want)
	}
	return nil
}

// serviceExe extracts the program from a service command line.
func serviceExe(pathName string) string {
	p := strings.TrimSpace(pathName)
	if strings.HasPrefix(p, `"`) {
		if i := strings.Index(p[1:], `"`); i >= 0 {
			return p[1 : i+1]
		}
	}
	if i := strings.Index(strings.ToLower(p), ".exe"); i >= 0 {
		return p[:i+4]
	}
	return p
}

func isElevated() (bool, error) {
	var admin bool
	err := powershellJSON(context.Background(),
		`ConvertTo-Json -InputObject ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)`,
		&admin)
	return admin, err
}

// powershellJSON runs a Windows PowerShell script and decodes its JSON output.
// Scripts must print JSON only; localized text is never parsed.
func powershellJSON(ctx context.Context, script string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'; "+script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("powershell: %w\n%s", err, stderr.String())
	}
	data := bytes.TrimSpace(stdout.Bytes())
	if len(data) == 0 {
		data = []byte("null")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("powershell output is not the expected JSON: %w\n%s", err, data)
	}
	return nil
}

// result is the outcome of one external command.
type result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (r result) String() string {
	return fmt.Sprintf("exit %d\nstdout: %s\nstderr: %s", r.ExitCode, r.Stdout, r.Stderr)
}

// command runs a program with no console input (stdin is the null device), so
// any prompt fails instead of waiting; the deadline ends a hanging command.
func command(ctx context.Context, timeout time.Duration, environ []string, stdin string, name string, args ...string) (result, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = harness.root
	if environ != nil {
		cmd.Env = environ
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	r := result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1}
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return r, fmt.Errorf("%s did not finish within %s", filepath.Base(name), timeout)
	}
	return r, err
}

func runHlab(ctx context.Context, timeout time.Duration, args ...string) (result, error) {
	return command(ctx, timeout, nil, "", harness.hlab, args...)
}

// cloudState returns the state of `hlab cloud status --output json`.
func cloudState() (string, error) {
	r, err := runHlab(context.Background(), time.Minute, "cloud", "status", "--output", "json")
	var st struct {
		State string `json:"state"`
	}
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &st); jerr != nil || st.State == "" {
		return "", fmt.Errorf("hlab cloud status --output json gave no state (%v): %s", err, r)
	}
	return st.State, nil
}

func waitCloudState(want string, timeout time.Duration) error {
	var last string
	err := waitFor(timeout, 2*time.Second, func() (bool, error) {
		s, err := cloudState()
		if err != nil {
			return false, nil
		}
		last = s
		if s == "refused" {
			return false, fmt.Errorf("the cloud refused to start; see hlab cloud status")
		}
		return s == want, nil
	})
	if err != nil {
		return fmt.Errorf("cloud did not reach %q (last %q): %w", want, last, err)
	}
	return nil
}

// waitFor polls cond until it is true, it fails, or the deadline passes.
func waitFor(timeout, interval time.Duration, cond func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		ok, err := cond()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("condition not met within %s", timeout)
		}
		<-ticker.C
	}
}

// sharedEnv creates the run's project, read/write token, hcloud configuration
// and SSH key once.
func sharedEnv(t *testing.T) *env {
	t.Helper()
	envOnce.Do(func() { envVal, envErr = newEnv() })
	if envErr != nil {
		t.Fatalf("shared setup: %v", envErr)
	}
	return envVal
}

func newEnv() (*env, error) {
	ctx := context.Background()
	e := &env{project: harness.runID, sshKey: harness.runID}

	if r, err := runHlab(ctx, 2*time.Minute, "project", "create", e.project); err != nil || r.ExitCode != 0 {
		return nil, fmt.Errorf("hlab project create: %v\n%s", err, r)
	}
	r, err := runHlab(ctx, 2*time.Minute, "token", "create", "--project", e.project, "--name", "e2e", "--output", "json")
	if err != nil || r.ExitCode != 0 {
		return nil, fmt.Errorf("hlab token create: %v (exit %d)\nstderr: %s", err, r.ExitCode, r.Stderr)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &tok); err != nil || tok.Token == "" {
		return nil, errors.New("hlab token create --output json printed no token")
	}
	e.token = tok.Token

	// hcloud never sees the user's configuration or context.
	cfg := filepath.Join(harness.tmp, "hcloud.toml")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		return nil, err
	}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "HCLOUD_") {
			continue
		}
		e.hcloudEnv = append(e.hcloudEnv, kv)
	}
	e.hcloudEnv = append(e.hcloudEnv,
		"HCLOUD_TOKEN="+e.token,
		"HCLOUD_ENDPOINT="+apiEndpoint,
		"HCLOUD_CONFIG="+cfg,
	)

	e.key = filepath.Join(harness.tmp, "id_ed25519")
	e.pubKey = e.key + ".pub"
	e.known = filepath.Join(harness.tmp, "known_hosts")
	if r, err := command(ctx, time.Minute, nil, "", harness.sshKeygen, "-q", "-t", "ed25519", "-N", "", "-C", "hlab-e2e", "-f", e.key); err != nil {
		return nil, fmt.Errorf("ssh-keygen: %v\n%s", err, r)
	}
	if r, err := e.hcloudRun(ctx, 2*time.Minute, "ssh-key", "create", "--name", e.sshKey, "--public-key-from-file", e.pubKey); err != nil {
		return nil, fmt.Errorf("hcloud ssh-key create: %v\n%s", err, r)
	}

	e.userData = filepath.Join(harness.tmp, "user-data.yaml")
	userData := "#cloud-config\nruncmd:\n  - [sh, -c, \"echo " + userDataValue + " > " + userDataMarker + "\"]\n"
	if err := os.WriteFile(e.userData, []byte(userData), 0o600); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *env) hcloudRun(ctx context.Context, timeout time.Duration, args ...string) (result, error) {
	return command(ctx, timeout, e.hcloudEnv, "", harness.hcloud, args...)
}

// createServer creates a server with the run's SSH key and user_data and
// returns it as the API describes it.
func (e *env) createServer(name string) (*server, error) {
	ctx := context.Background()
	// The first server of a fresh data folder downloads and converts the image.
	r, err := e.hcloudRun(ctx, 45*time.Minute, "server", "create",
		"--name", name, "--type", serverType, "--location", serverLocation, "--image", serverImage,
		"--ssh-key", e.sshKey, "--user-data-from-file", e.userData)
	if err != nil {
		return nil, fmt.Errorf("hcloud server create %s: %v\n%s", name, err, r)
	}
	return e.describeServer(name)
}

func (e *env) describeServer(name string) (*server, error) {
	r, err := e.hcloudRun(context.Background(), 2*time.Minute, "server", "describe", name, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("hcloud server describe %s: %v\n%s", name, err, r)
	}
	var s struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		PublicNet struct {
			IPv4 struct {
				IP string `json:"ip"`
			} `json:"ipv4"`
		} `json:"public_net"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &s); err != nil {
		return nil, fmt.Errorf("hcloud server describe %s: %w\n%s", name, err, r.Stdout)
	}
	return &server{ID: s.ID, Name: s.Name, IPv4: s.PublicNet.IPv4.IP}, nil
}

func (e *env) deleteServer(name string) error {
	r, err := e.hcloudRun(context.Background(), 15*time.Minute, "server", "delete", name)
	if err != nil {
		return fmt.Errorf("hcloud server delete %s: %v\n%s", name, err, r)
	}
	return nil
}

// deleteAllServers removes every server of the run's project one by one
// (deleting the project comes in phase 02).
func deleteAllServers() error {
	r, err := envVal.hcloudRun(context.Background(), 2*time.Minute, "server", "list", "-o", "json")
	if err != nil {
		return fmt.Errorf("hcloud server list: %v\n%s", err, r)
	}
	var list []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &list); err != nil {
		return fmt.Errorf("hcloud server list: %w", err)
	}
	var errs []error
	for _, s := range list {
		if err := envVal.deleteServer(s.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sharedServer is the one Ubuntu server the server rows share; it is ready
// when SSH answers and cloud-init has finished.
func sharedServer(t *testing.T) (*env, *server) {
	t.Helper()
	e := sharedEnv(t)
	serverOnce.Do(func() {
		serverVal, serverErr = e.createServer(harness.runID + "-shared")
		if serverErr != nil {
			return
		}
		serverErr = e.waitReady(serverVal)
	})
	if serverErr != nil {
		t.Fatalf("shared server: %v", serverErr)
	}
	return e, serverVal
}

// waitReady waits until SSH answers and cloud-init is done.
func (e *env) waitReady(s *server) error {
	var last result
	err := waitFor(10*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(s, 30*time.Second, "", "true")
		last = r
		return err == nil && r.ExitCode == 0, nil
	})
	if err != nil {
		return fmt.Errorf("ssh root@%s: %w\n%s", s.IPv4, err, last)
	}
	if r, err := e.ssh(s, 15*time.Minute, "", "cloud-init status --wait >/dev/null; true"); err != nil {
		return fmt.Errorf("cloud-init status --wait on %s: %v\n%s", s.Name, err, r)
	}
	return nil
}

// ssh runs a command as root on the server with the run's key.
func (e *env) ssh(s *server, timeout time.Duration, stdin, remote string) (result, error) {
	return command(context.Background(), timeout, nil, stdin, harness.ssh,
		"-i", e.key,
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile="+e.known,
		"-o", "ConnectTimeout=10",
		"-o", "LogLevel=ERROR",
		"root@"+s.IPv4, remote)
}

// vmName is the Hyper-V name of a server in the dev profile.
func vmName(id int64) string {
	return fmt.Sprintf("%s-server-%d", vmPrefix, id)
}

// vhd is a disk of a VM as Get-VHD reports it.
type vhd struct {
	Path    string
	VhdType string
}

func vmDisks(name string) ([]vhd, error) {
	var disks []vhd
	script := fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-VMHardDiskDrive -VMName '%s' | ForEach-Object { $v = Get-VHD -Path $_.Path; [pscustomobject]@{ Path = [string]$v.Path; VhdType = [string]$v.VhdType } })`, name)
	err := powershellJSON(context.Background(), script, &disks)
	return disks, err
}

func vmExists(name string) (bool, error) {
	var n int
	err := powershellJSON(context.Background(),
		fmt.Sprintf(`ConvertTo-Json -InputObject @(Get-VM -Name '%s' -ErrorAction SilentlyContinue).Count`, name), &n)
	return n > 0, err
}

var dataFolderRe = regexp.MustCompile(`(?m)^\s*data_folder\s*=\s*['"]([^'"]+)['"]`)

// dataFolder is storage.data_folder of the dev settings, or its default.
func dataFolder() string {
	pd := os.Getenv("ProgramData")
	if b, err := os.ReadFile(filepath.Join(pd, "hlab-dev", "settings.toml")); err == nil {
		if m := dataFolderRe.FindSubmatch(b); m != nil {
			return string(m[1])
		}
	}
	return filepath.Join(pd, "hlab-dev", "data")
}

// within reports whether path lies inside dir (case-insensitive, Windows).
func within(path, dir string) bool {
	p := strings.ToLower(filepath.Clean(path))
	d := strings.ToLower(filepath.Clean(dir)) + string(filepath.Separator)
	return strings.HasPrefix(p, d)
}

func notExist(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}
