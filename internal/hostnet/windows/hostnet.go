//go:build windows

// Package windows implements hostnet.HostNet on a Windows host. Every host
// change runs as one PowerShell script per call through Options.Run; scripts
// that return data print it as JSON and print nothing when there is nothing
// to report.
package windows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/webcodingcat/hlab/internal/hostnet"
	"github.com/webcodingcat/hlab/internal/profile"
)

// apiPort is the port hlabd listens on at the host address.
const apiPort = 8780

// restoreFile records host settings hostnet changes but does not own.
const restoreFile = "hostnet-restore.json"

// Runner runs one PowerShell script and returns its standard output.
type Runner func(ctx context.Context, script string) ([]byte, error)

// Options configures a HostNet.
type Options struct {
	// Run runs the scripts; default: a fresh powershell.exe per call.
	Run Runner
	// OtherInstanceRunning reports whether the service of the other profile
	// runs; default: a query of the service control manager.
	OtherInstanceRunning func(ctx context.Context) (bool, error)
	// StateDir holds hostnet-restore.json.
	StateDir string
	// Program is the executable the firewall rule admits; default os.Executable().
	Program string
}

// HostNet configures the Windows host network for one cloud.
type HostNet struct {
	opts Options
	tag  string

	mu  sync.Mutex
	cfg hostnet.Config // of the last successful Up
}

var _ hostnet.HostNet = (*HostNet)(nil)

// New returns a HostNet with defaults filled in.
func New(opts Options) *HostNet {
	if opts.Run == nil {
		opts.Run = runPowerShell
	}
	if opts.OtherInstanceRunning == nil {
		opts.OtherInstanceRunning = otherServiceRunning
	}
	if opts.Program == "" {
		if exe, err := os.Executable(); err == nil {
			opts.Program = exe
		}
	}
	return &HostNet{opts: opts, tag: profile.Current().Tag}
}

