//go:build e2einstall

package install

import (
	"context"
	"crypto/sha1" //nolint:gosec // the Windows store names certificates by their SHA-1 thumbprint
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Probes of Hyper-V and the host. Every script prints JSON only.

func psString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func vmNames(ctx context.Context) ([]string, error) {
	var names []string
	err := powershellJSON(ctx, `ConvertTo-Json -Compress -InputObject @(Get-VM | ForEach-Object { [string]$_.Name })`, &names)
	return names, err
}

func vmExists(ctx context.Context, name string) (bool, error) {
	names, err := vmNames(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true, nil
		}
	}
	return false, nil
}

// profileVMs are the VMs of the release profile by exact name:
// hlab-appliance and hlab-server-<digits>.
func profileVMs(ctx context.Context) ([]string, error) {
	names, err := vmNames(ctx)
	if err != nil {
		return nil, err
	}
	server := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(releaseTag) + `-server-\d+$`)
	var out []string
	for _, n := range names {
		if strings.EqualFold(n, releaseTag+"-appliance") || server.MatchString(n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func vmDiskPaths(ctx context.Context, name string) ([]string, error) {
	var paths []string
	err := powershellJSON(ctx, fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-VMHardDiskDrive -VMName %s | ForEach-Object { [string]$_.Path })`, psString(name)), &paths)
	return paths, err
}

func switchExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := powershellJSON(ctx, fmt.Sprintf(`ConvertTo-Json -InputObject @(Get-VMSwitch -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq %s }).Count`, psString(name)), &n)
	return n > 0, err
}

// releaseRoutes are the IPv4 routes on the release adapter or via the
// appliance's transit address of the default range.
func releaseRoutes(ctx context.Context) ([]string, error) {
	var routes []string
	err := powershellJSON(ctx, fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-NetRoute -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceAlias -eq %s -or $_.NextHop -eq '198.19.0.1' } | ForEach-Object { '{0} via {1} on {2}' -f $_.DestinationPrefix, $_.NextHop, $_.InterfaceAlias })`,
		psString("vEthernet ("+releaseTag+")")), &routes)
	return routes, err
}

func nrptRules(ctx context.Context, comment string) ([]string, error) {
	var rules []string
	err := powershellJSON(ctx, fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-DnsClientNrptRule -ErrorAction SilentlyContinue | Where-Object { $_.Comment -eq %s } | ForEach-Object { [string]($_.Namespace -join ',') })`, psString(comment)), &rules)
	return rules, err
}

func natExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := powershellJSON(ctx, fmt.Sprintf(`ConvertTo-Json -InputObject @(Get-NetNat -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq %s }).Count`, psString(name)), &n)
	return n > 0, err
}

func firewallRules(ctx context.Context, group string) ([]string, error) {
	var rules []string
	err := powershellJSON(ctx, fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-NetFirewallRule -ErrorAction SilentlyContinue | Where-Object { $_.Group -eq %s } | ForEach-Object { [string]$_.DisplayName })`, psString(group)), &rules)
	return rules, err
}

// wslForwarding maps each vEthernet (WSL*) adapter to its IPv4 forwarding.
func wslForwarding(ctx context.Context) (map[string]string, error) {
	var list []struct {
		Alias      string
		Forwarding string
	}
	err := powershellJSON(ctx, `ConvertTo-Json -Compress -InputObject @(Get-NetIPInterface -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceAlias -like 'vEthernet (WSL*' } | ForEach-Object { [pscustomobject]@{ Alias = [string]$_.InterfaceAlias; Forwarding = [string]$_.Forwarding } })`, &list)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, e := range list {
		m[e.Alias] = e.Forwarding
	}
	return m, nil
}

// serviceInfo is a Windows service as Win32_Service reports it (not localized).
type serviceInfo struct {
	Name      string
	State     string // "Running", "Stopped", ...
	StartMode string // "Manual", "Auto", ...
	StartName string // "LocalSystem", ...
	PathName  string
}

