// Package labapi holds the requests and answers of the lab control
// (/_lab/...), which the service (api/control) and its client (client)
// share. Errors have the shape of Hetzner's: {"error": {"code", "message"}}.
package labapi

import "time"

// Prefix is where the lab control answers.
const Prefix = "/_lab"

// TokenFile is the name of the control token's file in the data folder.
const TokenFile = "control.token"

// The default data folders, where the service writes the control token and
// the CLI looks for it.
const (
	WindowsDataDir = `C:\ProgramData\HLab`
	LinuxDataDir   = "/var/lib/hlab"
	// WSLDataDir is the Windows folder as WSL2 sees it.
	WSLDataDir = "/mnt/c/ProgramData/HLab"
)

// The lab's states, as Status reports them.
const (
	StateRunning  = "running"
	StateStopping = "stopping"
)

// Status is the answer of GET /_lab/status.
type Status struct {
	Version   string    `json:"version"`
	State     string    `json:"state"`
	Platform  string    `json:"platform"`
	DataDir   string    `json:"data_dir"`
	Started   time.Time `json:"started"`
	Projects  int       `json:"projects"`
	Endpoints Endpoints `json:"endpoints"`
}

// Endpoints are the URLs the lab answers at, as the tools take them
// (HCLOUD_ENDPOINT, HETZNER_ENDPOINT).
type Endpoints struct {
	Cloud   string `json:"cloud"`
	Hetzner string `json:"hetzner"`
	Control string `json:"control"`
	WebUI   string `json:"web_ui"`
}

// Project is a project as the lab control shows it.
type Project struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Internet bool      `json:"internet"`
}

// ProjectRequest is the body of POST /_lab/projects.
type ProjectRequest struct {
	Name string `json:"name"`
}

// Token is an API token as the lab control shows it: without the token
// itself, which only its creation returns.
type Token struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Permission string    `json:"permission"`
	Prefix     string    `json:"prefix"` // the token's first characters
	Created    time.Time `json:"created"`
}

// TokenRequest is the body of POST /_lab/projects/{name}/tokens.
type TokenRequest struct {
	Name       string `json:"name"`
	Permission string `json:"permission"` // "read" or "read_write"
	// Replace deletes a token of the same name first, instead of refusing
	// the name (hlab env makes a new token each time).
	Replace bool `json:"replace,omitempty"`
}

// CreatedToken is the answer of POST /_lab/projects/{name}/tokens: the only
// answer that carries the token itself.
type CreatedToken struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret"`
}

// Error is an error answer of the lab control.
type Error struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