// runPowerShell runs script in a fresh powershell.exe and returns its output.
func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"[Console]::OutputEncoding = [Text.Encoding]::UTF8\n"+script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// run runs script and decodes its JSON output into v; empty output leaves v unchanged.
func (h *HostNet) run(ctx context.Context, script string, v any) error {
	out, err := h.opts.Run(ctx, script)
	if err != nil {
		return err
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 || v == nil {
		return nil
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("decode script output: %w", err)
	}
	return nil
}

// hostEntry is one host address, route or NAT instance reported by checkScript.
type hostEntry struct {
	Kind   string `json:"kind"` // "address", "route" or "nat"
	Alias  string `json:"alias"`
	Prefix string `json:"prefix"`
}

// Check reports range collisions, a foreign WinNAT and a running cloud of
// the other profile.
func (h *HostNet) Check(ctx context.Context, cfg hostnet.Config) []hostnet.Problem {
	var problems []hostnet.Problem
	other := profile.Other().Name
	running, err := h.opts.OtherInstanceRunning(ctx)
	switch {
	case err != nil:
		problems = append(problems, hostnet.Problem{Code: hostnet.ProblemOtherInstanceRunning, Fatal: true,
			Message: fmt.Sprintf("cannot tell whether the hlab cloud of profile %s runs: %v", other, err)})
	case running:
		problems = append(problems, hostnet.Problem{Code: hostnet.ProblemOtherInstanceRunning, Fatal: true,
			Message: fmt.Sprintf("the hlab cloud of profile %s is running; stop it first", other)})
	}

	var entries []hostEntry
	if err := h.run(ctx, checkScript(alias(cfg.SwitchName), h.tag), &entries); err != nil {
		return append(problems, hostnet.Problem{Code: hostnet.ProblemRangeCollision, Fatal: true,
			Message: fmt.Sprintf("cannot read the host's addresses and routes: %v", err)})
	}
	for _, e := range entries {
		switch e.Kind {
		case "nat":
			problems = append(problems, hostnet.Problem{Code: hostnet.ProblemWinNATExists, Fatal: true,
				Message: fmt.Sprintf("the WinNAT instance %q exists; Windows allows only one, remove it with Remove-NetNat", e.Alias)})
		case "address", "route":
			p, err := netip.ParsePrefix(e.Prefix)
			if err != nil || p.Bits() == 0 || !p.Addr().Is4() {
				continue
			}
			p = p.Masked()
			if !p.Overlaps(cfg.PublicRange) {
				continue
			}
			what := "address " + e.Prefix + " of interface " + e.Alias
			if e.Kind == "route" {
				what = "route " + e.Prefix + " via interface " + e.Alias
			}
			problems = append(problems, hostnet.Problem{Code: hostnet.ProblemRangeCollision, Fatal: true,
				Message: fmt.Sprintf("the public range %s collides with the host's %s", cfg.PublicRange, what)})
		}
	}
	return problems
}

// forwarding is the previous IPv4 forwarding value of one adapter.
type forwarding struct {
	Alias      string `json:"alias"`
	Forwarding string `json:"forwarding"` // "Enabled" or "Disabled"
}

// restoreState is the content of hostnet-restore.json.
type restoreState struct {
	Forwarding []forwarding `json:"forwarding"`
}

// Up restores leftovers of a crash, records the forwarding values it is
// about to change and configures the host for cfg.
func (h *HostNet) Up(ctx context.Context, cfg hostnet.Config) error {
	if !cfg.PublicRange.IsValid() || !cfg.HostAddr.IsValid() || !cfg.ApplianceAddr.IsValid() || cfg.SwitchName == "" {
		return errors.New("hostnet: incomplete config")
	}
	if h.opts.Program == "" {
		return errors.New("hostnet: no program for the firewall rule")
	}
	if err := h.restore(ctx); err != nil {
		return err
	}
	var prev []forwarding
	if err := h.run(ctx, forwardingQueryScript(alias(cfg.SwitchName)), &prev); err != nil {
		return fmt.Errorf("hostnet: read forwarding: %w", err)
	}
	data, err := json.Marshal(restoreState{Forwarding: prev})
	if err != nil {
		return err
	}
	if err := os.WriteFile(h.restorePath(), data, 0o600); err != nil {
		return fmt.Errorf("hostnet: write restore file: %w", err)
	}
	if err := h.run(ctx, upScript(cfg, h.tag, h.opts.Program), nil); err != nil {
		return fmt.Errorf("hostnet: up: %w", err)
	}
	if err := h.run(ctx, nrptScript(h.tag, cfg.ApplianceAddr, cfg.BaseZones), nil); err != nil {
		return fmt.Errorf("hostnet: NRPT rules: %w", err)
	}
	h.mu.Lock()
	h.cfg = cfg
	h.mu.Unlock()
	return nil
}

// SetZones keeps exactly one NRPT rule per zone: the base zones of Up plus zones.
func (h *HostNet) SetZones(ctx context.Context, zones []string) error {
	h.mu.Lock()
	cfg := h.cfg
	h.mu.Unlock()
	if !cfg.ApplianceAddr.IsValid() {
		return errors.New("hostnet: SetZones before Up")
	}
	all := append(append([]string{}, cfg.BaseZones...), zones...)
	if err := h.run(ctx, nrptScript(h.tag, cfg.ApplianceAddr, all), nil); err != nil {
		return fmt.Errorf("hostnet: NRPT rules: %w", err)
	}
	return nil
}

// Down removes everything Up and SetZones added and restores the recorded
// forwarding values.
func (h *HostNet) Down(ctx context.Context) error {
	if err := h.run(ctx, downScript(h.tag, alias(h.tag)), nil); err != nil {
		return fmt.Errorf("hostnet: down: %w", err)
	}
	if err := h.restore(ctx); err != nil {
		return err
	}
	h.mu.Lock()
	h.cfg = hostnet.Config{}
	h.mu.Unlock()
	return nil
}

// WatchWSL is a stub: re-applying forwarding after a WSL restart is built
// in a later phase.
func (h *HostNet) WatchWSL(context.Context, func(hostnet.WSLInfo)) {}

// TrustCA is a stub: the CA trust store is handled in a later phase.
func (h *HostNet) TrustCA(context.Context, []byte, bool) error { return nil }

// DNSServers returns the IPv4 DNS servers of the interfaces with a default
// route, without the hlab, WSL and loopback adapters.
func (h *HostNet) DNSServers(ctx context.Context) ([]netip.Addr, error) {
	var raw []string
	if err := h.run(ctx, dnsServersScript(alias(h.tag)), &raw); err != nil {
		return nil, fmt.Errorf("hostnet: DNS servers: %w", err)
	}
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, s := range raw {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() || a.IsLoopback() || a.IsUnspecified() || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out, nil
}

func (h *HostNet) restorePath() string {
	return filepath.Join(h.opts.StateDir, restoreFile)
}

// restore sets the forwarding values recorded in the restore file and
// deletes it; without the file it does nothing.
func (h *HostNet) restore(ctx context.Context) error {
	data, err := os.ReadFile(h.restorePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("hostnet: read restore file: %w", err)
	}
	var st restoreState
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("hostnet: decode restore file: %w", err)
	}
	if script := restoreScript(st.Forwarding); script != "" {
		if err := h.run(ctx, script, nil); err != nil {
			return fmt.Errorf("hostnet: restore forwarding: %w", err)
		}
	}
	if err := os.Remove(h.restorePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("hostnet: delete restore file: %w", err)
	}
	return nil
}
