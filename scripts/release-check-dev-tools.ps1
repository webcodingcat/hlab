#Requires -Version 5.1
<#
.SYNOPSIS
    Installs the tools of the elevated installer suite inside the
    development test VM and clones the repository bundle.

.DESCRIPTION
    Run in an administrator PowerShell inside the VM release-check-dev:

        powershell -ExecutionPolicy Bypass -File C:\Temp\release-check-dev-tools.ps1

    It installs, each step skipped when already done:
      - with winget: Git, Go, Node.js LTS, Task, .NET SDK 8
      - WiX 7.0.1 with its licence accepted and its util and bootstrapper
        extensions, and Wails v3.1.0
      - WSL2 with Ubuntu-24.04
    and refreshes PATH between the steps, so no new terminal is needed.
    The versions must match docs\development\setup.md, "Tools"; the script
    prints them at the end.

    With -Bundle and -Hash it compares the bundle's SHA-256 hash and clones
    its branch main to C:\src\hlab.

    After the first run, restart the VM and open "Ubuntu" from the Start
    menu once to create its user. A second run finishes what is left.

.PARAMETER Bundle
    Path of the repository bundle copied into the VM, e.g. C:\Temp\hlab.bundle.

.PARAMETER Hash
    SHA-256 hash of the bundle as printed on the development machine.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File C:\Temp\release-check-dev-tools.ps1 -Bundle C:\Temp\hlab.bundle -Hash <sha256>
#>
[CmdletBinding()]
param(
    [string]$Bundle = '',
    [string]$Hash = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$WingetPackages = @('Git.Git', 'GoLang.Go', 'OpenJS.NodeJS.LTS', 'Task.Task', 'Microsoft.DotNet.SDK.8')
$WixVersion = '7.0.1'
$WixExtensions = @('WixToolset.Util.wixext', 'WixToolset.BootstrapperApplications.wixext')
$WailsModule = 'github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0'
$Distro = 'Ubuntu-24.04'
$CloneDir = 'C:\src\hlab'

function Assert-Elevated {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'This script needs an administrator terminal. Start PowerShell with "Run as administrator" and run it again.'
    }
}

function Invoke-Native {
    param([string]$Exe, [string[]]$Arguments)
    & $Exe @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Exe $($Arguments -join ' ') failed with code $LASTEXITCODE"
    }
}

# Runs a program and returns its standard output and exit code; its error
# output is dropped. Windows PowerShell 5.1 turns redirected error output of
# a program into an error, so this runs with 'Continue'.
function Get-NativeOutput {
    param([string]$Exe, [string[]]$Arguments)
    $ErrorActionPreference = 'Continue'
    $output = @(& $Exe @Arguments 2>$null)
    return [pscustomobject]@{ Output = $output; ExitCode = $LASTEXITCODE }
}

function Update-Path {
    $parts = @(
        [Environment]::GetEnvironmentVariable('Path', 'Machine') -split ';'
        [Environment]::GetEnvironmentVariable('Path', 'User') -split ';'
        (Join-Path $env:USERPROFILE '.dotnet\tools')
        (Join-Path $env:USERPROFILE 'go\bin')
    )
    $env:Path = (@($parts | Where-Object { $_ } | Select-Object -Unique) -join ';')
}

function Assert-Command([string]$Command) {
    if (-not (Get-Command $Command -ErrorAction SilentlyContinue)) {
        throw "$Command is not on PATH after its installation. Open a new administrator PowerShell and run this script again."
    }
}

function Install-WingetPackages {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        throw 'winget is not available yet. Wait a few minutes after the first sign-in (or update "App Installer" in the Microsoft Store) and run this script again.'
    }
    foreach ($id in $WingetPackages) {
        $installed = Get-NativeOutput 'winget' @('list', '--id', $id, '-e', '--accept-source-agreements', '--disable-interactivity')
        if ($installed.ExitCode -eq 0) {
            Write-Host "    $id is installed."
            continue
        }
        Write-Host "    Installing $id"
        Invoke-Native 'winget' @('install', '--id', $id, '-e', '--accept-source-agreements', '--accept-package-agreements', '--disable-interactivity')
    }
    Update-Path
    foreach ($command in @('git', 'go', 'node', 'task', 'dotnet')) {
        Assert-Command $command
    }
}

