//go:build windows

package windows

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	xwin "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/webcodingcat/hlab/internal/profile"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// serviceSDDL is the default service DACL with Interactive Users (IU)
// widened to query, start (RP) and stop (WP), never change-config (DC).
// It must stay identical to $ServiceSddl in scripts/dev-setup.ps1.
const serviceSDDL = "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWRPWPLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)"

const (
	preshutdownTimeout = 180 * time.Second
	interactiveUsers   = "*S-1-5-4"
	uninstallStopWait  = preshutdownTimeout + 20*time.Second
)

// Installer registers and removes the service of the current profile. It
// needs administrator rights.
type Installer struct{}

var _ sysint.Installer = (*Installer)(nil)

// NewInstaller returns the installer of the current profile.
func NewInstaller() *Installer {
	return &Installer{}
}

// Install registers the service (manual start, LocalSystem, the running
// executable, the service descriptor, preshutdown timeout) and creates the
// data folder with settings.toml, which Interactive Users may modify. With
// dev it refuses when the release service is installed under
// %ProgramFiles%.
func (*Installer) Install(dev bool) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (administrator rights needed): %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	if dev {
		installed, err := releaseInstalled(m)
		if err != nil {
			return err
		}
		if installed {
			return fmt.Errorf("an installed hlab (service %s) exists; this command is for the dev build of a working copy", profile.Other().ServiceName)
		}
	}

	if err := registerService(m, exe); err != nil {
		return err
	}
	return createDataFolder()
}

func displayName() string {
	if profile.Current().Name == "release" {
		return "hlab"
	}
	return "hlab (dev)"
}

func registerService(m *mgr.Mgr, exe string) error {
	name := profile.Current().ServiceName
	s, err := m.OpenService(name)
	switch {
	case err == nil:
		st, err := s.Query()
		if err != nil {
			_ = s.Close()
			return fmt.Errorf("query service %s: %w", name, err)
		}
		if st.State != svc.Stopped {
			_ = s.Close()
			return fmt.Errorf("service %s is not stopped; stop it first (hlab cloud stop)", name)
		}
		c, err := s.Config()
		if err != nil {
			_ = s.Close()
			return fmt.Errorf("read configuration of service %s: %w", name, err)
		}
		c.BinaryPathName = `"` + exe + `"`
		c.StartType = mgr.StartManual
		c.ServiceStartName = "LocalSystem"
		c.DisplayName = displayName()
		if err := s.UpdateConfig(c); err != nil {
			_ = s.Close()
			return fmt.Errorf("update service %s: %w", name, err)
		}
	case errors.Is(err, xwin.ERROR_SERVICE_DOES_NOT_EXIST):
		s, err = m.CreateService(name, exe, mgr.Config{
			StartType:   mgr.StartManual,
			DisplayName: displayName(),
		})
		if err != nil {
			return fmt.Errorf("create service %s: %w", name, err)
		}
	default:
		return fmt.Errorf("open service %s: %w", name, err)
	}
	defer func() { _ = s.Close() }()

	if err := setServiceDACL(s.Handle); err != nil {
		return fmt.Errorf("set security descriptor of service %s: %w", name, err)
	}
	info := struct{ Timeout uint32 }{Timeout: uint32(preshutdownTimeout / time.Millisecond)}
	if err := xwin.ChangeServiceConfig2(s.Handle, xwin.SERVICE_CONFIG_PRESHUTDOWN_INFO, (*byte)(unsafe.Pointer(&info))); err != nil {
		return fmt.Errorf("set preshutdown timeout of service %s: %w", name, err)
	}
	return nil
}

// setServiceDACL applies serviceSDDL like sc.exe sdset.
func setServiceDACL(h xwin.Handle) error {
	sd, err := xwin.SecurityDescriptorFromString(serviceSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return xwin.SetSecurityInfo(h, xwin.SE_SERVICE, xwin.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// releaseInstalled reports whether the release service exists and runs a
// binary below %ProgramFiles%. An old dev service of the same name running
// a working copy is not a release installation.
func releaseInstalled(m *mgr.Mgr) (bool, error) {
	name := profile.Other().ServiceName
	if profile.Current().Name == "release" {
		name = profile.Current().ServiceName
	}
	s, err := m.OpenService(name)
	if errors.Is(err, xwin.ERROR_SERVICE_DOES_NOT_EXIST) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open service %s: %w", name, err)
	}
	defer func() { _ = s.Close() }()
	c, err := s.Config()
	if err != nil {
		return false, fmt.Errorf("read configuration of service %s: %w", name, err)
	}
	bin := binaryPath(c.BinaryPathName)
	pf := strings.ToLower(filepath.Clean(programFiles())) + string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(filepath.Clean(bin)), pf), nil
}

// binaryPath is the program of a service command line, without quotes and
// arguments.
func binaryPath(cmdline string) string {
	cmdline = strings.TrimSpace(cmdline)
	if rest, ok := strings.CutPrefix(cmdline, `"`); ok {
		p, _, _ := strings.Cut(rest, `"`)
		return p
	}
	p, _, _ := strings.Cut(cmdline, " ")
	return p
}

// createDataFolder creates the data folder and settings.toml and lets
// Interactive Users modify settings.toml.
func createDataFolder() error {
	dir := DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, "settings.toml")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	icacls := filepath.Join(systemRoot(), "System32", "icacls.exe")
	out, err := exec.Command(icacls, file, "/grant", interactiveUsers+":(M)").CombinedOutput()
	if err != nil {
		return fmt.Errorf("grant Interactive Users modify on %s: %w: %s", file, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func systemRoot() string {
	if d := os.Getenv("SystemRoot"); d != "" {
		return d
	}
	return `C:\Windows`
}

// Uninstall stops and removes the service and, unless keepData, the data
// folder %ProgramData%\<profile>. Removing the Hyper-V and host objects
// tagged with the profile (VMs, switch, NAT, routes, NRPT and firewall
// rules) belongs to the installer phase 12 of the roadmap.
func (*Installer) Uninstall(keepData bool) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (administrator rights needed): %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	if err := removeService(m, profile.Current().ServiceName); err != nil {
		return err
	}
	if keepData {
		return nil
	}
	return os.RemoveAll(DataDir())
}

func removeService(m *mgr.Mgr, name string) error {
	s, err := m.OpenService(name)
	if errors.Is(err, xwin.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open service %s: %w", name, err)
	}
	defer func() { _ = s.Close() }()
	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("query service %s: %w", name, err)
	}
	if st.State != svc.Stopped {
		if st.State != svc.StopPending {
			if _, err := s.Control(svc.Stop); err != nil {
				return fmt.Errorf("stop service %s: %w", name, err)
			}
		}
		deadline := time.Now().Add(uninstallStopWait)
		for st.State != svc.Stopped {
			if time.Now().After(deadline) {
				return fmt.Errorf("service %s did not stop within %s", name, uninstallStopWait)
			}
			time.Sleep(time.Second)
			if st, err = s.Query(); err != nil {
				return fmt.Errorf("query service %s: %w", name, err)
			}
		}
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %s: %w", name, err)
	}
	return nil
}
