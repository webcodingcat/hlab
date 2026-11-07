# Create and start a Gen2 test VM on a differencing disk with a cidata seed.
param(
    [Parameter(Mandatory)] [string] $Name,
    [Parameter(Mandatory)] [string] $Base,
    [Parameter(Mandatory)] [string] $Mac,
    [ValidateSet('iso', 'vhdx')] [string] $Seed = 'iso',
    [ValidateSet('on', 'off')] [string] $SecureBoot = 'on',
    [string] $Switch = 'HLabSpike'
)
$ErrorActionPreference = 'Stop'
$dir = "C:\HLabSpike\vms\$Name"
if (Get-VM -Name $Name -ErrorAction SilentlyContinue) {
    Stop-VM -Name $Name -TurnOff -Force
    Remove-VM -Name $Name -Force
}
Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $dir | Out-Null
Copy-Item "C:\HLabSpike\seeds\$Name\cidata.$Seed" $dir
New-VHD -Path "$dir\disk.vhdx" -ParentPath "C:\HLabSpike\images\$Base.vhdx" -Differencing | Out-Null
Resize-VHD -Path "$dir\disk.vhdx" -SizeBytes 20GB
$vm = New-VM -Name $Name -Generation 2 -MemoryStartupBytes 2GB -VHDPath "$dir\disk.vhdx" -Path $dir -SwitchName $Switch
Set-VM -VM $vm -ProcessorCount 2 -StaticMemory -AutomaticCheckpointsEnabled $false -Notes 'hlab-spike'
Set-VMNetworkAdapter -VMName $Name -StaticMacAddress ($Mac -replace ':', '')
if ($SecureBoot -eq 'on') {
    Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate MicrosoftUEFICertificateAuthority
} else {
    Set-VMFirmware -VM $vm -EnableSecureBoot Off
}
if ($Seed -eq 'iso') {
    Add-VMDvdDrive -VM $vm -Path "$dir\cidata.iso"
} else {
    Add-VMHardDiskDrive -VM $vm -Path "$dir\cidata.vhdx"
}
Set-VMFirmware -VM $vm -FirstBootDevice (Get-VMHardDiskDrive -VM $vm | Select-Object -First 1)
Start-VM -VM $vm
(Get-Date).ToString('o')
