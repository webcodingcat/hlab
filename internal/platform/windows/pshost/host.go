//go:build windows

// Package pshost runs PowerShell commands in long-running powershell.exe
// processes and talks to them in JSON over stdin and stdout. Starting
// powershell.exe and loading a module such as Hyper-V takes most of a second;
// a command in a running process takes milliseconds.
//
// The caller gives a script that returns a hashtable from command names to
// script blocks (see host.ps1). Call runs one command with its arguments and
// decodes its result. Commands of one process run one after another; a Host
// keeps a few processes so that a slow command does not hold up the others.
//
// A process that ends on its own, or whose call is canceled, is killed and
// left behind; the next call starts a new one. A canceled call does not
// undo what the command did so far.
package pshost

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

//go:embed host.ps1
var loop string

// marker starts every answer of the loop, to tell it from stray output.
const marker = '\x1e'

// maxAnswer is the largest answer the loop may send, in bytes.
const maxAnswer = 16 << 20

// stopWait is how long Close lets a process end by itself before killing it.
const stopWait = 5 * time.Second

// createNoWindow keeps powershell.exe from opening a console window when
// hlabd runs without one (CREATE_NO_WINDOW).
const createNoWindow = 0x08000000

// ErrCrashed means the PowerShell process ended in the middle of a call.
var ErrCrashed = errors.New("the PowerShell process ended")

// ErrClosed means the Host was closed.
var ErrClosed = errors.New("the PowerShell host is closed")

// Error is a command that failed. Kind is what the command reported, such as
// "not_found" for `throw "not_found: VM web"`; it is "" for the errors of
// cmdlets.
type Error struct {
	Command string
	Kind    string
	Message string
}

func (e *Error) Error() string {
	if e.Kind == "" {
		return fmt.Sprintf("%s: %s", e.Command, e.Message)
	}
	return fmt.Sprintf("%s: %s: %s", e.Command, e.Kind, e.Message)
}

// Options tune a Host.
type Options struct {
	// Processes is the most processes that run at once; 0 means 4.
	Processes int
	// Logger receives stray output of the processes; nil discards it.
	Logger *slog.Logger
}

// Host runs commands in a pool of PowerShell processes. Processes start on
// the first call that finds none idle.
type Host struct {
	script string
	logger *slog.Logger
	slots  chan struct{} // one per running process
	idle   chan *process // processes that wait for a call
	mu     sync.Mutex    // guards closed
	closed bool
}

