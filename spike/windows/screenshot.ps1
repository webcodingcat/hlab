# Save a screenshot of a VM console as PNG (Msvm thumbnail API). Spike tool.
param([Parameter(Mandatory)] [string] $Name, [string] $Out = "C:\HLabSpike\screen.png")
Add-Type -AssemblyName System.Drawing
$vm = Get-CimInstance -Namespace root\virtualization\v2 -ClassName Msvm_ComputerSystem -Filter "ElementName='$Name'"
$settings = Get-CimAssociatedInstance -InputObject $vm -ResultClassName Msvm_VirtualSystemSettingData |
    Where-Object VirtualSystemType -eq 'Microsoft:Hyper-V:System:Realized'
$service = Get-CimInstance -Namespace root\virtualization\v2 -ClassName Msvm_VirtualSystemManagementService
$width = 800; $height = 600
$result = Invoke-CimMethod -InputObject $service -MethodName GetVirtualSystemThumbnailImage -Arguments @{
    TargetSystem = $settings; WidthPixels = [uint16]$width; HeightPixels = [uint16]$height }
$bitmap = New-Object Drawing.Bitmap($width, $height, [Drawing.Imaging.PixelFormat]::Format16bppRgb565)
$data = $bitmap.LockBits((New-Object Drawing.Rectangle(0, 0, $width, $height)), 'WriteOnly', $bitmap.PixelFormat)
$bytes = [byte[]]$result.ImageData
"image bytes: $($bytes.Length), buffer: $($data.Stride * $height)"
[Runtime.InteropServices.Marshal]::Copy($bytes, 0, $data.Scan0, [math]::Min($bytes.Length, $data.Stride * $height))
$bitmap.UnlockBits($data)
$bitmap.Save($Out, [Drawing.Imaging.ImageFormat]::Png)
