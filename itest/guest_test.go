//go:build live && windows

package itest

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform/windows"
	"github.com/webcodingcat/hlab/internal/sshkey"
	"github.com/webcodingcat/hlab/internal/system"
)

// The environment of the process that runs the lab for a crash
// (TestLabForACrash): the data folder, the appliance's disk, the lab's key.
const (
	crashDataVar      = "HLAB_ITEST_CRASH_DATA"
	crashReleaseVar   = "HLAB_ITEST_CRASH_RELEASE"
	crashLabKeyVar    = "HLAB_ITEST_CRASH_LAB_KEY"
	crashLabRunsLine  = "itest: the lab runs"
	crashStartTimeout = 5 * time.Minute
)

// labRun opens and closes the lab on one data folder, as hlabd's starts
// and stops do.
type labRun struct {
	dataDir string
	release string
	labKey  string
	parts   *windows.Platform
	caches  []*imageCache

	mu     sync.Mutex
	closed map[*lab.Lab]bool
}

// open opens the lab and starts its machine, with the kept images.
func (r *labRun) open(ctx context.Context, t *testing.T) *lab.Lab {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	l, err := lab.Open(lab.Options{DataDir: r.dataDir, Logger: logger, LabKey: r.labKey}, system.New(r.parts.Platform))
	if err != nil {
		t.Fatal(err)
	}
	for _, cache := range r.caches {
		cache.lend(t, l)
	}
	if err := l.Machine().Start(ctx, lab.MachineOptions{ApplianceRelease: r.release, ServerShutdown: 30 * time.Second}); err != nil {
		_ = l.Close()
		t.Fatal(err)
	}
	return l
}

// close stops the lab once: the servers shut down, the appliance and the
// host link go. After the last close, the lab's VMs that a failed test
// left go too.
func (r *labRun) close(t *testing.T, l *lab.Lab) {
	t.Helper()
	r.mu.Lock()
	if r.closed == nil {
		r.closed = map[*lab.Lab]bool{}
	}
	done := r.closed[l]
	r.closed[l] = true
	r.mu.Unlock()
	if done {
		return
	}
	if err := l.Close(); err != nil {
		t.Errorf("closing the lab: %v", err)
	}
}

// removeVMs removes the lab's VMs, which a test that failed may leave.
func (r *labRun) removeVMs(t *testing.T) {
	found, _ := r.parts.Hypervisor.FindVMs(context.Background(), system.VMTag)
	for _, vm := range found {
		if err := r.parts.Hypervisor.DeleteVM(context.Background(), vm.Name); err != nil {
			t.Errorf("removing %s: %v", vm.Name, err)
		}
	}
}

// crash runs the lab in another process of this test binary
// (TestLabForACrash) and kills it once its machine runs, as a crash of
// hlabd would end it: the appliance, the servers and the host link stay.
func (r *labRun) crash(ctx context.Context, t *testing.T) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, executable, "-test.run=^TestLabForACrash$", "-test.v")
	child.Env = append(os.Environ(),
		crashDataVar+"="+r.dataDir, crashReleaseVar+"="+r.release, crashLabKeyVar+"="+r.labKey)
	child.Stderr = os.Stderr
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	runs := make(chan bool, 1)
	go func() {
		lines := bufio.NewScanner(output)
		for lines.Scan() {
			if strings.Contains(lines.Text(), crashLabRunsLine) {
				runs <- true
			}
		}
		runs <- false
	}()
	select {
	case ok := <-runs:
		if !ok {
			t.Fatal("the lab of the crash ended before its machine ran")
		}
	case <-time.After(crashStartTimeout):
		t.Fatal("the lab of the crash did not start in time")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	t.Log("killed the process that ran the lab")
}

