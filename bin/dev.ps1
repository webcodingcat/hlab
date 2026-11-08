<#
.SYNOPSIS
Development commands for HLab on Windows (DEVELOPING.md, Tools).

.DESCRIPTION
The same commands as bin/dev:
  bin/dev.ps1 lint             check formatting and style (STYLE.md); shellcheck
                               and yamllint run only in bin/dev
  bin/dev.ps1 format           apply what the formatters can fix
  bin/dev.ps1 test             the unit tests (on Windows, without the race
                               detector, which needs cgo)
  bin/dev.ps1 test --windows   the same as test
  bin/dev.ps1 test --agent     the agent's tests run from WSL2
                               (bin/dev test --agent)
  bin/dev.ps1 build            the binaries for Linux and Windows into build/
  bin/dev.ps1 spec-diff        Hetzner's current specs against compat/specs;
                               fails if they differ
  bin/dev.ps1 spec-diff --update
                               replace compat/specs with Hetzner's current ones
  bin/dev.ps1 smoke            the smoke tests run from WSL2 (bin/dev smoke)
  bin/dev.ps1 appliance        the appliance is built in WSL2
                               (bin/dev appliance)
  bin/dev.ps1 itest [--run <pattern>]
                               the live tests against Hyper-V, with
                               administrator rights through the task
                               "HLab itest"
  bin/dev.ps1 dev-install      once, as administrator: the folder
                               C:\HLabDev\itest, the task "HLab itest" and
                               the agent's hv_sock service
#>
param(
    [Parameter(Position = 0)]
    [string]$Command,

    # a string, not a switch, so --windows and --agent read as in bin/dev
    [Parameter(Position = 1, ValueFromRemainingArguments)]
    [string]$Option
)

$ErrorActionPreference = 'Stop'
$Repo = Split-Path -Parent $PSScriptRoot
$Tools = @('tool', "-modfile=$Repo/tools/go.mod")
$ShfmtFlags = @('-i', '2', '-ci', '-bn')
$Specs = @('cloud', 'hetzner')
$SpecUrl = 'https://docs.hetzner.cloud'
# the live tests run elevated from here, through the task (dev-install)
$ItestDir = 'C:\HLabDev\itest'
$ItestTask = 'HLab itest'

