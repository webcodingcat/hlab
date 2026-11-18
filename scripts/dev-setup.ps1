#Requires -Version 5.1
<#
.SYNOPSIS
    Registers or removes the hlab development service on a developer machine.

.DESCRIPTION
    Run once per machine in an administrator terminal. Afterwards build,
    tests and the dev service run without elevated rights.

    "register" sets up:
      - the Windows service "hlab" (manual start, LocalSystem) running
        bin\hlabd.exe of this working copy
      - the service security descriptor of the installer: Interactive Users
        may query, start and stop the service, never change its configuration
      - the pre-shutdown timeout of 180 seconds
      - the folder %ProgramData%\hlab and settings.toml in it, which
        Interactive Users may modify

    Until hlabd implements "hlabd install --dev" (phase 01), this script does
    the same with sc.exe and icacls.

    SECURITY: the service runs a program from a folder the developer account
    can write to, as LocalSystem. Every process of that account can therefore
    act with system rights through the service. Acceptable on a development
    machine only. "unregister" takes it back.

    The dev service and an installed hlab cannot exist on the same machine.

.EXAMPLE
    .\scripts\dev-setup.ps1 register

.EXAMPLE
    .\scripts\dev-setup.ps1 unregister
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('register', 'unregister')]
    [string]$Action = 'register'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ServiceName = 'hlab'
$DisplayName = 'hlab (development)'
$RepoRoot = Split-Path -Parent $PSScriptRoot
$ExePath = Join-Path $RepoRoot 'bin\hlabd.exe'
$DataDir = Join-Path $env:ProgramData 'hlab'
$SettingsFile = Join-Path $DataDir 'settings.toml'
$PreshutdownTimeoutMs = 180000
# Default service rights, with Interactive Users (IU) widened to query (LC and
# others), start (RP) and stop (WP); no change-config (DC). Same as the
# installer.
$ServiceSddl = 'D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWRPWPLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)'
$InteractiveUsersSid = '*S-1-5-4'

function Assert-Elevated {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'This script needs an administrator terminal. Start PowerShell with "Run as administrator" and run it again.'
    }
}

function Invoke-Native {
    param([string]$Exe, [string[]]$Arguments)
    $output = & $Exe @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "$Exe $($Arguments -join ' ') failed with code ${LASTEXITCODE}: $($output -join ' ')"
    }
}

function Set-PreshutdownTimeout {
    if (-not ('Hlab.ServiceConfig' -as [type])) {
        Add-Type -Namespace Hlab -Name ServiceConfig -MemberDefinition @'
[StructLayout(LayoutKind.Sequential)]
public struct PreshutdownInfo { public uint Timeout; }

[DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
public static extern IntPtr OpenSCManager(IntPtr machine, IntPtr database, uint access);

[DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
public static extern IntPtr OpenService(IntPtr manager, string name, uint access);

[DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
public static extern bool ChangeServiceConfig2(IntPtr service, uint infoLevel, ref PreshutdownInfo info);

[DllImport("advapi32.dll", SetLastError = true)]
public static extern bool CloseServiceHandle(IntPtr handle);
'@
    }
    $scManagerConnect = 0x0001
    $serviceChangeConfig = 0x0002
    $serviceConfigPreshutdownInfo = 7

    $manager = [Hlab.ServiceConfig]::OpenSCManager([IntPtr]::Zero, [IntPtr]::Zero, $scManagerConnect)
    if ($manager -eq [IntPtr]::Zero) {
        throw "OpenSCManager failed with error $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
    }
    try {
        $service = [Hlab.ServiceConfig]::OpenService($manager, $ServiceName, $serviceChangeConfig)
        if ($service -eq [IntPtr]::Zero) {
            throw "OpenService failed with error $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
        }
        try {
            $info = New-Object 'Hlab.ServiceConfig+PreshutdownInfo'
            $info.Timeout = $PreshutdownTimeoutMs
            if (-not [Hlab.ServiceConfig]::ChangeServiceConfig2($service, $serviceConfigPreshutdownInfo, [ref]$info)) {
                throw "ChangeServiceConfig2 failed with error $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
            }
        }
        finally {
            [Hlab.ServiceConfig]::CloseServiceHandle($service) | Out-Null
        }
    }
    finally {
        [Hlab.ServiceConfig]::CloseServiceHandle($manager) | Out-Null
    }
}

function Register-DevService {
    $binPath = '"' + $ExePath + '"'
    $existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($existing) {
        if ($existing.Status -ne 'Stopped') {
            throw "Service $ServiceName is $($existing.Status). Stop it first (hlab cloud stop) and run this script again."
        }
        Invoke-Native 'sc.exe' @('config', $ServiceName, 'binPath=', $binPath, 'start=', 'demand', 'obj=', 'LocalSystem', 'DisplayName=', $DisplayName)
    }
    else {
        New-Service -Name $ServiceName -BinaryPathName $binPath -DisplayName $DisplayName -StartupType Manual | Out-Null
    }
    Invoke-Native 'sc.exe' @('sdset', $ServiceName, $ServiceSddl)
    Set-PreshutdownTimeout

    New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
    if (-not (Test-Path -LiteralPath $SettingsFile)) {
        New-Item -ItemType File -Path $SettingsFile | Out-Null
    }
    Invoke-Native 'icacls.exe' @($SettingsFile, '/grant', "${InteractiveUsersSid}:(M)")

    Write-Host "Service '$ServiceName' registered: $ExePath"
    Write-Host "Data folder: $DataDir"
}

function Unregister-DevService {
    $existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($existing) {
        if ($existing.Status -ne 'Stopped') {
            Stop-Service -Name $ServiceName
        }
        Invoke-Native 'sc.exe' @('delete', $ServiceName)
        Write-Host "Service '$ServiceName' removed. $DataDir is kept."
    }
    else {
        Write-Host "Service '$ServiceName' is not registered."
    }
}

Assert-Elevated
switch ($Action) {
    'register' { Register-DevService }
    'unregister' { Unregister-DevService }
}
