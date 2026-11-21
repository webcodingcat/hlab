//go:build windows

package windows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	xwin "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/webcodingcat/hlab/internal/prereq"
)

// cimScript reads both CIM facts in one powershell.exe call; any processor
// with the firmware flag counts.
const cimScript = `$c = Get-CimInstance -ClassName Win32_ComputerSystem
$p = @(Get-CimInstance -ClassName Win32_Processor | Where-Object { $_.VirtualizationFirmwareEnabled })
[pscustomobject]@{ Hypervisor = [bool]$c.HypervisorPresent; Firmware = ($p.Count -gt 0) } | ConvertTo-Json -Compress`

// Prerequisites reads the machine facts for prereq.Missing without
// elevation: the registry, the Hyper-V module file, an SCM query of vmms
// and one CIM query through powershell.exe.
func Prerequisites(ctx context.Context) (prereq.Facts, error) {
	var f prereq.Facts
	arch, err := nativeArch()
	if err != nil {
		return f, err
	}
	f.Arch = arch

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return f, fmt.Errorf("read the Windows version: %w", err)
	}
	defer func() { _ = k.Close() }()
	build, _, err := k.GetStringValue("CurrentBuildNumber")
	if err != nil {
		return f, fmt.Errorf("read the Windows build: %w", err)
	}
	if f.Build, err = strconv.Atoi(build); err != nil {
		return f, fmt.Errorf("read the Windows build %q: %w", build, err)
	}
	if f.EditionID, _, err = k.GetStringValue("EditionID"); err != nil {
		return f, fmt.Errorf("read the Windows edition: %w", err)
	}
	inst, _, err := k.GetStringValue("InstallationType")
	if err != nil {
		return f, fmt.Errorf("read the Windows installation type: %w", err)
	}
	f.Server = inst == "Server"
	f.ServerCore = inst == "Server Core"

	// The manifest lies in the module folder or in a version folder below it
	// (Windows 11 has Hyper-V\2.0.0.0\Hyper-V.psd1).
	module := filepath.Join(systemRoot(), "System32", "WindowsPowerShell", "v1.0", "Modules", "Hyper-V")
	for _, pattern := range []string{"Hyper-V.psd1", filepath.Join("*", "Hyper-V.psd1")} {
		if m, _ := filepath.Glob(filepath.Join(module, pattern)); len(m) > 0 {
			f.PSModule = true
		}
	}

	if f.VMMS, err = serviceExists("vmms"); err != nil {
		return f, err
	}

	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cimScript)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return f, fmt.Errorf("query virtualization through powershell: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var cim struct{ Hypervisor, Firmware bool }
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &cim); err != nil {
		return f, fmt.Errorf("read the virtualization query: %w", err)
	}
	f.Hypervisor, f.FirmwareVirt = cim.Hypervisor, cim.Firmware
	return f, nil
}

// nativeArch is the processor architecture of the OS, independent of the
// architecture this binary was built for.
func nativeArch() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("read the processor architecture: %w", err)
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue("PROCESSOR_ARCHITECTURE")
	if err != nil {
		return "", fmt.Errorf("read the processor architecture: %w", err)
	}
	switch strings.ToUpper(v) {
	case "AMD64":
		return "amd64", nil
	case "ARM64":
		return "arm64", nil
	case "X86":
		return "386", nil
	}
	return strings.ToLower(v), nil
}

// serviceExists reports whether the SCM knows the service name; it needs
// only connect and query rights.
func serviceExists(name string) (bool, error) {
	m, err := xwin.OpenSCManager(nil, nil, xwin.SC_MANAGER_CONNECT)
	if err != nil {
		return false, fmt.Errorf("connect to the service manager: %w", err)
	}
	defer func() { _ = xwin.CloseServiceHandle(m) }()
	p, err := xwin.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}
	h, err := xwin.OpenService(m, p, xwin.SERVICE_QUERY_STATUS)
	if errors.Is(err, xwin.ERROR_SERVICE_DOES_NOT_EXIST) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query service %s: %w", name, err)
	}
	_ = xwin.CloseServiceHandle(h)
	return true, nil
}