// New returns a Host for the commands of script; it starts no process yet.
func New(script string, opts Options) *Host {
	if opts.Processes <= 0 {
		opts.Processes = 4
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Host{
		script: script,
		logger: opts.Logger,
		slots:  make(chan struct{}, opts.Processes),
		idle:   make(chan *process, opts.Processes),
	}
}

// Call runs a command with args (anything that encodes to JSON) and decodes
// its result into result, unless result is nil. A failed command returns an
// *Error; a process that ended returns ErrCrashed with the end of its error
// output.
func (h *Host) Call(ctx context.Context, command string, args, result any) error {
	proc, err := h.take(ctx)
	if err != nil {
		return err
	}
	answer, err := proc.call(ctx, command, args)
	h.give(proc, err == nil)
	if err != nil {
		return err
	}
	if !answer.OK {
		return &Error{Command: command, Kind: answer.Kind, Message: answer.Message}
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(answer.Result, result); err != nil {
		return fmt.Errorf("%s: decoding the result %s: %w", command, answer.Result, err)
	}
	return nil
}

// take returns an idle process, or starts one if fewer than the most run.
func (h *Host) take(ctx context.Context) (*process, error) {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	select {
	case proc := <-h.idle:
		return proc, nil
	default:
	}
	select {
	case proc := <-h.idle:
		return proc, nil
	case h.slots <- struct{}{}:
		proc, err := h.start(ctx)
		if err != nil {
			<-h.slots
			return nil, err
		}
		return proc, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// give takes a process back after a call. A process that failed its call is
// killed; so is any process after Close.
func (h *Host) give(proc *process, healthy bool) {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	switch {
	case !healthy:
		proc.kill()
		<-h.slots
	case closed:
		proc.stop()
		<-h.slots
	default:
		h.idle <- proc
	}
}

// Close stops the idle processes and those that finish their call; it waits
// for the idle ones. Calls after Close return ErrClosed.
func (h *Host) Close() error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	for {
		select {
		case proc := <-h.idle:
			proc.stop()
			<-h.slots
		default:
			return nil
		}
	}
}

// process is one powershell.exe that runs the loop.
type process struct {
	command string // the process's own name for logs
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	answers chan answer // closed when stdout ends
	stderr  *tail
	done    chan struct{} // closed when the process has been waited for
	nextID  int64
}

// answer is one line of the loop.
type answer struct {
	ID      int64           `json:"id"`
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result"`
	Kind    string          `json:"kind"`
	Message string          `json:"message"`
}

// request is one call to the loop.
type request struct {
	ID      int64  `json:"id"`
	Command string `json:"command"`
	Args    any    `json:"args"`
}

// program is Windows PowerShell 5.1, which every Windows 11 has and which
// loads the Hyper-V module natively; its full path keeps PATH out of it.
func program() string {
	return filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
}

// encodeCommand encodes a script for -EncodedCommand: base64 of UTF-16LE.
func encodeCommand(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 2*len(units))
	for i, unit := range units {
		raw[2*i] = byte(unit)
		raw[2*i+1] = byte(unit >> 8)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// start starts a process, sends it the script and waits until it is ready.
func (h *Host) start(ctx context.Context) (*process, error) {
	cmd := exec.Command(program(), "-NoLogo", "-NoProfile", "-NonInteractive",
		"-EncodedCommand", encodeCommand(loop))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	proc := &process{
		cmd:     cmd,
		stdin:   stdin,
		answers: make(chan answer),
		stderr:  &tail{},
		done:    make(chan struct{}),
	}
	cmd.Stderr = proc.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting PowerShell: %w", err)
	}
	proc.command = fmt.Sprintf("powershell.exe (pid %d)", cmd.Process.Pid)
	go proc.read(stdout, h.logger)

	script, err := json.Marshal(h.script)
	if err != nil {
		return nil, err
	}
	ready, err := proc.exchange(ctx, 0, script)
	if err == nil && !ready.OK {
		err = fmt.Errorf("loading the PowerShell script: %s", ready.Message)
	}
	if err != nil {
		proc.kill()
		return nil, err
	}
	return proc, nil
}

// read passes the answers on stdout to the answers channel and logs anything
// else; it closes the channel, waits for the process and closes done when
// stdout ends.
func (p *process) read(stdout io.Reader, logger *slog.Logger) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxAnswer)
	for scanner.Scan() {
		line := bytes.TrimRight(scanner.Bytes(), "\r")
		if len(line) == 0 || line[0] != marker {
			logger.Debug("stray PowerShell output", "process", p.command, "line", string(line))
			continue
		}
		var got answer
		if err := json.Unmarshal(line[1:], &got); err != nil {
			logger.Warn("broken PowerShell answer", "process", p.command, "error", err)
			continue
		}
		p.answers <- got
	}
	close(p.answers)
	_ = p.cmd.Wait()
	close(p.done)
}

// call sends one command and waits for its answer.
func (p *process) call(ctx context.Context, command string, args any) (answer, error) {
	p.nextID++
	line, err := json.Marshal(request{ID: p.nextID, Command: command, Args: args})
	if err != nil {
		return answer{}, fmt.Errorf("%s: encoding the arguments: %w", command, err)
	}
	return p.exchange(ctx, p.nextID, line)
}

// exchange writes a line and waits for the answer with the given ID.
func (p *process) exchange(ctx context.Context, id int64, line []byte) (answer, error) {
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		return answer{}, p.crashed()
	}
	for {
		select {
		case got, open := <-p.answers:
			if !open {
				return answer{}, p.crashed()
			}
			if got.ID == id {
				return got, nil
			}
		case <-ctx.Done():
			return answer{}, ctx.Err()
		}
	}
}

// crashed waits for the process to end and describes how it ended.
func (p *process) crashed() error {
	select {
	case <-p.done:
	case <-time.After(stopWait):
	}
	return fmt.Errorf("%w: %s; error output: %s", ErrCrashed, p.command, p.stderr.String())
}

// stop closes stdin, so the loop ends; after stopWait it kills the process.
func (p *process) stop() {
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(stopWait):
		p.kill()
	}
}

// kill ends the process at once and drains its answers.
func (p *process) kill() {
	_ = p.cmd.Process.Kill()
	_ = p.stdin.Close()
	go func() {
		for range p.answers { //nolint:revive // drain until the reader stops
		}
	}()
}

// tail keeps the last bytes written to it: the end of a process's error
// output, for ErrCrashed.
type tail struct {
	mu   sync.Mutex
	data []byte
}

// tailSize is how many bytes of error output a tail keeps.
const tailSize = 2048

func (t *tail) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, data...)
	if len(t.data) > tailSize {
		t.data = t.data[len(t.data)-tailSize:]
	}
	return len(data), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.data))
}
