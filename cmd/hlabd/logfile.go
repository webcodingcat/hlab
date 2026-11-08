package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// The log file in the data folder, and the size at which a start moves it
// aside as hlabd.log.1, so that the folder keeps two runs' worth at most.
const (
	logDir      = "logs"
	logFile     = "hlabd.log"
	logRotateAt = 10 << 20
)

// openLog returns a logger that writes text to stderr and JSON lines to
// logs/hlabd.log in the data folder, and the file to close at the end.
// hlabd often runs without a console (the start menu, `hlab start`, the
// dev loop's task), so the file is where its log can be read.
func openLog(dataDir string) (*slog.Logger, io.Closer, error) {
	dir := filepath.Join(dataDir, logDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, logFile)
	if info, err := os.Stat(path); err == nil && info.Size() >= logRotateAt {
		// fails while another hlabd writes it; its start stops at the lock
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	logger := slog.New(slog.NewMultiHandler(
		slog.NewTextHandler(os.Stderr, nil),
		slog.NewJSONHandler(file, nil),
	))
	return logger, file, nil
}
