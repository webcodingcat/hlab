//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"unicode/utf16"
	"unsafe"

	xwin "golang.org/x/sys/windows"
)

// ErrNoConsoleUser reports that there is no interactive user (see
// interactiveSession), so commands in the user's context cannot run. The
// name is kept from when only the console user counted.
var ErrNoConsoleUser = errors.New("hostnet: no interactive user logged on")

// errNoUser is the package's short name for ErrNoConsoleUser.
var errNoUser = ErrNoConsoleUser

// noConsoleSession is what WTSGetActiveConsoleSessionId returns when no
// session is attached to the console.
const noConsoleSession = 0xFFFFFFFF

// interactiveSession chooses the interactive user's session: the console
// session when a user is logged on there, else the single active session
// with a user (a Remote Desktop session, such as vmconnect's enhanced
// session). With nobody at the console and none or several active sessions
// with a user it returns errNoUser.
func interactiveSession(console uint32, sessions []xwin.WTS_SESSION_INFO, hasUser func(id uint32) bool) (uint32, error) {
	if console != noConsoleSession && hasUser(console) {
		return console, nil
	}
	var found []uint32
	for _, s := range sessions {
		if s.SessionID != console && s.State == xwin.WTSActive && hasUser(s.SessionID) {
			found = append(found, s.SessionID)
		}
	}
	if len(found) != 1 {
		return 0, errNoUser
	}
	return found[0], nil
}

// interactiveUserToken returns the token of the interactive user, or
// errNoUser when there is none or the caller lacks the rights of
// LocalSystem to obtain it.
func interactiveUserToken() (xwin.Token, error) {
	var list *xwin.WTS_SESSION_INFO
	var count uint32
	var sessions []xwin.WTS_SESSION_INFO
	if err := xwin.WTSEnumerateSessions(0, 0, 1, &list, &count); err == nil {
		sessions = slices.Clone(unsafe.Slice(list, count))
		xwin.WTSFreeMemory(uintptr(unsafe.Pointer(list)))
	}
	hasUser := func(id uint32) bool {
		var t xwin.Token
		if xwin.WTSQueryUserToken(id, &t) != nil {
			return false
		}
		_ = t.Close()
		return true
	}
	session, err := interactiveSession(xwin.WTSGetActiveConsoleSessionId(), sessions, hasUser)
	if err != nil {
		return 0, err
	}
	var token xwin.Token
	if err := xwin.WTSQueryUserToken(session, &token); err != nil {
		return 0, errNoUser
	}
	return token, nil
}

// UserRunner runs name with args in the interactive user's context, writes
// stdin to its standard input and returns its standard output and exit
// code. It returns errNoUser when there is no interactive user.
type UserRunner func(ctx context.Context, stdin []byte, name string, args ...string) (stdout []byte, exitCode int, err error)

// spawnMu keeps the inheritable pipe ends of one child from being
// inherited by another child started at the same time.
var spawnMu sync.Mutex

// userRunner starts name as the interactive user (WTSQueryUserToken,
// CreateProcessAsUser) on desktop winsta0\default without a window, with
// the user's environment plus WSL_UTF8=1. On a non-zero exit code the
// standard error output is appended to the returned output.
func userRunner(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, int, error) {
	token, err := interactiveUserToken()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = token.Close() }()

	env, err := token.Environ(false)
	if err != nil {
		return nil, 0, fmt.Errorf("user environment: %w", err)
	}
	block := envBlock(append(env, "WSL_UTF8=1"))

	inW, outR, errR, proc, err := spawnAsUser(token, xwin.ComposeCommandLine(append([]string{name}, args...)), block)
	if err != nil {
		return nil, 0, fmt.Errorf("start %s as the interactive user: %w", name, err)
	}
	defer func() { _ = xwin.CloseHandle(proc) }()

	stdinFile := os.NewFile(uintptr(inW), "stdin")
	stdoutFile := os.NewFile(uintptr(outR), "stdout")
	stderrFile := os.NewFile(uintptr(errR), "stderr")
	defer func() { _ = stdoutFile.Close(); _ = stderrFile.Close() }()

	go func() {
		_, _ = stdinFile.Write(stdin)
		_ = stdinFile.Close()
	}()
	var stdout, stderr []byte
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); stdout, _ = io.ReadAll(stdoutFile) }()
	go func() { defer wg.Done(); stderr, _ = io.ReadAll(stderrFile) }()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = xwin.TerminateProcess(proc, 1)
		case <-done:
		}
	}()
	_, err = xwin.WaitForSingleObject(proc, xwin.INFINITE)
	close(done)
	if err != nil {
		return nil, 0, fmt.Errorf("wait for %s: %w", name, err)
	}
	var code uint32
	if err := xwin.GetExitCodeProcess(proc, &code); err != nil {
		return nil, 0, fmt.Errorf("exit code of %s: %w", name, err)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	exit := int(int32(code))
	if exit != 0 {
		stdout = append(stdout, stderr...)
	}
	return stdout, exit, nil
}

// spawnAsUser creates the child with pipes for its standard handles and
// returns the parent's ends and the process handle.
func spawnAsUser(token xwin.Token, cmdline string, env []uint16) (inW, outR, errR, proc xwin.Handle, err error) {
	spawnMu.Lock()
	defer spawnMu.Unlock()

	sa := &xwin.SecurityAttributes{InheritHandle: 1}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	var inR, outW, errW xwin.Handle
	closeAll := func(hs ...xwin.Handle) {
		for _, h := range hs {
			if h != 0 {
				_ = xwin.CloseHandle(h)
			}
		}
	}
	if err = xwin.CreatePipe(&inR, &inW, sa, 0); err != nil {
		return 0, 0, 0, 0, err
	}
	if err = xwin.CreatePipe(&outR, &outW, sa, 0); err != nil {
		closeAll(inR, inW)
		return 0, 0, 0, 0, err
	}
	if err = xwin.CreatePipe(&errR, &errW, sa, 0); err != nil {
		closeAll(inR, inW, outR, outW)
		return 0, 0, 0, 0, err
	}
	// The parent's ends stay in the parent.
	for _, h := range []xwin.Handle{inW, outR, errR} {
		if err = xwin.SetHandleInformation(h, xwin.HANDLE_FLAG_INHERIT, 0); err != nil {
			closeAll(inR, inW, outR, outW, errR, errW)
			return 0, 0, 0, 0, err
		}
	}

	si := xwin.StartupInfo{
		Desktop:    xwin.StringToUTF16Ptr(`winsta0\default`),
		Flags:      xwin.STARTF_USESTDHANDLES | xwin.STARTF_USESHOWWINDOW,
		ShowWindow: xwin.SW_HIDE,
		StdInput:   inR,
		StdOutput:  outW,
		StdErr:     errW,
	}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi xwin.ProcessInformation
	err = xwin.CreateProcessAsUser(token, nil, xwin.StringToUTF16Ptr(cmdline), nil, nil, true,
		xwin.CREATE_NO_WINDOW|xwin.CREATE_UNICODE_ENVIRONMENT, &env[0], nil, &si, &pi)
	closeAll(inR, outW, errW)
	if err != nil {
		closeAll(inW, outR, errR)
		return 0, 0, 0, 0, err
	}
	_ = xwin.CloseHandle(pi.Thread)
	return inW, outR, errR, pi.Process, nil
}

// envBlock encodes env as a Unicode environment block.
func envBlock(env []string) []uint16 {
	var b []uint16
	for _, kv := range env {
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	return append(b, 0)
}
