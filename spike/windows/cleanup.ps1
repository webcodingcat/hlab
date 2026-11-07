# Remove everything the phase 0 spike created on Windows, except C:\HLabSpike itself
# (the runner lives there; it is deleted after the runner stops).
$ErrorActionPreference = 'Continue'
$vms = Get-VM | Where-Object { $_.Notes -like 'hlab-spike*' -or $_.Name -in 'old-snap', 'hlab-appliance', 'hlab-appliance-b' }
$vms | Stop-VM -TurnOff -Force
$vms | Remove-VM -Force
'VMs removed: ' + $vms.Count + '; left: ' + ((Get-VM).Name -join ', ')
foreach ($switch in 'HLabSpike', 'HLabLink', 'HLabPublic', 'HLabPrivate') {
    Get-VMSwitch -Name $switch -ErrorAction SilentlyContinue | Remove-VMSwitch -Force
}
'switches left: ' + ((Get-VMSwitch).Name -join ', ')
foreach ($prefix in '198.18.0.0/15', '2001:db8::/32', '198.18.16.0/24', '2001:db8:1::/64') {
    Get-NetRoute -DestinationPrefix $prefix -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false
}
'lab routes left: ' + @(Get-NetRoute | Where-Object DestinationPrefix -match '^198\.1[89]\.|^2001:db8').Count
Get-DnsClientNrptRule | Where-Object Comment -eq 'hlab-spike' | Remove-DnsClientNrptRule -Force
'NRPT rules left: ' + @(Get-DnsClientNrptRule).Count
Remove-Item 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Virtualization\GuestCommunicationServices\00001388-facb-11e6-bd58-64006a7986d3' -ErrorAction SilentlyContinue
Get-Process hlabd-dev -ErrorAction SilentlyContinue | Stop-Process -Force
Unregister-ScheduledTask -TaskName 'HLab dev' -Confirm:$false -ErrorAction SilentlyContinue
Get-NetFirewallRule -Name hlab-dev-control -ErrorAction SilentlyContinue | Remove-NetFirewallRule
'task: ' + [bool](Get-ScheduledTask -TaskName 'HLab dev' -ErrorAction SilentlyContinue) + ', firewall rule: ' + [bool](Get-NetFirewallRule -Name hlab-dev-control -ErrorAction SilentlyContinue)
Start-Sleep 1
Remove-Item -Recurse -Force 'C:\HLabDev', 'C:\ProgramData\HLab' -ErrorAction SilentlyContinue
'folders HLabDev/ProgramData\HLab left: ' + (Test-Path 'C:\HLabDev') + '/' + (Test-Path 'C:\ProgramData\HLab')
Remove-LocalGroupMember -SID S-1-5-32-578 -Member 'SAIJBOOK\Saij' -ErrorAction SilentlyContinue
'Hyper-V-Administratoren: ' + ((Get-LocalGroupMember -SID S-1-5-32-578).Name -join ', ')
