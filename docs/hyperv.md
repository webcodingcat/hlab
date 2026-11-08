# Hyper-V

How HLab drives Hyper-V and the host's network on Windows. DEVELOPING.md
explains the platform interfaces this implements (Platform).

The Windows hypervisor (`internal/platform/windows/hyperv.go`) runs the commands of
`hyperv.ps1` in long-running PowerShell processes (`pshost`). It needs administrator
rights, which `hlabd` and the live tests have, or the group Hyper-V Administrators.

**pshost** keeps up to four `powershell.exe` (Windows PowerShell 5.1, which every
Windows 11 has and which loads the Hyper-V module natively). Starting one and
loading the module takes most of a second; a command then takes about 30 ms.

Why a pool of a fixed size and not one process: commands of one process run one
after another, and some take minutes. While `Convert-VHD` flattens a 40 GB disk for
a snapshot, a single process would hold up every `VMState`, `StartVM` and
`CreateVM` of the lab. The pool stays simple: a fixed size, no priorities; a call
waits only when all four are busy.

- The processes start on demand; commands in one process run one after another, so
  a slow command (`Convert-VHD`) holds up only its own process.
- The loop (`pshost/host.ps1`) gets the caller's script as its first line. The
  script returns a hashtable from command names to script blocks, so only those
  commands can run.
- A command fails with a kind by throwing `"<kind>: <message>"`, such as
  `"not_found: VM web"`. `hyperv.go` turns the kinds `not_found`, `exists`,
  `running` and `not_running` into the platform's errors; a cmdlet's error has no
  kind.
- A command returns a list with a leading comma (`, @(...)`); PowerShell would turn
  a list of one into the item, and an empty list into nothing.
- Answers start with the character 0x1E. Anything else on stdout (`Write-Host`,
  `[Console]::WriteLine`) is logged at debug level and skipped.
- A canceled call kills its process, and so does a process that ends by itself
  (`pshost.ErrCrashed`, with the end of its error output). The next call starts a
  new one. Hyper-V may still finish what the killed command had started, so a
  caller looks at the VM again rather than assume nothing happened.
- The processes run with `CREATE_NO_WINDOW`, so `hlabd` without a console opens no
  window.

**What Hyper-V does differently**, and how `hyperv.ps1` deals with it:

- A VM is generation 2 with Secure Boot and the template "Microsoft UEFI
  Certificate Authority"; the official cloud images boot with it unchanged.
- Memory is static, as on Hetzner: with dynamic memory the guest would see only the
  start value.
- At every VM, `AutomaticStopAction` is `ShutDown` and `AutomaticStartAction` is
  `Nothing`: a stopping host shuts the guests down, and HLab starts them itself.
- Disks sit on SCSI controller 0, a slot is its location; the system disk is slot
  0 and the first boot device, so the guest sees it as `sda`. A Gen2 VM takes a DVD
  drive only while it is off, so every VM gets an empty one on controller 1 when it
  is created; `SetISO` only fills or empties it.
- `New-VM` adds a NIC of its own; `CreateVM` removes it and adds the spec's NICs
  with their MAC addresses.
- Ports: servers' public NICs are isolated in the private VLAN 10/11 and see only
  the appliance, which is promiscuous; private NICs are access ports, the
  appliance's private NIC a trunk for VLANs 2–4094.
- Hyper-V has no ACPI button. `ShutdownVM` asks the guest's shutdown service
  (`Msvm_ShutdownComponent.InitiateShutdown`) and does not wait; a guest without
  it ignores the request, as a guest without ACPI does on Hetzner. `ResetVM` is
  `Restart-VM -Type Reset`.
- States: `Saved` and `Hibernated` count as off, `Pausing` and `Resuming` as
  paused, everything else that is not off as running. The heartbeat counts when
  Hyper-V reports `Ok…`.
- A VM's power state and heartbeat come from WMI (`Msvm_ComputerSystem`,
  `Msvm_HeartbeatComponent`), not from `Get-VM`: in a long-running process,
  `Get-VM` shows a VM as it was up to seconds ago when another process of
  the pool changed it, so the watchdog took a server that had just started
  for one that had shut down, and an appliance turned off for one running.
- `Get-VM -Name` takes wildcards, so names are escaped. `Get-VM` without a name
  reads every VM (about 10 ms each); only `FindVMs` does that, when HLab starts.
- Disks are dynamic VHDX with 1 MB blocks, as Microsoft advises for Linux guests,
  or differencing VHDX on a parent. Hyper-V cannot use files in WSL2's file system.
- `qemu-img` cannot open a differencing VHDX; `FlattenDisk` (`Convert-VHD`) writes
  one with its parents into a plain one first.
- The seed is an ISO 9660 with Joliet, labeled `cidata`, written with IMAPI2.
  Joliet keeps the long names `meta-data` and `network-config`. PowerShell cannot
  read the IStream that IMAPI2 returns, so a few lines of C# (`Add-Type`, once per
  process) write it to the file.
- `DeleteDisk` asks Hyper-V which VM uses the disk: an off VM does not hold its
  disks open, so the file alone would not tell.

**The host's side** (`hostnet.go`, `hostnet.ps1`) runs in a `pshost` of one
process each for `HostNet` and `HostDNS`; it needs administrator rights.

- `SetUp` creates the internal switch (Windows adds the host's NIC `vEthernet
  (<switch>)`) and the private ones, or takes them over, and makes the NIC's
  addresses and routes exactly those of the link: what is missing is added,
  what is too much goes, Windows' own link-local addresses stay. DHCP is off on
  that NIC.
- Addresses and routes go to the active store only: a restart of Windows clears
  them, and the next start of HLab sets them again. The switches stay until
  `TearDown`, which removes the switches of the last `SetUp`; with the internal
  one go its NIC, addresses and routes.
- `CheckRanges` lists every route and address of the active store, but those of
  NICs on the lab's own switches (`platform.SwitchPrefix`), and Go finds the
  overlaps; default routes do not count.
- The NRPT rules carry the comment `hlab`, so a start finds those of a crashed
  HLab. `Apply` adds and removes only what differs and then empties the DNS
  cache, which would answer from before the rule. A domain `example.com` becomes
  the namespace `.example.com`, which covers the domain and every name below.
- The agent dialer finds the VM's ID through `hyperv.ps1` and dials hv_sock with
  go-winio; the service ID is `VsockServiceID(port)`, which needs no entry in
  the registry: only a service that listens on the host does. A connection to a VM
  whose agent does not listen yet hangs for seconds, so the appliance's start
  gives each try one second.
