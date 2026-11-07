# P0-01 only: internal switch whose host vNIC plays the guest gateway, so guests are
# reachable without the appliance (172.31.1.1 and fe80::1 on-link, guest routes on-link).
if (-not (Get-VMSwitch -Name HLabSpike -ErrorAction SilentlyContinue)) {
    New-VMSwitch -Name HLabSpike -SwitchType Internal | Out-Null
}
$ifIndex = (Get-NetAdapter -Name 'vEthernet (HLabSpike)').ifIndex
Get-NetIPAddress -InterfaceIndex $ifIndex -ErrorAction SilentlyContinue |
    Where-Object { $_.IPAddress -notlike 'fe80::*' -or $_.IPAddress -eq 'fe80::1' } |
    Remove-NetIPAddress -Confirm:$false
New-NetIPAddress -InterfaceIndex $ifIndex -IPAddress 172.31.1.1 -PrefixLength 32 | Out-Null
New-NetIPAddress -InterfaceIndex $ifIndex -IPAddress fe80::1 -PrefixLength 64 | Out-Null
foreach ($prefix in '198.18.16.0/24', '2001:db8:1::/64') {
    Get-NetRoute -DestinationPrefix $prefix -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false
    $hop = if ($prefix -like '*:*') { '::' } else { '0.0.0.0' }
    New-NetRoute -InterfaceIndex $ifIndex -DestinationPrefix $prefix -NextHop $hop | Out-Null
}
Get-NetIPAddress -InterfaceIndex $ifIndex | Format-Table IPAddress, PrefixLength -AutoSize | Out-String
Get-NetRoute -InterfaceIndex $ifIndex | Where-Object DestinationPrefix -match '198.18|2001:db8' |
    Format-Table DestinationPrefix, NextHop -AutoSize | Out-String
