package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheLogGoesToTheDataFolderAndALargeOneIsMovedAside(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, logDir, logFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, logRotateAt), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, closeLog, err := openLog(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hlabd is running")
	if err := closeLog.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != logRotateAt {
		t.Errorf("the large log was not moved to %s.1: %v", logFile, err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `"msg":"hlabd is running"`) {
		t.Errorf("%s holds %q, want the message as a JSON line", logFile, written)
	}
}
