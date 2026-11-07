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
  bin/dev.ps1 test --agent     the agent's tests (not yet implemented)
  bin/dev.ps1 build            the binaries for Linux and Windows into build/
  bin/dev.ps1 spec-diff        Hetzner's current specs against compat/
                               (not yet implemented)
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
                '--agent' { throw 'test --agent is not yet implemented (docs/roadmap.md)' }
                default { throw "unknown option: $Option" }
            }
        }
        'build' { Invoke-Build }
        'spec-diff' { throw 'spec-diff is not yet implemented (docs/roadmap.md)' }
        default {
            Get-Usage | Write-Output
            exit 2
        }
    }
} finally {
    Pop-Location
}