func service(ctx context.Context, name string) (serviceInfo, bool, error) {
	var list []serviceInfo
	err := powershellJSON(ctx, fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-CimInstance -ClassName Win32_Service -Filter "Name='%s'" | ForEach-Object { [pscustomobject]@{ Name = [string]$_.Name; State = [string]$_.State; StartMode = [string]$_.StartMode; StartName = [string]$_.StartName; PathName = [string]$_.PathName } })`, name), &list)
	if err != nil || len(list) == 0 {
		return serviceInfo{}, false, err
	}
	return list[0], true, nil
}

// serviceSDDL is the security descriptor of a service in SDDL (not localized).
func serviceSDDL(ctx context.Context, name string) (string, error) {
	r, err := command(ctx, time.Minute, "", "sc.exe", "sdshow", name)
	if err != nil {
		return "", fmt.Errorf("sc.exe sdshow %s: %v\n%s", name, err, r)
	}
	return strings.TrimSpace(r.Stdout), nil
}

// serviceExe extracts the program from a service command line.
func serviceExe(pathName string) string {
	p := strings.TrimSpace(pathName)
	if strings.HasPrefix(p, `"`) {
		if i := strings.Index(p[1:], `"`); i >= 0 {
			return p[1 : i+1]
		}
	}
	if i := strings.Index(strings.ToLower(p), ".exe"); i >= 0 {
		return p[:i+4]
	}
	return p
}

// caThumbprint is the SHA-1 thumbprint of the PEM certificate in file.
func caThumbprint(file string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("%s holds no PEM certificate", file)
	}
	sum := sha1.Sum(block.Bytes) //nolint:gosec // thumbprint, not a security check
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
}

func rootHas(ctx context.Context, thumbprint string) (bool, error) {
	var found bool
	err := powershellJSON(ctx, fmt.Sprintf(`Test-Path 'Cert:\LocalMachine\Root\%s' | ConvertTo-Json`, thumbprint), &found)
	return found, err
}

// wslHasCAFile reports whether the distro holds the hlab CA file.
func wslHasCAFile(ctx context.Context) (bool, error) {
	r, err := wslRun(ctx, time.Minute, "test -e '"+wslCAFile+"' && echo yes || echo no\n")
	if err != nil {
		return false, fmt.Errorf("look for %s in distro %s: %v\n%s", wslCAFile, harness.distro, err, r)
	}
	return strings.TrimSpace(r.Stdout) == "yes", nil
}

// machinePath is the system PATH as stored for the machine.
func machinePath(ctx context.Context) ([]string, error) {
	var p string
	if err := powershellJSON(ctx, `ConvertTo-Json -InputObject ([string][Environment]::GetEnvironmentVariable('Path', 'Machine'))`, &p); err != nil {
		return nil, err
	}
	return strings.Split(p, ";"), nil
}

func onMachinePath(ctx context.Context, dir string) (bool, error) {
	entries, err := machinePath(ctx)
	if err != nil {
		return false, err
	}
	want := strings.ToLower(strings.TrimRight(filepath.Clean(dir), `\`))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.ToLower(strings.TrimRight(filepath.Clean(e), `\`)) == want {
			return true, nil
		}
	}
	return false, nil
}

// shortcuts finds the HLab shortcuts in the start menus and desktops of the
// machine and of the current user.
func shortcuts() (startMenu, desktop []string) {
	find := func(dir string) []string {
		var found []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(d.Name(), "HLab.lnk") {
				found = append(found, p)
			}
			return nil
		})
		return found
	}
	startMenu = append(find(filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`)),
		find(filepath.Join(os.Getenv("AppData"), `Microsoft\Windows\Start Menu\Programs`))...)
	desktop = append(find(filepath.Join(os.Getenv("Public"), "Desktop")),
		find(filepath.Join(os.Getenv("UserProfile"), "Desktop"))...)
	return startMenu, desktop
}

func trayRunning(ctx context.Context) (bool, error) {
	var n int
	err := powershellJSON(ctx, `ConvertTo-Json -InputObject @(Get-Process -Name 'hlab-tray' -ErrorAction SilentlyContinue).Count`, &n)
	return n > 0, err
}
