package logfile_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/logfile"
)

const (
	name     = "hlabd.log"
	maxBytes = 200
	keep     = 5
)

// line returns the i-th test line with its newline; lines differ in length
// so that rotation points fall in the middle of a line.
func line(i int) string {
	return fmt.Sprintf(`{"msg":"line %04d","pad":"%s"}`, i, strings.Repeat("x", i%17)) + "\n"
}

func newWriter(t *testing.T, dir string) *logfile.Writer {
	t.Helper()
	w, err := logfile.NewWriter(dir, name, maxBytes, keep)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func write(t *testing.T, w *logfile.Writer, from, to int) {
	t.Helper()
	for i := from; i < to; i++ {
		if _, err := w.Write([]byte(line(i))); err != nil {
			t.Fatalf("Write line %d: %v", i, err)
		}
	}
}

func trim(b []byte) string { return string(bytes.TrimRight(b, "\r\n")) }

func TestLogfile(t *testing.T) {
	t.Run("LIFE-09", func(t *testing.T) {
		t.Run("rotate", func(t *testing.T) {
			dir := t.TempDir()
			w := newWriter(t, dir)
			const total = 100 // far more than keep+1 files hold
			write(t, w, 0, total)

			// Oldest first: name.5 .. name.1, name.
			var all []string
			for i := keep; i >= 0; i-- {
				p := filepath.Join(dir, name)
				if i > 0 {
					p += fmt.Sprintf(".%d", i)
				}
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("read %s: %v", p, err)
				}
				if len(data) > maxBytes {
					t.Errorf("%s has %d bytes, limit %d", p, len(data), maxBytes)
				}
				if i > 0 && len(data) == 0 {
					t.Errorf("rotated file %s is empty", p)
				}
				if len(data) > 0 && data[len(data)-1] != '\n' {
					t.Errorf("%s ends inside a line: %q", p, data)
				}
				for _, l := range strings.SplitAfter(string(data), "\n") {
					if l != "" {
						all = append(all, l)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%s.%d", name, keep+1))); !os.IsNotExist(err) {
				t.Errorf("%s.%d exists (%v), want at most %d rotated files", name, keep+1, err, keep)
			}

			// The files hold whole lines, a gapless run ending with the
			// last line written; the oldest lines are dropped.
			if len(all) == 0 || len(all) >= total {
				t.Fatalf("files hold %d lines of %d written, want the newest only", len(all), total)
			}
			first := total - len(all)
			for k, l := range all {
				if want := line(first + k); l != want {
					t.Fatalf("line %d of the files = %q, want %q", k, l, want)
				}
			}
		})

		t.Run("follow", func(t *testing.T) {
			dir := t.TempDir()
			w := newWriter(t, dir)
			const written = 40 // spread over several rotated files
			write(t, w, 0, written)

			// Last n lines over the rotated files, oldest first.
			const n = 12
			var got []string
			err := logfile.Tail(context.Background(), dir, name, n, false, func(l []byte) error {
				got = append(got, trim(l))
				return nil
			})
			if err != nil {
				t.Fatalf("Tail: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, name+".2")); err != nil {
				t.Fatalf("test setup: no second rotated file: %v", err)
			}
			if len(got) != n {
				t.Fatalf("Tail returned %d lines, want %d: %q", len(got), n, got)
			}
			for k, l := range got {
				if want := trim([]byte(line(written - n + k))); l != want {
					t.Errorf("Tail line %d = %q, want %q", k, l, want)
				}
			}

			// Follow: the last 2 lines, then lines written later, across a
			// rotation, until the context ends.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lines := make(chan string, 100)
			done := make(chan error, 1)
			go func() {
				done <- logfile.Tail(ctx, dir, name, 2, true, func(l []byte) error {
					lines <- trim(l)
					return nil
				})
			}()
			deadline := time.After(10 * time.Second)
			expect := func(i int) {
				t.Helper()
				want := trim([]byte(line(i)))
				select {
				case l := <-lines:
					if l != want {
						t.Fatalf("followed line = %q, want %q", l, want)
					}
				case err := <-done:
					t.Fatalf("Tail ended before line %d: %v", i, err)
				case <-deadline:
					t.Fatalf("no line %q within the deadline", want)
				}
			}
			expect(written - 2)
			expect(written - 1)
			const later = 20 // more than one file holds: rotates while following
			for i := written; i < written+later; i++ {
				write(t, w, i, i+1)
				expect(i)
			}

			cancel()
			select {
			case err := <-done:
				if err != nil && err != context.Canceled {
					t.Errorf("Tail after cancel: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Tail does not return after the context is cancelled")
			}
		})
	})
}
