# P0-10: a long-running PowerShell that reads one JSON request per line on stdin and
# answers with one JSON line, like the planned Hypervisor implementation.
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
while ($null -ne ($line = [Console]::In.ReadLine())) {
    $request = $line | ConvertFrom-Json
    try {
        $result = switch ($request.op) {
            'state' { (Get-VM -Name $request.name).State.ToString() }
            'list'  { @(Get-VM | Where-Object Notes -eq 'hlab-spike' | ForEach-Object Name) }
            'create' {
                $dir = "C:\HLabSpike\vms\$($request.name)"
                New-Item -ItemType Directory -Force $dir | Out-Null
                New-VHD -Path "$dir\disk.vhdx" -ParentPath 'C:\HLabSpike\images\debian-13-genericcloud-amd64.vhdx' -Differencing | Out-Null
                $vm = New-VM -Name $request.name -Generation 2 -MemoryStartupBytes 1GB -VHDPath "$dir\disk.vhdx" -Path $dir -SwitchName HLabPublic
                Set-VM -VM $vm -ProcessorCount 1 -StaticMemory -AutomaticCheckpointsEnabled $false -Notes 'hlab-spike-p010'
                Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate MicrosoftUEFICertificateAuthority
                Start-VM -VM $vm
                (Get-VM -Name $request.name).State.ToString()
            }
            'delete' {
                Stop-VM -Name $request.name -TurnOff -Force
                Remove-VM -Name $request.name -Force
                Remove-Item -Recurse -Force "C:\HLabSpike\vms\$($request.name)"
                'deleted'
            }
        }
        [Console]::Out.WriteLine((@{ ok = $true; result = $result } | ConvertTo-Json -Compress))
    } catch {
        [Console]::Out.WriteLine((@{ ok = $false; error = $_.Exception.Message } | ConvertTo-Json -Compress))
    }
}
