#Requires -Version 5.1
<#
.SYNOPSIS
    Creates a test VM for the release checks, from the empty VM up to the
    checkpoint "fresh".

.DESCRIPTION
    Run on the development machine in an administrator PowerShell. See
    docs\development\release-checks.md, "Create a test VM".

    The script:
      - refuses to run when a VM of that name already exists
      - creates the VM: Generation 2, Secure Boot template MicrosoftWindows,
        local key protector and virtual TPM, static memory, nested
        virtualization, "Default Switch", the integration service "Guest
        Service Interface" (selected by its ID), no automatic checkpoints,
        automatic stop action ShutDown; sizes follow from -Name
      - builds an answer file ISO that runs the whole Windows setup (language
        -Lang, disk layout for Generation 2, Windows 11 Pro without
        activation, a local administrator account, one automatic sign-in that
        enables Hyper-V and restarts)
      - starts the VM and opens its console, then waits until Windows answers
        over PowerShell Direct and Hyper-V is enabled
      - removes both ISOs from the VM and deletes the answer files, also when
        a step fails
      - checks the VM's subnet against HLab's addresses
      - for release-check-dev, copies release-check-dev-tools.ps1 to
        C:\Temp in the VM
      - shuts the VM down and takes the checkpoint "fresh"

    The password of the local account is asked twice at the start. It is
    written in plain text into the answer files, which are deleted at the
    end; use a password for this VM only.

.PARAMETER Name
    release-check-dev (8 vCPU, 24 GB, 200 GB) or release-check-user
    (4 vCPU, 16 GB, 150 GB).

.PARAMETER Iso
    Path to the Windows 11 ISO.

.PARAMETER Lang
    Language of the Windows ISO, for example de-DE or en-US.

.PARAMETER Cpu
    vCPU count; release-check-user accepts 4 to 8, release-check-dev only 8.

.PARAMETER VmRoot
    Folder for the VM and its disk.

.PARAMETER User
    Name of the local administrator account in the VM.

.EXAMPLE
    .\scripts\release-check-vm.ps1 -Name release-check-dev -Iso C:\VMs\iso\Win11_x64.iso -Lang de-DE
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidateSet('release-check-dev', 'release-check-user')]
    [string]$Name,

    [Parameter(Mandatory)]
    [string]$Iso,

    [Parameter(Mandatory)]
    [ValidatePattern('^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})+$')]
    [string]$Lang,

    [int]$Cpu = 0,

    [string]$VmRoot = 'C:\VMs',

    [ValidateNotNullOrEmpty()]
    [string]$User = 'tester'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Sizes = @{
    'release-check-dev'  = @{ Cpu = 8; MinCpu = 8; MaxCpu = 8; Memory = 24GB; Disk = 200GB }
    'release-check-user' = @{ Cpu = 4; MinCpu = 4; MaxCpu = 8; Memory = 16GB; Disk = 150GB }
}
$GuestServiceInterfaceId = '6C09BB55-D683-4DA0-8931-C9BF705F6480'
$SetupDeadline = [TimeSpan]::FromMinutes(90)
$AddressDeadline = [TimeSpan]::FromMinutes(5)
$PollInterval = 30
$StepCount = 9
$script:Step = 0

function Assert-Elevated {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'This script needs an administrator terminal. Start PowerShell with "Run as administrator" and run it again.'
    }
}

function Write-Step([string]$Text) {
    $script:Step++
    Write-Host "[$script:Step/$StepCount] $Text"
}

function ConvertTo-PlainText([Security.SecureString]$Secure) {
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
}

function Read-Password {
    $first = Read-Host "Password for the local account $User" -AsSecureString
    $second = Read-Host 'Repeat the password' -AsSecureString
    $a = ConvertTo-PlainText $first
    $b = ConvertTo-PlainText $second
    if ($a.Length -eq 0) {
        throw 'The password is empty. Run the script again with a password.'
    }
    if ($a -cne $b) {
        throw 'The two passwords differ. Run the script again.'
    }
    return $first
}

