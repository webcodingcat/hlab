//go:build e2einstall

package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestInstall is the one group of the elevated installer suite; every
// subtest is named with its requirement ID only. The rows run one after
// another (one machine, one release installation); each brings the machine
// to the state it needs through the shared helpers, so a -run filter for
// one row still works.
func TestInstall(t *testing.T) {
	t.Run("INST-01", row(20*time.Minute, testINST01))
	t.Run("INST-04", row(60*time.Minute, testINST04))
	t.Run("INST-04/dev-profile", row(60*time.Minute, testINST04DevProfile))
	t.Run("INST-05", row(60*time.Minute, testINST05))
	t.Run("INST-05/keep", row(75*time.Minute, testINST05Keep))
	t.Run("INST-06", row(75*time.Minute, testINST06))
	t.Run("INST-06/downgrade", row(40*time.Minute, testINST06Downgrade))
}

// testINST01: the quiet bundle install on a machine without hlab installs
// the files, registers the service, creates the data folder, sets the PATH;
// the Burn log shows the prerequisite action passed and WebView2 detected
// and skipped; hlab version prints the built version.
func testINST01(ctx context.Context, t *testing.T) {
	if err := ensureNotInstalled(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := install(ctx, newVersion)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{"hlabd.exe", "hlab.exe", "hlab-tray.exe", "hlab-agent"} {
		if notExist(filepath.Join(releaseDir(), f)) {
			t.Errorf("%s missing in %s", f, releaseDir())
		}
	}
	catalogs, err := filepath.Glob(filepath.Join(harness.root, "catalog", "*.json"))
	if err != nil || len(catalogs) == 0 {
		t.Fatalf("no catalog\\*.json in the working copy (%v)", err)
	}
	for _, c := range catalogs {
		if p := filepath.Join(releaseDir(), "catalog", filepath.Base(c)); notExist(p) {
			t.Errorf("%s missing", p)
		}
	}

	svc, ok, err := service(ctx, releaseService)
	switch {
	case err != nil:
		t.Error(err)
	case !ok:
		t.Errorf("service %q is not registered", releaseService)
	default:
		if svc.StartMode != "Manual" {
			t.Errorf("service %q start mode %q, want Manual", releaseService, svc.StartMode)
		}
		if !strings.EqualFold(svc.StartName, "LocalSystem") {
			t.Errorf("service %q runs as %q, want LocalSystem", releaseService, svc.StartName)
		}
		if want := filepath.Join(releaseDir(), "hlabd.exe"); !strings.EqualFold(filepath.Clean(serviceExe(svc.PathName)), want) {
			t.Errorf("service %q runs %q, want %q", releaseService, svc.PathName, want)
		}
		// The installer and dev-setup.ps1 give both services the same descriptor.
		rel, err := serviceSDDL(ctx, releaseService)
		if err != nil {
			t.Error(err)
		} else if dev, err := serviceSDDL(ctx, devService); err != nil {
			t.Error(err)
		} else if rel != dev {
			t.Errorf("service %q descriptor %s differs from %q's %s", releaseService, rel, devService, dev)
		} else if !interactiveStartStop(rel) {
			t.Errorf("service %q descriptor %s gives Interactive Users no start and stop", releaseService, rel)
		}
	}

	if fi, err := os.Stat(releaseDataDir()); err != nil || !fi.IsDir() {
		t.Errorf("%s not created: %v", releaseDataDir(), err)
	}
	if ok, err := onMachinePath(ctx, releaseDir()); err != nil {
		t.Error(err)
	} else if !ok {
		t.Errorf("%s is not on the machine PATH", releaseDir())
	}

	prereq := regexp.MustCompile(`(?i)Action ended [0-9:]+: \w*prereq\w*\. Return value 1\.`)
	if !prereq.MatchString(run.log) {
		t.Errorf("the logs show no passed prerequisite action (Action ended ...: <prerequisite action>. Return value 1.)\n%s", tail(run.log))
	}
	if err := webView2Skipped(run.log); err != nil {
		t.Errorf("%v\n%s", err, tail(run.log))
	}

	if r, err := runRelease(ctx, time.Minute, "version"); err != nil {
		t.Errorf("hlab version: %v\n%s", err, r)
	} else if !strings.Contains(r.Stdout, newVersion) {
		t.Errorf("hlab version printed %q, want %s", strings.TrimSpace(r.Stdout), newVersion)
	}
}

// interactiveStartStop reports whether an allow ACE for Interactive Users
// holds the start (RP) and stop (WP) rights.
func interactiveStartStop(sddl string) bool {
	ace := regexp.MustCompile(`\(A;[^;]*;([A-Z]*);[^;]*;[^;]*;IU\)`)
	for _, m := range ace.FindAllStringSubmatch(sddl, -1) {
		var start, stop bool
		for i := 0; i+1 < len(m[1]); i += 2 {
			switch m[1][i : i+2] {
			case "RP":
				start = true
			case "WP":
				stop = true
			}
		}
		if start && stop {
			return true
		}
	}
	return false
}

// webView2Skipped checks the Burn log: the WebView2 version search found an
// installed runtime and the WebView2 package was planned with no execution.
func webView2Skipped(log string) error {
	search := regexp.MustCompile(`(?i)Setting (?:string|version) variable '([^']*webview[^']*)' to value '([^']*)'`)
	var found bool
	for _, m := range search.FindAllStringSubmatch(log, -1) {
		if v := strings.TrimSpace(m[2]); v != "" && v != "0.0.0.0" {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("the Burn log shows no WebView2 version found by the registry search")
	}
	detected := regexp.MustCompile(`(?i)Detected package: ([^,\s]*webview[^,\s]*),`).FindStringSubmatch(log)
	if detected == nil {
		return fmt.Errorf("the Burn log names no WebView2 package")
	}
	id := regexp.QuoteMeta(detected[1])
	if !regexp.MustCompile(`Planned package: ` + id + `,[^\n]*execute: None`).MatchString(log) {
		return fmt.Errorf("WebView2 package %s was not planned with execute: None", detected[1])
	}
	if regexp.MustCompile(`Applying execute package: ` + id + `\b`).MatchString(log) {
		return fmt.Errorf("WebView2 package %s was executed although WebView2 is present", detected[1])
	}
	return nil
}

// testINST04: after the default uninstall of a cloud that ran with a
// server, trust on and a WSL distro, no Hyper-V or host object of the
// release profile remains and the WSL forwarding is as before the install.
func testINST04(ctx context.Context, t *testing.T) {
	if harness.forwardingErr != nil {
		t.Fatalf("precondition: forwarding of the WSL adapter before the install: %v", harness.forwardingErr)
	}
	if err := sharedDefaultUninstall(ctx); err != nil {
		t.Fatal(err)
	}

	if vms, err := profileVMs(ctx); err != nil {
		t.Error(err)
	} else if len(vms) > 0 {
		t.Errorf("VMs of the release profile remain: %v", vms)
	}
	if ok, err := switchExists(ctx, releaseTag); err != nil {
		t.Error(err)
	} else if ok {
		t.Errorf("switch %q remains", releaseTag)
	}
	if routes, err := releaseRoutes(ctx); err != nil {
		t.Error(err)
	} else if len(routes) > 0 {
		t.Errorf("routes via the release adapter remain: %v", routes)
	}
	if rules, err := nrptRules(ctx, releaseTag); err != nil {
		t.Error(err)
	} else if len(rules) > 0 {
		t.Errorf("NRPT rules with comment %q remain: %v", releaseTag, rules)
	}
	if ok, err := natExists(ctx, releaseTag); err != nil {
		t.Error(err)
	} else if ok {
		t.Errorf("NAT %q remains", releaseTag)
	}
	if rules, err := firewallRules(ctx, releaseTag); err != nil {
		t.Error(err)
	} else if len(rules) > 0 {
		t.Errorf("firewall rules of group %q remain: %v", releaseTag, rules)
	}
	if fw, err := wslForwarding(ctx); err != nil {
		t.Error(err)
	} else {
		for alias, before := range harness.forwarding {
			if got, ok := fw[alias]; ok && got != before {
				t.Errorf("forwarding of %s is %s, was %s before the install", alias, got, before)
			}
		}
	}
	if _, ok, err := service(ctx, releaseService); err != nil {
		t.Error(err)
	} else if ok {
		t.Errorf("service %q remains", releaseService)
	}
	if ok, err := rootHas(ctx, defaultUninstall.thumbprint); err != nil {
		t.Error(err)
	} else if ok {
		t.Errorf(`the hlab CA %s remains in LocalMachine\Root`, defaultUninstall.thumbprint)
	}
	if ok, err := wslHasCAFile(ctx); err != nil {
		t.Error(err)
	} else if ok {
		t.Errorf("%s remains in distro %s", wslCAFile, harness.distro)
	}
}

// testINST04DevProfile: uninstalling the release leaves the dev profile's
// switch, appliance VM and service in place.
func testINST04DevProfile(ctx context.Context, t *testing.T) {
	if !harness.devSwitch || !harness.devAppliance || !harness.devServiceThere {
		t.Fatalf("precondition: the dev profile needs switch %q, VM %q and service %q before the run (found %v, %v, %v); start the dev cloud once",
			devTag, devTag+"-appliance", devService, harness.devSwitch, harness.devAppliance, harness.devServiceThere)
	}
	if err := sharedDefaultUninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := switchExists(ctx, devTag); err != nil {
		t.Error(err)
	} else if !ok {
		t.Errorf("switch %q of the dev profile is gone", devTag)
	}
	if ok, err := vmExists(ctx, devTag+"-appliance"); err != nil {
		t.Error(err)
	} else if !ok {
		t.Errorf("VM %q of the dev profile is gone", devTag+"-appliance")
	}
	if _, ok, err := service(ctx, devService); err != nil {
		t.Error(err)
	} else if !ok {
		t.Errorf("service %q of the dev profile is gone", devService)
	}
}

// testINST05: after the default quiet uninstall with the tray running no
// file of hlab remains, the PATH entry and the shortcuts are gone and the
// bundle is no longer registered.
func testINST05(ctx context.Context, t *testing.T) {
	if err := sharedDefaultUninstall(ctx); err != nil {
		t.Fatal(err)
	}
	for _, d := range append([]string{releaseDir(), releaseDataDir()}, userFolders()...) {
		if !notExist(d) {
			t.Errorf("%s remains", d)
		}
	}
	if ok, err := onMachinePath(ctx, releaseDir()); err != nil {
		t.Error(err)
	} else if ok {
		t.Errorf("%s remains on the machine PATH", releaseDir())
	}
	if start, desk := shortcuts(); len(start)+len(desk) > 0 {
		t.Errorf("shortcuts remain: %v", append(start, desk...))
	}
	if bs, err := registeredBundles(ctx); err != nil {
		t.Error(err)
	} else if len(bs) > 0 {
		t.Errorf("bundle still registered: %v", bs)
	}
}

// testINST05Keep: uninstall with KeepData=1 keeps the data folder and
// removes VMs, switch, NRPT rules and the service; a reinstall and cloud
// start shows the project and recreates the server's VM.
func testINST05Keep(ctx context.Context, t *testing.T) {
	if err := ensureInstalled(ctx, newVersion); err != nil {
		t.Fatal(err)
	}
	f, err := ensureFixture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	disks, err := vmDiskPaths(ctx, f.vm)
	if err != nil || len(disks) == 0 {
		t.Fatalf("precondition: disks of VM %s: %v %v", f.vm, disks, err)
	}

	if err := uninstall(ctx, true); err != nil {
		t.Fatal(err)
	}

	kept := append([]string{
		filepath.Join(releaseDataDir(), "settings.toml"),
		filepath.Join(releaseDataDir(), "state.db"),
		filepath.Join(releaseDataDir(), "ca", "ca.pem"),
	}, disks...)
	for _, p := range kept {
		if notExist(p) {
			t.Errorf("%s was deleted with KeepData=1", p)
		}
	}
	if vms, err := profileVMs(ctx); err != nil {
		t.Error(err)
	} else if len(vms) > 0 {
		t.Errorf("VMs of the release profile remain: %v", vms)
	}
	if ok, err := switchExists(ctx, releaseTag); err != nil {
		t.Error(err)
	} else if ok {
		t.Errorf("switch %q remains", releaseTag)
	}
	if rules, err := nrptRules(ctx, releaseTag); err != nil {
		t.Error(err)
	} else if len(rules) > 0 {
		t.Errorf("NRPT rules with comment %q remain: %v", releaseTag, rules)
	}
	if _, ok, err := service(ctx, releaseService); err != nil {
		t.Error(err)
	} else if ok {
		t.Errorf("service %q remains", releaseService)
	}
	if t.Failed() {
		return
	}

	if _, err := install(ctx, newVersion); err != nil {
		t.Fatal(err)
	}
	if err := startReleaseCloud(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := hasProject(ctx, f.project); err != nil {
		t.Error(err)
	} else if !ok {
		t.Errorf("project %s is not listed after the reinstall", f.project)
	}
	if err := waitFor(ctx, 10*time.Minute, 5*time.Second, func() (bool, error) { return vmExists(ctx, f.vm) }); err != nil {
		t.Errorf("VM %s of server %s not recreated after the reinstall: %v", f.vm, f.serverName, err)
	}
}

var stopBehaviourRe = regexp.MustCompile(`(?m)^(\s*stop_behaviour\s*=\s*)["']([^"']*)["']`)

// setStopBehaviour writes cloud.stop_behaviour into the release settings.
func setStopBehaviour(value string) error {
	file := filepath.Join(releaseDataDir(), "settings.toml")
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	s := string(data)
	switch {
	case stopBehaviourRe.MatchString(s):
		s = stopBehaviourRe.ReplaceAllString(s, `${1}"`+value+`"`)
	case strings.Contains(s, "[cloud]"):
		s = strings.Replace(s, "[cloud]", "[cloud]\nstop_behaviour = \""+value+"\"", 1)
	default:
		s += "\n[cloud]\nstop_behaviour = \"" + value + "\"\n"
	}
	return os.WriteFile(file, []byte(s), 0o644)
}

func stopBehaviour() (string, error) {
	data, err := os.ReadFile(filepath.Join(releaseDataDir(), "settings.toml"))
	if err != nil {
		return "", err
	}
	m := stopBehaviourRe.FindStringSubmatch(string(data))
	if m == nil {
		return "", nil
	}
	return m[2], nil
}

// testINST06: upgrading 0.0.1 to 0.0.2 with the cloud running stops it,
// leaves it stopped and keeps settings, projects, tokens and the server.
func testINST06(ctx context.Context, t *testing.T) {
	if err := ensureInstalled(ctx, oldVersion); err != nil {
		t.Fatal(err)
	}
	f, err := ensureFixture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setStopBehaviour("shutdown"); err != nil {
		t.Fatal(err)
	}
	if st, err := cloudState(ctx, releaseHlab()); err != nil || st != "running" {
		t.Fatalf("precondition: the release cloud is %q before the upgrade (%v)", st, err)
	}

	if _, err := install(ctx, newVersion); err != nil {
		t.Fatal(err)
	}

	if st, err := cloudState(ctx, releaseHlab()); err != nil {
		t.Error(err)
	} else if st != "stopped" {
		t.Errorf("the cloud is %q after the upgrade, want stopped", st)
	}
	if svc, ok, err := service(ctx, releaseService); err != nil {
		t.Error(err)
	} else if !ok || svc.State != "Stopped" {
		t.Errorf("service %q after the upgrade: registered %v, state %q, want Stopped", releaseService, ok, svc.State)
	}
	if v, err := stopBehaviour(); err != nil {
		t.Error(err)
	} else if v != "shutdown" {
		t.Errorf("stop_behaviour %q after the upgrade, want the changed \"shutdown\"", v)
	}
	if r, err := runRelease(ctx, time.Minute, "version"); err != nil {
		t.Errorf("hlab version: %v\n%s", err, r)
	} else if !strings.Contains(r.Stdout, newVersion) {
		t.Errorf("hlab version printed %q, want %s", strings.TrimSpace(r.Stdout), newVersion)
	}
	if t.Failed() {
		return
	}

	if err := startReleaseCloud(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := hasProject(ctx, f.project); err != nil {
		t.Error(err)
	} else if !ok {
		t.Errorf("project %s is gone after the upgrade", f.project)
	}
	var got struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
	}
	if code, err := f.api(ctx, "GET", fmt.Sprintf("/servers/%d", f.serverID), nil, &got); err != nil || code != 200 || got.Server.ID != f.serverID {
		t.Errorf("GET /servers/%d with the token from before the upgrade: status %d, id %d: %v", f.serverID, code, got.Server.ID, err)
	}
	if ok, err := vmExists(ctx, f.vm); err != nil {
		t.Error(err)
	} else if !ok {
		t.Errorf("VM %s of server %s is gone after the upgrade", f.vm, f.serverName)
	}
}

// testINST06Downgrade: installing 0.0.1 over 0.0.2 is refused and leaves
// 0.0.2 installed.
func testINST06Downgrade(ctx context.Context, t *testing.T) {
	if err := ensureInstalled(ctx, newVersion); err != nil {
		t.Fatal(err)
	}
	r, err := runBundle(ctx, oldVersion, "downgrade")
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 {
		t.Errorf("installing %s over %s succeeded, want refused\n%s", oldVersion, newVersion, tail(r.log))
	}
	if v, err := installedVersion(ctx); err != nil {
		t.Error(err)
	} else if v != newVersion {
		t.Errorf("installed bundle %q after the refused downgrade, want %s", v, newVersion)
	}
	if r, err := runRelease(ctx, time.Minute, "version"); err != nil {
		t.Errorf("hlab version: %v\n%s", err, r)
	} else if !strings.Contains(r.Stdout, newVersion) {
		t.Errorf("hlab version printed %q after the refused downgrade, want %s", strings.TrimSpace(r.Stdout), newVersion)
	}
}
