# From Windows: ping, TCP 22 banner, TCP 7000 and UDP 7001 peer echo for one address.
param([Parameter(Mandatory)] [string] $Address)
$ping = Test-Connection -ComputerName $Address -Count 3 -ErrorAction SilentlyContinue
"ping: " + $(if ($ping) { "ok avg $([math]::Round(($ping | Measure-Object ResponseTime -Average).Average, 2)) ms" } else { 'FAIL' })
function Read-Tcp([int] $Port) {
    try {
        $client = New-Object Net.Sockets.TcpClient([Net.IPAddress]::Parse($Address).AddressFamily)
        $client.Connect($Address, $Port)
        $reader = New-Object IO.StreamReader($client.GetStream())
        $line = $reader.ReadLine(); $client.Close(); $line
    } catch { "FAIL $($_.Exception.Message)" }
}
"tcp/22: " + (Read-Tcp 22)
"tcp/7000: " + (Read-Tcp 7000)
try {
    $udp = New-Object Net.Sockets.UdpClient([Net.IPAddress]::Parse($Address).AddressFamily)
    $udp.Client.ReceiveTimeout = 2000
    $udp.Connect($Address, 7001)
    [void] $udp.Send([byte[]](1, 2, 3), 3)
    $remote = New-Object Net.IPEndPoint([Net.IPAddress]::Any, 0)
    "udp/7001: " + [Text.Encoding]::ASCII.GetString($udp.Receive([ref] $remote)).Trim()
} catch { "udp/7001: FAIL $($_.Exception.Message)" }
