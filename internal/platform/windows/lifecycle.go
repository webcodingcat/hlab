//go:build windows

package windows

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/webcodingcat/hlab/internal/platform"
)

// MutexName is the machine-wide mutex that only one HLab holds; "Global\"
// makes it one for every session of the machine, not one per user.
const MutexName = `Global\HLab`

// tokenSDDL is the control token's ACL: full access for SYSTEM, the
// administrators and the file's owner, read for local users (the CLI runs
// without admin rights). "P" keeps the folder's ACL from adding to it.
const tokenSDDL = "D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;OW)(A;;FR;;;BU)"

// errorAlreadyExists is ERROR_ALREADY_EXISTS (winerror.h): CreateMutexW
// opened a mutex that another process holds.
const errorAlreadyExists = syscall.Errno(183)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	advapi32           = syscall.NewLazyDLL("advapi32.dll")
	createMutex        = kernel32.NewProc("CreateMutexW")
	localFree          = kernel32.NewProc("LocalFree")
	stringToDescriptor = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)

// InstanceFile is where the running HLab names its data folder, for the
// message of a second start: the mutex cannot carry it.
func InstanceFile() string {
	return filepath.Join(os.Getenv("ProgramData"), "HLab", "instance")
}

// Lifecycle is the process's life cycle on Windows.
type Lifecycle struct {
	mutexName    string
	instanceFile string
	dialer       platform.AgentDialer
	stopOnce     sync.Once
	stop         chan struct{}
}

// NewLifecycle returns the life cycle with its mutex and instance file
// (MutexName and InstanceFile; tests take their own) and the dialer of the
// agents (NewAgentDialer).
func NewLifecycle(mutexName, instanceFile string, dialer platform.AgentDialer) *Lifecycle {
	return &Lifecycle{mutexName: mutexName, instanceFile: instanceFile, dialer: dialer}
}

// Lock makes this process the only HLab on the machine: it holds the named
// mutex, which Windows closes when the process ends, also when it crashes.
func (l *Lifecycle) Lock(dataDir string) (func(), error) {
	name, err := syscall.UTF16PtrFromString(l.mutexName)
	if err != nil {
		return nil, err
	}
	handle, _, callErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, fmt.Errorf("creating the mutex %s: %w", l.mutexName, callErr)
	}
	if errors.Is(callErr, errorAlreadyExists) {
		_ = syscall.CloseHandle(syscall.Handle(handle))
		holder, _ := os.ReadFile(l.instanceFile)
		return nil, fmt.Errorf("HLab runs already with the data folder %s: %w", strings.TrimSpace(string(holder)), platform.ErrLocked)
	}
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		absolute = dataDir
	}
	// only for the message above: a folder that cannot be written loses
	// just that
	if os.MkdirAll(filepath.Dir(l.instanceFile), 0o755) == nil {
		_ = os.WriteFile(l.instanceFile, []byte(absolute+"\r\n"), 0o644)
	}
	unlock := func() { _ = syscall.CloseHandle(syscall.Handle(handle)) }
	return unlock, nil
}

// WriteSecret writes a secret file with its own ACL (tokenSDDL), so that
// local users may read it whatever the data folder allows. The file is
// created with the ACL and then renamed over the old one: a rename keeps a
// file's ACL, and a reader never sees half a token.
func (l *Lifecycle) WriteSecret(path string, token []byte) error {
	descriptor, err := securityDescriptor(tokenSDDL)
	if err != nil {
		return err
	}
	defer localFree.Call(descriptor) //nolint:errcheck // freeing memory does not fail
	temporary := path + ".tmp"
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	name, err := syscall.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	attributes := &syscall.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	handle, err := syscall.CreateFile(name, syscall.GENERIC_WRITE, 0, attributes,
		syscall.CREATE_NEW, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return fmt.Errorf("creating %s: %w", temporary, err)
	}
	file := os.NewFile(uintptr(handle), temporary)
	if _, err := file.Write(token); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// securityDescriptor turns an SDDL string into a self-relative security
// descriptor, which the caller frees with LocalFree.
func securityDescriptor(sddl string) (uintptr, error) {
	text, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		return 0, err
	}
	const sddlRevision1 = 1
	var descriptor uintptr
	ok, _, callErr := stringToDescriptor.Call(uintptr(unsafe.Pointer(text)), sddlRevision1,
		uintptr(unsafe.Pointer(&descriptor)), 0)
	if ok == 0 {
		return 0, fmt.Errorf("the ACL %s: %w", sddl, callErr)
	}
	return descriptor, nil
}

// StopRequests is closed on Ctrl+C or when the console closes; signing out
// and the tray follow with the tray (phase 6b).
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

// AgentDialer reaches the appliance's agent over hv_sock.
func (l *Lifecycle) AgentDialer() platform.AgentDialer {
	return l.dialer
}
