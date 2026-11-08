# The Hyper-V commands of the Windows hypervisor (hyperv.go), run by pshost.
# The script returns a hashtable from command names to script blocks; each
# block takes the arguments that Go sent, as one object.
#
# A command fails with a kind by throwing "<kind>: <message>"; hyperv.go turns
# the kinds not_found, exists, running and not_running into the platform's
# errors. Lists are returned with a leading comma, so that a list of one
# stays a list.

Import-Module Hyper-V

# The disks sit on SCSI controller 0, slot n at location n; the DVD drive for
# the seed and ISOs sits alone on controller 1. So the system disk is always
# the guest's first disk, and volumes keep their slots.
$diskController = 0
$dvdController = 1

# The private VLAN of the public switch: servers are isolated in the
# secondary VLAN and see only the appliance, which is promiscuous.
$pvlanPrimary = 10
$pvlanSecondary = 11

function Get-LabVM {
    # The VM with this name, or a not_found failure. -Name takes wildcards,
    # which the escape turns off.
    param([string] $Name)
    $vm = Get-VM -Name ([WildcardPattern]::Escape($Name)) -ErrorAction SilentlyContinue
    if ($null -eq $vm) {
        throw "not_found: VM $Name"
    }
    $vm
}

function Get-Power {
    # The platform's power state of a VM: off, running or paused.
    param($VM)
    switch -Wildcard ($VM.State.ToString()) {
        'Off*' { 'off' }
        'Saved*' { 'off' }
        'FastSaved*' { 'off' }
        'Hibernated*' { 'off' }
        'Paus*' { 'paused' }
        'Resuming*' { 'paused' }
        default { 'running' }
    }
}

function Assert-Path {
    # A not_found failure if the file is missing.
    param([string] $Path, [string] $What)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "not_found: $What $Path"
    }
}

function Add-Folder {
    # Creates the folder of a file that is to be written.
    param([string] $Path)
    $folder = Split-Path -Parent $Path
    if ($folder -and -not (Test-Path -LiteralPath $folder)) {
        New-Item -ItemType Directory -Path $folder -Force | Out-Null
    }
}

