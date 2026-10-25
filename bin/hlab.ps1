# The lab from Windows PowerShell; same commands as bin/hlab:
#   .\bin\hlab.ps1 [-c hlab.yaml] up | down | purge | logs | shell | build | test | lint | format
#   .\bin\hlab.ps1 [-c hlab.yaml] <command inside the lab>
# Native commands report through $LASTEXITCODE; Windows PowerShell would turn their stderr into errors.
$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot

$rest = [System.Collections.Generic.List[string]]::new()
$config = if ($env:HLAB_CONFIG) { $env:HLAB_CONFIG } else { 'hlab.yaml' }
$explicit = [bool]$env:HLAB_CONFIG
for ($i = 0; $i -lt $args.Count; $i++) {
    if ($i -eq 0 -and ($args[0] -eq '-c' -or $args[0] -eq '--config')) { $config = $args[1]; $explicit = $true; $i++; continue }
    $rest.Add([string]$args[$i])
}

if ($explicit -and -not (Test-Path $config)) {   # a mistyped path must not mean the lab "demo"
    [Console]::Error.WriteLine("hlab: no such configuration: $config"); exit 2
}
$name = 'demo'
$cfgMount = @()
if (Test-Path $config) {
    $m = Select-String -CaseSensitive -Path $config -Pattern '^name:\s*["'']?([A-Za-z0-9_-]+)' | Select-Object -First 1
    if ($m) { $name = $m.Matches[0].Groups[1].Value }
    $full = (Resolve-Path $config).Path
    $cfgMount = @('-v', "$(Split-Path -Parent $full):/config:ro", '-e', "HLAB_CONFIG=/config/$(Split-Path -Leaf $full)")
}
$container = "hlab-$name"

# Image tag: hash of everything that goes into the image, in byte order of the names (same as bin/hlab).
function Ordinal($items) { $a = [string[]]@($items); [Array]::Sort($a, [StringComparer]::Ordinal); $a }
$files = @("$root\Dockerfile") + (Ordinal (Get-ChildItem "$root\scripts\*" | Where-Object { -not $_.Name.StartsWith('.') }).FullName) +
         (Ordinal (Get-ChildItem "$root\hlabd\*.py").FullName) +
         (Ordinal (Get-ChildItem "$root\hlabd\*\*.py").FullName) +
         (Ordinal (Get-ChildItem "$root\hlabd\*\*\*.py").FullName) + (Ordinal (Get-ChildItem "$root\tests\*.py").FullName) +
         (Ordinal (Get-ChildItem "$root\tests\tofu-smoke\*.tf").FullName) +
         (Ordinal (Get-ChildItem "$root\tests\tofu-smoke\*.sh").FullName) +
         (Ordinal (Get-ChildItem "$root\itests\*.py").FullName)
$bytes = [System.Collections.Generic.List[byte]]::new()
foreach ($f in $files) { $bytes.AddRange([System.IO.File]::ReadAllBytes($f)) }
$sha = [System.Security.Cryptography.SHA256]::Create().ComputeHash($bytes.ToArray())
$tag = (-join ($sha | ForEach-Object { $_.ToString('x2') })).Substring(0, 12)
$image = "hlab:$tag"
$devImage = "hlab-dev:$tag"   # the development tools (STYLE.md), built on top of the lab image

function Build([string]$Target = 'lab', [string]$Tagged = $image) {
    docker image inspect $Tagged 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { return }
    Write-Host "Building $Tagged ..."
    docker build -q --target $Target -t $Tagged $root | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'docker build failed' }
    # older builds go (those a container still uses stay: docker refuses to remove them)
    $repository = $Tagged.Split(':')[0]
    docker images $repository --format '{{.Repository}}:{{.Tag}}' | Where-Object { $_ -ne $Tagged } |
        ForEach-Object { docker rmi $_ *> $null }
}

