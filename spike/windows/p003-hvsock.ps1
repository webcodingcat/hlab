# Register the agent's Hyper-V socket service (Linux vsock port 5000). Once, as admin.
$key = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Virtualization\GuestCommunicationServices\00001388-facb-11e6-bd58-64006a7986d3'
New-Item -Path $key -Force | Out-Null
Set-ItemProperty -Path $key -Name ElementName -Value 'HLab spike agent'
Get-ItemProperty -Path $key | Select-Object ElementName | Format-List | Out-String
