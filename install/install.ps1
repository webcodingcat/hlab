#Requires -RunAsAdministrator
<#
.SYNOPSIS
Install or uninstall HLab on Windows 11.

.DESCRIPTION
Run as administrator, from the folder that bin/dev package fills
(hlabd.exe, hlab.exe, the Linux CLI hlab, hlab-appliance.vhdx):

  install.ps1                       install, or update an installed HLab
  install.ps1 -Uninstall            remove HLab with its data
  install.ps1 -Uninstall -KeepData  remove HLab, keep C:\ProgramData\HLab
                                    and the lab's VMs

Installing turns on Hyper-V (Windows may want a restart), copies the files
to C:\Program Files\HLab, adds the start menu entry "HLab" (runs hlabd as
administrator), puts hlab.exe on the PATH, registers the agent's hv_sock
service, adds the firewall rule "HLab" (hlabd.exe, TCP 8080, 8081 and 8090
on 198.18.0.1), creates the data folder and copies the Linux CLI to
~/.local/bin of the default WSL distribution, if there is one.

Uninstalling stops HLab if it runs and removes the lab's switches, NRPT
rules, the hv_sock service, the firewall rule, the start menu entry, the PATH
entry, the program folder and the Linux CLI in WSL; the lab's VMs and the
data folder go unless -KeepData.
#>
[CmdletBinding()]
param(
    [switch]$Uninstall,

    [switch]$KeepData,

    # where the files to install are; the script's folder by default
    [string]$Source
)

$ErrorActionPreference = 'Stop'
# Windows PowerShell leaves $PSScriptRoot empty in parameter defaults
if (-not $Source) {
    $Source = $PSScriptRoot
}
$ProgramDir = Join-Path $env:ProgramFiles 'HLab'
$DataDir = Join-Path $env:ProgramData 'HLab'
$Hlabd = Join-Path $ProgramDir 'hlabd.exe'
$Files = @('hlabd.exe', 'hlab.exe', 'hlab', 'hlab-appliance.vhdx')
$Shortcut = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\HLab.lnk'
$FirewallRule = 'hlab'
# hlabd reaches the agent's vsock port 5000 through this service ID
$ServiceKey = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Virtualization\GuestCommunicationServices\00001388-facb-11e6-bd58-64006a7986d3'
# what the lab marks its VMs, switches and NRPT rules with
$VMTag = 'hlab'
$SwitchPattern = 'HLab-*'
$NrptComment = 'hlab'

function Close-HLab {
    <#
    .SYNOPSIS
    Stop the installed HLab if it runs, and wait until it has stopped. Fail
    if an hlabd from another folder runs: it owns the lab's switches and
    names now.
    #>
    $running = @(Get-Process -Name hlabd -ErrorAction SilentlyContinue)
    $others = @($running | Where-Object Path -NE $Hlabd)
    if ($others.Count -gt 0) {
        throw "hlabd runs from $($others[0].Path): stop it first (hlab stop)"
    }
    if ($running.Count -eq 0) {
        return
    }
    Write-Output 'stopping HLab'
    & (Join-Path $ProgramDir 'hlab.exe') stop
    if ($LASTEXITCODE -ne 0) {
        throw 'hlab stop failed'
    }
    $running | Wait-Process -Timeout 60
}

function Invoke-WslCommand {
    <#
    .SYNOPSIS
    Run a shell command in the default WSL distribution, in the program
    folder; warn instead of failing, since WSL is optional.
    #>
    param(
        [Parameter(Mandatory)]
        [string]$Command
    )
    if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) {
        return
    }
    # a native program's error output must not stop the script
    $ErrorActionPreference = 'Continue'
    # Windows PowerShell passes no double quotes inside arguments, and the
    # path has a space: so the command runs in the folder and quotes nothing
    $output = & wsl.exe --cd $ProgramDir -e sh -c $Command 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "WSL: '$Command' failed: $output"
    }
}

function Add-StartMenuEntry {
    <#
    .SYNOPSIS
    The start menu entry "HLab", which runs hlabd as administrator in a
    minimized window: closing the window or Ctrl+C stops HLab.
    #>
    $shell = New-Object -ComObject WScript.Shell
    $link = $shell.CreateShortcut($Shortcut)
    $link.TargetPath = $Hlabd
    $link.WorkingDirectory = $ProgramDir
    $link.Description = 'HLab, a local stand-in for Hetzner Cloud'
    $link.WindowStyle = 7 # minimized
    $link.Save()
    # "Run as administrator" is bit 0x20 of the link flags at byte 0x15;
    # WScript.Shell cannot set it
    $bytes = [IO.File]::ReadAllBytes($Shortcut)
    $bytes[0x15] = $bytes[0x15] -bor 0x20
    [IO.File]::WriteAllBytes($Shortcut, $bytes)
}

