//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/webcodingcat/hlab/internal/platform"
)

// LockFile is where the machine's HLab holds its lock; /run/lock is the
// system's place for lock files that any user may create.
const LockFile = "/run/lock/hlab.lock"

// tokenGroup is the group whose members may read the control token.
const tokenGroup = "hlab"

// Lifecycle is the process's life cycle on Linux.
type Lifecycle struct {
	lockFile string
	stopOnce sync.Once
	stop     chan struct{}
}

// NewLifecycle returns the life cycle with its lock in lockFile (LockFile;
// tests take their own).
func NewLifecycle(lockFile string) *Lifecycle {
	return &Lifecycle{lockFile: lockFile}
}

// Lock makes this process the only HLab on the machine: it holds an flock
// on the lock file, which the kernel releases when the process ends, also
// when it crashes. The file names the data folder, so that a second start
// can say which HLab runs.
func (l *Lifecycle) Lock(dataDir string) (func(), error) {
	file, err := os.OpenFile(l.lockFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, _ := os.ReadFile(l.lockFile)
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("HLab runs already with the data folder %s: %w", strings.TrimSpace(string(holder)), platform.ErrLocked)
		}
		return nil, err
	}
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		absolute = dataDir
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.WriteAt([]byte(absolute+"\n"), 0) // only for the message above
	}
	unlock := func() { _ = file.Close() } // closing the file releases the flock
	return unlock, nil
}

// WriteSecret writes a secret file readable by its owner and the group hlab,
// if the machine has it. It writes a new file and renames it over the old
// one, so that a reader never sees half a token.
func (l *Lifecycle) WriteSecret(path string, token []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, token, 0o640); err != nil {
		return err
	}
	if group, err := user.LookupGroup(tokenGroup); err == nil {
		gid, _ := strconv.Atoi(group.Gid)
		if err := os.Chown(temporary, -1, gid); err != nil {
			return err
		}
	}
	if err := os.Chmod(temporary, 0o640); err != nil { // the umask may have taken bits off
		return err
	}
	return os.Rename(temporary, path)
}

// StopRequests is closed on SIGINT (Ctrl+C) or SIGTERM.
func (l *Lifecycle) StopRequests() <-chan struct{} {
	l.stopOnce.Do(func() {
		l.stop = make(chan struct{})
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-signals
			close(l.stop)
		}()
	})
	return l.stop
}

// AgentDialer reaches the appliance's agent over vsock (the Linux platform).
func (l *Lifecycle) AgentDialer() platform.AgentDialer {
	return agentDialer{}
}

// agentDialer is vsock (the Linux platform).
type agentDialer struct{}

func (agentDialer) Dial(context.Context, string, uint32) (net.Conn, error) {
	return nil, platform.ErrNotImplemented
}
