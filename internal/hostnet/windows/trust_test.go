//go:build windows

package windows

import (
	"bytes"
	"context"
	"encoding/binary"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
)

// wslCall is one command the fake user runner received.
type wslCall struct {
	args  []string
	stdin []byte
}

// distro returns the distro a `wsl.exe -d <name> ...` call targets, or "".
func (c wslCall) distro() string {
	if i := slices.Index(c.args, "-d"); i >= 0 && i+1 < len(c.args) {
		return c.args[i+1]
	}
	return ""
}

func (c wslCall) asRoot() bool {
	i := slices.Index(c.args, "-u")
	return i >= 0 && i+1 < len(c.args) && c.args[i+1] == "root"
}

func (c wslCall) line() string { return strings.Join(c.args, " ") }

// fakeWSL is the injected runner for commands in the console user's
// context. It answers the distro list with list, and every distro command
// with the exit code in exit (default 0). noUser makes every call fail
// with errNoUser.
type fakeWSL struct {
	mu     sync.Mutex
	list   []byte
	exit   map[string]int
	noUser bool
	calls  []wslCall
}

func (f *fakeWSL) run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noUser {
		return nil, 0, errNoUser
	}
	if !strings.EqualFold(name, "wsl.exe") && !strings.EqualFold(name, "wsl") {
		return nil, 1, nil
	}
	c := wslCall{args: slices.Clone(args), stdin: slices.Clone(stdin)}
	f.calls = append(f.calls, c)
	if slices.Contains(args, "--list") || slices.Contains(args, "-l") {
		return f.list, 0, nil
	}
	return nil, f.exit[c.distro()], nil
}

// take returns the distro commands (not the list calls) since the last take.
func (f *fakeWSL) take() []wslCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []wslCall
	for _, c := range f.calls {
		if c.distro() != "" {
			out = append(out, c)
		}
	}
	f.calls = nil
	return out
}

func (f *fakeWSL) set(fn func(f *fakeWSL)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// utf16LE encodes s as UTF-16LE with a byte order mark, as wsl.exe writes
// its list without WSL_UTF8.
func utf16LE(s string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xFE})
	for _, u := range utf16.Encode([]rune(s)) {
		_ = binary.Write(&b, binary.LittleEndian, u)
	}
	return b.Bytes()
}

func distros(calls []wslCall) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.distro())
	}
	slices.Sort(out)
	return out
}

const testCAPEM = "-----BEGIN CERTIFICATE-----\nTUlJQ2FDQQ==\n-----END CERTIFICATE-----\n"

func TestHostNet(t *testing.T) {
	t.Run("CERT-04", func(t *testing.T) {
		ctx := context.Background()
		fake := &fakeWSL{exit: map[string]int{}}
		h := New(Options{
			Run:                  func(context.Context, string) ([]byte, error) { return nil, nil },
			OtherInstanceRunning: func(context.Context) (bool, error) { return false, nil },
			WSLConfig:            func(context.Context) ([]byte, bool, error) { return nil, false, nil },
			StateDir:             t.TempDir(),
			Program:              `C:\hlab\bin\hlabd.exe`,
			RunAsUser:            fake.run,
		})

		// Listing in UTF-16LE with BOM; docker-desktop distros are skipped.
		// On writes the PEM to each distro's install command as root.
		fake.set(func(f *fakeWSL) {
			f.list = utf16LE("Ubuntu\r\ndocker-desktop\r\ndocker-desktop-data\r\nDebian\r\n")
		})
		if err := h.trustWSL(ctx, []byte(testCAPEM), true); err != nil {
			t.Fatalf("trustWSL on: %v", err)
		}
		calls := fake.take()
		if got, want := distros(calls), []string{"Debian", "Ubuntu"}; !slices.Equal(got, want) {
			t.Fatalf("trustWSL on with a UTF-16LE list: commands for distros %v, want %v", got, want)
		}
		for _, c := range calls {
			if !c.asRoot() {
				t.Errorf("install command %q does not run as root", c.line())
			}
			if !strings.Contains(c.line(), "update-ca-certificates") || !strings.Contains(c.line(), "cat >") {
				t.Errorf("install command %q does not write the CA file and run update-ca-certificates", c.line())
			}
			if string(c.stdin) != testCAPEM {
				t.Errorf("install command for %s got stdin %q, want the CA PEM", c.distro(), c.stdin)
			}
		}

		// Listing in UTF-8; off runs the removal command.
		fake.set(func(f *fakeWSL) { f.list = []byte("Ubuntu\ndocker-desktop\n") })
		if err := h.trustWSL(ctx, []byte(testCAPEM), false); err != nil {
			t.Fatalf("trustWSL off: %v", err)
		}
		calls = fake.take()
		if got, want := distros(calls), []string{"Ubuntu"}; !slices.Equal(got, want) {
			t.Fatalf("trustWSL off with a UTF-8 list: commands for distros %v, want %v", got, want)
		}
		if c := calls[0]; !c.asRoot() || !strings.Contains(c.line(), "rm -f") || !strings.Contains(c.line(), "--fresh") {
			t.Errorf("removal command %q, want rm -f of the CA file and update-ca-certificates --fresh as root", c.line())
		}

		// A distro without update-ca-certificates (exit code 3) is skipped
		// and the others still run.
		fake.set(func(f *fakeWSL) {
			f.list = []byte("Alpine\nUbuntu\nDebian\n")
			f.exit["Alpine"] = 3
		})
		if err := h.trustWSL(ctx, []byte(testCAPEM), true); err != nil {
			t.Errorf("trustWSL on with a distro answering exit code 3: %v, want it skipped", err)
		}
		if got, want := distros(fake.take()), []string{"Alpine", "Debian", "Ubuntu"}; !slices.Equal(got, want) {
			t.Errorf("trustWSL on with a distro answering exit code 3: commands for %v, want %v", got, want)
		}

		// Without a logged-on user nothing runs and the change stays
		// pending; the next WSL change applies it.
		fake.set(func(f *fakeWSL) {
			f.list = []byte("Ubuntu\n")
			f.exit = map[string]int{}
			f.noUser = true
		})
		_ = h.trustWSL(ctx, []byte(testCAPEM), false)
		if got := fake.take(); len(got) != 0 {
			t.Errorf("trustWSL without a logged-on user ran %d distro commands, want none", len(got))
		}
		fake.set(func(f *fakeWSL) { f.noUser = false })
		h.wslChanged(ctx)
		calls = fake.take()
		if got, want := distros(calls), []string{"Ubuntu"}; !slices.Equal(got, want) {
			t.Fatalf("WSL change after a pending off: commands for %v, want %v", got, want)
		}
		if !strings.Contains(calls[0].line(), "rm -f") {
			t.Errorf("WSL change after a pending off ran %q, want the removal command", calls[0].line())
		}
		h.wslChanged(ctx)
		if got := fake.take(); len(got) != 0 {
			t.Errorf("a second WSL change ran %d distro commands, want none once the pending change is applied", len(got))
		}
	})
}