function New-TestVM([hashtable]$Size, [int]$Cpus) {
    New-VM -Name $Name -Generation 2 -Path $VmRoot -MemoryStartupBytes $Size.Memory `
        -NewVHDPath "$VmRoot\$Name\$Name.vhdx" -NewVHDSizeBytes $Size.Disk `
        -SwitchName 'Default Switch' | Out-Null
    Set-VMMemory -VMName $Name -DynamicMemoryEnabled $false
    Set-VMProcessor -VMName $Name -Count $Cpus -ExposeVirtualizationExtensions $true
    Set-VMFirmware -VMName $Name -EnableSecureBoot On -SecureBootTemplate MicrosoftWindows
    Set-VMKeyProtector -VMName $Name -NewLocalKeyProtector
    Enable-VMTPM -VMName $Name
    $dvd = Add-VMDvdDrive -VMName $Name -Path $Iso -Passthru
    Set-VMFirmware -VMName $Name -FirstBootDevice $dvd
    Get-VMIntegrationService -VMName $Name |
        Where-Object { $_.Id -like "*$GuestServiceInterfaceId" } |
        Enable-VMIntegrationService
    Set-VM -Name $Name -AutomaticCheckpointsEnabled $false -AutomaticStopAction ShutDown
}

function Write-AnswerFiles([string]$Dir, [string]$Password) {
    New-Item -ItemType Directory -Force $Dir | Out-Null

    $su = $User.Replace("'", "''")
    $sp = $Password.Replace("'", "''")
    $account = @"
New-LocalUser -Name '$su' -Password (ConvertTo-SecureString '$sp' -AsPlainText -Force) -PasswordNeverExpires
Add-LocalGroupMember -SID 'S-1-5-32-544' -Member '$su'
reg.exe add 'HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Setup\OOBE' /v UnattendCreatedUser /t REG_DWORD /d 1 /f
"@
    [IO.File]::WriteAllText("$Dir\account.ps1", $account, (New-Object Text.UTF8Encoding $true))

    $u = [Security.SecurityElement]::Escape($User)
    $p = [Security.SecurityElement]::Escape($Password)
    @"
<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <SetupUILanguage>
        <UILanguage>$Lang</UILanguage>
      </SetupUILanguage>
      <InputLocale>$Lang</InputLocale>
      <SystemLocale>$Lang</SystemLocale>
      <UILanguage>$Lang</UILanguage>
      <UserLocale>$Lang</UserLocale>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DiskConfiguration>
        <Disk wcm:action="add">
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add">
              <Order>1</Order>
              <Type>EFI</Type>
              <Size>100</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>2</Order>
              <Type>MSR</Type>
              <Size>16</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>3</Order>
              <Type>Primary</Type>
              <Extend>true</Extend>
            </CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add">
              <Order>1</Order>
              <PartitionID>1</PartitionID>
              <Format>FAT32</Format>
              <Label>System</Label>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>2</Order>
              <PartitionID>2</PartitionID>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>3</Order>
              <PartitionID>3</PartitionID>
              <Format>NTFS</Format>
              <Label>Windows</Label>
              <Letter>C</Letter>
            </ModifyPartition>
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>
      <ImageInstall>
        <OSImage>
          <InstallTo>
            <DiskID>0</DiskID>
            <PartitionID>3</PartitionID>
          </InstallTo>
        </OSImage>
      </ImageInstall>
      <UserData>
        <ProductKey>
          <Key>W269N-WFGWX-YVC9B-4J6C9-T83GX</Key>
          <WillShowUI>OnError</WillShowUI>
        </ProductKey>
        <AcceptEula>true</AcceptEula>
      </UserData>
    </component>
  </settings>
  <settings pass="specialize">
    <component name="Microsoft-Windows-Deployment" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <RunSynchronous>
        <RunSynchronousCommand wcm:action="add">
          <Order>1</Order>
          <Path>powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "&amp; ('{0}:\account.ps1' -f (Get-Volume -FileSystemLabel UNATTEND).DriveLetter)"</Path>
        </RunSynchronousCommand>
      </RunSynchronous>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <InputLocale>$Lang</InputLocale>
      <SystemLocale>$Lang</SystemLocale>
      <UILanguage>$Lang</UILanguage>
      <UserLocale>$Lang</UserLocale>
    </component>
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideOEMRegistrationScreen>true</HideOEMRegistrationScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>1</ProtectYourPC>
      </OOBE>
      <AutoLogon>
        <Username>$u</Username>
        <Password>
          <Value>$p</Value>
          <PlainText>true</PlainText>
        </Password>
        <Enabled>true</Enabled>
        <LogonCount>1</LogonCount>
      </AutoLogon>
      <FirstLogonCommands>
        <SynchronousCommand wcm:action="add">
          <Order>1</Order>
          <CommandLine>reg.exe add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v AutoLogonCount /t REG_DWORD /d 0 /f</CommandLine>
        </SynchronousCommand>
        <SynchronousCommand wcm:action="add">
          <Order>2</Order>
          <CommandLine>powershell.exe -NoProfile -Command "Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All -NoRestart"</CommandLine>
        </SynchronousCommand>
        <SynchronousCommand wcm:action="add">
          <Order>3</Order>
          <CommandLine>shutdown.exe /r /t 0</CommandLine>
        </SynchronousCommand>
      </FirstLogonCommands>
    </component>
  </settings>
</unattend>
"@ | Set-Content -Encoding UTF8 -LiteralPath "$Dir\autounattend.xml"

    if (-not (Test-Path -LiteralPath "$Dir\autounattend.xml" -PathType Leaf)) {
        throw "$Dir\autounattend.xml was not written; the answer ISO is not built."
    }
}

