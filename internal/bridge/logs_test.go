package bridge_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/bridge"
)

// logStream is the GET /mgmt/v1/logs?follow=1 of the fake management API:
// it sends the last 1000 lines, then every added line, until the test
// breaks the stream.
type logStream struct {
	mu    sync.Mutex
	lines []string
	// breaks ends the open streams.
	breaks chan struct{}
	// connects counts the stream requests.
	connects atomic.Int32
}

func newLogStream() *logStream { return &logStream{breaks: make(chan struct{})} }

func (l *logStream) add(lines ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, lines...)
}

// breakStreams ends every open stream as a broken connection would.
func (l *logStream) breakStreams() {
	l.mu.Lock()
	defer l.mu.Unlock()
	close(l.breaks)
	l.breaks = make(chan struct{})
}

func (l *logStream) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("follow") != "1" {
		http.Error(w, "want follow=1", http.StatusBadRequest)
		return
	}
	l.connects.Add(1)
	l.mu.Lock()
	breaks := l.breaks
	l.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	sent := 0
	first := true
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		l.mu.Lock()
		pending := slices.Clone(l.lines[sent:])
		if first {
			from := max(0, len(l.lines)-1000)
			pending = slices.Clone(l.lines[from:])
			first = false
		}
		sent = len(l.lines)
		l.mu.Unlock()
		for _, line := range pending {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return
			}
		}
		if len(pending) > 0 && flusher != nil {
			flusher.Flush()
		}
		select {
		case <-breaks:
			return
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

// emitted records the batches Logs emits.
type emitted struct {
	mu      sync.Mutex
	batches [][]string
}

func (e *emitted) emit(lines []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.batches = append(e.batches, slices.Clone(lines))
}

func (e *emitted) lines() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, b := range e.batches {
		out = append(out, b...)
	}
	return out
}

func (e *emitted) largestBatch() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, b := range e.batches {
		n = max(n, len(b))
	}
	return n
}

func logLine(i int) string {
	return fmt.Sprintf(`{"time":"2026-11-19T10:00:%02dZ","level":"INFO","msg":"line %d"}`, i%60, i)
}

func logLines(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, logLine(i))
	}
	return out
}

func testLogs(t *testing.T) {
	stream := newLogStream()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mgmt/v1/logs", stream.handler)
	f := newFakeMgmt(t, mux)
	logDir := t.TempDir()
	logFile := filepath.Join(logDir, "hlabd.log")
	var running atomic.Bool
	running.Store(true)
	out := &emitted{}
	logs := bridge.NewLogs(bridge.LogsDeps{
		Client:   f.client(),
		LogDir:   logDir,
		Running:  running.Load,
		Emit:     out.emit,
		Interval: 20 * time.Millisecond,
		Batch:    10 * time.Millisecond,
	})
	t.Cleanup(func() { logs.Follow(false) })

	// exactly waits for the last line of want; a duplicate of an earlier
	// line would have been sent before it on the stream.
	exactly := func(what string, want []string) {
		t.Helper()
		last := want[len(want)-1]
		waitFor(t, what, func() bool { return slices.Contains(out.lines(), last) })
		if got := out.lines(); !slices.Equal(got, want) {
			t.Fatalf("%s: emitted\n%s\nwant\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}

	stream.add(logLines(1, 5)...)
	logs.Follow(true)
	exactly("the last lines", logLines(1, 5))
	if n := out.largestBatch(); n < 2 {
		t.Errorf("largest batch %d lines, want the last lines batched together", n)
	}

	stream.add(logLine(6))
	exactly("a new line", logLines(1, 6))

	connects := stream.connects.Load()
	stream.breakStreams()
	stream.add(logLine(7))
	waitFor(t, "reconnect after a broken stream", func() bool { return stream.connects.Load() > connects })
	stream.add(logLine(8))
	exactly("after the reconnect", logLines(1, 8))

	running.Store(false)
	f.down.Store(true)
	stream.breakStreams()
	file := strings.Join(logLines(1, 8), "\n") + "\n" + logLine(9) + "\n"
	if err := os.WriteFile(logFile, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a line of the file while stopped", func() bool { return slices.Contains(out.lines(), logLine(9)) })
	fh, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintln(fh, logLine(10))
	_ = fh.Close()
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a line added to the file while stopped", func() bool { return slices.Contains(out.lines(), logLine(10)) })

	stream.add(logLine(9), logLine(10))
	f.down.Store(false)
	running.Store(true)
	stream.add(logLine(11))
	waitFor(t, "a stream line after the start", func() bool { return slices.Contains(out.lines(), logLine(11)) })
	got := out.lines()
	if i, j := slices.Index(got, logLine(10)), slices.Index(got, logLine(11)); i > j {
		t.Errorf("stream line 11 emitted before file line 10: %v", got)
	}
}
