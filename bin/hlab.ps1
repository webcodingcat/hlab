<#
.SYNOPSIS
    The lab from Windows PowerShell; the same commands as bin/hlab.

.DESCRIPTION
    .\bin\hlab.ps1 [-c hlab.yaml] up         build the image (cached), start the lab container hlab-<name>
    .\bin\hlab.ps1 [-c hlab.yaml] down       stop it (VM disks and state stay in the volume hlab-<name>)
    .\bin\hlab.ps1 [-c hlab.yaml] purge      stop it and delete the volume
    .\bin\hlab.ps1 [-c hlab.yaml] logs | shell | build
    .\bin\hlab.ps1 [-c hlab.yaml] test       style checks (STYLE.md) and unit tests
    .\bin\hlab.ps1 [-c hlab.yaml] lint       style checks only
    .\bin\hlab.ps1 [-c hlab.yaml] format     apply what the formatters can fix
    .\bin\hlab.ps1 [-c hlab.yaml] itest [pytest args]
                                             live tests with VMs in the running lab (resets it first)
    .\bin\hlab.ps1 [-c hlab.yaml] <command>  any other command runs inside the lab:
                                             status, env, ssh, fault, client, ...
    The configuration defaults to $env:HLAB_CONFIG or .\hlab.yaml; without a file the lab "demo" runs with
    defaults.

.EXAMPLE
    .\bin\hlab.ps1 -c examples\hlab.yaml up
#>

# The arguments come from $args, not from a param() block. An advanced script (needed for
# ValueFromRemainingArguments) rejects or swallows the arguments meant for the lab: -v binds to -Verbose, -d to
# -Debug, -e and -i are ambiguous common parameters (itest -v, ssh a ls -d). A simple param() block binds its
# parameters by position, so `up` would become the configuration file. Hence -c is only recognised as the first
# argument, as in bin/hlab.

# Native commands report through $LASTEXITCODE; with 'Stop', Windows PowerShell would turn their stderr into
# errors.
$ErrorActionPreference = 'Continue'

Set-Variable -Name Root -Value (Split-Path -Parent $PSScriptRoot) -Option ReadOnly
Set-Variable -Name Image -Value 'hlab:latest' -Option ReadOnly
# The development tools (STYLE.md), built on top of the lab image.
Set-Variable -Name DevImage -Value 'hlab-dev:latest' -Option ReadOnly
Set-Variable -Name TofuImage -Value 'ghcr.io/opentofu/opentofu:1' -Option ReadOnly

function Show-LabUsage {
    <#
    .SYNOPSIS
        Prints the usage from the comment-based help at the top of this file to stderr
        (shown on a usage error).
    #>
    [CmdletBinding()]
    param()
    $help = Get-Help -Name $PSCommandPath
    [Console]::Error.WriteLine($help.Synopsis)
    foreach ($line in $help.Description.Text) {
        [Console]::Error.WriteLine($line)
    }
}

function New-LabException {
    <#
    .SYNOPSIS
        Creates an error that ends the script with the exit code (as bin/hlab ends with the exit code of the
        command that failed).
    .PARAMETER Message
        What went wrong. Without a message the failed command has already said it.
    .PARAMETER ExitCode
        The exit code the script ends with.
    #>
    [CmdletBinding()]
    param(
        [string]$Message,
        [Parameter(Mandatory)]
        [int]$ExitCode
    )
    $exception = New-Object -TypeName System.Exception -ArgumentList $Message
    $exception.Data['ExitCode'] = $ExitCode
    return $exception
}

function Get-LabConfig {
    <#
    .SYNOPSIS
        Gets the lab's name from the configuration file and the docker arguments that mount its directory into
        the container.
    .DESCRIPTION
        Without the file the lab "demo" runs with defaults, unless the file was named explicitly: a mistyped
        path must not mean the lab "demo".
    .PARAMETER Path
        The configuration file.
    .PARAMETER IsExplicit
        The file was named explicitly (-c or $env:HLAB_CONFIG), so it must exist.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string]$Path,
        [switch]$IsExplicit
    )
    $labConfig = [pscustomobject]@{ Name = 'demo'; Mount = @() }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        if ($IsExplicit) {
            throw (New-LabException -Message "no such configuration: $Path" -ExitCode 2)
        }
        return $labConfig
    }
    $nameLine = Select-String -CaseSensitive -LiteralPath $Path -Pattern '^name:\s*["'']?([A-Za-z0-9_-]+)' |
        Select-Object -First 1
    if ($nameLine) {
        $labConfig.Name = $nameLine.Matches[0].Groups[1].Value
    }
    $fullPath = (Resolve-Path -LiteralPath $Path).Path
    $labConfig.Mount = @('-v', "$(Split-Path -Parent $fullPath):/config:ro",
        '-e', "HLAB_CONFIG=/config/$(Split-Path -Leaf $fullPath)")
    return $labConfig
}

