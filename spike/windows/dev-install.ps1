# P0-13: one-time admin setup for the development loop (operator approved).
# Scheduled task "HLab dev" starts the dev binary elevated without a UAC prompt
# (development machines only); firewall rule lets WSL2 reach the control port.
$ErrorActionPreference = 'Stop'
$binary = 'C:\HLabDev\bin\hlabd-dev.exe'
$action = New-ScheduledTaskAction -Execute $binary -WorkingDirectory 'C:\HLabDev'
$principal = New-ScheduledTaskPrincipal -UserId 'SAIJBOOK\Saij' -LogonType Interactive -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -TaskName 'HLab dev' -Action $action -Principal $principal -Settings $settings -Force | Out-Null
'task: ' + (Get-ScheduledTask -TaskName 'HLab dev').State
Get-NetFirewallRule -Name hlab-dev-control -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -Name hlab-dev-control -DisplayName 'HLab dev control port' -Direction Inbound `
    -Protocol TCP -LocalPort 8090 -LocalAddress 198.18.0.1 -Program $binary -Action Allow | Out-Null
'firewall rule: ' + (Get-NetFirewallRule -Name hlab-dev-control).Enabled
