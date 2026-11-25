package storageagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// commandTimeout bounds every command the storage agent runs. The longest
// legitimate one is a btrfs command that waits for the file system on a
// large data disk: "btrfs subvolume sync" after the deletion of a box of
// several hundred GB, "btrfs quota rescan -w" or fstrim over the whole
// disk, each a few minutes at most; 10 minutes leaves a wide margin and
// stays below the deadline of hlabd's apply (agentclient), so an expired
// command reaches hlabd in the apply's error.
var commandTimeout = 10 * time.Minute

// commandWaitGrace is how long run waits for a command killed at its
// deadline to exit; a process stuck in the kernel cannot be killed, and
// run returns without it.
const commandWaitGrace = 5 * time.Second

// run executes a command and returns its output; on failure the error
// carries the command's output. A command that does not finish within
// commandTimeout (or ctx) is killed, and the error names it and how long
// it ran. Never pass a secret as an argument.
func run(ctx context.Context, name string, args ...string) (string, error) {
	var out bytes.Buffer
	err := runBounded(ctx, nil, &out, &out, out.Bytes, name, args...)
	if errors.Is(err, errNotExited) {
		return "", err
	}
	return out.String(), err
}

// runIO executes a command like run, with stdin and its standard output
// and standard error apart. On failure the error carries only the
// standard error, so a standard output that may hold a secret stays with
// the caller.
func runIO(ctx context.Context, stdin io.Reader, name string, args ...string) (stdout, stderr string, err error) {
	var o, e bytes.Buffer
	err = runBounded(ctx, stdin, &o, &e, e.Bytes, name, args...)
	if errors.Is(err, errNotExited) {
		return "", "", err
	}
	return o.String(), e.String(), err
}

// errNotExited marks a command killed at its deadline whose process did
// not exit within commandWaitGrace; its output buffers may still be
// written to and must not be read.
var errNotExited = errors.New("the process did not exit")

// runBounded runs the command with stdin, stdout and stderr under
// commandTimeout (or ctx) and commandWaitGrace; detail returns the output
// an error carries and is only called once the command has exited. The
// command's exit error stays wrapped, so callers can read its exit code.
func runBounded(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, detail func() []byte, name string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.WaitDelay = commandWaitGrace
	go func() { done <- cmd.Run() }()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	var err error
	select {
	case err = <-done:
	case <-cctx.Done():
		select {
		case err = <-done:
		case <-time.After(commandWaitGrace):
			return fmt.Errorf("%s: not finished after %v, %w: %w",
				line, time.Since(start).Round(time.Millisecond), errNotExited, cctx.Err())
		}
	}
	if err != nil {
		if cerr := cctx.Err(); cerr != nil && !errors.Is(err, cerr) {
			return fmt.Errorf("%s: ended after %v: %w (%w): %s",
				line, time.Since(start).Round(time.Millisecond), cerr, err, bytes.TrimSpace(detail()))
		}
		return fmt.Errorf("%s: %w: %s", line, err, bytes.TrimSpace(detail()))
	}
	return nil
}
