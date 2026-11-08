//go:build live && windows

package itest

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/webcodingcat/hlab/internal/lab"
)

// newRelease writes a copy of the appliance's release disk that HLab takes
// for another release: the same appliance, with another creator in the
// VHDX's file type identifier (bytes 8 to 520, UTF-16, not part of any
// checksum), so that its SHA-256 differs.
func newRelease(t *testing.T, release string) string {
	t.Helper()
	next := filepath.Join(t.TempDir(), "hlab-appliance-next.vhdx")
	in, err := os.Open(release)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, 8)
	if _, err := out.ReadAt(signature, 0); err != nil || string(signature) != "vhdxfile" {
		t.Fatalf("the release disk starts with %q, %v; want a VHDX", signature, err)
	}
	creator := make([]byte, 512)
	for i, unit := range utf16.Encode([]rune("hlab itest: the next release")) {
		binary.LittleEndian.PutUint16(creator[2*i:], unit)
	}
	if _, err := out.WriteAt(creator, 8); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return next
}

// outageWatch measures how long a TCP port of a guest took no new
// connection: it tries every 100 ms until end.
type outageWatch struct {
	stop    context.CancelFunc
	longest chan time.Duration
}

// watchOutage starts measuring the port's outages.
func watchOutage(address string, port int) *outageWatch {
	ctx, cancel := context.WithCancel(context.Background())
	watch := &outageWatch{stop: cancel, longest: make(chan time.Duration, 1)}
	go func() {
		target := net.JoinHostPort(address, fmt.Sprint(port))
		lastUp, longest := time.Now(), time.Duration(0)
		for ctx.Err() == nil {
			conn, err := (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext(ctx, "tcp", target)
			if err == nil {
				_ = conn.Close()
				longest = max(longest, time.Since(lastUp))
				lastUp = time.Now()
			}
			select {
			case <-ctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		watch.longest <- max(longest, time.Since(lastUp)) // still down at the end counts
	}()
	return watch
}

// end stops the watch and returns the longest time without a connection;
// the time between two tries (100 ms) is part of it.
func (w *outageWatch) end() time.Duration {
	w.stop()
	return <-w.longest
}

// sessionWatch is an SSH session to a guest whose command prints lines:
// it shows whether a TCP connection through the appliance lives on, and
// how long no line came.
type sessionWatch struct {
	ssh    *exec.Cmd
	exited chan struct{}

	mu       sync.Mutex
	lastLine time.Time
	longest  time.Duration
}

// The commands of the sessions: the first prints a line every 100 ms; the
// second prints one line and then waits silently for a mark, so that the
// guest sends first after the update, before the host says anything.
const (
	tickingSession = "while sleep 0.1; do echo tick; done"
	quietMark      = "/run/itest-go"
	quietSession   = "echo waiting; while [ ! -e " + quietMark + " ]; do sleep 0.1; done; " + tickingSession
)

// watchSession logs into the guest, runs the command and waits for its
// first line.
func (k labKey) watchSession(t *testing.T, address, command string) *sessionWatch {
	t.Helper()
	args := append([]string{"-i", k.file}, sshOptions(os.DevNull)...)
	args = append(args, "root@"+address, command)
	watch := &sessionWatch{ssh: exec.Command(k.ssh, args...), exited: make(chan struct{})}
	stdout, err := watch.ssh.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := watch.ssh.Start(); err != nil {
		t.Fatal(err)
	}
	first := make(chan struct{})
	go func() {
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			watch.mu.Lock()
			if watch.lastLine.IsZero() {
				close(first)
			} else {
				watch.longest = max(watch.longest, time.Since(watch.lastLine))
			}
			watch.lastLine = time.Now()
			watch.mu.Unlock()
		}
		_ = watch.ssh.Wait()
		close(watch.exited)
	}()
	select {
	case <-first:
	case <-watch.exited:
		t.Fatalf("the SSH session to %s ended before its first line", address)
	case <-time.After(time.Minute):
		t.Fatalf("the SSH session to %s printed nothing within a minute", address)
	}
	return watch
}

// end waits up to 10 s for one more line, ends the session and returns the
// longest time without a line, and whether the session lived until now: it
// still ran and printed that line. A session that hangs runs on, so the
// line is what tells.
func (w *sessionWatch) end() (longest time.Duration, lived bool) {
	w.mu.Lock()
	seen := w.lastLine
	w.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	newer := false
	for !newer && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		w.mu.Lock()
		newer = w.lastLine.After(seen)
		w.mu.Unlock()
	}
	select {
	case <-w.exited:
	default:
		lived = newer
		_ = w.ssh.Process.Kill()
		<-w.exited
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.longest, lived
}

// checkApplianceFolder fails the test unless the appliance's folder holds
// only the release's disk and the system disk of the MAC set.
func checkApplianceFolder(t *testing.T, dataDir, release, set string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, lab.ApplianceDir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	id, err := sha256File(release)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{id + ".vhdx", "system-" + set + ".vhdx"}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("the appliance's folder holds %q, want %q", names, want)
	}
}

// sha256File returns a file's SHA-256, hex, which names a release's disk in
// the appliance's folder.
func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
