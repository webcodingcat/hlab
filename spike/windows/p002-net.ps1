# P0-02: host link (internal switch with host vNIC), public switch (private, no host
# vNIC), lab routes via the appliance. Replaces the P0-01 shortcut.
$ErrorActionPreference = 'Stop'
foreach ($prefix in '198.18.16.0/24', '2001:db8:1::/64', '198.18.0.0/15', '2001:db8::/32') {
    Get-NetRoute -DestinationPrefix $prefix -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false
}
if (-not (Get-VMSwitch -Name HLabLink -ErrorAction SilentlyContinue)) {
    New-VMSwitch -Name HLabLink -SwitchType Internal | Out-Null
}
if (-not (Get-VMSwitch -Name HLabPublic -ErrorAction SilentlyContinue)) {
    New-VMSwitch -Name HLabPublic -SwitchType Private | Out-Null
}
$ifIndex = (Get-NetAdapter -Name 'vEthernet (HLabLink)').ifIndex
Get-NetIPAddress -InterfaceIndex $ifIndex -ErrorAction SilentlyContinue |
    Where-Object { $_.IPAddress -notlike 'fe80::*' } | Remove-NetIPAddress -Confirm:$false
New-NetIPAddress -InterfaceIndex $ifIndex -IPAddress 198.18.0.1 -PrefixLength 29 | Out-Null
New-NetIPAddress -InterfaceIndex $ifIndex -IPAddress 2001:db8:0:ffff::1 -PrefixLength 64 | Out-Null
New-NetRoute -InterfaceIndex $ifIndex -DestinationPrefix 198.18.0.0/15 -NextHop 198.18.0.2 | Out-Null
New-NetRoute -InterfaceIndex $ifIndex -DestinationPrefix 2001:db8::/32 -NextHop 2001:db8:0:ffff::2 | Out-Null
Get-NetRoute -InterfaceIndex $ifIndex | Where-Object DestinationPrefix -match '198.18|2001:db8' |
    Format-Table DestinationPrefix, NextHop -AutoSize | Out-String