function Add-NIC {
    # Adds a NIC with its MAC address and port mode, on its switch if it
    # names one.
    param($VM, $NIC)
    $mac = $NIC.mac -replace '[:-]', ''
    $existing = Get-VMNetworkAdapter -VM $VM | Where-Object MacAddress -EQ $mac
    if ($null -ne $existing) {
        throw "exists: NIC $($NIC.mac) of VM $($VM.Name)"
    }
    $options = @{ VM = $VM; StaticMacAddress = $mac; Passthru = $true }
    if ($NIC.switch) {
        $options.SwitchName = $NIC.switch
    }
    $adapter = Add-VMNetworkAdapter @options
    # how the switch port sees VLANs
    switch ($NIC.port.kind) {
        'plain' {}
        'isolated' {
            Set-VMNetworkAdapterVlan -VMNetworkAdapter $adapter -Isolated `
                -PrimaryVlanId $pvlanPrimary -SecondaryVlanId $pvlanSecondary
        }
        'promiscuous' {
            Set-VMNetworkAdapterVlan -VMNetworkAdapter $adapter -Promiscuous `
                -PrimaryVlanId $pvlanPrimary -SecondaryVlanIdList $pvlanSecondary
        }
        'access' {
            Set-VMNetworkAdapterVlan -VMNetworkAdapter $adapter -Access -VlanId $NIC.port.vlan
        }
        'trunk' {
            Set-VMNetworkAdapterVlan -VMNetworkAdapter $adapter -Trunk `
                -AllowedVlanIdList '2-4094' -NativeVlanId 0
        }
        default { throw "invalid: unknown port kind '$($NIC.port.kind)'" }
    }
}

function Get-SlotDrive {
    # The disk drive in a slot, or $null.
    param($VM, [int] $Slot)
    Get-VMHardDiskDrive -VM $VM -ControllerType SCSI -ControllerNumber $diskController |
        Where-Object ControllerLocation -EQ $Slot
}

function Add-Disk {
    # Puts a disk file into a free slot.
    param($VM, $Disk)
    if ($null -ne (Get-SlotDrive $VM $Disk.slot)) {
        throw "exists: slot $($Disk.slot) of VM $($VM.Name)"
    }
    Assert-Path $Disk.path 'disk'
    Add-VMHardDiskDrive -VM $VM -ControllerType SCSI -ControllerNumber $diskController `
        -ControllerLocation $Disk.slot -Path $Disk.path
}

function Save-ImageStream {
    # Writes the IStream of an IMAPI2 image to a file; PowerShell cannot read
    # an IStream itself, so a few lines of C# do it.
    param($Stream, [string] $Path)
    if (-not ('HLab.ImageStream' -as [type])) {
        Add-Type -TypeDefinition @'
namespace HLab {
    using System.IO;
    using System.Runtime.InteropServices.ComTypes;
    public static class ImageStream {
        public static void Save(object image, string path) {
            var stream = (IStream)image;
            var buffer = new byte[1 << 16];
            var count = System.Runtime.InteropServices.Marshal.AllocHGlobal(sizeof(int));
            try {
                using (var file = File.Create(path)) {
                    while (true) {
                        stream.Read(buffer, buffer.Length, count);
                        int read = System.Runtime.InteropServices.Marshal.ReadInt32(count);
                        if (read == 0) { break; }
                        file.Write(buffer, 0, read);
                    }
                }
            } finally {
                System.Runtime.InteropServices.Marshal.FreeHGlobal(count);
            }
        }
    }
}
'@
    }
    [HLab.ImageStream]::Save($Stream, $Path)
}

@{
    'create-vm'      = {
        param($Spec)
        if ($null -ne (Get-VM -Name ([WildcardPattern]::Escape($Spec.name)) -ErrorAction SilentlyContinue)) {
            throw "exists: VM $($Spec.name)"
        }
        foreach ($disk in $Spec.disks) {
            Assert-Path $disk.path 'disk'
        }
        $vm = New-VM -Name $Spec.name -Generation 2 -NoVHD `
            -MemoryStartupBytes ([int64]$Spec.memory_mb * 1MB)
        try {
            # New-VM adds a NIC without a MAC of ours; the spec's NICs replace it
            Get-VMNetworkAdapter -VM $vm | Remove-VMNetworkAdapter
            # Hetzner's servers have static memory; the guest would see only
            # the start value with dynamic memory. A stopping host shuts the
            # guests down; HLab starts them itself.
            Set-VM -VM $vm -ProcessorCount $Spec.cpus -StaticMemory `
                -AutomaticCheckpointsEnabled $false -AutomaticStartAction Nothing `
                -AutomaticStopAction ShutDown -Notes $Spec.tag
            # the official cloud images boot with Microsoft's UEFI CA
            Set-VMFirmware -VM $vm -EnableSecureBoot On `
                -SecureBootTemplate MicrosoftUEFICertificateAuthority
            foreach ($disk in $Spec.disks) {
                Add-Disk $vm $disk
            }
            # a Gen2 VM takes a DVD drive only while it is off: add an empty
            # one now, SetISO fills it later
            Add-VMScsiController -VM $vm
            Add-VMDvdDrive -VM $vm -ControllerNumber $dvdController -ControllerLocation 0
            foreach ($nic in $Spec.nics) {
                Add-NIC $vm $nic
            }
            $system = Get-SlotDrive $vm 0
            if ($null -ne $system) {
                Set-VMFirmware -VM $vm -FirstBootDevice $system
            }
        } catch {
            Remove-VM -VM $vm -Force
            throw
        }
    }

    'start-vm'       = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        switch (Get-Power $vm) {
            'off' { Start-VM -VM $vm }
            'paused' { throw "running: VM $($vm.Name) is paused" }
        }
    }

    'shutdown-vm'    = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        switch (Get-Power $vm) {
            'paused' { throw "running: VM $($vm.Name) is paused" }
            'running' {
                # Hyper-V has no power button; the guest's shutdown service is
                # the nearest thing. A guest without it ignores the request,
                # as a guest ignores ACPI on Hetzner. InitiateShutdown returns
                # once the guest took the request, without waiting for off.
                $system = Get-CimInstance -Namespace root\virtualization\v2 `
                    -ClassName Msvm_ComputerSystem -Filter "Name='$($vm.Id)'"
                $service = Get-CimAssociatedInstance -InputObject $system `
                    -ResultClassName Msvm_ShutdownComponent
                if ($null -ne $service) {
                    Invoke-CimMethod -InputObject $service -MethodName InitiateShutdown `
                        -Arguments @{ Force = $true; Reason = 'HLab' } | Out-Null
                }
            }
        }
    }

    'stop-vm'        = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        if ((Get-Power $vm) -ne 'off') {
            Stop-VM -VM $vm -TurnOff -Force
        }
    }

    'reset-vm'       = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        if ((Get-Power $vm) -ne 'running') {
            throw "not_running: VM $($vm.Name) is $(Get-Power $vm)"
        }
        Restart-VM -VM $vm -Type Reset -Force
    }

    'pause-vm'       = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        $power = Get-Power $vm
        if ($power -eq 'off') {
            throw "not_running: VM $($vm.Name) is off"
        }
        if ($Arguments.paused -and $power -eq 'running') {
            Suspend-VM -VM $vm
        } elseif (-not $Arguments.paused -and $power -eq 'paused') {
            Resume-VM -VM $vm
        }
    }

    'delete-vm'      = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        if ((Get-Power $vm) -ne 'off') {
            Stop-VM -VM $vm -TurnOff -Force
        }
        Remove-VM -VM $vm -Force
    }

    'vm-state'       = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        $power = Get-Power $vm
        @{
            power     = $power
            heartbeat = $power -eq 'running' -and $vm.Heartbeat.ToString() -like 'Ok*'
        }
    }

    'find-vms'       = {
        param($Arguments)
        # Get-VM reads every VM (about 10 ms each): only for the start
        , @(Get-VM | Where-Object Notes -EQ $Arguments.tag | Sort-Object Name |
                ForEach-Object { @{ name = $_.Name; power = Get-Power $_ } })
    }

    'set-cpu-memory' = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        if ((Get-Power $vm) -ne 'off') {
            throw "running: VM $($vm.Name) must be off"
        }
        Set-VM -VM $vm -ProcessorCount $Arguments.cpus -StaticMemory `
            -MemoryStartupBytes ([int64]$Arguments.memory_mb * 1MB)
    }

    'attach-disk'    = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        Add-Disk $vm $Arguments.disk
    }

    'detach-disk'    = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        $drive = Get-SlotDrive $vm $Arguments.slot
        if ($null -eq $drive) {
            throw "not_found: slot $($Arguments.slot) of VM $($vm.Name)"
        }
        Remove-VMHardDiskDrive -VMHardDiskDrive $drive
    }

    'attach-nic'     = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        Add-NIC $vm $Arguments.nic
    }

    'detach-nic'     = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        $mac = $Arguments.mac -replace '[:-]', ''
        $adapter = Get-VMNetworkAdapter -VM $vm | Where-Object MacAddress -EQ $mac
        if ($null -eq $adapter) {
            throw "not_found: NIC $($Arguments.mac) of VM $($vm.Name)"
        }
        Remove-VMNetworkAdapter -VMNetworkAdapter $adapter
    }

    'set-iso'        = {
        param($Arguments)
        $vm = Get-LabVM $Arguments.name
        $drive = Get-VMDvdDrive -VM $vm -ControllerNumber $dvdController
        if ($Arguments.path) {
            Assert-Path $Arguments.path 'ISO'
            Set-VMDvdDrive -VMDvdDrive $drive -Path $Arguments.path
        } else {
            Set-VMDvdDrive -VMDvdDrive $drive -Path $null
        }
    }

    'create-disk'    = {
        param($Arguments)
        if (Test-Path -LiteralPath $Arguments.path) {
            throw "exists: disk $($Arguments.path)"
        }
        $size = [int64]$Arguments.size_gb * 1GB
        Add-Folder $Arguments.path
        if ($Arguments.parent) {
            Assert-Path $Arguments.parent 'parent disk'
            New-VHD -Path $Arguments.path -ParentPath $Arguments.parent -Differencing | Out-Null
            if ((Get-VHD -Path $Arguments.path).Size -lt $size) {
                Resize-VHD -Path $Arguments.path -SizeBytes $size
            }
        } else {
            # Microsoft's advice for Linux guests: 1 MB blocks keep the
            # dynamic disk small
            New-VHD -Path $Arguments.path -SizeBytes $size -Dynamic -BlockSizeBytes 1MB | Out-Null
        }
    }

    'resize-disk'    = {
        param($Arguments)
        Assert-Path $Arguments.path 'disk'
        $size = [int64]$Arguments.size_gb * 1GB
        $current = (Get-VHD -Path $Arguments.path).Size
        if ($size -lt $current) {
            throw "invalid: disk $($Arguments.path) has $($current / 1GB) GB and cannot shrink"
        }
        if ($size -gt $current) {
            Resize-VHD -Path $Arguments.path -SizeBytes $size
        }
    }

    'flatten-disk'   = {
        param($Arguments)
        Assert-Path $Arguments.src 'disk'
        if (Test-Path -LiteralPath $Arguments.dst) {
            throw "exists: disk $($Arguments.dst)"
        }
        Add-Folder $Arguments.dst
        Convert-VHD -Path $Arguments.src -DestinationPath $Arguments.dst -VHDType Dynamic
    }

    'delete-disk'    = {
        param($Arguments)
        Assert-Path $Arguments.path 'disk'
        # an off VM does not hold its disks open, so ask Hyper-V who uses it
        $user = Get-VM | Get-VMHardDiskDrive | Where-Object Path -EQ $Arguments.path |
            Select-Object -First 1
        if ($null -ne $user) {
            throw "in_use: disk $($Arguments.path) of VM $($user.VMName)"
        }
        Remove-Item -LiteralPath $Arguments.path -Force
    }

    'vm-id'          = {
        param($Arguments)
        (Get-LabVM $Arguments.name).Id.Guid
    }

    'create-seed'    = {
        param($Arguments)
        # IMAPI2 writes ISO 9660 with Joliet, whose long names cloud-init
        # reads; it takes its files from a folder
        $work = Join-Path ([IO.Path]::GetTempPath()) ('hlab-seed-' + [guid]::NewGuid())
        New-Item -ItemType Directory -Path $work | Out-Null
        try {
            foreach ($file in $Arguments.files.PSObject.Properties) {
                [IO.File]::WriteAllBytes((Join-Path $work $file.Name),
                    [Convert]::FromBase64String($file.Value))
            }
            $image = New-Object -ComObject IMAPI2FS.MsftFileSystemImage
            $image.FileSystemsToCreate = 3 # ISO 9660 and Joliet
            $image.VolumeName = 'cidata'
            $image.Root.AddTree($work, $false)
            $partial = "$($Arguments.path).tmp"
            Add-Folder $Arguments.path
            Save-ImageStream $image.CreateResultImage().ImageStream $partial
            Move-Item -LiteralPath $partial -Destination $Arguments.path -Force
        } finally {
            Remove-Item -LiteralPath $work -Recurse -Force
        }
    }
}
