// Package sysint is the system integration of hlab: everything about the
// host operating system that is not networking or the hypervisor. The
// interfaces live here; sysint/windows implements them for Windows.
package sysint

import "context"

// Paths are the folders hlab uses on the host.
type Paths struct {
	Config   string // folder of settings.toml and server-types.custom.json
	State    string // folder of state.db and the status file
	Logs     string // log files
	CA       string // certificate authority (SYSTEM only)
	Catalog  string // shipped catalogs
	Programs string // folder of the hlab programs
}

// Service states reported by ServiceControl.State.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateStopping = "stopping"
)

// ServiceControl starts, stops and queries the hlabd service with the
// rights an interactive user has. Used by hlab and hlab-tray.
type ServiceControl interface {
	// Start asks the service manager to start the service with params as
	// start parameters. It returns once the request is accepted.
	Start(ctx context.Context, params ...string) error
	// Stop asks the service to stop. It returns once the request is
	// accepted.
	Stop(ctx context.Context) error
	// State is one of StateStopped, StateStarting, StateRunning and
	// StateStopping.
	State(ctx context.Context) (string, error)
}

// StopReason says why the service stops.
type StopReason int

const (
	// StopRequested: a user or program asked the service to stop.
	StopRequested StopReason = iota
	// StopHostShutdown: the host shuts down or restarts.
	StopHostShutdown
)

func (r StopReason) String() string {
	if r == StopHostShutdown {
		return "host_shutdown"
	}
	return "requested"
}

// ServiceHost runs hlabd as a service of the host's service manager.
type ServiceHost interface {
	// Run reports the service as starting, calls start with the start
	// parameters, reports it as running and waits for a stop request or
	// the host shutdown; then it calls stop and reports it as stopped. The
	// context given to start ends when Run returns. An error from start
	// stops the service with that error.
	Run(start func(ctx context.Context, params []string) error, stop func(ctx context.Context, reason StopReason) error) error
}

// Installer registers and removes the service and its folders. Used by
// hlabd install and hlabd uninstall-cleanup.
type Installer interface {
	// Install registers the service of the current profile and creates
	// its data folder; dev marks the call of scripts/dev-setup.ps1. The
	// release and the dev profile may be installed side by side.
	Install(dev bool) error
	// Uninstall removes the service and, unless keepData, the data folder.
	Uninstall(keepData bool) error
}
