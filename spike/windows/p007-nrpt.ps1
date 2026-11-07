# P0-07: NRPT rules for the lab zones, time from rule to answer (negative cache primed first).
function Wait-Name([string] $Name, [string] $Type = 'A') {
    $start = Get-Date
    while (((Get-Date) - $start).TotalSeconds -lt 30) {
        $answer = Resolve-DnsName $Name -Type $Type -ErrorAction SilentlyContinue | Where-Object { $_.Section -eq 'Answer' }
        if ($answer) { return '{0} {1} -> {2} after {3:N0} ms' -f $Name, $Type, (($answer | ForEach-Object { $_.IPAddress + $_.NameHost }) -join ','), ((Get-Date) - $start).TotalMilliseconds }
        Start-Sleep -Milliseconds 100
    }
    "$Name $Type -> no answer after 30 s"
}
Get-DnsClientNrptRule | Where-Object Comment -eq 'hlab-spike' | Remove-DnsClientNrptRule -Force
# Prime the negative cache.
Resolve-DnsName web.example.com -ErrorAction SilentlyContinue | Out-Null
Resolve-DnsName u123.your-storagebox.de -ErrorAction SilentlyContinue | Out-Null
Add-DnsClientNrptRule -Namespace '.example.com' -NameServers 198.18.0.2 -Comment 'hlab-spike'
Wait-Name web.example.com
Add-DnsClientNrptRule -Namespace '.16.18.198.in-addr.arpa' -NameServers 198.18.0.2 -Comment 'hlab-spike'
Wait-Name 10.16.18.198.in-addr.arpa PTR
Add-DnsClientNrptRule -Namespace 'u123.your-storagebox.de' -NameServers 198.18.0.2 -Comment 'hlab-spike'
Wait-Name u123.your-storagebox.de
'-- side effects'
'your-storagebox.de (real) -> ' + ((Resolve-DnsName your-storagebox.de -Type A -ErrorAction SilentlyContinue | Where-Object Section -eq Answer).IPAddress -join ',')
'www.hetzner.com -> ' + ((Resolve-DnsName www.hetzner.com -Type A -ErrorAction SilentlyContinue | Where-Object Section -eq Answer | Select-Object -First 1).IPAddress)
'example.com (apex, real IANA site, now lab) -> ' + ((Resolve-DnsName example.com -Type A -ErrorAction SilentlyContinue | Where-Object Section -eq Answer).IPAddress -join ',')
'-- curl.exe http://web.example.com:7000 (peer echo on guest)'
& curl.exe -s -m 5 http://web.example.com:7000/ 2>&1 | Select-Object -First 1
Get-DnsClientNrptRule | Where-Object Comment -eq 'hlab-spike' | Format-Table Namespace, NameServers -AutoSize | Out-String
