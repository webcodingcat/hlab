package logfile_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

	t.Run("UI-06", func(t *testing.T) {
		t.Run("search", testSearch)
		t.Run("older", testBefore)
	})
}

// writeLogs writes files[0] as hlabd.log, files[1] as hlabd.log.1 and so
// on, each line followed by a newline.
func writeLogs(t *testing.T, dir string, files ...[]string) {
	t.Helper()
	for i, lines := range files {
		p := filepath.Join(dir, name)
		if i > 0 {
			p += fmt.Sprintf(".%d", i)
		}
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func testSearch(t *testing.T) {
	const (
		started = `{"time":"2026-11-20T10:00:00Z","level":"INFO","msg":"Backup started","project":"alpha"}`
		panicky = `panic: Backup failed in goroutine 7`
		tick    = `{"time":"2026-11-20T10:01:00Z","level":"DEBUG","msg":"backup tick"}`
		created = `{"time":"2026-11-20T10:02:00Z","level":"WARN","msg":"server created","project":"Demo-Web"}`
		failed  = `{"time":"2026-11-20T10:05:00Z","level":"ERROR","msg":"backup failed","error":"disk full"}`
		listen  = `{"time":"2026-11-20T10:06:00Z","level":"INFO","msg":"listening","addr":"127.0.0.1:8443"}`
	)
	dir := t.TempDir()
	writeLogs(t, dir,
		[]string{failed, listen},   // hlabd.log
		[]string{tick, created},    // hlabd.log.1
		[]string{started, panicky}, // hlabd.log.2
	)
	cases := []struct {
		name     string
		query    string
		minLevel string
		max      int
		want     []string
		capped   bool
	}{
		{"msg in any case across files newest first", "BACKUP", "", 100, []string{failed, tick, panicky, started}, false},
		{"field value", "demo-web", "", 100, []string{created}, false},
		{"field as key=value", "project=demo", "", 100, []string{created}, false},
		{"level", "warn", "", 100, []string{created}, false},
		{"time not searched", "10:05", "", 100, nil, false},
		{"minimum level; a line without a level passes", "backup", "WARN", 100, []string{failed, panicky}, false},
		{"exactly max matches", "backup", "", 4, []string{failed, tick, panicky, started}, false},
		{"more than max matches", "backup", "", 2, []string{failed, tick}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, capped, err := logfile.Search(dir, name, tc.query, tc.minLevel, tc.max)
			if err != nil {
				t.Fatalf("Search(%q, %q, %d): %v", tc.query, tc.minLevel, tc.max, err)
			}
			if !slices.Equal(got, tc.want) || capped != tc.capped {
				t.Errorf("Search(%q, %q, %d) = %q, capped %v; want %q, capped %v", tc.query, tc.minLevel, tc.max, got, capped, tc.want, tc.capped)
			}
		})
	}

	t.Run("rotated during the search", func(t *testing.T) {
		dir := t.TempDir()
		w := newWriter(t, dir)
		write(t, w, 0, 40)
		const total = 2000 // rotates every few lines
		done := make(chan error, 1)
		go func() {
			for i := 40; i < total; i++ {
				if _, err := w.Write([]byte(line(i))); err != nil {
					done <- fmt.Errorf("write line %d: %w", i, err)
					return
				}
			}
			done <- nil
		}()
		for running := true; running; {
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
				running = false
			default:
			}
			got, _, err := logfile.Search(dir, name, "LINE", "", 1000)
			if err != nil {
				t.Fatalf("Search while the log rotates: %v", err)
			}
			prev := total
			for _, l := range got {
				var n int
				if _, err := fmt.Sscanf(l, `{"msg":"line %d"`, &n); err != nil {
					t.Fatalf("Search returned %q, not a written line", l)
				}
				if n >= prev {
					t.Fatalf("Search while the log rotates returned line %d after line %d, want newest first without repeats", n, prev)
				}
				prev = n
			}
		}
	})
}

func testBefore(t *testing.T) {
	l := func(i int) string {
		return fmt.Sprintf(`{"time":"2026-11-20T10:%02d:00Z","level":"INFO","msg":"line %02d"}`, i, i)
	}
	ls := func(from, to int) []string {
		var out []string
		for i := from; i < to; i++ {
			out = append(out, l(i))
		}
		return out
	}
	dir := t.TempDir()
	writeLogs(t, dir, ls(10, 15), ls(5, 10), ls(0, 5)) // hlabd.log, .1, .2
	cases := []struct {
		name    string
		line    string
		n       int
		want    []string
		atStart bool
	}{
		{"within the current file", l(13), 2, ls(11, 13), false},
		{"across into rotated files", l(11), 7, ls(4, 11), false},
		{"start of the oldest file reached", l(3), 5, ls(0, 3), true},
		{"the first line", l(0), 5, nil, true},
		{"line not found", `{"msg":"never logged"}`, 5, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, atStart, err := logfile.Before(dir, name, tc.line, tc.n)
			if err != nil {
				t.Fatalf("Before(%q, %d): %v", tc.line, tc.n, err)
			}
			if !slices.Equal(got, tc.want) || atStart != tc.atStart {
				t.Errorf("Before(%q, %d) = %q, atStart %v; want %q, atStart %v", tc.line, tc.n, got, atStart, tc.want, tc.atStart)
			}
		})
	}

	t.Run("rotation between two calls", func(t *testing.T) {
		dir := t.TempDir()
		writeLogs(t, dir, ls(10, 15), ls(5, 10), ls(0, 5))
		first, _, err := logfile.Before(dir, name, l(12), 3)
		if err != nil || !slices.Equal(first, ls(9, 12)) {
			t.Fatalf("Before(line 12, 3) = %q, %v; want lines 9 to 11", first, err)
		}
		// Rotate as the writer does: name.2 -> name.3, name.1 -> name.2,
		// name -> name.1, then a new name.
		for i := 2; i >= 0; i-- {
			from := filepath.Join(dir, name)
			if i > 0 {
				from += fmt.Sprintf(".%d", i)
			}
			if err := os.Rename(from, filepath.Join(dir, fmt.Sprintf("%s.%d", name, i+1))); err != nil {
				t.Fatal(err)
			}
		}
		writeLogs(t, dir, ls(15, 18))
		got, atStart, err := logfile.Before(dir, name, first[0], 4)
		if err != nil {
			t.Fatalf("Before after a rotation: %v", err)
		}
		if !slices.Equal(got, ls(5, 9)) || atStart {
			t.Errorf("Before(line 9, 4) after a rotation = %q, atStart %v; want lines 5 to 8, atStart false", got, atStart)
		}
	})
}
