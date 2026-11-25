//go:build e2einstall

package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// The suite installs the release profile next to the dev profile of this
// working copy (ARCHITECTURE.md "Profiles").
const (
	releaseService = "hlab"
	releaseTag     = "hlab"
	devService     = "hlab-dev"
	devTag         = "hlab-dev"
	oldVersion     = "0.0.1"
	newVersion     = "0.0.2"
	apiEndpoint    = "http://api.hetzner.internal/v1"
	serverType     = "cx23"
	serverLocation = "fsn1"
	serverImage    = "ubuntu-24.04"
	wslCAFile      = "/usr/local/share/ca-certificates/hlab-ca.crt"
	// distroEnv names the WSL2 distro explicitly, as in test/e2e; without
	// it the one registered distro whose name starts with Ubuntu is used.
	distroEnv = "HLAB_E2E_WSL_DISTRO"
)

// harness holds what TestMain prepares once for every row.
var harness struct {
	root          string            // repository root
	tmp           string            // temporary folder of this run
	devHlab       string            // bin\hlab.exe of the working copy (profile dev)
	devWasRunning bool              // the dev cloud ran at the start; teardown starts it again
	setups        map[string]string // version -> hlab-setup-<version>.exe in tmp
	logSeq        int               // numbers the Burn logs of this run

	distro    string // WSL2 distro for the CA trust and the forwarding
	distroErr error

	// forwarding of the WSL adapters before the first install of this run.
	forwarding    map[string]string
	forwardingErr error

	// The dev profile's objects at the start (INST-04/dev-profile).
	devSwitch, devAppliance, devServiceThere bool

	installGen int // counts the installs; a shared uninstall is valid only for its install
}

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2einstall:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func run(m *testing.M) (code int, err error) {
	defer func() {
		if terr := teardown(code != 0 || err != nil); terr != nil && err == nil {
			err = terr
		}
	}()
	if err := setup(); err != nil {
		return 1, err
	}
	return m.Run(), nil
}

func setup() error {
	ctx := context.Background()
	root, err := repoRoot()
	if err != nil {
		return err
	}
	harness.root = root
	harness.devHlab = filepath.Join(root, "bin", "hlab.exe")
	harness.setups = map[string]string{}
	if harness.tmp, err = os.MkdirTemp("", "hlab-e2einstall-"); err != nil {
		return err
	}

	elevated, err := isElevated(ctx)
	if err != nil {
		return err
	}
	if !elevated {
		return errors.New("the installer suite installs MSI packages and needs elevated rights; run it in an administrator terminal")
	}
	if err := refuseRelease(ctx); err != nil {
		return err
	}

	logf("stopping the dev cloud if it runs")
	if harness.devWasRunning, err = stopDevCloud(ctx); err != nil {
		return err
	}

	if err := buildFrontend(ctx); err != nil {
		return err
	}
	for _, v := range []string{oldVersion, newVersion} {
		if err := buildSetup(ctx, v); err != nil {
			return err
		}
	}

	if harness.devSwitch, err = switchExists(ctx, devTag); err != nil {
		return err
	}
	if harness.devAppliance, err = vmExists(ctx, devTag+"-appliance"); err != nil {
		return err
	}
	if _, harness.devServiceThere, err = service(ctx, devService); err != nil {
		return err
	}

	// WSL creates its adapter when a distro runs; the forwarding of that
	// adapter is recorded before anything of hlab is installed.
	harness.distro, harness.distroErr = selectDistro(ctx)
	if harness.distroErr == nil {
		if r, err := wslRun(ctx, time.Minute, "true\n"); err != nil {
			harness.distroErr = fmt.Errorf("starting distro %s: %v\n%s", harness.distro, err, r)
		}
	}
	harness.forwarding, harness.forwardingErr = wslForwarding(ctx)
	if harness.forwardingErr == nil && len(harness.forwarding) == 0 {
		harness.forwardingErr = errors.New("no adapter vEthernet (WSL*) found before the install; start a WSL2 distro")
	}
	return nil
}

