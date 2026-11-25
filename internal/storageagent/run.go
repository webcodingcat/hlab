package storageagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// commandContext returns ctx bounded by commandTimeout, for a command not
// started through run.
func commandContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, commandTimeout)
}

// run executes a command and returns its output; on failure the error
// carries the command's output. A command that does not finish within
// commandTimeout (or ctx) is killed, and the error names it and how long
// it ran. Never pass a secret as an argument.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := commandContext(ctx)
	defer cancel()
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	start := time.Now()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.WaitDelay = commandWaitGrace
	go func() {
		out, err := cmd.CombinedOutput()
		done <- result{out, err}
	}()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	var r result
	select {
	case r = <-done:
	case <-cctx.Done():
		select {
		case r = <-done:
		case <-time.After(commandWaitGrace):
			return "", fmt.Errorf("%s: not finished after %v, the process did not exit: %w",
				line, time.Since(start).Round(time.Millisecond), cctx.Err())
		}
	}
	if r.err != nil {
		if cerr := cctx.Err(); cerr != nil && !errors.Is(r.err, cerr) {
			return string(r.out), fmt.Errorf("%s: ended after %v: %w (%v): %s",
				line, time.Since(start).Round(time.Millisecond), cerr, r.err, bytes.TrimSpace(r.out))
		}
		return string(r.out), fmt.Errorf("%s: %w: %s", line, r.err, bytes.TrimSpace(r.out))
	}
	return string(r.out), nil
}
