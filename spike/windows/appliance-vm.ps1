# Create and start the spike appliance: eth0 host link, eth1 public, eth2 uplink.
param(
    [string] $Name = 'hlab-appliance',
    [string] $Base = 'debian-13-genericcloud-amd64',
    [string] $Seed = 'C:\HLabSpike\seeds\appliance\cidata.iso',
    [int] $Cpus = 2,
    [long] $Memory = 1GB
)
$ErrorActionPreference = 'Stop'
$dir = "C:\HLabSpike\vms\$Name"
if (Get-VM -Name $Name -ErrorAction SilentlyContinue) {
    Stop-VM -Name $Name -TurnOff -Force
    Remove-VM -Name $Name -Force
}
Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $dir | Out-Null
Copy-Item $Seed "$dir\cidata.iso"
New-VHD -Path "$dir\disk.vhdx" -ParentPath "C:\HLabSpike\images\$Base.vhdx" -Differencing | Out-Null
Resize-VHD -Path "$dir\disk.vhdx" -SizeBytes 8GB
$vm = New-VM -Name $Name -Generation 2 -MemoryStartupBytes $Memory -VHDPath "$dir\disk.vhdx" -Path $dir -SwitchName HLabLink
Set-VM -VM $vm -ProcessorCount $Cpus -StaticMemory -AutomaticCheckpointsEnabled $false -Notes 'hlab-spike-appliance'
Get-VMNetworkAdapter -VM $vm | Set-VMNetworkAdapter -StaticMacAddress 00155DBB0001
Add-VMNetworkAdapter -VM $vm -SwitchName HLabPublic -StaticMacAddress 00155DBB0002
Add-VMNetworkAdapter -VM $vm -SwitchName 'Default Switch' -StaticMacAddress 00155DBB0003
Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate MicrosoftUEFICertificateAuthority
Add-VMDvdDrive -VM $vm -Path "$dir\cidata.iso"
Set-VMFirmware -VM $vm -FirstBootDevice (Get-VMHardDiskDrive -VM $vm)
Start-VM -VM $vm