function New-AnswerIso([string]$Dir, [string]$Path) {
    if (-not ('IsoFile' -as [type])) {
        Add-Type -TypeDefinition @'
using System.IO;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;
public static class IsoFile {
  public static void Save(object image, string path) {
    var s = (IStream)image;
    var buf = new byte[65536];
    var read = Marshal.AllocHGlobal(4);
    try {
      using (var f = File.Create(path)) {
        while (true) {
          s.Read(buf, buf.Length, read);
          int n = Marshal.ReadInt32(read);
          if (n == 0) break;
          f.Write(buf, 0, n);
        }
      }
    } finally { Marshal.FreeHGlobal(read); }
  }
}
'@
    }
    $fsi = New-Object -ComObject IMAPI2FS.MsftFileSystemImage
    $fsi.FileSystemsToCreate = 3
    $fsi.VolumeName = 'UNATTEND'
    $fsi.Root.AddTree($Dir, $false)
    [IsoFile]::Save($fsi.CreateResultImage().ImageStream, $Path)
}

function Wait-WindowsSetup([pscredential]$Credential) {
    $started = Get-Date
    $deadline = $started + $SetupDeadline
    while ($true) {
        $state = 'not reachable yet'
        try {
            $state = Invoke-Command -VMName $Name -Credential $Credential -ErrorAction Stop -ScriptBlock {
                [string](Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V).State
            }
        } catch {
            $state = 'not reachable yet'
        }
        $minutes = [int]((Get-Date) - $started).TotalMinutes
        if ($state -eq 'Enabled') {
            Write-Host "    Windows is set up and Hyper-V is enabled after $minutes min."
            return
        }
        if ((Get-Date) -gt $deadline) {
            throw "Windows setup did not finish within $([int]$SetupDeadline.TotalMinutes) minutes (last state: $state). Look at the VM's console."
        }
        Write-Host "    $minutes min: Windows setup running, Hyper-V $state"
        Start-Sleep -Seconds $PollInterval
    }
}

function Remove-AnswerMedia([string]$Dir, [string]$Path) {
    if (Get-VM -Name $Name -ErrorAction SilentlyContinue) {
        Get-VMDvdDrive -VMName $Name | Where-Object { $_.Path } | Set-VMDvdDrive -Path $null
    }
    foreach ($item in @($Dir, $Path)) {
        if (Test-Path -LiteralPath $item) {
            Remove-Item -LiteralPath $item -Recurse -Force
        }
    }
}

function Test-VMSubnet([pscredential]$Credential) {
    $check = {
        function Get-IPv4Number([string]$Ip) {
            $b = ([Net.IPAddress]$Ip).GetAddressBytes()
            [double]$b[0] * 16777216 + [double]$b[1] * 65536 + [double]$b[2] * 256 + [double]$b[3]
        }
        function Test-SameSubnet([string]$A, [string]$B, [int]$Length) {
            $size = [Math]::Pow(2, 32 - $Length)
            [Math]::Floor((Get-IPv4Number $A) / $size) -eq [Math]::Floor((Get-IPv4Number $B) / $size)
        }
        Get-NetIPAddress -AddressFamily IPv4 | Where-Object PrefixOrigin -eq 'Dhcp' | ForEach-Object {
            $len = $_.PrefixLength
            $overlap = (Test-SameSubnet $_.IPAddress '172.31.1.1' $len) -or
                       (Test-SameSubnet $_.IPAddress '198.19.0.0' ([Math]::Min($len, 16)))
            [pscustomobject]@{ Line = ('{0}/{1} on {2}' -f $_.IPAddress, $len, $_.InterfaceAlias); Overlap = [bool]$overlap }
        }
    }
    $deadline = (Get-Date) + $AddressDeadline
    while ($true) {
        $result = @(Invoke-Command -VMName $Name -Credential $Credential -ScriptBlock $check)
        if ($result.Count -gt 0) {
            break
        }
        if ((Get-Date) -gt $deadline) {
            throw "The VM got no address from the Default Switch within $([int]$AddressDeadline.TotalMinutes) minutes. Look at its network adapter."
        }
        Start-Sleep -Seconds 10
    }
    foreach ($r in $result) {
        Write-Host ('    {0}: {1}' -f $r.Line, $(if ($r.Overlap) { 'OVERLAP' } else { 'ok' }))
    }
    if (@($result | Where-Object { $_.Overlap }).Count -gt 0) {
        $rest = 'Stop-VM, then Checkpoint-VM -SnapshotName fresh'
        if ($Name -eq 'release-check-dev') {
            $rest = 'copy release-check-dev-tools.ps1 to C:\Temp in the VM, ' + $rest
        }
        throw ("The VM's subnet overlaps HLab's addresses (198.19.0.0/16 or 172.31.1.1). " +
            'Shut the VM down, restart the development machine, start the VM and check again as ' +
            '"Check the subnet before the first cloud start" in docs\development\release-checks.md says; ' +
            "then finish by hand: $rest.")
    }
}

