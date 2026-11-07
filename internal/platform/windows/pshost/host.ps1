# The loop of a pshost process. Go starts powershell.exe with this script as
# -EncodedCommand and talks to it over stdin and stdout, one JSON value per
# line:
#   1. Go sends the commands' script as a JSON string. Dot-sourced here, it
#      returns a hashtable from command names to script blocks.
#   2. The loop answers {"id":0,"ok":true} when the script is loaded.
#   3. Go sends {"id","command","args"}; the loop runs the command's script
#      block with args and answers {"id","ok","result"} or
#      {"id","ok":false,"kind","message"}.
# Answers start with the character 0x1E, so that Go can tell them from stray
# output such as [Console]::WriteLine in a command.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$utf8 = New-Object System.Text.UTF8Encoding $false
[Console]::InputEncoding = $utf8
[Console]::OutputEncoding = $utf8
$reader = [Console]::In
$writer = [Console]::Out
$marker = [string][char]0x1E

function Send-Answer {
    param([hashtable] $Answer)
    $writer.WriteLine($marker + (ConvertTo-Json -InputObject $Answer -Compress -Depth 20))
    $writer.Flush()
}

function Get-Failure {
    # A command fails with a kind by throwing "<kind>: <message>", such as
    # "not_found: VM web". Errors of cmdlets have no kind.
    param([System.Management.Automation.ErrorRecord] $Record)
    $exception = $Record.Exception
    $answer = @{ ok = $false; kind = ''; message = $exception.Message }
    $thrown = $exception -is [System.Management.Automation.RuntimeException] -and
        $exception.WasThrownFromThrowStatement
    if ($thrown -and $exception.Message -match '^([a-z_]+): (.*)$') {
        $answer.kind = $Matches[1]
        $answer.message = $Matches[2]
    }
    $answer
}

try {
    $commands = . ([scriptblock]::Create(($reader.ReadLine() | ConvertFrom-Json)))
    if ($commands -isnot [hashtable]) {
        throw 'the script does not return a hashtable of commands'
    }
} catch {
    $failure = Get-Failure $_
    $failure.id = 0
    Send-Answer $failure
    exit 1
}
Send-Answer @{ id = 0; ok = $true }

while ($null -ne ($line = $reader.ReadLine())) {
    $request = ConvertFrom-Json $line
    try {
        $command = $commands[$request.command]
        if ($null -eq $command) {
            throw "unknown_command: $($request.command)"
        }
        # warnings and Write-Host would end up on stdout
        $result = & $command $request.args 3>$null 6>$null
        Send-Answer @{ id = $request.id; ok = $true; result = $result }
    } catch {
        $failure = Get-Failure $_
        $failure.id = $request.id
        Send-Answer $failure
    }
}
