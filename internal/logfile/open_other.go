//go:build !windows

package logfile

import "os"

// openRead opens a log file for reading.
func openRead(path string) (*os.File, error) { return os.Open(path) }
