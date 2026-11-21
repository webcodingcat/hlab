// Package uninstall removes what hlab created on the host, in the order of
// hlabd uninstall-cleanup: service, forwarding, CA trust, host network,
// VMs, switch, files.
package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/settings"
)

// Service stops and deletes the Windows service; mode is "off" or
// "shutdown" (the stop override the cloud reads).
type Service interface {
	RemoveService(ctx context.Context, mode string) error
}

// Host restores and removes the host settings hlab changed.
type Host interface {
	Restore(ctx context.Context) error
	RemoveRootTrust(pemData []byte) (had bool, err error)
	RemoveWSLTrust(ctx context.Context, pemData []byte) error
	Down(ctx context.Context) error
}

// Hypervisor lists and deletes VMs and the switch.
type Hypervisor interface {
	VMs(ctx context.Context, prefix string) ([]string, error)
	DeleteVM(ctx context.Context, name string) error
	DeleteSwitch(ctx context.Context, name string) error
}

// Options names what Run removes.
type Options struct {
	Tag        string // profile tag: VM names, switch
	DataDir    string // %ProgramData%\<profile>: settings.toml, ca\ca.pem
	KeepData   bool
	Service    Service
	Host       Host
	Hypervisor Hypervisor
}

// Result.Notes: what was skipped without failing (WSL without a console
// user, a failed distro).
type Result struct{ Notes []string }

// hlabDirs are the folders hlabd creates in the data_folder.
var hlabDirs = []string{"disks", "volumes", "images", "isos", "cache", "appliance", "vms"}

// Run removes everything of the profile in a fixed order. Every step runs
// even after an earlier one failed; the joined error names each object that
// exists and could not be removed. Missing objects are no error.
func Run(ctx context.Context, o Options) (Result, error) {
	var res Result
	var errs []error

	// Read before anything is deleted.
	dataFolder, trustCA := readSettings(o.DataDir)

	mode := "off"
	if o.KeepData {
		mode = "shutdown"
	}
	if err := o.Service.RemoveService(ctx, mode); err != nil {
		errs = append(errs, fmt.Errorf("remove service: %w", err))
	}

	if err := o.Host.Restore(ctx); err != nil {
		errs = append(errs, fmt.Errorf("restore IPv4 forwarding: %w", err))
	}

	caPath := filepath.Join(o.DataDir, "ca", "ca.pem")
	pemData, err := os.ReadFile(caPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// No CA: nothing is trusted.
	case err != nil:
		errs = append(errs, fmt.Errorf("read %s: %w", caPath, err))
	default:
		had, err := o.Host.RemoveRootTrust(pemData)
		if err != nil {
			errs = append(errs, fmt.Errorf("remove CA from the Root store: %w", err))
		}
		if trustCA || had {
			if err := o.Host.RemoveWSLTrust(ctx, pemData); err != nil {
				res.Notes = append(res.Notes, fmt.Sprintf("CA not removed from WSL: %v", err))
			}
		}
	}

	if err := o.Host.Down(ctx); err != nil {
		errs = append(errs, fmt.Errorf("remove host network: %w", err))
	}

	vms, err := o.Hypervisor.VMs(ctx, o.Tag+"-")
	if err != nil && !notFound(err) {
		errs = append(errs, fmt.Errorf("list VMs: %w", err))
	}
	for _, name := range vms {
		if !ProfileVM(o.Tag, name) {
			continue
		}
		if err := o.Hypervisor.DeleteVM(ctx, name); err != nil && !notFound(err) {
			errs = append(errs, fmt.Errorf("delete VM %s: %w", name, err))
		}
	}

	if err := o.Hypervisor.DeleteSwitch(ctx, o.Tag); err != nil && !notFound(err) {
		errs = append(errs, fmt.Errorf("delete switch %s: %w", o.Tag, err))
	}

	if !o.KeepData {
		if err := RemoveFiles(o.DataDir, dataFolder); err != nil {
			errs = append(errs, err)
		}
	}
	return res, errors.Join(errs...)
}

// readSettings returns data_folder and trust_ca from <dataDir>\settings.toml;
// a missing or unreadable file gives <dataDir>\data and false.
func readSettings(dataDir string) (dataFolder string, trustCA bool) {
	def := filepath.Join(dataDir, "data")
	p := filepath.Join(dataDir, "settings.toml")
	if _, err := os.Stat(p); err != nil {
		return def, false
	}
	s, err := settings.Load(p)
	if err != nil {
		return def, false
	}
	return s.Storage.DataFolder, s.Certificates.TrustCA
}

// notFound reports a platform error of kind NotFound; a Hyper-V that is
// not installed reports its missing cmdlets this way.
func notFound(err error) bool {
	var pe *platform.Error
	return errors.As(err, &pe) && pe.Kind == platform.NotFound
}

// ProfileVM reports whether name is "<tag>-appliance" or
// "<tag>-server-<digits>".
func ProfileVM(tag, name string) bool {
	if name == tag+"-appliance" {
		return true
	}
	n, ok := strings.CutPrefix(name, tag+"-server-")
	if !ok || n == "" {
		return false
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// RemoveFiles deletes dataDir; a data_folder outside it loses only disks,
// volumes, images, isos, cache, appliance, vms and itself when then empty.
func RemoveFiles(dataDir, dataFolder string) error {
	var errs []error
	if dataFolder != "" && !inside(dataDir, dataFolder) {
		for _, d := range hlabDirs {
			p := filepath.Join(dataFolder, d)
			if err := os.RemoveAll(p); err != nil {
				errs = append(errs, fmt.Errorf("delete %s: %w", p, err))
			}
		}
		if entries, err := os.ReadDir(dataFolder); err == nil && len(entries) == 0 {
			if err := os.Remove(dataFolder); err != nil {
				errs = append(errs, fmt.Errorf("delete %s: %w", dataFolder, err))
			}
		}
	}
	if err := os.RemoveAll(dataDir); err != nil {
		errs = append(errs, fmt.Errorf("delete %s: %w", dataDir, err))
	}
	return errors.Join(errs...)
}

// inside reports whether path is dir or below it, ignoring case as Windows
// does.
func inside(dir, path string) bool {
	d := strings.ToLower(filepath.Clean(dir))
	p := strings.ToLower(filepath.Clean(path))
	return p == d || strings.HasPrefix(p, strings.TrimRight(d, `\/`)+string(filepath.Separator))
}

// userDataRetry is how long RemoveUserData retries while WebView2
// processes still hold files.
const userDataRetry = 10 * time.Second

// RemoveUserData deletes <appData>\hlab-tray.exe and <localAppData>\hlab,
// retrying for up to 10 s while WebView2 processes still hold files.
func RemoveUserData(appData, localAppData string) error {
	paths := []string{filepath.Join(appData, "hlab-tray.exe"), filepath.Join(localAppData, "hlab")}
	deadline := time.Now().Add(userDataRetry)
	for {
		var errs []error
		for _, p := range paths {
			if err := os.RemoveAll(p); err != nil {
				errs = append(errs, fmt.Errorf("delete %s: %w", p, err))
			}
		}
		err := errors.Join(errs...)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}