function New-LabImage {
    <#
    .SYNOPSIS
        Builds a stage of the Dockerfile. Docker's layer cache makes this quick when nothing has changed.
    .PARAMETER Target
        The Dockerfile stage.
    .PARAMETER Tag
        The image's tag.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$Target,
        [Parameter(Mandatory)]
        [string]$Tag
    )
    [Console]::Error.WriteLine("Building $Tag ...")
    docker build -q --target $Target -t $Tag $Root | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw (New-LabException -ExitCode $LASTEXITCODE)
    }
    Remove-UntaggedLabImage
}

function Remove-UntaggedLabImage {
    <#
    .SYNOPSIS
        Removes the untagged images that rebuilds leave behind (all carry the label hlab=1).
    .DESCRIPTION
        One that a container still uses stays until a later prune.
    #>
    [CmdletBinding()]
    param()
    docker image prune -f --filter label=hlab=1 *> $null
}

function Invoke-LabLint {
    <#
    .SYNOPSIS
        Runs the style checks or fixes (STYLE.md): tools/lint.sh in the development image, tofu fmt in
        OpenTofu's image.
    .PARAMETER Mode
        check or fix.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$Mode
    )
    $tofuArgs = @('fmt', '-check', '-diff', '-recursive')
    if ($Mode -eq 'fix') {
        $tofuArgs = @('fmt', '-recursive')
    }
    New-LabImage -Target 'dev' -Tag $DevImage
    docker run --rm -v "${Root}:/src" --entrypoint bash $DevImage /src/tools/lint.sh $Mode
    if ($LASTEXITCODE -ne 0) {
        throw (New-LabException -ExitCode $LASTEXITCODE)
    }
    docker run --rm -v "${Root}\tests\tofu-smoke:/work" -w /work $TofuImage @tofuArgs
    if ($LASTEXITCODE -ne 0) {
        throw (New-LabException -ExitCode $LASTEXITCODE)
    }
}

function Stop-Lab {
    <#
    .SYNOPSIS
        Stops and removes the lab container; the volume with the VM disks and the lab's state stays.
    .PARAMETER Container
        The lab container.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$Container
    )
    docker stop $Container *> $null
    docker rm -f $Container *> $null
}

function Start-Lab {
    <#
    .SYNOPSIS
        Starts the lab container afresh and shows its log until the lab is ready (or the container ends).
    .DESCRIPTION
        Loading the OS images takes a while.
    .PARAMETER Container
        The lab container.
    .PARAMETER LabConfig
        The lab's name and configuration mount, from Get-LabConfig.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$Container,
        [Parameter(Mandatory)]
        [pscustomobject]$LabConfig
    )
    New-LabImage -Target 'lab' -Tag $Image
    # A running lab shuts its guests down first.
    Stop-Lab -Container $Container
    $configMount = $LabConfig.Mount
    docker run -d --name $Container --init --privileged --device /dev/kvm --device /dev/net/tun `
        --stop-timeout 20 -e "HLAB_NAME=$($LabConfig.Name)" -v "${Container}:/hlab" @configMount `
        $Image serve | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw (New-LabException -ExitCode $LASTEXITCODE)
    }
    # The old container is gone, and with it the last user of an old image.
    Remove-UntaggedLabImage
    $shown = 0
    while ($true) {
        # The lab logs to stdout, but some of its output (uvicorn's warnings, tracebacks) arrives on stderr,
        # which Windows PowerShell wraps in error records: back to plain lines.
        $lines = @(docker logs $Container 2>&1 | ForEach-Object { [string]$_ })
        $lines | Select-Object -Skip $shown
        $shown = $lines.Count
        if ($lines -match 'hlab ready') {
            return
        }
        $running = docker inspect -f '{{.State.Running}}' $Container 2> $null
        if ($running -ne 'true') {
            throw (New-LabException -ExitCode 1)
        }
        Start-Sleep -Seconds 1
    }
}

