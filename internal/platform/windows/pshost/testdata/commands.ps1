# Commands for the tests of pshost. They create nothing.
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '',
    Justification = 'the noise command tests stray output')]
param()

@{
    'echo'    = {
        param($Arguments)
        $Arguments
    }
    'list'    = {
        param($Arguments)
        # the leading comma keeps a list of one (or none) a list
        , @(for ($i = 1; $i -le $Arguments.count; $i++) { $i })
    }
    'fail'    = {
        param($Arguments)
        throw "$($Arguments.kind): $($Arguments.message)"
    }
    'cmdlet'  = {
        Get-Item -Path 'C:\hlab-pshost-test-missing'
    }
    'noise'   = {
        Write-Host 'host output'
        Write-Warning 'a warning'
        [Console]::WriteLine('stray output')
        'quiet'
    }
    'sleep'   = {
        param($Arguments)
        Start-Sleep -Seconds $Arguments.seconds
    }
    'exit'    = {
        [Environment]::Exit(3)
    }
    'pid'     = {
        $PID
    }
}
