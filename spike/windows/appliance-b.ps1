# P0-03 update: build appliance B from a copy of A's disk (stands in for the new image),
# with the same MACs and seed but no switch connected, and start it.
param([string] $a = 'hlab-appliance', [string] $b = 'hlab-appliance-b', [string] $MacPrefix = '00155DBB00')
$ErrorActionPreference = 'Stop'
$dirA = "C:\HLabSpike\vms\$a"; $dirB = "C:\HLabSpike\vms\$b"
if (Get-VM -Name $b -ErrorAction SilentlyContinue) { Stop-VM $b -TurnOff -Force; Remove-VM $b -Force }
Remove-Item -Recurse -Force $dirB -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $dirB | Out-Null
Stop-VM $a -Force
Copy-Item "$dirA\disk.vhdx", "$dirA\cidata.iso" $dirB
Start-VM $a
$vm = New-VM -Name $b -Generation 2 -MemoryStartupBytes 1GB -VHDPath "$dirB\disk.vhdx" -Path $dirB
Set-VM -VM $vm -ProcessorCount 2 -StaticMemory -AutomaticCheckpointsEnabled $false -Notes 'hlab-spike-appliance'
Get-VMNetworkAdapter -VM $vm | Set-VMNetworkAdapter -StaticMacAddress "${MacPrefix}01"
Add-VMNetworkAdapter -VM $vm -StaticMacAddress "${MacPrefix}02"
Add-VMNetworkAdapter -VM $vm -StaticMacAddress "${MacPrefix}03"
Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate MicrosoftUEFICertificateAuthority
Add-VMDvdDrive -VM $vm -Path "$dirB\cidata.iso"
Set-VMFirmware -VM $vm -FirstBootDevice (Get-VMHardDiskDrive -VM $vm)
Start-VM -VM $vm
(Get-VM $a).Id.Guid
(Get-VM $b).Id.Guid