function Invoke-LabBox {
    <#
    .SYNOPSIS
        Runs "hlab box" in the lab container; the exit code is left in $LASTEXITCODE.
    .DESCRIPTION
        "box user <name> <key file>" reads the key file on this host and hands it to the container on stdin;
        anything else runs in the container as it is.
    .PARAMETER Container
        The lab container.
    .PARAMETER TtyFlag
        docker exec's -i or -it.
    .PARAMETER BoxArgs
        The arguments after "box".
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$Container,
        [Parameter(Mandatory)]
        [string]$TtyFlag,
        [string[]]$BoxArgs
    )
    # "-" already means stdin inside the container.
    if ($BoxArgs.Count -ge 3 -and $BoxArgs[0] -eq 'user' -and $BoxArgs[2] -ne '-' -and
        (Test-Path -LiteralPath $BoxArgs[2] -PathType Leaf)) {
        $keyFile = (Resolve-Path -LiteralPath $BoxArgs[2]).Path
        # cmd's redirection hands the file over byte for byte (a PowerShell pipe would re-encode it).
        cmd /c "docker exec -i $Container hlab box user `"$($BoxArgs[1])`" - < `"$keyFile`""
        return
    }
    docker exec $TtyFlag $Container hlab box @BoxArgs
}

try {
    $CommandLine = @($args | ForEach-Object { [string]$_ })
    $ConfigPath = 'hlab.yaml'
    $IsExplicit = $false
    if ($env:HLAB_CONFIG) {
        $ConfigPath = $env:HLAB_CONFIG
        $IsExplicit = $true
    }
    if ($CommandLine.Count -gt 0 -and $CommandLine[0] -in '-c', '--config') {
        if ($CommandLine.Count -lt 2) {
            throw (New-LabException -Message "$($CommandLine[0]) needs a configuration file" -ExitCode 2)
        }
        $ConfigPath = $CommandLine[1]
        $IsExplicit = $true
        $CommandLine = @($CommandLine | Select-Object -Skip 2)
    }
    $LabConfig = Get-LabConfig -Path $ConfigPath -IsExplicit:$IsExplicit
    $Container = "hlab-$($LabConfig.Name)"
    # docker exec gets a terminal only when both ends are one: redirected output must not get a terminal's line
    # endings. A pipe after this script (hlab.ps1 status | ...) leaves the console as it is, so the script must
    # be the pipeline's last command too. Captured output ($status = hlab.ps1 status, or in parentheses) cannot
    # be told apart from the console and still gets a terminal.
    $TtyFlag = '-i'
    $IsPipelineEnd = $MyInvocation.PipelinePosition -eq $MyInvocation.PipelineLength
    if ($IsPipelineEnd -and -not [Console]::IsInputRedirected -and -not [Console]::IsOutputRedirected) {
        $TtyFlag = '-it'
    }
    $Command = ''
    if ($CommandLine.Count -gt 0) {
        $Command = $CommandLine[0]
    }
    $CommandArgs = @($CommandLine | Select-Object -Skip 1)

    switch -CaseSensitive ($Command) {
        'build' {
            New-LabImage -Target 'lab' -Tag $Image
            exit 0
        }
        'up' {
            Start-Lab -Container $Container -LabConfig $LabConfig
            exit 0
        }
        'down' {
            Stop-Lab -Container $Container
            exit 0
        }
        'purge' {
            Stop-Lab -Container $Container
            docker volume rm $Container *> $null
            exit 0
        }
        'logs' {
            docker logs -f $Container
            exit $LASTEXITCODE
        }
        'shell' {
            docker exec $TtyFlag $Container bash
            exit $LASTEXITCODE
        }
        'lint' {
            Invoke-LabLint -Mode 'check'
            exit 0
        }
        'format' {
            Invoke-LabLint -Mode 'fix'
            exit 0
        }
        'test' {
            Invoke-LabLint -Mode 'check'
            New-LabImage -Target 'lab' -Tag $Image
            # NET_ADMIN: nft -c checks the rulesets against the kernel
            docker run --rm --cap-add NET_ADMIN --entrypoint python3 -w /opt/hlab $Image `
                -m pytest -q -p no:cacheprovider tests
            exit $LASTEXITCODE
        }
        'itest' {
            docker exec $TtyFlag -e HLAB_LIVE=1 -w /opt/hlab/itests $Container python3 -m pytest `
                -p no:cacheprovider -v @CommandArgs
            exit $LASTEXITCODE
        }
        'box' {
            Invoke-LabBox -Container $Container -TtyFlag $TtyFlag -BoxArgs $CommandArgs
            exit $LASTEXITCODE
        }
        '' {
            Show-LabUsage
            exit 2
        }
        default {
            docker exec $TtyFlag $Container hlab @CommandLine
            exit $LASTEXITCODE
        }
    }
} catch {
    if ($_.Exception.Message) {
        [Console]::Error.WriteLine("hlab: $($_.Exception.Message)")
    }
    $ExitCode = $_.Exception.Data['ExitCode']
    if ($null -eq $ExitCode) {
        $ExitCode = 1
    }
    exit $ExitCode
}
