# Phase 0 spike only: runs queued scripts with admin rights.
#
# Start once per session in an elevated PowerShell:
#   powershell -NoProfile -ExecutionPolicy Bypass -File C:\HLabSpike\runner.ps1
#
# WSL drops job files into C:\HLabSpike\queue\<name>.ps1. The runner executes each
# one in its own process (a failing job must not stop the runner), writes
# <name>.out (stdout and stderr) and <name>.rc (exit code) to C:\HLabSpike\done,
# and moves the job there. Stop with Ctrl+C; delete C:\HLabSpike after Phase 0.

$ErrorActionPreference = 'Stop'
$root = 'C:\HLabSpike'
$queue = Join-Path $root 'queue'
$done = Join-Path $root 'done'
New-Item -ItemType Directory -Force -Path $queue, $done | Out-Null

# Only the operator, Administrators and SYSTEM may write here, because whatever
# lands in the queue runs elevated.
$user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
$acl = New-Object Security.AccessControl.DirectorySecurity
$acl.SetAccessRuleProtection($true, $false)
foreach ($who in @($user, 'S-1-5-32-544', 'S-1-5-18')) {
    $identity = if ($who -like 'S-1-*') {
        New-Object Security.Principal.SecurityIdentifier $who
    } else {
        New-Object Security.Principal.NTAccount $who
    }
    $rule = New-Object Security.AccessControl.FileSystemAccessRule(
        $identity, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
    $acl.AddAccessRule($rule)
}
Set-Acl -Path $root -AclObject $acl

Write-Host "HLab spike runner: watching $queue (Ctrl+C to stop)"
while ($true) {
    $jobs = Get-ChildItem -Path $queue -Filter '*.ps1' | Sort-Object Name
    foreach ($job in $jobs) {
        $base = Join-Path $done $job.BaseName
        Write-Host "$(Get-Date -Format HH:mm:ss) $($job.Name)"
        $proc = Start-Process -FilePath powershell.exe -Wait -PassThru -NoNewWindow `
            -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $job.FullName `
            -RedirectStandardOutput "$base.out" -RedirectStandardError "$base.err"
        Move-Item -Force -Path $job.FullName -Destination "$base.ps1"
        # .rc last: WSL waits for it as the sign that the job is finished.
        Set-Content -Path "$base.rc" -Value $proc.ExitCode -Encoding ascii
    }
    Start-Sleep -Milliseconds 300
}
