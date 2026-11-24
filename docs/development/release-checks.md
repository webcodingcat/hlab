# Release checks

Before a milestone is released, HLab is checked on Windows machines that
look like a user's: a fresh Windows 11 Pro without the history of the
development machine. Two Hyper-V VMs on the development machine serve for
this. Each has nested virtualization on, so HLab can run its own VMs
inside it. This guide says how to set them up by hand and what runs in
which.

## What runs where

| Where | What | Who starts it |
|---|---|---|
| Development machine | `task e2e` in full against the dev service ([Build and test](build-and-test.md#end-to-end-tests)) | the developer, in a normal terminal |
| Development test VM | `task e2e:elevated`, the [elevated installer suite](build-and-test.md#elevated-installer-suite) (INST-01, INST-04, INST-05, INST-06) | the developer, in an administrator terminal in the VM |
| User test VM | the manual checks a user goes through: installing with the setup program, the tray console, shutting down, uninstalling | the developer, by hand, as a user would |

The development test VM comes first: its suite installs, updates and
uninstalls the release automatically, so the user test VM only sees setup
programs that already passed it. The two VMs need not run at the same
time; set up and use one, shut it down, then the other.

## Create a test VM

Both VMs are created the same way; only their sizes differ:

| VM | Name used below | vCPU | Memory (static) | Disk |
|---|---|---|---|---|
| Development test VM | `release-check-dev` | 8 | 24 GB | 200 GB |
| User test VM | `release-check-user` | 4 to 8 | 16 GB | 150 GB |

The names avoid the prefix `hlab-`, which HLab uses for its own VMs. Run the
PowerShell commands of this section on the development machine, in an
administrator PowerShell or as a member of "Hyper-V Administrators".

### Get the Windows 11 ISO

Download "Windows 11 Disk Image (ISO) for x64 devices" from
<https://www.microsoft.com/software-download/windows11> (the download link
is valid for 24 hours). The page lists the SHA-256 hash of each language's
ISO; compare it with the downloaded file:

```powershell
Get-FileHash C:\VMs\iso\Win11_x64.iso -Algorithm SHA256
```

### Create the VM

Set the first lines to the VM you create, then paste the whole block.
`$VmRoot` must be on a disk with room for the VM's disk.

```powershell
$Name   = 'release-check-dev'          # or 'release-check-user'
$Cpu    = 8                            # user test VM: 4 to 8
$Memory = 24GB                         # user test VM: 16GB
$Disk   = 200GB                        # user test VM: 150GB
$Iso    = 'C:\VMs\iso\Win11_x64.iso'
$VmRoot = 'C:\VMs'

New-VM -Name $Name -Generation 2 -Path $VmRoot -MemoryStartupBytes $Memory `
  -NewVHDPath "$VmRoot\$Name\$Name.vhdx" -NewVHDSizeBytes $Disk `
  -SwitchName 'Default Switch'
Set-VMMemory -VMName $Name -DynamicMemoryEnabled $false
Set-VMProcessor -VMName $Name -Count $Cpu -ExposeVirtualizationExtensions $true
Set-VMFirmware -VMName $Name -EnableSecureBoot On -SecureBootTemplate MicrosoftWindows
Set-VMKeyProtector -VMName $Name -NewLocalKeyProtector
Enable-VMTPM -VMName $Name
$dvd = Add-VMDvdDrive -VMName $Name -Path $Iso -Passthru
Set-VMFirmware -VMName $Name -FirstBootDevice $dvd
Enable-VMIntegrationService -VMName $Name -Name 'Guest Service Interface'
Set-VM -Name $Name -AutomaticCheckpointsEnabled $false -AutomaticStopAction ShutDown
```

What the block sets and why:

- Generation 2 with Secure Boot (template `MicrosoftWindows`) and a
  virtual TPM: Windows 11 requires them. The TPM's key protector is bound
  to this development machine; the VM does not start on another host
  without a new one.
- Static memory: while Hyper-V runs inside a VM, its memory cannot change.
- `-ExposeVirtualizationExtensions $true` turns on nested virtualization.
  It can only be set while the VM is off. On an AMD processor the VM's
  configuration version must be 9.3 or higher; `Get-VM $Name | Select-Object
  Name, Version` shows it (a Windows 11 host creates 12.x).
- "Default Switch" gives the VM internet access through the development
  machine. HLab builds its own switch and NAT inside the VM, as on a real
  machine.
- The integration service "Guest Service Interface" lets `Copy-VMFile` copy
  files from the development machine into the VM. On a Windows host in
  another language the service has a translated name;
  `Get-VMIntegrationService -VMName $Name` lists the names.
- No automatic checkpoints, and the VM shuts down instead of saving its
  state when the development machine shuts down (see
  [Shut a VM down, do not save it](#shut-a-vm-down-do-not-save-it)).

### Install Windows

Windows setup asks for a Microsoft account; these checks need a local
account. Which way to it works depends on the Windows build; try them in
this order.

**Answer file (first choice).** Before the first start, put an answer file
on a second virtual DVD. Windows setup reads `autounattend.xml` from the
root of a DVD; the file below hides the online account screens and creates
one local account in the group Administrators. It holds the password in
plain text, so use one for this VM only, and delete the folder and the ISO
once Windows is set up. Paste the block on the development machine, with
`$Name` and `$VmRoot` as above:

```powershell
$User     = 'tester'
$Password = Read-Host "Password for the local account $User"
$Dir      = "$VmRoot\$Name\unattend"
$UnattendIso = "$VmRoot\$Name\unattend.iso"
New-Item -ItemType Directory -Force $Dir | Out-Null
$u = [Security.SecurityElement]::Escape($User)
$p = [Security.SecurityElement]::Escape($Password)
@"
<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
      </OOBE>
      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Name>$u</Name>
            <Group>Administrators</Group>
            <Password>
              <Value>$p</Value>
              <PlainText>true</PlainText>
            </Password>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>
    </component>
  </settings>
</unattend>
"@ | Set-Content -Encoding UTF8 "$Dir\autounattend.xml"

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
$fsi = New-Object -ComObject IMAPI2FS.MsftFileSystemImage
$fsi.FileSystemsToCreate = 3
$fsi.VolumeName = 'UNATTEND'
$fsi.Root.AddTree($Dir, $false)
[IsoFile]::Save($fsi.CreateResultImage().ImageStream, $UnattendIso)
Add-VMDvdDrive -VMName $Name -Path $UnattendIso
```

The ISO is built with Windows' own image mastering interface (IMAPI), so no
extra tool is needed. The Windows ISO stays the first boot device.

**"Domain join instead".** Without the answer file, on "How would you like
to set up this device?" choose "Set up for work or school", then "Sign-in
options", then "Domain join instead".

**Setup without network.** Disconnect the VM from the network before the
setup screens ask for an account, so setup offers "I don't have internet",
and connect it again after the first sign-in:

```powershell
Disconnect-VMNetworkAdapter -VMName $Name
Connect-VMNetworkAdapter -VMName $Name -SwitchName 'Default Switch'
```

Then start the VM and open its console:

```powershell
Start-VM -Name $Name
vmconnect.exe localhost $Name
```

Press a key when the console shows "Press any key to boot from CD or DVD".
In Windows setup:

1. Choose "I don't have a product key"; Windows stays unactivated, which is
   enough for these checks (a watermark, and personalization is locked).
2. Choose **Windows 11 Pro**: Hyper-V needs Pro, Enterprise or Education.
3. Install on the empty disk.

After the restarts, sign in with the local account. It is an administrator
under User Account Control, like on most users' machines. Then remove the
ISOs from the VM, and delete the answer file folder and its ISO if you
used them:

```powershell
Get-VMDvdDrive -VMName $Name | Set-VMDvdDrive -Path $null
Remove-Item -Recurse "$VmRoot\$Name\unattend", "$VmRoot\$Name\unattend.iso" -ErrorAction SilentlyContinue
```

### Enable Hyper-V in the VM

In an administrator PowerShell inside the VM:

```powershell
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All
Restart-Computer
```

If the feature cannot be enabled, nested virtualization is off: shut the
VM down, check `Get-VMProcessor -VMName $Name | Select-Object
ExposeVirtualizationExtensions` on the development machine, and set it
again while the VM is off.

### Check the subnet before the first cloud start

HLab's public range is `198.19.0.0/16`, and its servers use `172.31.1.1`
as their gateway. The "Default Switch" gives the VM an address from
`172.16.0.0/12` and may choose another subnet after the development machine
restarts. If the VM's subnet contains `172.31.1.1` or overlaps
`198.19.0.0/16`, HLab's addresses inside the VM clash with it. Before the
first cloud start in each VM, run in a PowerShell inside the VM:

```powershell
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
  '{0}/{1} on {2}: {3}' -f $_.IPAddress, $len, $_.InterfaceAlias, $(if ($overlap) { 'OVERLAP' } else { 'ok' })
}
```

Every line must end in `ok`. On `OVERLAP`, shut the VM down, restart the
development machine, start the VM and check again; the Default Switch then
picks a new subnet.

### Shut a VM down, do not save it

A VM with Hyper-V running inside cannot be saved reliably. End every
session by shutting Windows down in the VM, or from the development
machine:

```powershell
Stop-VM -Name $Name
```

Never use `Save-VM` or "Save" in Hyper-V Manager for these VMs.

### Checkpoint the fresh VM

Once a VM is set up as its section below describes, shut it down and
take a checkpoint while it is off. Each check run can then start from the
same clean state:

```powershell
Checkpoint-VM -Name $Name -SnapshotName 'fresh'
# later, with the VM off, back to the clean state:
Restore-VMCheckpoint -VMName $Name -Name 'fresh' -Confirm:$false
```

## Development test VM

The development test VM runs only the elevated installer suite. It gets
the tools the suite needs and a clone of the repository, nothing else.

### Install the tools

In an administrator PowerShell inside the VM, install the tools with
winget. The versions must match [Developer setup](setup.md#tools); check
them with `go version`, `node --version` and `task --version` afterwards.

```powershell
winget install --id Git.Git -e
winget install --id GoLang.Go -e
winget install --id OpenJS.NodeJS.LTS -e
winget install --id Task.Task -e
winget install --id Microsoft.DotNet.SDK.8 -e
```

Open a new administrator PowerShell, so the new `PATH` applies, and add
WiX with its license and extensions, and Wails:

```powershell
dotnet tool install --global wix --version 7.0.1
wix eula accept wix7
wix extension add -g WixToolset.Util.wixext/7.0.1
wix extension add -g WixToolset.BootstrapperApplications.wixext/7.0.1
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
```

Install WSL2 with Ubuntu, restart, then open "Ubuntu" from the Start menu
once and create its user:

```powershell
wsl --install -d Ubuntu-24.04
Restart-Computer
```

The suite needs no hcloud, OpenTofu or Ansible.

### Bring the repository in

On the development machine, pack `main` into a bundle, copy it into the
running VM and print its hash:

```powershell
git -C C:\path\to\hlab bundle create C:\VMs\hlab.bundle main
Copy-VMFile -Name 'release-check-dev' -SourcePath C:\VMs\hlab.bundle `
  -DestinationPath C:\Temp\hlab.bundle -FileSource Host -CreateFullPath -Force
(Get-FileHash C:\VMs\hlab.bundle -Algorithm SHA256).Hash
```

The bundle holds what is committed on `main`, nothing uncommitted.
`Copy-VMFile` fails with "Failed to initiate copying files to the guest"
while the VM is still booting; wait until Windows shows the sign-in screen
and run it again.

Inside the VM, compare the hash with the one printed on the development
machine, then clone:

```powershell
(Get-FileHash C:\Temp\hlab.bundle -Algorithm SHA256).Hash
git clone -b main C:\Temp\hlab.bundle C:\src\hlab
```

### Register the service and start the dev cloud once

In an administrator PowerShell inside the VM, in `C:\src\hlab`:

```powershell
task build
.\scripts\dev-setup.ps1
```

[Register the development service](setup.md#register-the-development-service)
says what `dev-setup.ps1` does. Run
[the subnet check](#check-the-subnet-before-the-first-cloud-start), then
start the dev cloud once, so it downloads its images and packages; the
suite copies them for the release cloud, which then downloads nothing:

```powershell
.\bin\hlab.exe cloud start
.\bin\hlab.exe cloud status
```

The first start takes several minutes; repeat `cloud status` until it says
`state: running`. The cloud may keep running; the suite stops it and starts
it again at the end.

### Run the elevated installer suite

The suite needs:

- an administrator terminal, in `C:\src\hlab`,
- no release HLab installed: neither the service `hlab` nor an HLab entry
  in Settings > Apps (on a fresh VM there is none),
- the dev service `hlab-dev` registered from this clone, and the Ubuntu
  distribution of the step above.

Run it, keeping a copy of the output in `C:\Temp`:

```powershell
cmd /c "task e2e:elevated 2>&1" | Tee-Object -FilePath C:\Temp\e2e-elevated.log
```

The suite builds the setup programs 0.0.1 and 0.0.2 from the clone, then
runs the rows `INST-01`, `INST-04`, `INST-05` and `INST-06` with their
subtests one after another. Each row logs
`start TestInstall/<row>` and `end TestInstall/<row>: ok` with its time.
A full run can take several hours; the suite's limit is seven hours.

It passed when the last line starts with `ok` and names the package
`github.com/webcodingcat/hlab/test/install`. On a failure the output names the failing row
with its message, and the suite's own logs (the setup programs' logs
among them) stay in the folder `%TEMP%\hlab-e2einstall-*` that its line
`teardown: the logs of this run stay in ...` names. Keep that folder and
`C:\Temp\e2e-elevated.log` for the report.

## User test VM

The user test VM gets no development tools: no Git, Go, WSL2 or hcloud.
It has only what Windows brings, among them Explorer and the built-in
`ssh` client, and the setup programs of the release.

### Copy the setup programs in

On the development machine, in the repository, build two versions, so the
update can be checked as well:

```powershell
task installer VERSION=0.1.0
task installer VERSION=0.1.1
```

Each build ends with `built ...\bin\hlab-setup-<version>.exe`. Start the
user test VM and copy both into it:

```powershell
foreach ($v in '0.1.0', '0.1.1') {
  $exe = (Resolve-Path "bin\hlab-setup-$v.exe").Path
  Copy-VMFile -Name 'release-check-user' -SourcePath $exe `
    -DestinationPath "C:\Temp\hlab-setup-$v.exe" -FileSource Host -CreateFullPath -Force
}
```

Run the loop in the repository root. The version of the milestone itself
is decided when the milestone closes; 0.1.0 and 0.1.1 only serve the
checks.
