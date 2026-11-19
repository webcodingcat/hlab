// Package logfile writes the service log with size-based rotation and reads
// its last lines, optionally following new lines across rotations.
package logfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// MaxBytes is the size at which hlabd.log rotates.
	MaxBytes = 10 << 20
	// Keep is the number of rotated files kept (name.1 .. name.Keep).
	Keep = 5
)

// pollInterval is how often Tail looks for new lines while following.
var pollInterval = 100 * time.Millisecond

// Writer appends to dir/name and rotates it to name.1 .. name.<keep> before
// a write would take it past maxBytes. Each Write stays whole in one file.
// It is safe for concurrent use.
type Writer struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
}

// NewWriter opens (creating dir and file if needed) dir/name for appending.
func NewWriter(dir, name string, maxBytes int64, keep int) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	w := &Writer{path: filepath.Join(dir, name), maxBytes: maxBytes, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

// Write appends p, rotating first when p would not fit any more.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the current file, shifts name.i to name.i+1 (the oldest is
// replaced) and name to name.1, and opens a new name. When the renames fail
// (a file held by another program), it goes on with the current file.
func (w *Writer) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	w.f = nil
	if w.keep > 0 {
		_ = os.Remove(rotated(w.path, w.keep))
		for i := w.keep - 1; i >= 1; i-- {
			_ = os.Rename(rotated(w.path, i), rotated(w.path, i+1))
		}
		_ = os.Rename(w.path, rotated(w.path, 1))
	} else {
		_ = os.Remove(w.path)
	}
	return w.open()
}

// Close closes the current file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

func rotated(path string, i int) string { return fmt.Sprintf("%s.%d", path, i) }

// Tail emits the last n lines over the rotated files and the current one,
// oldest first; with follow it then emits new lines across rotations until
// ctx ends. Lines are passed with their newline; an unfinished last line is
// left for a later poll.
func Tail(ctx context.Context, dir, name string, n int, follow bool, emit func(line []byte) error) error {
	path := filepath.Join(dir, name)

	// The current file first, so following starts where this read ends.
	cur, data, err := readFrom(path, 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	offset := int64(len(data))
	lines := splitLines(data)
	for i := 1; len(lines) < n; i++ {
		_, old, err := readFrom(rotated(path, i), 0)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		lines = append(splitLines(old), lines...)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if err := emitAll(lines, emit); err != nil {
		return err
	}
	if !follow {
		return nil
	}

	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		st, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if cur != nil && !os.SameFile(cur, st) {
			// Rotated: finish the old file, then any file rotated after it.
			k := 0
			for i := 1; ; i++ {
				ri, err := os.Stat(rotated(path, i))
				if err != nil {
					break
				}
				if os.SameFile(cur, ri) {
					k = i
					break
				}
			}
			if k > 0 {
				if _, rest, err := readFrom(rotated(path, k), offset); err == nil {
					if err := emitAll(splitLines(rest), emit); err != nil {
						return err
					}
				}
				for i := k - 1; i >= 1; i-- {
					if _, all, err := readFrom(rotated(path, i), 0); err == nil {
						if err := emitAll(splitLines(all), emit); err != nil {
							return err
						}
					}
				}
			}
			cur, offset = nil, 0
		}
		info, data, err := readFrom(path, offset)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if cur == nil {
			cur = info
		} else if !os.SameFile(cur, info) {
			// Rotated between the check and the read; the next poll handles it.
			continue
		}
		offset += int64(len(data))
		if err := emitAll(splitLines(data), emit); err != nil {
			return err
		}
	}
}

// readFrom reads path from offset up to its last complete line.
func readFrom(path string, offset int64) (os.FileInfo, []byte, error) {
	f, err := openRead(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	return info, data[:bytes.LastIndexByte(data, '\n')+1], nil
}

// splitLines splits data made of complete lines, keeping the newlines.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		lines = append(lines, data[:i+1])
		data = data[i+1:]
	}
	return lines
}

func emitAll(lines [][]byte, emit func([]byte) error) error {
	for _, l := range lines {
		if err := emit(l); err != nil {
			return err
		}
	}
	return nil
}