Assert-Elevated

$size = $Sizes[$Name]
if ($Cpu -eq 0) {
    $Cpu = $size.Cpu
}
if ($Cpu -lt $size.MinCpu -or $Cpu -gt $size.MaxCpu) {
    throw "-Cpu for $Name must be between $($size.MinCpu) and $($size.MaxCpu)."
}
if (-not (Test-Path -LiteralPath $Iso -PathType Leaf)) {
    throw "Windows ISO $Iso not found."
}
$Iso = (Resolve-Path -LiteralPath $Iso).Path
$DevTools = Join-Path $PSScriptRoot 'release-check-dev-tools.ps1'
if ($Name -eq 'release-check-dev' -and -not (Test-Path -LiteralPath $DevTools -PathType Leaf)) {
    throw "$DevTools not found."
}

Write-Step "Checking that no VM named $Name exists"
if (Get-VM -Name $Name -ErrorAction SilentlyContinue) {
    throw "A VM named $Name already exists. Remove it (Remove-VM and its folder under $VmRoot) or keep it; this script creates new VMs only."
}

$securePassword = Read-Password
$credential = New-Object Management.Automation.PSCredential($User, $securePassword)
$AnswerDir = "$VmRoot\$Name\unattend"
$AnswerIso = "$VmRoot\$Name\unattend.iso"

Write-Step "Creating $Name ($Cpu vCPU, $($size.Memory / 1GB) GB memory, $($size.Disk / 1GB) GB disk) in $VmRoot"
New-TestVM $size $Cpu

try {
    Write-Step 'Writing the answer files and building the answer ISO'
    $plain = ConvertTo-PlainText $securePassword
    try {
        Write-AnswerFiles $AnswerDir $plain
    } finally {
        $plain = $null
    }
    New-AnswerIso $AnswerDir $AnswerIso
    Add-VMDvdDrive -VMName $Name -Path $AnswerIso

    Write-Step 'Starting the VM and opening its console'
    Start-Process -FilePath 'vmconnect.exe' -ArgumentList 'localhost', $Name
    Start-VM -Name $Name
    Write-Host '    In the console, press a key when it shows "Press any key to boot from CD or DVD".'
    Write-Host '    Then wait; setup restarts the VM several times.'

    Write-Step "Waiting for Windows setup and Hyper-V (up to $([int]$SetupDeadline.TotalMinutes) minutes)"
    Wait-WindowsSetup $credential
} finally {
    Write-Step 'Removing the ISOs from the VM and deleting the answer files'
    try {
        Remove-AnswerMedia $AnswerDir $AnswerIso
    } catch {
        Write-Warning "Could not remove the answer files: $($_.Exception.Message). Delete $AnswerDir and $AnswerIso by hand; they hold the password."
    }
}

Write-Step 'Checking the VM subnet against 198.19.0.0/16 and 172.31.1.1'
Test-VMSubnet $credential

Write-Step 'Copying release-check-dev-tools.ps1 into the VM'
if ($Name -eq 'release-check-dev') {
    Copy-VMFile -Name $Name -SourcePath $DevTools -DestinationPath 'C:\Temp\release-check-dev-tools.ps1' `
        -FileSource Host -CreateFullPath -Force
    Write-Host '    Copied to C:\Temp\release-check-dev-tools.ps1.'
} else {
    Write-Host '    Not needed for this VM.'
}

Write-Step 'Shutting the VM down and taking the checkpoint "fresh"'
Stop-VM -Name $Name
Checkpoint-VM -Name $Name -SnapshotName 'fresh'

Write-Host "$Name is set up, shut down and has the checkpoint 'fresh'. Sign in as $User."
