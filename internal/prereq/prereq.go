// Package prereq decides which machine prerequisites of hlab are missing
// (INST-02). It holds no Windows calls; sysint/windows reads the facts.
package prereq

import "fmt"

// Facts are what sysint/windows reads without elevation and without DISM.
type Facts struct {
	Arch         string // runtime of the OS: "amd64", "arm64", "386"
	Build        int    // CurrentBuildNumber
	Server       bool   // InstallationType "Server" (Desktop Experience)
	ServerCore   bool   // InstallationType "Server Core" (no Desktop Experience, unsupported)
	EditionID    string // HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\EditionID
	VMMS         bool   // service vmms is registered (Hyper-V platform and services)
	PSModule     bool   // ...\WindowsPowerShell\v1.0\Modules\Hyper-V\[<version>\]Hyper-V.psd1 exists
	Hypervisor   bool   // Win32_ComputerSystem.HypervisorPresent
	FirmwareVirt bool   // Win32_Processor.VirtualizationFirmwareEnabled
}

const (
	clientMinBuild = 22000 // Windows 11
	serverMinBuild = 20348 // Windows Server 2022
)

// RestartLine is the line Missing returns when Hyper-V is enabled but the
// hypervisor does not run yet.
const RestartLine = "Restart Windows to finish enabling Hyper-V"

// homeEditions are the EditionID values of Windows Home, which has no Hyper-V.
var homeEditions = map[string]bool{
	"Core":                true,
	"CoreN":               true,
	"CoreSingleLanguage":  true,
	"CoreCountrySpecific": true,
}

// Missing returns one English line per missing prerequisite in a fixed
// order (architecture, Windows version, edition, hardware virtualization,
// Hyper-V, Hyper-V PowerShell module, restart pending); nil when nothing
// is missing.
func Missing(f Facts) []string {
	var out []string
	if f.Arch != "amd64" {
		out = append(out, fmt.Sprintf("hlab needs 64-bit Windows on an x64 processor (this machine: %s)", f.Arch))
	}
	server := f.Server || f.ServerCore
	if server && f.Build < serverMinBuild {
		out = append(out, fmt.Sprintf("hlab needs Windows Server 2022 (build %d) or newer with Desktop Experience (this machine: build %d)", serverMinBuild, f.Build))
	} else if !server && f.Build < clientMinBuild {
		out = append(out, fmt.Sprintf("hlab needs Windows 11 (build %d) or newer, or Windows Server 2022 with Desktop Experience (this machine: build %d)", clientMinBuild, f.Build))
	}
	if homeEditions[f.EditionID] {
		out = append(out, "Windows Home has no Hyper-V; hlab needs the Pro, Enterprise or Education edition")
	} else if f.ServerCore {
		out = append(out, "Windows Server Core is not supported; hlab needs Windows Server with the Desktop Experience edition")
	}
	if !f.FirmwareVirt && !f.Hypervisor {
		out = append(out, "Hardware virtualization is off; turn it on in the firmware (UEFI/BIOS) settings")
	}
	if !f.VMMS {
		out = append(out, "Hyper-V is not enabled; turn on the Windows feature Hyper-V (platform and services)")
	}
	if !f.PSModule {
		out = append(out, "The Hyper-V module for Windows PowerShell is missing; turn on that Windows feature")
	}
	if f.VMMS && f.FirmwareVirt && !f.Hypervisor {
		out = append(out, RestartLine)
	}
	return out
}
