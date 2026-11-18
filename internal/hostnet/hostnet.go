// Package hostnet describes everything on the host outside the hypervisor:
// host IP on the hlab adapter, routes, IP forwarding, outbound NAT, NRPT
// rules, WSL2 detection and forwarding, collision checks, CA trust store.
package hostnet

import (
	"context"
	"net/netip"
)

// HostNet configures the host network for one cloud.
type HostNet interface {
	// Check reports range collisions, other instances, WSL mode and dnsTunneling.
	Check(ctx context.Context, cfg Config) []Problem
	// Up sets the adapter IP, route, forwarding, NAT and base NRPT rules.
	Up(ctx context.Context, cfg Config) error
	// SetZones keeps exactly one NRPT rule per zone, idempotently.
	SetZones(ctx context.Context, zones []string) error
	// Down removes everything Up and SetZones added.
	Down(ctx context.Context) error
	// WatchWSL re-applies forwarding when WSL restarts.
	WatchWSL(ctx context.Context, onChange func(WSLInfo))
	// TrustCA adds (on) or removes the CA in the Windows store and WSL2 distros.
	TrustCA(ctx context.Context, pem []byte, on bool) error
	// DNSServers returns the host's DNS servers: those of adapters with a
	// default route, without the hlab, WSL and loopback adapters.
	DNSServers(ctx context.Context) ([]netip.Addr, error)
}

// Config is the host side of the cloud network.
type Config struct {
	PublicRange   netip.Prefix // from settings
	HostAddr      netip.Prefix // 198.19.0.2/30
	ApplianceAddr netip.Addr   // 198.19.0.1
	SwitchName    string       // the profile's tag
	BaseZones     []string     // "hetzner.internal", reverse zones of PublicRange
}

// Problem codes reported by Check.
const (
	ProblemRangeCollision       = "range_collision"
	ProblemWinNATExists         = "winnat_exists"
	ProblemOtherInstanceRunning = "other_instance_running"
	ProblemWSLMirrored          = "wsl_mirrored"
	ProblemWSLVirtioProxy       = "wsl_virtioproxy"
	ProblemWSLDNSTunnelingOff   = "wsl_dns_tunneling_off"
	ProblemHyperVMissing        = "hyperv_missing"
)

// Problem is one finding of Check.
type Problem struct {
	Code    string // one of the Problem* constants
	Message string // English, shown as is
	Fatal   bool   // true: the cloud does not start
}

// WSLInfo describes the WSL2 networking of the host.
type WSLInfo struct {
	Present      bool
	Mode         string
	DNSTunneling bool
	AdapterAlias string
	Subnet       netip.Prefix
}