# Style checks or fixes (STYLE.md): tools/lint.sh in the development image, tofu fmt in OpenTofu's.
function Invoke-Lint([string]$Mode) {
    Build 'dev' $devImage
    # the tools' output goes to the console, not into this function's return value
    docker run --rm -v "${root}:/src" --entrypoint bash $devImage /src/tools/lint.sh $Mode | Out-Host
    if ($LASTEXITCODE -ne 0) { return $LASTEXITCODE }
    $tofuArgs = if ($Mode -eq 'fix') { @('fmt', '-recursive') } else { @('fmt', '-check', '-diff', '-recursive') }
    docker run --rm -v "${root}\tests\tofu-smoke:/work" -w /work ghcr.io/opentofu/opentofu:1 @tofuArgs | Out-Host
    return $LASTEXITCODE
}

$cmd = if ($rest.Count -gt 0) { $rest[0] } else { '' }
switch ($cmd) {
    'build' { Build; exit 0 }
    'up' {
        Build
        docker stop $container *> $null   # a running lab shuts its guests down first
        docker rm -f $container *> $null
        docker run -d --name $container --init --privileged --device /dev/kvm --device /dev/net/tun `
            --stop-timeout 20 -e "HLAB_NAME=$name" -v "${container}:/hlab" @cfgMount $image serve | Out-Null
        # the start's log until the lab is ready (or the container ends): image downloads take a while
        $shown = 0
        while ($true) {
            $lines = @(docker logs $container 2>&1)
            $lines | Select-Object -Skip $shown | ForEach-Object { Write-Host $_ }
            $shown = $lines.Count
            if ($lines -match 'hlab ready') { exit 0 }
            if ((docker inspect -f '{{.State.Running}}' $container 2>$null) -ne 'true') { exit 1 }
            Start-Sleep -Seconds 1
        }
    }
    'down' { docker stop $container *> $null; docker rm -f $container *> $null; exit 0 }
    'purge' { docker stop $container *> $null; docker rm -f $container *> $null; docker volume rm $container *> $null; exit 0 }
    'logs' { docker logs -f $container; exit $LASTEXITCODE }
    'shell' { docker exec -it $container bash; exit $LASTEXITCODE }
    'lint' { exit (Invoke-Lint 'check') }
    'format' { exit (Invoke-Lint 'fix') }
    'test' {
        $status = Invoke-Lint 'check'
        if ($status -ne 0) { exit $status }
        Build
        docker run --rm --entrypoint python3 -w /opt/hlab $image -m pytest -q -p no:cacheprovider tests
        exit $LASTEXITCODE
    }
    'itest' {
        $extra = @(if ($rest.Count -gt 1) { $rest.GetRange(1, $rest.Count - 1) })   # an array, also of one
        docker exec -e HLAB_LIVE=1 -w /opt/hlab/itests $container python3 -m pytest -p no:cacheprovider -v @extra
        exit $LASTEXITCODE
    }
    '' { Get-Content $PSCommandPath -TotalCount 3 | Select-Object -Skip 1; exit 2 }
    'box' {
        # box user <name> <key file>: the key file is on this host, the command runs in the container
        if ($rest.Count -ge 4 -and $rest[1] -eq 'user' -and $rest[3] -ne '-' -and (Test-Path $rest[3])) {
            # cmd's redirection hands the file over byte for byte (a PowerShell pipe would re-encode it)
            cmd /c "docker exec -i $container hlab box user $($rest[2]) - < `"$((Resolve-Path $rest[3]).Path)`""
            exit $LASTEXITCODE
        }
        docker exec -i $container hlab @rest
        exit $LASTEXITCODE
    }
    default {
        # a terminal only when both ends are one (like bin/hlab): redirected output gets no TTY line endings
        $tty = if ([Console]::IsInputRedirected -or [Console]::IsOutputRedirected) { '-i' } else { '-it' }
        docker exec $tty $container hlab @rest
        exit $LASTEXITCODE
    }
}
