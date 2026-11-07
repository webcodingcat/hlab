package state

import (
	"log/slog"
	"os"
	"sync"
	"time"
)

// How long the writer waits before it tries a failed write again: the first
// wait, doubled after each failure up to the longest. On Windows, a virus
// scanner or the indexer may hold the file for a moment.
var (
	retryFirst   = time.Second
	retryLongest = 30 * time.Second
)

// Writer saves the state file in the background: the lab hands it each new
// content, and it writes only the newest, so that a burst of changes costs one
// write instead of many. A failed write is logged and tried again until it
// succeeds.
type Writer struct {
	path   string
	logger *slog.Logger
	wake   chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	written *sync.Cond
	latest  []byte
	queued  int   // contents handed over so far
	saved   int   // contents tried so far, on disk or failed
	err     error // of the last try
}

// NewWriter starts a writer for the file at path; it logs failed writes.
func NewWriter(path string, logger *slog.Logger) *Writer {
	w := &Writer{
		path:   path,
		logger: logger,
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	w.written = sync.NewCond(&w.mu)
	go w.run()
	return w
}

// Put hands over the newest content of the file; it does not wait.
func (w *Writer) Put(data []byte) {
	w.mu.Lock()
	w.latest = data
	w.queued++
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default: // a wake-up is pending already; it will write the newest content
	}
}

// Flush waits until the newest content has been tried once and returns the
// error of the last try; after a failure the writer keeps trying in the
// background.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.saved < w.queued {
		w.written.Wait()
	}
	return w.err
}

// Close writes what is left and stops the writer, also when that write failed
// (it returns the error then); Put must not follow.
func (w *Writer) Close() error {
	err := w.Flush()
	close(w.wake)
	<-w.done
	return err
}

// run writes the newest content after each wake-up, and again after a wait
// while the last write failed.
func (w *Writer) run() {
	defer close(w.done)
	var retry <-chan time.Time // nil while the last write succeeded
	wait := retryFirst
	for {
		select {
		case _, open := <-w.wake:
			if !open {
				return
			}
		case <-retry:
		}
		w.mu.Lock()
		data, queued := w.latest, w.queued
		w.mu.Unlock()
		err := writeFile(w.path, data)
		w.mu.Lock()
		w.saved, w.err = queued, err
		w.written.Broadcast()
		w.mu.Unlock()

		if err == nil {
			retry, wait = nil, retryFirst
			continue
		}
		w.logger.Error("saving the state failed; trying again", "path", w.path, "in", wait, "error", err)
		retry = time.After(wait)
		wait = min(2*wait, retryLongest)
	}
}

// writeFile replaces the file at path with data, so that the file is always
// either the old or the new content, also when the machine stops halfway: the
// data goes to path.tmp first, is synced, and then replaces the file.
func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return replaceFile(tmp, path)
}