function Invoke-Native {
    <#
    .SYNOPSIS
    Run a program and stop when it fails, as Bash does with set -e.
    #>
    param(
        [Parameter(Mandatory)]
        [string]$Program,

        [string[]]$Arguments = @()
    )
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Program $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Get-ShellFile {
    <#
    .SYNOPSIS
    The shell scripts of the repository.
    #>
    @('bin/dev') + @(git ls-files --cached --others --exclude-standard '*.sh')
}

function Invoke-Lint {
    <#
    .SYNOPSIS
    Check formatting and style of the Go code, the shell scripts' formatting
    and the PowerShell files.
    #>
    $unformatted = gofmt -l cmd internal
    if ($unformatted) {
        throw "gofmt would change: $unformatted"
    }
    # go tool would build the linter for GOOS; -n builds it for this machine
    # and prints its path
    $golangciLint = go ($Tools + @('-n', 'golangci-lint'))
    if ($LASTEXITCODE -ne 0) {
        throw 'building golangci-lint failed'
    }
    # both targets, so files for the other system are checked as well
    foreach ($goos in 'linux', 'windows') {
        $env:GOOS = $goos
        try {
            Invoke-Native go @('vet', './...')
            Invoke-Native $golangciLint @('run', './...')
        } finally {
            Remove-Item Env:GOOS
        }
    }
    Invoke-Native go ($Tools + @('shfmt') + $ShfmtFlags + @('-d') + (Get-ShellFile))
    if (-not (Get-Module -ListAvailable PSScriptAnalyzer)) {
        throw 'PSScriptAnalyzer is missing: Install-Module PSScriptAnalyzer -Scope CurrentUser'
    }
    $found = @(Invoke-ScriptAnalyzer -Path $Repo -Recurse)
    if ($found.Count -gt 0) {
        $found | Format-Table -AutoSize | Out-String -Width 160 | Write-Output
        throw 'PSScriptAnalyzer found problems'
    }
}

function Invoke-Format {
    <#
    .SYNOPSIS
    Apply what the formatters can fix; lint reports the rest.
    #>
    Invoke-Native gofmt @('-w', 'cmd', 'internal')
    Invoke-Native go ($Tools + @('golangci-lint', 'fmt', './...'))
    Invoke-Native go ($Tools + @('shfmt') + $ShfmtFlags + @('-w') + (Get-ShellFile))
}

function Invoke-Build {
    <#
    .SYNOPSIS
    Build the binaries without cgo: everything for Linux, the service and the
    CLI for Windows.
    #>
    $version = git describe --always --dirty --match 'v[0-9]*.[0-9]*'
    $flags = @('-trimpath', '-ldflags', "-X main.version=$version")
    $env:CGO_ENABLED = '0'
    $env:GOARCH = 'amd64'
    try {
        $env:GOOS = 'linux'
        Invoke-Native go (@('build') + $flags + @('-o', 'build/linux/', './cmd/...'))
        $env:GOOS = 'windows'
        Invoke-Native go (@('build') + $flags + @('-o', 'build/windows/', './cmd/hlabd', './cmd/hlab'))
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
    }
    Write-Output "built $version into build/linux and build/windows"
}

function Compare-Spec {
    <#
    .SYNOPSIS
    Compare Hetzner's current specs with those in compat/specs, and replace
    them when asked to. Fails if a spec differs and is not replaced.
    #>
    param(
        [switch]$Update
    )
    $dir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
    New-Item -ItemType Directory -Path $dir | Out-Null
    $changed = $false
    try {
        foreach ($name in $Specs) {
            $kept = "compat/specs/$name.spec.json"
            $fetched = Join-Path $dir "$name.spec.json"
            Invoke-WebRequest -UseBasicParsing -Uri "$SpecUrl/$name.spec.json" -OutFile $fetched
            if ((Get-FileHash $kept).Hash -eq (Get-FileHash $fetched).Hash) {
                Write-Output "${name}: unchanged"
                continue
            }
            $changed = $true
            Write-Output "${name}: changed"
            git diff --no-index --stat $kept $fetched | Write-Output
            if ($Update) {
                Copy-Item $fetched $kept
            }
        }
    } finally {
        Remove-Item -Recurse -Force $dir
    }
    if ($changed -and -not $Update) {
        throw 'the specs differ: bin/dev.ps1 spec-diff --update takes them over (DEVELOPING.md)'
    }
}

function Install-DevMachine {
    <#
    .SYNOPSIS
    Prepare this machine for the live tests, once, as administrator: the
    folder C:\HLabDev\itest, which only this user, the administrators and
    SYSTEM may write, the task "HLab itest" that runs its run.cmd with the
    highest rights and without a window, and the agent's hv_sock service.
    #>
    $principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'dev-install needs an elevated PowerShell (Run as administrator)'
    }
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    New-Item -ItemType Directory -Force -Path $ItestDir | Out-Null
    # whatever lands in the folder runs elevated: no inherited rights
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    $rights = @(
        @($user, 'FullControl'),
        @('S-1-5-32-544', 'FullControl'), # Administrators
        @('S-1-5-18', 'FullControl'), # SYSTEM
        @('S-1-5-83-0', 'ReadAndExecute') # Hyper-V's VMs read the appliance's disk
    )
    foreach ($right in $rights) {
        $identity = if ($right[0] -like 'S-1-*') {
            New-Object Security.Principal.SecurityIdentifier $right[0]
        } else {
            New-Object Security.Principal.NTAccount $right[0]
        }
        $rule = New-Object Security.AccessControl.FileSystemAccessRule(
            $identity, $right[1], 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    Set-Acl -Path $ItestDir -AclObject $acl
    # S4U: runs whether the user is signed in or not, without a window and
    # without a stored password
    $action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument "/c $ItestDir\run.cmd" -WorkingDirectory $ItestDir
    $taskPrincipal = New-ScheduledTaskPrincipal -UserId $user -LogonType S4U -RunLevel Highest
    $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Hours 1) `
        -MultipleInstances IgnoreNew -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Register-ScheduledTask -TaskName $ItestTask -Action $action -Principal $taskPrincipal `
        -Settings $settings -Force | Out-Null
    # hlabd reaches the agent's vsock port 5000 through this service ID
    $service = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Virtualization\GuestCommunicationServices\00001388-facb-11e6-bd58-64006a7986d3'
    New-Item -Path $service -Force | Out-Null
    Set-ItemProperty -Path $service -Name ElementName -Value 'HLab agent'
    Write-Output "set up $ItestDir, the task `"$ItestTask`" and the agent's hv_sock service"
}

function Invoke-LiveTest {
    <#
    .SYNOPSIS
    Build the live tests and run them through the task "HLab itest", with
    administrator rights, as bin/dev itest does; print their output as it
    comes and fail as they do.
    #>
    param(
        [string]$Pattern
    )
    if (-not (Test-Path $ItestDir)) {
        throw "$ItestDir is missing: run bin/dev.ps1 dev-install once as administrator"
    }
    Invoke-Native go @('test', '-c', '-tags', 'live', '-o', "$ItestDir\itest.test.exe", './itest')
    $appliance = 'build/appliance/hlab-appliance.vhdx'
    $copy = "$ItestDir\hlab-appliance.vhdx"
    # Hyper-V keeps the disk open while a test's appliance runs on it
    if ((Test-Path $appliance) -and
        (-not (Test-Path $copy) -or (Get-Item $appliance).LastWriteTime -gt (Get-Item $copy).LastWriteTime)) {
        Copy-Item $appliance $copy
    }
    $output = "$ItestDir\itest.out"
    $code = "$ItestDir\itest.rc"
    Remove-Item $output, $code -ErrorAction SilentlyContinue
    $run = if ($Pattern) { " -test.run `"$Pattern`"" } else { '' }
    Set-Content -Path "$ItestDir\run.cmd" -Encoding ascii -Value @(
        '@echo off', "cd /d $ItestDir",
        "itest.test.exe -test.v -test.timeout 30m$run > itest.out 2>&1",
        'echo %ERRORLEVEL% > itest.rc')
    Invoke-Native schtasks.exe @('/run', '/tn', $ItestTask)
    $printed = 0
    $deadline = (Get-Date).AddMinutes(40)
    while ($true) {
        $finished = Test-Path $code
        if (Test-Path $output) {
            $text = [IO.File]::ReadAllText($output)
            # whole lines only: Write-Output ends each piece with one
            $end = if ($finished) { $text.Length } else { $text.LastIndexOf("`n") + 1 }
            if ($end -gt $printed) {
                Write-Output $text.Substring($printed, $end - $printed).TrimEnd("`r", "`n")
                $printed = $end
            }
        }
        if ($finished) {
            break
        }
        if ((Get-Date) -gt $deadline) {
            throw 'the live tests did not finish in 40 minutes'
        }
        Start-Sleep -Seconds 1
    }
    $exitCode = [int](Get-Content $code -Raw).Trim()
    if ($exitCode -ne 0) {
        throw "the live tests failed with exit code $exitCode"
    }
}

function Get-Usage {
    <#
    .SYNOPSIS
    The commands, from the help at the top of this script.
    #>
    (Get-Help $PSCommandPath).description.Text
}

Push-Location $Repo
try {
    switch ($Command) {
        'lint' { Invoke-Lint }
        'format' { Invoke-Format }
        'test' {
            switch ($Option) {
                # -count=1: the layer test reads packages through the go
                # command, which the test cache does not see
                { $_ -in '', '--windows' } { Invoke-Native go @('test', '-count=1', './...') }
                # they need Linux network namespaces (DEVELOPING.md, Tools)
                '--agent' { throw 'test --agent runs from WSL2 or Linux: bin/dev test --agent' }
                default { throw "unknown option: $Option" }
            }
        }
        'build' { Invoke-Build }
        'spec-diff' {
            switch ($Option) {
                '' { Compare-Spec }
                '--update' { Compare-Spec -Update }
                default { throw "unknown option: $Option" }
            }
        }
        # the smoke tests use the tools as users do, from WSL2 (DEVELOPING.md, Tools)
        'smoke' { throw 'smoke runs from WSL2: bin/dev smoke' }
        # mmdebstrap is a Linux tool
        'appliance' { throw 'appliance runs from WSL2: bin/dev appliance' }
        'itest' {
            if ($Option -match '^--run (.+)$') {
                Invoke-LiveTest -Pattern $Matches[1]
            } elseif ($Option) {
                throw "unknown option: $Option"
            } else {
                Invoke-LiveTest
            }
        }
        'dev-install' { Install-DevMachine }
        default {
            Get-Usage | Write-Output
            exit 2
        }
    }
} finally {
    Pop-Location
}
