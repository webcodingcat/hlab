package apitest

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
)

// LogBuffer is a log destination safe for concurrent writes.
type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// CaptureLog sends the default logger, at debug level, to a buffer until
// t ends. A test using it must not run in parallel.
func CaptureLog(t *testing.T) *LogBuffer {
	t.Helper()
	buf := &LogBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}