// refuseRelease stops the run while a release hlab is on the machine: the
// suite installs and uninstalls it and must not touch the user's.
func refuseRelease(ctx context.Context) error {
	svc, ok, err := service(ctx, releaseService)
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("service %q (%s, %s) is installed; uninstall the release hlab before running the installer suite", releaseService, svc.PathName, svc.State)
	}
	bs, err := registeredBundles(ctx)
	if err != nil {
		return err
	}
	if len(bs) > 0 {
		var names []string
		for _, b := range bs {
			names = append(names, fmt.Sprintf("%q %s", b.DisplayName, b.DisplayVersion))
		}
		return fmt.Errorf("an HLab setup bundle is registered (%s); uninstall it before running the installer suite", strings.Join(names, ", "))
	}
	return nil
}

// teardown uninstalls the release and starts the dev cloud again; the run's
// folder with the setup, MSI and service logs stays when failed is set or
// the teardown itself fails.
func teardown(failed bool) error {
	ctx := context.Background()
	var errs []error
	if len(harness.setups) == 2 {
		if v, err := installedVersion(ctx); err != nil {
			errs = append(errs, err)
		} else if v != "" {
			logf("teardown: uninstalling release %s", v)
			if err := uninstall(ctx, false); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if harness.devWasRunning {
		logf("teardown: starting the dev cloud again")
		if r, err := command(ctx, 10*time.Minute, "", harness.devHlab, "cloud", "start"); err != nil {
			errs = append(errs, fmt.Errorf("dev hlab cloud start: %v\n%s", err, r))
		} else if err := waitCloudState(ctx, harness.devHlab, "running", 15*time.Minute); err != nil {
			errs = append(errs, err)
		}
	}
	if harness.tmp != "" && !failed && len(errs) == 0 {
		_ = os.RemoveAll(harness.tmp)
	} else if harness.tmp != "" {
		logf("teardown: the logs of this run stay in %s", harness.tmp)
	}
	return errors.Join(errs...)
}

// row runs one row of the test map with its own deadline and logs its start
// and end with the limit.
func row(limit time.Duration, f func(ctx context.Context, t *testing.T)) func(*testing.T) {
	return func(t *testing.T) {
		logf("start %s (limit %s)", t.Name(), limit)
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		defer func() {
			status := "ok"
			if t.Failed() {
				status = "FAIL"
			}
			logf("end %s: %s after %s (limit %s)", t.Name(), status, time.Since(start).Round(time.Second), limit)
		}()
		f(ctx, t)
	}
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s e2einstall: %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
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

// buildFrontend builds the tray frontend once; tools/installer embeds the
// built frontend but does not build it (`task installer` does).
func buildFrontend(ctx context.Context) error {
	logf("building the tray frontend")
	return runBuild(ctx, 15*time.Minute, "task", "frontend:build")
}

// buildSetup builds the installers of one version with tools/installer into
// the run's temporary folder.
func buildSetup(ctx context.Context, version string) error {
	logf("building the installers %s", version)
	out := filepath.Join(harness.tmp, "build")
	if err := runBuild(ctx, 30*time.Minute, "go", "run", "./tools/installer", "-out", out, version); err != nil {
		return err
	}
	exe := filepath.Join(out, "hlab-setup-"+version+".exe")
	if notExist(exe) {
		return fmt.Errorf("tools/installer %s wrote no %s", version, exe)
	}
	harness.setups[version] = exe
	return nil
}

// runBuild runs a build step in the repository root with its output on
// stderr and a deadline.
func runBuild(ctx context.Context, timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = harness.root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s %s did not finish in time (limit %s)", name, strings.Join(args, " "), timeout)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func isElevated(ctx context.Context) (bool, error) {
	var admin bool
	err := powershellJSON(ctx,
		`ConvertTo-Json -InputObject ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)`,
		&admin)
	return admin, err
}

// powershellJSON runs a Windows PowerShell script and decodes its JSON output.
// Scripts print JSON only; localized text is never parsed.
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

// command runs a program with no console input unless stdin is given; the
// deadline (the shorter of timeout and ctx) ends a hanging command.
func command(ctx context.Context, timeout time.Duration, stdin, name string, args ...string) (result, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = harness.root
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
		return r, fmt.Errorf("%s did not finish in time (limit %s)", filepath.Base(name), timeout)
	}
	return r, err
}

// waitFor polls cond until it is true, it fails, the deadline passes or ctx ends.
func waitFor(ctx context.Context, timeout, interval time.Duration, cond func() (bool, error)) error {
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
		select {
		case <-ctx.Done():
			return fmt.Errorf("condition not met before the row's deadline: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func notExist(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// --- the dev and the release cloud ---

func releaseDir() string     { return filepath.Join(os.Getenv("ProgramFiles"), "hlab") }
func releaseHlab() string    { return filepath.Join(releaseDir(), "hlab.exe") }
func releaseDataDir() string { return filepath.Join(os.Getenv("ProgramData"), "hlab") }

func runRelease(ctx context.Context, timeout time.Duration, args ...string) (result, error) {
	return command(ctx, timeout, "", releaseHlab(), args...)
}

// cloudState returns the state of `<hlab> cloud status --output json`.
func cloudState(ctx context.Context, hlab string) (string, error) {
	r, err := command(ctx, time.Minute, "", hlab, "cloud", "status", "--output", "json")
	var st struct {
		State string `json:"state"`
	}
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &st); jerr != nil || st.State == "" {
		return "", fmt.Errorf("%s cloud status --output json gave no state (%v): %s", hlab, err, r)
	}
	return st.State, nil
}

// waitCloudState waits until the cloud reports want; while it waits for
// "stopped", "refused" counts as stopped (the service has ended).
func waitCloudState(ctx context.Context, hlab, want string, timeout time.Duration) error {
	var last string
	err := waitFor(ctx, timeout, 2*time.Second, func() (bool, error) {
		s, err := cloudState(ctx, hlab)
		if err != nil {
			return false, nil
		}
		last = s
		if s == "refused" && want == "stopped" {
			return true, nil
		}
		if s == "refused" {
			return false, fmt.Errorf("the cloud of %s refused to start; see its cloud status", hlab)
		}
		return s == want, nil
	})
	if err != nil {
		return fmt.Errorf("cloud of %s did not reach %q (last %q): %w", hlab, want, last, err)
	}
	return nil
}

// stopDevCloud stops a running or starting dev cloud and reports whether it
// ran, so teardown leaves it as found. Without bin\hlab.exe there is
// nothing to stop.
func stopDevCloud(ctx context.Context) (bool, error) {
	if notExist(harness.devHlab) {
		return false, nil
	}
	state, err := cloudState(ctx, harness.devHlab)
	if err != nil {
		return false, err
	}
	if state == "starting" {
		if err := waitFor(ctx, 15*time.Minute, 2*time.Second, func() (bool, error) {
			s, err := cloudState(ctx, harness.devHlab)
			if err != nil {
				return false, nil
			}
			state = s
			return s != "starting", nil
		}); err != nil {
			return false, fmt.Errorf("the dev cloud is still starting: %w", err)
		}
	}
	switch state {
	case "running":
		if r, err := command(ctx, 10*time.Minute, "", harness.devHlab, "cloud", "stop"); err != nil {
			return false, fmt.Errorf("dev hlab cloud stop: %v\n%s", err, r)
		}
		return true, waitCloudState(ctx, harness.devHlab, "stopped", 10*time.Minute)
	case "stopping":
		return false, waitCloudState(ctx, harness.devHlab, "stopped", 10*time.Minute)
	default:
		return false, nil
	}
}

var dataFolderRe = regexp.MustCompile(`(?m)^\s*data_folder\s*=\s*['"]([^'"]+)['"]`)

// dataFolder is storage.data_folder of the profile's settings, or its default.
func dataFolder(profileDir string) string {
	if b, err := os.ReadFile(filepath.Join(profileDir, "settings.toml")); err == nil {
		if m := dataFolderRe.FindSubmatch(b); m != nil {
			return strings.ReplaceAll(string(m[1]), `\\`, `\`)
		}
	}
	return filepath.Join(profileDir, "data")
}

// seedCaches copies the dev profile's base images and appliance packages
// into the release data folder, so the release cloud downloads nothing.
// Images of snapshots (image-<id>.vhdx) belong to dev servers and stay.
func seedCaches() error {
	dev := dataFolder(filepath.Join(os.Getenv("ProgramData"), "hlab-dev"))
	rel := dataFolder(releaseDataDir())
	snapshot := regexp.MustCompile(`(?i)^image-\d+\.vhdx$`)
	entries, err := os.ReadDir(filepath.Join(dev, "images"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || snapshot.MatchString(e.Name()) {
			continue
		}
		dst := filepath.Join(rel, "images", e.Name())
		if !notExist(dst) {
			continue
		}
		logf("seeding %s", dst)
		if err := copyFile(filepath.Join(dev, "images", e.Name()), dst); err != nil {
			return fmt.Errorf("seed %s: %w", dst, err)
		}
	}
	src := filepath.Join(dev, "cache")
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rp, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(rel, "cache", rp)
		if !notExist(dst) {
			return nil
		}
		return copyFile(p, dst)
	})
}

// startReleaseCloud seeds the caches while the cloud is stopped and starts it.
func startReleaseCloud(ctx context.Context) error {
	state, err := cloudState(ctx, releaseHlab())
	if err != nil {
		return err
	}
	if state == "running" {
		return nil
	}
	if state == "stopping" {
		if err := waitCloudState(ctx, releaseHlab(), "stopped", 10*time.Minute); err != nil {
			return err
		}
	}
	if state != "starting" {
		if err := seedCaches(); err != nil {
			return err
		}
		logf("starting the release cloud")
		if r, err := runRelease(ctx, 10*time.Minute, "cloud", "start"); err != nil {
			return fmt.Errorf("release hlab cloud start: %v\n%s", err, r)
		}
	}
	return waitCloudState(ctx, releaseHlab(), "running", 15*time.Minute)
}

// --- bundles ---

type bundle struct {
	DisplayName    string
	DisplayVersion string
	Key            string
}

// registeredBundles lists the HLab setup bundles in "Apps" (Burn registers
// them with a BundleUpgradeCode).
func registeredBundles(ctx context.Context) ([]bundle, error) {
	var list []bundle
	err := powershellJSON(ctx, `$keys = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
ConvertTo-Json -Compress -InputObject @(Get-ItemProperty -Path $keys -ErrorAction SilentlyContinue | Where-Object { $_.BundleUpgradeCode -and ($_.Publisher -eq 'webcodingcat' -or $_.DisplayName -like 'HLab*') } | ForEach-Object { [pscustomobject]@{ DisplayName = [string]$_.DisplayName; DisplayVersion = [string]$_.DisplayVersion; Key = [string]$_.PSChildName } })`, &list)
	return list, err
}

// installedVersion is the version of the registered bundle, "" without one.
func installedVersion(ctx context.Context) (string, error) {
	bs, err := registeredBundles(ctx)
	if err != nil {
		return "", err
	}
	switch len(bs) {
	case 0:
		return "", nil
	case 1:
		return bs[0].DisplayVersion, nil
	default:
		return "", fmt.Errorf("%d HLab bundles are registered: %v", len(bs), bs)
	}
}

// burnRun is one quiet run of a setup bundle with its logs (the bundle's
// log and the package logs Burn writes next to it).
type burnRun struct {
	result
	log string
}

// runBundle runs the bundle of version quietly with a log and a deadline.
func runBundle(ctx context.Context, version, what string, args ...string) (burnRun, error) {
	exe, ok := harness.setups[version]
	if !ok {
		return burnRun{}, fmt.Errorf("no bundle %s was built", version)
	}
	harness.logSeq++
	base := fmt.Sprintf("%02d-%s-%s", harness.logSeq, what, version)
	logFile := filepath.Join(harness.tmp, base+".log")
	logf("running %s %s (log %s)", filepath.Base(exe), strings.Join(args, " "), logFile)
	args = append(args, "/quiet", "/norestart", "/log", logFile)
	r, err := command(ctx, 30*time.Minute, "", exe, args...)
	run := burnRun{result: r, log: readLogs(filepath.Join(harness.tmp, base+"*.log"))}
	if err != nil && r.ExitCode <= 0 {
		return run, fmt.Errorf("%s: %w", filepath.Base(exe), err)
	}
	return run, nil
}

// readLogs concatenates the matching logs; MSI writes UTF-16 logs.
func readLogs(pattern string) string {
	files, _ := filepath.Glob(pattern)
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "==== %s\n%s\n", filepath.Base(f), decodeLog(data))
	}
	return b.String()
}

func decodeLog(data []byte) string {
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		u := make([]uint16, 0, len(data)/2)
		for i := 2; i+1 < len(data); i += 2 {
			u = append(u, uint16(data[i])|uint16(data[i+1])<<8)
		}
		return string(utf16.Decode(u))
	}
	return string(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))
}

// tail is the end of a log for an error message.
func tail(s string) string {
	const n = 4000
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// install runs the bundle of version on a machine without hlab or with an
// older version and expects success.
func install(ctx context.Context, version string) (burnRun, error) {
	r, err := runBundle(ctx, version, "install")
	harness.installGen++
	if err != nil {
		return r, err
	}
	if r.ExitCode != 0 {
		return r, fmt.Errorf("installing %s: exit %d\n%s", version, r.ExitCode, tail(r.log))
	}
	return r, nil
}

// uninstall removes the installed release quietly; keep passes KeepData=1.
// Without keep the fixture goes with the data.
func uninstall(ctx context.Context, keep bool) error {
	v, err := installedVersion(ctx)
	if err != nil || v == "" {
		return err
	}
	args := []string{"/uninstall"}
	what := "uninstall"
	if keep {
		args = append(args, "KeepData=1")
		what = "uninstall-keep"
	}
	saveServiceLogs(what, v)
	r, err := runBundle(ctx, v, what, args...)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("uninstalling %s (%s): exit %d\n%s", v, strings.Join(args, " "), r.ExitCode, tail(r.log))
	}
	if !keep {
		fix = nil
	}
	return nil
}

// saveServiceLogs copies the release hlabd.log* into the run's folder before
// an uninstall removes them, numbered like the Burn log that follows.
func saveServiceLogs(what, version string) {
	files, _ := filepath.Glob(filepath.Join(releaseDataDir(), "logs", "hlabd.log*"))
	dir := filepath.Join(harness.tmp, fmt.Sprintf("%02d-%s-%s-hlabd", harness.logSeq+1, what, version))
	for _, f := range files {
		if err := copyFile(f, filepath.Join(dir, filepath.Base(f))); err != nil {
			logf("copying %s: %v", f, err)
		}
	}
}

// ensureInstalled brings the machine to exactly version installed; a newer
// version is uninstalled first, since a downgrade is refused.
func ensureInstalled(ctx context.Context, version string) error {
	v, err := installedVersion(ctx)
	if err != nil {
		return err
	}
	if v == version {
		return nil
	}
	if v != "" && v != oldVersion {
		if err := uninstall(ctx, false); err != nil {
			return err
		}
	}
	_, err = install(ctx, version)
	return err
}

// ensureNotInstalled removes an installed release with all its data.
func ensureNotInstalled(ctx context.Context) error {
	return uninstall(ctx, false)
}

// --- WSL ---

// selectDistro returns HLAB_E2E_WSL_DISTRO or the one registered distro
// whose name starts with Ubuntu; never the default distro implicitly.
func selectDistro(ctx context.Context) (string, error) {
	if d := strings.TrimSpace(os.Getenv(distroEnv)); d != "" {
		return d, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", "--list", "--quiet")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", errors.New("wsl.exe --list --quiet did not finish in time (limit 1m0s)")
	}
	if err != nil {
		return "", fmt.Errorf("wsl.exe --list --quiet: %w", err)
	}
	text := strings.NewReplacer("\x00", "", string(rune(0xFEFF)), "", "\r", "").Replace(string(out))
	var found []string
	for _, line := range strings.Split(text, "\n") {
		if name := strings.TrimSpace(line); strings.HasPrefix(name, "Ubuntu") {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("found %d WSL2 distros starting with Ubuntu (%v); set %s to the one to use", len(found), found, distroEnv)
	}
	return found[0], nil
}

// wslRun feeds script to a login bash in the selected distro.
func wslRun(ctx context.Context, timeout time.Duration, script string) (result, error) {
	if harness.distroErr != nil {
		return result{}, harness.distroErr
	}
	return command(ctx, timeout, script, "wsl.exe", "-d", harness.distro, "-e", "bash", "-l", "-s")
}