// TestLabForACrash is not a test of its own: labRun.crash runs it in
// another process, which opens the lab, starts its machine and waits to be
// killed.
func TestLabForACrash(t *testing.T) {
	dataDir := os.Getenv(crashDataVar)
	if dataDir == "" {
		t.Skip("runs only in the process of labRun.crash")
	}
	parts := newParts(dataDir) // the parent removes it
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	l, err := lab.Open(lab.Options{DataDir: dataDir, Logger: logger, LabKey: os.Getenv(crashLabKeyVar)}, system.New(parts.Platform))
	if err != nil {
		t.Fatal(err)
	}
	machine := lab.MachineOptions{ApplianceRelease: os.Getenv(crashReleaseVar), ServerShutdown: 30 * time.Second}
	if err := l.Machine().Start(t.Context(), machine); err != nil {
		t.Fatal(err)
	}
	fmt.Println(crashLabRunsLine)
	time.Sleep(crashStartTimeout) // killed long before
}

// labKey is the lab's SSH key of the test, which every server's root
// takes, with ssh.exe to use it.
type labKey struct {
	private []byte
	public  string
	file    string
	ssh     string
}

// newLabKey makes the test's lab key; the private key goes to a file of the
// test's temporary folder, which only the user, the administrators and
// SYSTEM may read, as ssh.exe demands.
func newLabKey(t *testing.T) labKey {
	t.Helper()
	ssh, err := exec.LookPath("ssh.exe")
	if err != nil {
		t.Skipf("no ssh.exe (Windows' OpenSSH client): %v", err)
	}
	private, public, err := sshkey.New("hlab")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "lab-key")
	if err := os.WriteFile(file, private, 0o600); err != nil {
		t.Fatal(err)
	}
	return labKey{private: private, public: public, file: file, ssh: ssh}
}

// sshOptions are ssh's options for the test's guests: no known hosts (the
// null device of the system ssh runs on), as their addresses come back
// with new host keys, and no questions.
func sshOptions(nullDevice string) []string {
	return []string{
		"-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=" + nullDevice,
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "LogLevel=ERROR",
	}
}

// run runs a command on a guest as root with the lab's key, with stdin as
// its input, and returns its output without the line end. Right after the
// SSH banner, cloud-init may not have written the keys yet, so it tries
// again for a while.
func (k labKey) run(t *testing.T, address, stdin, command string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		args := append([]string{"-i", k.file}, sshOptions(os.DevNull)...)
		ssh := exec.Command(k.ssh, append(args, "root@"+address, command)...)
		ssh.Stdin = strings.NewReader(stdin)
		var stdout, stderr bytes.Buffer
		ssh.Stdout, ssh.Stderr = &stdout, &stderr
		err := ssh.Run()
		var exitErr *exec.ExitError
		// 255 is ssh's own failure; anything else is the command's
		if err == nil || (errors.As(err, &exitErr) && exitErr.ExitCode() != 255) {
			return strings.TrimSpace(stdout.String())
		}
		if time.Now().After(deadline) {
			t.Fatalf("ssh root@%s %q: %v: %s", address, command, err, stderr.String())
		}
		time.Sleep(2 * time.Second)
	}
}

// bootID returns the guest's boot ID, which a boot changes.
func (k labKey) bootID(t *testing.T, server guest) string {
	t.Helper()
	return k.run(t, server.ipv4, "", "cat /proc/sys/kernel/random/boot_id")
}

// fromWSL logs into the guest from WSL2 with the lab's key and checks that
// the guest sees the host's address, as Windows' NAT hands WSL2's traffic
// on. It logs and returns if there is no WSL with ssh.
func (k labKey) fromWSL(t *testing.T, address string) {
	t.Helper()
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		t.Log("no WSL: the check from WSL2 is left out")
		return
	}
	// no double quotes: they would not survive wsl.exe's command line
	script := "command -v ssh > /dev/null || exit 3; k=$(mktemp) && cat > $k && ssh -i $k " +
		strings.Join(sshOptions("/dev/null"), " ") + " root@" + address + " echo \\$SSH_CLIENT; s=$?; rm -f $k; exit $s"
	cmd := exec.Command(wsl, "-e", "sh", "-c", script)
	cmd.Stdin = bytes.NewReader(k.private)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
		t.Log("WSL has no ssh: the check from WSL2 is left out")
		return
	}
	if err != nil {
		t.Errorf("ssh from WSL2 to %s: %v: %s", address, err, stderr.String())
		return
	}
	if got := strings.TrimSpace(stdout.String()); !strings.HasPrefix(got, "198.18.0.1 ") {
		t.Errorf("the guest sees an SSH connection from WSL2 from %q, want 198.18.0.1", got)
	}
}

