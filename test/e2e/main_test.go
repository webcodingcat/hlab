//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
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
	projectMade  bool   // the run's project exists; teardown deletes it
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

	// A running dev service holds bin\hlabd.exe, so the build could not
	// replace it: stop the cloud with the existing binary first and start it
	// again after the build, leaving it running as found.
	wasRunning, err := stopRunningCloud()
	if err != nil {
		return err
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
		harness.startedCloud = !wasRunning
		if err := waitCloudState("running", 15*time.Minute); err != nil {
			return err
		}
	}
	return deleteLeftoverProjects()
}

// leftoverProject matches exactly the project names this harness creates:
// the run's project e2e-<time> and the projects of newProjectEnv,
// e2e-<time>-<suffix>.
var leftoverProject = regexp.MustCompile(`^e2e-\d{8}-\d{6}(-[a-z0-9][a-z0-9-]*)?$`)

// deleteLeftoverProjects deletes the projects earlier runs left behind
// (a run that was killed before its teardown); other projects stay.
func deleteLeftoverProjects() error {
	r, err := runHlab(context.Background(), 2*time.Minute, "project", "list", "--output", "json")
	if err != nil {
		return fmt.Errorf("hlab project list: %w\n%s", err, r)
	}
	var body struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &body); err != nil {
		return fmt.Errorf("hlab project list --output json: %w\n%s", err, r.Stdout)
	}
	var errs []error
	for _, p := range body.Projects {
		if p.Name == harness.runID || !leftoverProject.MatchString(p.Name) {
			continue
		}
		if err := deleteProject(p.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// deleteProject deletes the project with every resource in it and waits
// until the deletion is done.
func deleteProject(name string) error {
	if r, err := runHlab(context.Background(), 35*time.Minute, "project", "delete", name); err != nil {
		return fmt.Errorf("hlab project delete %s: %w\n%s", name, err, r)
	}
	return nil
}

// stopRunningCloud frees bin\hlabd.exe for the build in every state in
// which the dev service holds it: a starting cloud is waited for and then
// handled by its new state, a running one is stopped, a stopping one is
// waited for. In stopped and refused the service process has exited (a
// refused start ends the service), so nothing needs to stop. It reports
// whether the cloud was running (or came up from starting), so the harness
// leaves it running as found. Without a built hlab.exe there is nothing to
// ask and nothing to stop.
func stopRunningCloud() (bool, error) {
	if notExist(harness.hlab) {
		return false, nil
	}
	state, err := cloudState()
	if err != nil {
		return false, err
	}
	if state == "starting" {
		err := waitFor(15*time.Minute, 2*time.Second, func() (bool, error) {
			s, err := cloudState()
			if err != nil {
				return false, nil
			}
			state = s
			return s != "starting", nil
		})
		if err != nil {
			return false, fmt.Errorf("the cloud is still starting before the build: %w", err)
		}
	}
	switch state {
	case "running":
		if out, err := runHlab(context.Background(), 10*time.Minute, "cloud", "stop"); err != nil {
			return false, fmt.Errorf("hlab cloud stop before the build: %w\n%s", err, out)
		}
		if err := waitCloudState("stopped", 10*time.Minute); err != nil {
			return false, err
		}
		return true, nil
	case "stopping":
		return false, waitCloudState("stopped", 10*time.Minute)
	default:
		return false, nil
	}
}

func teardown() error {
	var errs []error
	if err := removeWSLDir(); err != nil {
		errs = append(errs, err)
	}
	if harness.projectMade {
		if err := deleteProject(harness.runID); err != nil {
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

// waitCloudState waits until `hlab cloud status` reports want. While it
// waits for "stopped", a refused state counts as stopped: hlab reports it
// only when the service has stopped, and the state file can still hold the
// refusal of an earlier start.
func waitCloudState(want string, timeout time.Duration) error {
	var last string
	err := waitFor(timeout, 2*time.Second, func() (bool, error) {
		s, err := cloudState()
		if err != nil {
			return false, nil
		}
		last = s
		if s == "refused" && want == "stopped" {
			return true, nil
		}
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
	harness.projectMade = true
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

// deleteServer deletes the server; while an action of it still runs (a row
// that failed during the create) the API answers locked, and the delete is
// tried again until that action is over.
func (e *env) deleteServer(name string) error {
	var r result
	var derr error
	err := waitFor(50*time.Minute, 10*time.Second, func() (bool, error) {
		r, derr = e.hcloudRun(context.Background(), 15*time.Minute, "server", "delete", name)
		if derr == nil {
			return true, nil
		}
		if strings.Contains(r.Stdout+r.Stderr, "(locked)") {
			return false, nil
		}
		return false, derr
	})
	if err != nil {
		return fmt.Errorf("hcloud server delete %s: %v\n%s", name, err, r)
	}
	return nil
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

// apiError is the error object of an API answer that is not 2xx.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// apiDo sends method path to the hlab API with the run's token and an
// optional JSON body. A 2xx body is decoded into out (nil: ignored); any
// other body gives its error object. It returns the status code.
func (e *env) apiDo(ctx context.Context, method, path string, body, out any) (int, apiError, error) {
	return e.apiDoAt(ctx, apiEndpoint, method, path, body, out)
}

// apiDoAt is apiDo against the API under base (apiEndpoint or
// hetznerEndpoint).
func (e *env) apiDoAt(ctx context.Context, base, method, path string, body, out any) (int, apiError, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, apiError{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return 0, apiError{}, err
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, apiError{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var eb struct {
			Error apiError `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&eb)
		return resp.StatusCode, eb.Error, nil
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, apiError{}, fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, apiError{}, nil
}

// waitAction waits until the action is no longer running; an action ending
// in error is an error.
func (e *env) waitAction(id int64, timeout time.Duration) error {
	var last string
	err := waitFor(timeout, 2*time.Second, func() (bool, error) {
		var body struct {
			Action struct {
				Status string    `json:"status"`
				Error  *apiError `json:"error"`
			} `json:"action"`
		}
		status, _, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/actions/%d", id), nil, &body)
		if err != nil || status != http.StatusOK {
			return false, nil
		}
		last = body.Action.Status
		switch body.Action.Status {
		case "success":
			return true, nil
		case "error":
			if body.Action.Error != nil {
				return false, fmt.Errorf("action %d ended in error %s: %s", id, body.Action.Error.Code, body.Action.Error.Message)
			}
			return false, fmt.Errorf("action %d ended in error", id)
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("action %d (last status %q): %w", id, last, err)
	}
	return nil
}

// serverStatus is the status of the server as GET /servers/{id} reports it.
func (e *env) serverStatus(id int64) (string, error) {
	var body struct {
		Server struct {
			Status string `json:"status"`
		} `json:"server"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &body)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("GET /servers/%d: status %d %s", id, status, aerr.Code)
	}
	return body.Server.Status, nil
}

// waitStatus waits until the server shows the given status.
func (e *env) waitStatus(id int64, want string, timeout time.Duration) error {
	var last string
	err := waitFor(timeout, 2*time.Second, func() (bool, error) {
		s, err := e.serverStatus(id)
		if err != nil {
			return false, nil
		}
		last = s
		return s == want, nil
	})
	if err != nil {
		return fmt.Errorf("server %d did not reach status %q (last %q): %w", id, want, last, err)
	}
	return nil
}

// sshPassword runs a command as root on the server with a password login
// only (no key is offered); see askpassEnviron. A fresh known_hosts file (a
// password login is refused for a host whose key changed) is deleted when
// the command returns.
func sshPassword(s *server, password string, timeout time.Duration, remote string) (result, error) {
	dir, err := os.MkdirTemp(harness.tmp, "askpass-")
	if err != nil {
		return result{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	environ, err := askpassEnviron(dir, password)
	if err != nil {
		return result{}, err
	}
	return command(context.Background(), timeout, environ, "", harness.ssh,
		"-o", "PubkeyAuthentication=no",
		"-o", "IdentitiesOnly=yes",
		"-o", "PreferredAuthentications=password,keyboard-interactive",
		"-o", "NumberOfPasswordPrompts=1",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile="+filepath.Join(dir, "known_hosts"),
		"-o", "ConnectTimeout=10",
		"-o", "LogLevel=ERROR",
		"root@"+s.IPv4, remote)
}

// askpassEnviron writes an askpass script into dir and returns the
// environment for one Windows OpenSSH process (ssh, sftp, scp) that makes it
// answer the password prompt through the script. The script prints the
// password from the environment of that one process and never holds it;
// the caller deletes dir when the command returns.
func askpassEnviron(dir, password string) ([]string, error) {
	ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	script := filepath.Join(dir, "askpass.cmd")
	content := "@echo off\r\n\"" + ps + "\" -NoProfile -NonInteractive -Command \"[Console]::Out.WriteLine($env:HLAB_E2E_PASSWORD)\"\r\n"
	if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
		return nil, err
	}
	var environ []string
	for _, kv := range os.Environ() {
		u := strings.ToUpper(kv)
		if strings.HasPrefix(u, "SSH_ASKPASS") || strings.HasPrefix(u, "HLAB_E2E_PASSWORD=") {
			continue
		}
		environ = append(environ, kv)
	}
	return append(environ,
		"SSH_ASKPASS="+script,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=hlab-e2e:0",
		"HLAB_E2E_PASSWORD="+password,
	), nil
}
