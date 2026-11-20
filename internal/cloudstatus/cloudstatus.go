// Package cloudstatus reads hlabd's status file (status.json): a refused
// start carries the problem, a failed start the error.
package cloudstatus

import (
	"encoding/json"
	"os"
)

// Problem is the problem that refused a start.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

// File is the content of the status file.
type File struct {
	State   string   `json:"state"`
	Problem *Problem `json:"problem,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Read reads the status file at path; a missing or unreadable file reports
// false.
func Read(path string) (File, bool) {
	var f File
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &f) != nil {
		return File{}, false
	}
	return f, true
}