// probeScript connects to each host:port of its arguments and prints the
// outcome: open, or the error's errno name.
const probeScript = `import errno, socket, sys
for target in sys.argv[1:]:
    host, port = target.rsplit(":", 1)
    try:
        socket.create_connection((host, int(port)), timeout=5).close()
        outcome = "open"
    except socket.timeout:
        outcome = "timeout"
    except socket.gaierror:
        outcome = "no-name"
    except OSError as e:
        outcome = errno.errorcode.get(e.errno, str(e))
    print(target, outcome)
`

// checkUplink checks from a guest that the appliance lets it through to
// the host's APIs and the Internet, but neither to the lab control, which
// it rejects as a closed port (ECONNREFUSED), nor to the private ranges
// behind the uplink, which it rejects as administratively prohibited
// (EHOSTUNREACH). Nothing of the test listens on 8080 and 8081 of the host,
// and Windows' firewall drops what has no rule (hlabd's rule opens them),
// so the test sees them pass the appliance by a timeout.
func checkUplink(t *testing.T, keys labKey, address string) {
	t.Helper()
	want := map[string]string{
		"198.18.0.1:8080":   "timeout",
		"198.18.0.1:8081":   "timeout",
		"198.18.0.1:8090":   "ECONNREFUSED",
		"deb.debian.org:80": "open",
		"10.0.0.1:80":       "EHOSTUNREACH",
		"172.16.0.1:80":     "EHOSTUNREACH",
		"192.168.0.1:80":    "EHOSTUNREACH",
		"169.254.1.1:80":    "EHOSTUNREACH",
		"100.64.0.1:80":     "EHOSTUNREACH",
	}
	var targets []string
	for target := range want {
		targets = append(targets, target)
	}
	output := keys.run(t, address, probeScript, "python3 - "+strings.Join(targets, " "))
	got := map[string]string{}
	for line := range strings.Lines(output) {
		target, outcome, _ := strings.Cut(strings.TrimSpace(line), " ")
		got[target] = outcome
	}
	for target, outcome := range want {
		if got[target] != outcome {
			t.Errorf("from a guest, %s is %q, want %q", target, got[target], outcome)
		}
	}
}

// checkHostIsClean checks that the host has nothing of the lab's network
// left: no switch HLab-*, no address or route of the lab's ranges, no NRPT
// rule with the comment hlab.
func checkHostIsClean(t *testing.T) {
	t.Helper()
	script := `$counts = @(
    @(Get-VMSwitch -Name 'HLab-*' -ErrorAction SilentlyContinue).Count,
    @(Get-NetIPAddress | Where-Object { $_.IPAddress -like '198.18.*' -or $_.IPAddress -like '2001:db8:*' }).Count,
    @(Get-NetRoute | Where-Object { $_.DestinationPrefix -like '198.1[89].*' -or $_.DestinationPrefix -like '2001:db8:*' }).Count,
    @(Get-DnsClientNrptRule | Where-Object Comment -EQ 'hlab').Count)
$counts -join ' '`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Errorf("looking at the host: %v: %s", err, output)
		return
	}
	if got := strings.TrimSpace(string(output)); got != "0 0 0 0" {
		t.Errorf("the host keeps switches, addresses, routes, NRPT rules of the lab: %s, want 0 0 0 0", got)
	}
}