function Install-Wix {
    $tools = (Get-NativeOutput 'dotnet' @('tool', 'list', '--global')).Output
    $wixLine = @($tools | Where-Object { $_ -match '^wix\s' })
    if ($wixLine.Count -gt 0 -and $wixLine[0] -match "^wix\s+$([regex]::Escape($WixVersion))\s") {
        Write-Host "    WiX $WixVersion is installed."
    } elseif ($wixLine.Count -gt 0) {
        Write-Host "    Updating WiX to $WixVersion"
        Invoke-Native 'dotnet' @('tool', 'update', '--global', 'wix', '--version', $WixVersion)
    } else {
        Write-Host "    Installing WiX $WixVersion"
        Invoke-Native 'dotnet' @('tool', 'install', '--global', 'wix', '--version', $WixVersion)
    }
    Update-Path
    Assert-Command 'wix'

    Invoke-Native 'wix' @('eula', 'accept', 'wix7')
    $extensions = (Get-NativeOutput 'wix' @('extension', 'list', '-g')).Output
    foreach ($ext in $WixExtensions) {
        $pattern = "^$([regex]::Escape($ext))\s+$([regex]::Escape($WixVersion))"
        if (@($extensions | Where-Object { $_ -match $pattern }).Count -gt 0) {
            Write-Host "    $ext $WixVersion is installed."
            continue
        }
        Write-Host "    Adding $ext $WixVersion"
        Invoke-Native 'wix' @('extension', 'add', '-g', "$ext/$WixVersion")
    }
}

function Install-Wails {
    if (Get-Command wails3 -ErrorAction SilentlyContinue) {
        Write-Host '    Wails is installed.'
        return
    }
    Write-Host "    Installing $WailsModule"
    Invoke-Native 'go' @('install', $WailsModule)
    Update-Path
    Assert-Command 'wails3'
}

# Returns $true when the distribution is registered, that is, "Ubuntu" was
# started once and its user created.
function Test-DistroRegistered {
    $env:WSL_UTF8 = '1'
    $list = Get-NativeOutput 'wsl.exe' @('--list', '--quiet')
    if ($list.ExitCode -ne 0) {
        return $false
    }
    return @($list.Output | Where-Object { $_.Trim() -eq $Distro }).Count -gt 0
}

function Install-Wsl {
    if (Test-DistroRegistered) {
        Write-Host "    WSL2 with $Distro is set up."
        return $false
    }
    Write-Host "    Installing WSL2 with $Distro"
    Invoke-Native 'wsl.exe' @('--install', '-d', $Distro, '--no-launch')
    return $true
}

function Copy-Repository {
    if (-not $Bundle) {
        Write-Host '    No -Bundle given; skipped.'
        return
    }
    if (Test-Path -LiteralPath $CloneDir) {
        Write-Host "    $CloneDir exists; skipped."
        return
    }
    $actual = (Get-FileHash -LiteralPath $Bundle -Algorithm SHA256).Hash
    if ($actual -ne $Hash.Trim()) {
        throw "The hash of $Bundle is $actual, not $Hash. Copy the bundle into the VM again."
    }
    Write-Host '    Hash matches.'
    Invoke-Native 'git' @('clone', '-b', 'main', $Bundle, $CloneDir)
}

Assert-Elevated
if ([bool]$Bundle -ne [bool]$Hash) {
    throw 'Give -Bundle and -Hash together.'
}
if ($Bundle -and -not (Test-Path -LiteralPath $Bundle -PathType Leaf)) {
    throw "Bundle $Bundle not found."
}

Update-Path
Write-Host '[1/5] winget packages: Git, Go, Node.js LTS, Task, .NET SDK 8'
Install-WingetPackages
Write-Host "[2/5] WiX $WixVersion with its licence and extensions"
Install-Wix
Write-Host '[3/5] Wails'
Install-Wails
Write-Host '[4/5] Repository'
Copy-Repository
Write-Host "[5/5] WSL2 with $Distro"
$wslPending = Install-Wsl

Write-Host ''
Write-Host 'Installed versions; compare them with docs\development\setup.md, "Tools":'
& go version
& node --version
& task --version
& wix --version

Write-Host ''
if ($wslPending) {
    Write-Host 'Next: restart the VM (Restart-Computer), then open "Ubuntu" from the Start menu once and create its user.'
    Write-Host 'Run this script again afterwards; it finishes what is left and skips what is done.'
} else {
    Write-Host 'All tools are set up.'
}
