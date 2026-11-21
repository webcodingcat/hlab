//go:build windows

package windows

import (
	"context"
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

// stopOverrideFile is cloud.StopOverrideFile (cloud imports this package):
// the one-shot stop override in Paths.State that the cloud reads when it
// halts; its content is "off" or "shutdown".
const stopOverrideFile = "uninstall-stop"

// Uninstall stops the service with the configured stop_behaviour, removes
// it and, unless keepData, the data folder %ProgramData%\<profile>.
func (*Installer) Uninstall(keepData bool) error {
	ctx := context.Background()
	if err := StopService(ctx); err != nil {
		return err
	}
	if err := RemoveService(ctx, "shutdown"); err != nil {
		return err
	}
	if keepData {
		return nil
	}
	return os.RemoveAll(DataDir())
}

// RemoveService writes mode ("off" or "shutdown") to the stop override
// <Paths.State>\uninstall-stop when the service runs, stops the service,
// waits up to uninstallStopWait and deletes it. A missing service is no
// error. The override file is deleted afterwards if the cloud left it.
func RemoveService(ctx context.Context, mode string) error {
	return withService(func(s *mgr.Service, name string) error {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("query service %s: %w", name, err)
		}
		if st.State != svc.Stopped {
			override := filepath.Join(DataDir(), stopOverrideFile)
			if err := os.WriteFile(override, []byte(mode), 0o644); err != nil {
				return fmt.Errorf("write stop override %s: %w", override, err)
			}
			defer func() { _ = os.Remove(override) }()
			if err := stopAndWait(ctx, s, name, st); err != nil {
				return err
			}
		}
		if err := s.Delete(); err != nil {
			return fmt.Errorf("delete service %s: %w", name, err)
		}
		return nil
	})
}

// StopService stops the service of the current profile if it runs and
// waits until it has stopped; the configured stop_behaviour applies. A
// missing service is no error.
func StopService(ctx context.Context) error {
	return withService(func(s *mgr.Service, name string) error {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("query service %s: %w", name, err)
		}
		return stopAndWait(ctx, s, name, st)
	})
}

// withService opens the service of the current profile and calls f; a
// missing service is no error and f is not called.
func withService(f func(s *mgr.Service, name string) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (administrator rights needed): %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	name := profile.Current().ServiceName
	s, err := m.OpenService(name)
	if errors.Is(err, xwin.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open service %s: %w", name, err)
	}
	defer func() { _ = s.Close() }()
	return f(s, name)
}

// stopAndWait stops the service in state st unless it is stopped and waits
// up to uninstallStopWait until it has stopped.
func stopAndWait(ctx context.Context, s *mgr.Service, name string, st svc.Status) error {
	if st.State == svc.Stopped {
		return nil
	}
	if st.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("stop service %s: %w", name, err)
		}
	}
	deadline := time.Now().Add(uninstallStopWait)
	for {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("query service %s: %w", name, err)
		}
		if st.State == svc.Stopped {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service %s did not stop within %s", name, uninstallStopWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
