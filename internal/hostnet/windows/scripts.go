//go:build windows

package windows

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/webcodingcat/hlab/internal/hostnet"
)

// Scripts use single-quoted literals only, one statement per line, and every
// cmdlet with its parameters inline on one line.

// q quotes s as a PowerShell single-quoted string.
func q(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// alias is the interface alias of the host adapter of switch name.
func alias(name string) string {
	return "vEthernet (" + name + ")"
}

// script joins lines after a header that stops at the first error.
func script(lines ...string) string {
	return strings.Join(append([]string{"$ErrorActionPreference = 'Stop'"}, lines...), "\n")
}

// wslOrHlab filters NetIPInterface objects to the hlab and WSL adapters.
func wslOrHlab(hlabAlias string) string {
	return "Where-Object { $_.InterfaceAlias -eq " + q(hlabAlias) + " -or $_.InterfaceAlias -like 'vEthernet (WSL*' }"
}

// checkScript lists host IPv4 addresses and routes outside the hlab adapter
// and WinNAT instances not named tag.
func checkScript(hlabAlias, tag string) string {
	return script(
		"$a = @(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceAlias -ne "+q(hlabAlias)+" } | ForEach-Object { [pscustomobject]@{ kind = 'address'; alias = [string]$_.InterfaceAlias; prefix = [string]$_.IPAddress + '/' + [string]$_.PrefixLength } })",
		"$r = @(Get-NetRoute -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceAlias -ne "+q(hlabAlias)+" } | ForEach-Object { [pscustomobject]@{ kind = 'route'; alias = [string]$_.InterfaceAlias; prefix = [string]$_.DestinationPrefix } })",
		"$n = @(Get-NetNat -ErrorAction SilentlyContinue | Where-Object { $_.Name -ne "+q(tag)+" } | ForEach-Object { [pscustomobject]@{ kind = 'nat'; alias = [string]$_.Name; prefix = [string]$_.InternalIPInterfaceAddressPrefix } })",
		"$all = @($a) + @($r) + @($n)",
		"if ($all.Count -gt 0) { ConvertTo-Json -InputObject $all -Compress }",
	)
}

// forwardingQueryScript lists the IPv4 forwarding values of the hlab and WSL adapters.
func forwardingQueryScript(hlabAlias string) string {
	return script(
		"$f = @(Get-NetIPInterface -AddressFamily IPv4 -ErrorAction SilentlyContinue | "+wslOrHlab(hlabAlias)+" | ForEach-Object { [pscustomobject]@{ alias = [string]$_.InterfaceAlias; forwarding = [string]$_.Forwarding } })",
		"if ($f.Count -gt 0) { ConvertTo-Json -InputObject $f -Compress }",
	)
}

// restoreScript sets the recorded forwarding values; adapters that are gone
// are skipped. It returns "" when there is nothing to restore.
func restoreScript(entries []forwarding) string {
	var lines []string
	for _, e := range entries {
		if e.Forwarding != "Enabled" && e.Forwarding != "Disabled" {
			continue
		}
		lines = append(lines, "Set-NetIPInterface -InterfaceAlias "+q(e.Alias)+" -AddressFamily IPv4 -Forwarding "+e.Forwarding+" -ErrorAction SilentlyContinue")
	}
	if len(lines) == 0 {
		return ""
	}
	return script(lines...)
}

// upScript sets the host IP, the route of the public range, WinNAT,
// forwarding and the firewall rule for the API and console ports.
func upScript(cfg hostnet.Config, tag, program string) string {
	a := q(alias(cfg.SwitchName))
	host := q(cfg.HostAddr.Addr().String())
	appliance := q(cfg.ApplianceAddr.String())
	public := q(cfg.PublicRange.String())
	return script(
		fmt.Sprintf("if (-not (Get-NetIPAddress -InterfaceAlias %s -AddressFamily IPv4 -IPAddress %s -ErrorAction SilentlyContinue)) { New-NetIPAddress -InterfaceAlias %s -AddressFamily IPv4 -IPAddress %s -PrefixLength %d | Out-Null }",
			a, host, a, host, cfg.HostAddr.Bits()),
		fmt.Sprintf("if (-not (Get-NetRoute -DestinationPrefix %s -InterfaceAlias %s -PolicyStore ActiveStore -ErrorAction SilentlyContinue)) { New-NetRoute -DestinationPrefix %s -InterfaceAlias %s -NextHop %s -PolicyStore ActiveStore | Out-Null }",
			public, a, public, a, appliance),
		fmt.Sprintf("$nat = Get-NetNat -Name %s -ErrorAction SilentlyContinue", q(tag)),
		fmt.Sprintf("if ($nat -and $nat.InternalIPInterfaceAddressPrefix -ne %s) { Remove-NetNat -Name %s -Confirm:$false; $nat = $null }", public, q(tag)),
		fmt.Sprintf("if (-not $nat) { New-NetNat -Name %s -InternalIPInterfaceAddressPrefix %s | Out-Null }", q(tag), public),
		"Get-NetIPInterface -AddressFamily IPv4 -ErrorAction SilentlyContinue | "+wslOrHlab(alias(cfg.SwitchName))+" | Set-NetIPInterface -Forwarding Enabled",
		fmt.Sprintf("Remove-NetFirewallRule -Group %s -ErrorAction SilentlyContinue", q(tag)),
		fmt.Sprintf("New-NetFirewallRule -DisplayName %s -Group %s -Direction Inbound -Action Allow -Protocol TCP -LocalPort %d,%d -LocalAddress %s -RemoteAddress %s -InterfaceAlias %s -Program %s -Profile Any | Out-Null",
			q(tag+" API"), q(tag), apiPort, consolePort, host, appliance, a, q(program)),
	)
}

// nrptScript makes the NRPT rules with Comment tag exactly one rule per zone
// pointing to appliance.
func nrptScript(tag string, appliance netip.Addr, zones []string) string {
	seen := map[string]bool{}
	var want []string
	for _, z := range zones {
		z = "." + strings.Trim(strings.ToLower(strings.TrimSpace(z)), ".")
		if z == "." || seen[z] {
			continue
		}
		seen[z] = true
		want = append(want, q(z))
	}
	return script(
		"$want = @("+strings.Join(want, ", ")+")",
		"$seen = @{}",
		"$have = @(Get-DnsClientNrptRule | Where-Object { $_.Comment -eq "+q(tag)+" })",
		"foreach ($r in $have) { $ns = [string](@($r.Namespace)[0]); if (($want -notcontains $ns) -or $seen.ContainsKey($ns) -or (@($r.NameServers) -notcontains "+q(appliance.String())+")) { Remove-DnsClientNrptRule -Name $r.Name -Force } else { $seen[$ns] = $true } }",
		"foreach ($ns in $want) { if (-not $seen.ContainsKey($ns)) { Add-DnsClientNrptRule -Namespace $ns -NameServers "+q(appliance.String())+" -Comment "+q(tag)+" } }",
	)
}

// downScript removes the NRPT rules, the firewall rule group, WinNAT, the
// routes and the host IP that carry the profile's tag or adapter.
func downScript(tag, hlabAlias string) string {
	a := q(hlabAlias)
	return script(
		"Get-DnsClientNrptRule | Where-Object { $_.Comment -eq "+q(tag)+" } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force }",
		"Remove-NetFirewallRule -Group "+q(tag)+" -ErrorAction SilentlyContinue",
		"Get-NetNat -Name "+q(tag)+" -ErrorAction SilentlyContinue | Remove-NetNat -Confirm:$false",
		"Get-NetRoute -InterfaceAlias "+a+" -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.NextHop -ne '0.0.0.0' } | Remove-NetRoute -Confirm:$false",
		"Get-NetIPAddress -InterfaceAlias "+a+" -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.PrefixOrigin -eq 'Manual' } | Remove-NetIPAddress -Confirm:$false",
	)
}

// dnsServersScript lists the IPv4 DNS servers of interfaces with a default
// route, without the hlab, WSL and loopback adapters.
func dnsServersScript(hlabAlias string) string {
	return script(
		"$ifs = @(Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty ifIndex -Unique)",
		"$s = @(foreach ($i in $ifs) { Get-DnsClientServerAddress -InterfaceIndex $i -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceAlias -ne "+q(hlabAlias)+" -and $_.InterfaceAlias -notlike 'vEthernet (WSL*' -and $_.InterfaceAlias -notlike 'Loopback*' } | ForEach-Object { $_.ServerAddresses } })",
		"if ($s.Count -gt 0) { ConvertTo-Json -InputObject $s -Compress }",
	)
}
