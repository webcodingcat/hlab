//go:build windows

package windows

import (
	"errors"
	"fmt"
	"io/fs"
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
// data folder with settings.toml, which Interactive Users may modify. The
// profile of the binary decides the names, so dev changes nothing here; the
// release and the dev service may be installed side by side.
func (*Installer) Install(_ bool) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (administrator rights needed): %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	if err := registerService(m, exe); err != nil {
		return err
	}
	return createDataFolder()
}

func displayName() string {
	if profile.Current().Name == "release" {
		return "HLab"
	}
	return "HLab (dev)"
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

// dataFolderSDDL: owner Administrators; a protected DACL with SYSTEM and
// Administrators full control and Interactive Users read and execute, all
// inherited by files and folders below. No other user can create ca\,
// logs\, state.db or junctions in the data folder.
//
// The Hyper-V worker processes run as "NT VIRTUAL MACHINE\Virtual Machines"
// (S-1-5-83-0) and must open the disks and seed ISOs the VMs attach, which
// hlabd (re)creates below the data folder and which therefore only carry the
// inherited entries. The group gets read and execute on folders (CI) and
// read and write on files (OIIO): enough to open and write VHDX disks, but
// no delete, no change of permissions and no creating files in folders.
const dataFolderSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;IU)" +
	"(A;CI;0x1200a9;;;S-1-5-83-0)(A;OIIO;0x12019f;;;S-1-5-83-0)"

// createDataFolder creates the data folder with its protected DACL (also
// when it exists already) and settings.toml, which Interactive Users may
// modify.
func createDataFolder() error {
	dir := DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode().Type() != fs.ModeDir {
		return fmt.Errorf("%s is not a plain folder (link or junction); remove it and install again", dir)
	}
	if err := setFolderSecurity(dir); err != nil {
		return fmt.Errorf("set permissions of %s: %w", dir, err)
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

// setFolderSecurity sets owner and protected DACL of dataFolderSDDL on dir;
// Windows propagates the inheritable entries to existing children.
func setFolderSecurity(dir string) error {
	sd, err := xwin.SecurityDescriptorFromString(dataFolderSDDL)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	info := xwin.SECURITY_INFORMATION(xwin.OWNER_SECURITY_INFORMATION | xwin.DACL_SECURITY_INFORMATION | xwin.PROTECTED_DACL_SECURITY_INFORMATION)
	return xwin.SetNamedSecurityInfo(dir, xwin.SE_FILE_OBJECT, info, owner, nil, dacl, nil)
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
