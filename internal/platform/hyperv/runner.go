package hyperv

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/webcodingcat/hlab/internal/platform"
)

// runner owns one long-running Windows PowerShell 5.1 process. Commands are
// written to its stdin as one line each; each answers one JSON document on
// a line starting with the process's marker and the command's sequence
// number. Calls are serialized; a dead process is replaced at the next call.
type runner struct {
	mu   sync.Mutex
	exe  string
	proc *process
	seq  uint64
}

type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string // lines carrying the marker; closed when stdout ends
	done   chan struct{} // closed by kill
	marker string
	stderr *tailBuffer
}

// psError is the error document a failed command answers. Only the type,
// HResult, category and error ID are interpreted; the message is localized
// and only passed on for logs.
type psError struct {
	Type     string `json:"type"`
	HResult  int64  `json:"hresult"`
	Category string `json:"category"`
	ID       string `json:"id"`
	Message  string `json:"message"`
}

type psAnswer struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
	Err  *psError        `json:"err"`
}

// run executes body with vars assigned as PowerShell variables of the same
// name and returns the JSON of the body's output.
func (r *runner) run(ctx context.Context, op string, vars map[string]any, body string) (json.RawMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.proc == nil {
		p, err := startProcess(r.exe)
		if err != nil {
			return nil, &platform.Error{Kind: platform.Failed, Op: op, Err: err}
		}
		r.proc = p
	}
	p := r.proc
	r.seq++
	prefix := p.marker + ":" + strconv.FormatUint(r.seq, 10) + ":"

	if _, err := io.WriteString(p.stdin, wrap(prefix, vars, body)); err != nil {
		r.kill()
		return nil, &platform.Error{Kind: platform.Failed, Op: op, Err: fmt.Errorf("powershell: write: %w", err)}
	}
	for {
		select {
		case <-ctx.Done():
			// The answer would arrive out of order; start afresh next time.
			r.kill()
			return nil, &platform.Error{Kind: platform.Failed, Op: op, Err: ctx.Err()}
		case line, ok := <-p.lines:
			if !ok {
				tail := p.stderr.String()
				r.kill()
				return nil, &platform.Error{Kind: platform.Failed, Op: op, Err: fmt.Errorf("powershell exited: %s", tail)}
			}
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			var a psAnswer
			if err := json.Unmarshal([]byte(line[len(prefix):]), &a); err != nil {
				return nil, &platform.Error{Kind: platform.Failed, Op: op, Err: fmt.Errorf("powershell: bad answer: %w", err)}
			}
			if !a.OK {
				if a.Err == nil {
					return nil, &platform.Error{Kind: platform.Failed, Op: op, Err: errors.New("powershell: error without details")}
				}
				return nil, &platform.Error{Kind: a.Err.kind(), Op: op, Err: a.Err}
			}
			return a.Data, nil
		}
	}
}

func (r *runner) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kill()
	return nil
}

// kill ends the current process; r.mu is held.
func (r *runner) kill() {
	if r.proc == nil {
		return
	}
	close(r.proc.done)
	_ = r.proc.stdin.Close()
	if r.proc.cmd.Process != nil {
		_ = r.proc.cmd.Process.Kill()
	}
	_ = r.proc.cmd.Wait()
	r.proc = nil
}

func startProcess(exe string) (*process, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "-NoProfile", "-NonInteractive", "-Command", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &process{
		cmd:    cmd,
		stdin:  stdin,
		lines:  make(chan string, 4),
		done:   make(chan struct{}),
		marker: "HLAB" + hex.EncodeToString(b[:]),
		stderr: &tailBuffer{max: 4096},
	}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("powershell: start: %w", err)
	}
	go func() {
		defer close(p.lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			if line := strings.TrimRight(sc.Text(), "\r"); strings.HasPrefix(line, p.marker) {
				select {
				case p.lines <- line:
				case <-p.done:
					return
				}
			}
		}
	}()
	return p, nil
}

// wrap builds the single line sent for one command. The body runs as a
// script block with errors terminating; success and failure both answer
// one compressed JSON document with every non-ASCII character escaped, so
// the console code page does not matter.
func wrap(prefix string, vars map[string]any, body string) string {
	var pre strings.Builder
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		pre.WriteString("$" + n + " = " + psValue(vars[n]) + "\n")
	}
	script := pre.String() + body
	return "& { $ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'; $WarningPreference = 'SilentlyContinue'; " +
		"try { $sb = [scriptblock]::Create(" + psQuote(script) + "); $r = & $sb; " +
		"$j = ConvertTo-Json -InputObject @{ok = $true; data = $r} -Compress -Depth 5 } " +
		"catch { $x = $_.Exception; $j = ConvertTo-Json -InputObject @{ok = $false; err = @{type = $x.GetType().FullName; hresult = $x.HResult; " +
		"category = $_.CategoryInfo.Category.ToString(); id = $_.FullyQualifiedErrorId; message = $x.Message}} -Compress -Depth 3 }; " +
		"$j = [regex]::Replace($j, '[^\\x00-\\x7F]', { param($c) '\\u{0:x4}' -f [int][char]$c.Value }); " +
		"[Console]::Out.WriteLine(" + psQuote(prefix) + " + $j) }\n"
}

// psValue renders a Go value as a PowerShell expression.
func psValue(v any) string {
	switch v := v.(type) {
	case string:
		return psQuote(v)
	case bool:
		if v {
			return "$true"
		}
		return "$false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	panic(fmt.Sprintf("hyperv: unsupported value type %T", v))
}

// psQuote renders s as a single-line PowerShell string expression: printable
// ASCII in single quotes, every other character as [char]N.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteString("('")
	for _, c := range s {
		switch {
		case c == '\'':
			b.WriteString("''")
		case c >= 0x20 && c < 0x7f:
			b.WriteRune(c)
		case c > 0xffff:
			// Surrogate pair for characters outside the BMP.
			c -= 0x10000
			fmt.Fprintf(&b, "'+[char]%d+[char]%d+'", 0xd800+(c>>10), 0xdc00+(c&0x3ff))
		default:
			fmt.Fprintf(&b, "'+[char]%d+'", c)
		}
	}
	b.WriteString("')")
	return b.String()
}

// HResults that mean the host lacks memory or disk space.
var noResourceHResults = map[uint32]bool{
	0x80070008: true, // ERROR_NOT_ENOUGH_MEMORY
	0x8007000E: true, // E_OUTOFMEMORY
	0x80070027: true, // ERROR_HANDLE_DISK_FULL
	0x80070070: true, // ERROR_DISK_FULL
	0x800705AA: true, // ERROR_NO_SYSTEM_RESOURCES
	0x800705AD: true, // ERROR_WORKING_SET_QUOTA
}

func (e *psError) kind() platform.ErrorKind {
	switch {
	case e.Type == "System.Management.Automation.ItemNotFoundException" || e.Category == "ObjectNotFound":
		return platform.NotFound
	case noResourceHResults[uint32(e.HResult)] ||
		e.Category == "ResourceUnavailable" || e.Category == "LimitsExceeded" || e.Category == "QuotaExceeded":
		return platform.NoResources
	case e.Type == "System.InvalidOperationException" || e.Category == "InvalidOperation" || e.Category == "ResourceBusy":
		return platform.InvalidState
	}
	return platform.Failed
}

func (e *psError) Error() string {
	return fmt.Sprintf("%s (HResult 0x%08X, category %s, id %s): %s", e.Type, uint32(e.HResult), e.Category, e.ID, e.Message)
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}
