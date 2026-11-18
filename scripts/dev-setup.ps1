#Requires -Version 5.1
<#
.SYNOPSIS
    Registers the hlab development service on a developer machine.

.DESCRIPTION
    Run once per machine in an administrator terminal, after "task build".
    Afterwards build, tests and the dev service run without elevated rights.

    It runs "bin\hlabd.exe install --dev" of this working copy (profile
    "dev"), which sets up:
      - the Windows service "hlab-dev" (manual start, LocalSystem) running
        bin\hlabd.exe of this working copy
      - the service security descriptor of the installer: Interactive Users
        may query, start and stop the service, never change its configuration
      - the pre-shutdown timeout of 180 seconds
      - the folder %ProgramData%\hlab-dev and settings.toml in it, which
        Interactive Users may modify

    Before that it removes an older dev service named "hlab", but only if it
    runs bin\hlabd.exe of this working copy (an installed hlab is never
    touched).

    SECURITY: the service runs a program from a folder the developer account
    can write to, as LocalSystem. Every process of that account can therefore
    act with system rights through the service. Acceptable on a development
    machine only.

    The dev service and an installed hlab (service "hlab") may both be
    installed; only one of their clouds runs at a time.

.EXAMPLE
    .\scripts\dev-setup.ps1
#>
[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$LegacyServiceName = 'hlab'
$RepoRoot = Split-Path -Parent $PSScriptRoot
$ExePath = Join-Path $RepoRoot 'bin\hlabd.exe'

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

function Register-DevService {
    if (-not (Test-Path -LiteralPath $ExePath)) {
        throw "$ExePath not found. Run 'task build' first and run this script again."
    }
    & $ExePath install --dev
    if ($LASTEXITCODE -ne 0) {
        throw "$ExePath install --dev failed with code $LASTEXITCODE"
    }
    Write-Host "Service 'hlab-dev' registered: $ExePath"
    Write-Host "Data folder: $(Join-Path $env:ProgramData 'hlab-dev')"
}

function Remove-LegacyDevService {
    # Before the release and dev profiles existed, the dev service was named
    # "hlab", the name of the installed product. Remove it only if it runs
    # this working copy's binary.
    $legacy = Get-CimInstance -ClassName Win32_Service -Filter "Name='$LegacyServiceName'"
    if (-not $legacy) {
        return
    }
    $legacyPath = $legacy.PathName.Trim('"')
    if ($legacyPath -ne $ExePath) {
        Write-Host "Service '$LegacyServiceName' runs $legacyPath, not this working copy; left as it is."
        return
    }
    if ($legacy.State -ne 'Stopped') {
        throw "The old dev service '$LegacyServiceName' is $($legacy.State). Stop it first and run this script again."
    }
    Invoke-Native 'sc.exe' @('delete', $LegacyServiceName)
    Write-Host "Old dev service '$LegacyServiceName' removed."
}

Assert-Elevated
Remove-LegacyDevService
Register-DevService