function Edit-MachinePath {
    <#
    .SYNOPSIS
    Add the program folder to the machine's PATH, or remove it.
    #>
    param(
        [switch]$Remove
    )
    $entries = @([Environment]::GetEnvironmentVariable('Path', 'Machine') -split ';' |
            Where-Object { $_ -and $_.TrimEnd('\') -ne $ProgramDir })
    if (-not $Remove) {
        $entries += $ProgramDir
    }
    [Environment]::SetEnvironmentVariable('Path', ($entries -join ';'), 'Machine')
}

function Install-HLab {
    <#
    .SYNOPSIS
    Install HLab from a folder, or update it.
    #>
    param(
        [Parameter(Mandatory)]
        [string]$Source
    )
    foreach ($file in $Files) {
        if (-not (Test-Path (Join-Path $Source $file))) {
            throw "$file is missing in $Source (bin/dev package builds the folder)"
        }
    }
    $restart = $false
    $feature = Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All
    if ($feature.State -ne 'Enabled') {
        Write-Output 'turning on Hyper-V'
        $result = Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All -All -NoRestart
        $restart = $result.RestartNeeded
    }
    if (Test-Path $Hlabd) {
        Close-HLab
    }
    New-Item -ItemType Directory -Force -Path $ProgramDir, $DataDir | Out-Null
    foreach ($file in $Files) {
        Copy-Item -Path (Join-Path $Source $file) -Destination $ProgramDir -Force
    }
    Add-StartMenuEntry
    Edit-MachinePath
    New-Item -Path $ServiceKey -Force | Out-Null
    Set-ItemProperty -Path $ServiceKey -Name ElementName -Value 'HLab agent'
    Remove-NetFirewallRule -Name $FirewallRule -ErrorAction SilentlyContinue
    # the guests reach 8080 and 8081 through the appliance, WSL2 all three;
    # the appliance keeps the guests off 8090
    New-NetFirewallRule -Name $FirewallRule -DisplayName 'HLab' -Direction Inbound -Action Allow `
        -Program $Hlabd -Protocol TCP -LocalPort 8080, 8081, 8090 -LocalAddress 198.18.0.1 `
        -Profile Any | Out-Null
    Invoke-WslCommand 'mkdir -p $HOME/.local/bin && cp hlab $HOME/.local/bin/hlab && chmod 755 $HOME/.local/bin/hlab'
    Write-Output "installed HLab into $ProgramDir; start it with the start menu entry HLab or hlab start"
    if ($restart) {
        Write-Warning 'Hyper-V was turned on: restart Windows before starting HLab'
    }
}

function Uninstall-HLab {
    <#
    .SYNOPSIS
    Remove HLab and what it leaves on the machine; the data folder too,
    unless KeepData.
    #>
    param(
        [switch]$KeepData
    )
    Close-HLab
    if (Get-Module -ListAvailable Hyper-V) {
        # kept data keeps the VMs: their disks are in the data folder, and a
        # server without its VM would be built anew over its disk
        $vms = if ($KeepData) { @() } else { @(Get-VM | Where-Object Notes -EQ $VMTag) }
        foreach ($vm in $vms) {
            Write-Output "removing the VM $($vm.Name)"
            if ($vm.State -ne 'Off') {
                $vm | Stop-VM -TurnOff -Force
            }
            $vm | Remove-VM -Force
        }
        # the host's NIC of HLab-Link goes with its switch, and its
        # addresses and routes with it
        Get-VMSwitch -Name $SwitchPattern -ErrorAction SilentlyContinue | Remove-VMSwitch -Force
    }
    Get-DnsClientNrptRule | Where-Object Comment -EQ $NrptComment | ForEach-Object {
        Remove-DnsClientNrptRule -Name $_.Name -Force
    }
    Remove-Item -Path $ServiceKey -Recurse -ErrorAction SilentlyContinue
    Remove-NetFirewallRule -Name $FirewallRule -ErrorAction SilentlyContinue
    Remove-Item -Path $Shortcut -ErrorAction SilentlyContinue
    Edit-MachinePath -Remove
    Invoke-WslCommand 'rm -f $HOME/.local/bin/hlab'
    Remove-Item -Path $ProgramDir -Recurse -Force -ErrorAction SilentlyContinue
    if ($KeepData) {
        Write-Output "uninstalled HLab; its data stays in $DataDir"
        return
    }
    Remove-Item -Path $DataDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Output 'uninstalled HLab'
}

if ($Uninstall) {
    Uninstall-HLab -KeepData:$KeepData
} else {
    Install-HLab -Source $Source
}
