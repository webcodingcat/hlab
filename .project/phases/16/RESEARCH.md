## Hyper-V nested virtualization and Windows 11 guests

### 1. Enabling nested virtualization on the AMD host
- Steps (VM must be OFF): `Set-VMProcessor -VMName <VM> -ExposeVirtualizationExtensions $true`, start the VM, install Hyper-V inside it like on a physical machine (`Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All`, restart). Undo with `$false` while off.
  Source: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/enable-nested-virtualization
- AMD minimum: "AMD EPYC / Ryzen processor or later"; host Windows Server 2022 or later, or Windows 11 or later; VM configuration version 9.3 or higher. Guest: "any Windows supported guest operating system" (newer Windows may use extra CPU features). Intel needs only config version 8.0. Same source. A Microsoft blog says AMD support arrived with build 19636 (https://techcommunity.microsoft.com/blog/virtualization/amd-nested-virtualization-support/1434841; not opened in full). The dev host runs build 26300, so it is fine. Use a Windows 11 guest (current ISO) and `Get-VM | select Version` to check the config version (Windows 11 creates 12.x).
- Inner Hyper-V and WSL2 in the guest: both supported. Microsoft states "Running WSL2 in a Hyper-V VM running nested on Hyper-V is supported". Hyper-V-isolated containers are also supported (one nesting level). Source: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/nested-virtualization
- Non-Microsoft hypervisors (VirtualBox, VMware, QEMU/KVM acceleration) in the guest are unsupported and likely to fail. Same source.
- Pitfalls (troubleshooting page, search snippet only, not opened in full: https://learn.microsoft.com/troubleshoot/windows-server/high-availability/hyper-v-nested-virtualization): VBS, Credential Guard and Device Guard active in host or guest can block nesting. Recommended guest: Gen2, at least 2 vCPU, 4 GB RAM. The VM host needs AMD-V (SVM) on in the BIOS.
- Unclear: whether the Ryzen AI MAX+ 395 (Strix Halo) has quirks with nested Hyper-V. Nothing found in official docs. A Linux KVM bug about random host reboots on Zen 4 is a KVM issue, not Hyper-V (https://bugzilla.kernel.org/show_bug.cgi?id=219009). Test on the real host.

### 2. What nesting rules out
- Dynamic Memory and runtime memory resize: while Hyper-V runs inside the VM, memory can only be changed with the VM off. Dynamic Memory enabled has no effect (memory does not fluctuate); without Dynamic Memory a resize of a running VM fails. Use static memory. Source: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/nested-virtualization
- Saved state and checkpoints: the Microsoft pages do not forbid them. The nested-virtualization overview even recommends saved states to revert test labs ("saved states can be used to revert to specific configurations"). Not documented as a limit: running-VM checkpoint (standard checkpoint with memory) versus off VM. Safest plan: use production checkpoints or take checkpoints with the VM off. UNCLEAR: whether a memory-state checkpoint or save of a running VM with inner Hyper-V active works reliably on AMD. I found no official statement and did not test it.
- MAC address spoofing: required only for the "MAC spoofing" option, where inner VMs sit on an external or bridged inner switch and packets pass through two switches. For the NAT option (internal inner switch plus `New-NetNat`, host-IP on `vEthernet (name)`) it is NOT needed; the docs call NAT the option for cases where spoofing is not possible. So an inner internal switch with WinNAT works without `Set-VMNetworkAdapter -MacAddressSpoofing On`. Source: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/enable-nested-virtualization
  Pitfall: the NAT subnet inside the guest must not overlap the outer network (the outer is the host's Default Switch or similar, often 172.x). Not from docs; general caution.

### 3. Windows 11 in a Gen2 VM
- Requirements for VMs (official): Generation 2; 64 GB or more disk; 4 GB or more RAM; 2 or more virtual processors; Secure Boot and virtual TPM enabled. The vTPM is emulated, independent of the host TPM. Source: https://learn.microsoft.com/en-us/windows/whats-new/windows-11-requirements (page dated 2026-07). Practical for nested use: 8 GB or more RAM, 4 or more vCPU, 100 GB or more dynamic VHDX (my recommendation, not from docs).
- Secure Boot templates: "Microsoft Windows" for Windows (default; "Microsoft UEFI Certificate Authority" is for Linux). `Set-VMFirmware -VMName <VM> -EnableSecureBoot On -SecureBootTemplate MicrosoftWindows`. Source: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/generation-2-virtual-machine-security-features
- vTPM with a local key protector, run on the host (elevated), VM off:
  `Set-VMKeyProtector -VMName <VM> -NewLocalKeyProtector` then `Enable-VMTPM -VMName <VM>`. The cmdlets are documented in the Hyper-V module reference; the security-features page confirms the key protector concept (moving the VM to another host may need a key protector update) but gives no command line. The tooling I did not run on this machine. Pitfall: the key protector is bound to the host. The VM export and import on another host fails to start without an updated protector.
- Install media attach: `Add-VMDvdDrive -VMName <VM> -Path <iso>`, `Set-VMFirmware -FirstBootDevice <dvd>`. Memory: set static (`Set-VMMemory -DynamicMemoryEnabled $false`) because of nesting.
- Local account without Microsoft account (OOBE bypass). Microsoft says it removed `oobe\bypassnro` and `start ms-cxh:localonly` in newer builds; reports on whether they still work conflict (search results: https://techcommunity.microsoft.com/discussions/windows11/why-oobebypassnro-not-working-for-windows-11-25h2/4465864, https://www.windowslatest.com/2025/10/07/microsoft-confirms-windows-11-to-require-microsoft-account-internet-during-oobe-tested). UNCONFIRMED which methods work on today's media. Options, in order of reliability:
  1. Unattended answer file (`autounattend.xml` on a second virtual DVD/VHD, or `unattend.xml`): set `Microsoft-Windows-Shell-Setup` `OOBE` with `HideOnlineAccountScreens` true plus a `LocalAccounts` entry. This is the documented route and does not depend on a trick. Not tested here.
  2. Windows 11 Pro only: in OOBE choose "Set up for work or school", then "Domain join instead" (reported by users, not verified).
  3. Disconnect the VM's network adapter before OOBE (`Disconnect-VMNetworkAdapter`) so the network screen offers "I don't have internet" (reported, not verified for 25H2 or later).
  4. Tricks reported by Thurrott for 25H2 (BypassNRO registry value plus restart; or Ctrl+Shift+J, `WinJS.Application.restart("ms-cxh://LOCALONLY")`); the article states the old `oobe\bypassnro` command no longer works: https://www.thurrott.com/?p=328183. Could be patched at any time.

### 4. ISO, activation and Hyper-V
- Official ISO: https://www.microsoft.com/en-us/software-download/windows11 "Download Windows 11 Disk Image (ISO) for x64 devices", multi-edition, pick the language, then SHA256 table for verification (`Get-FileHash`). Download link valid for 24 hours. The page lists the current release as "Windows 11 2026 Update, Version 26H2". The page says it is for installing with a licence; it does not state the evaluation or no-key behaviour.
- Unactivated Windows 11 Pro: the Microsoft activation page does not describe restrictions (https://support.microsoft.com/en-us/windows/activate-windows-c39005d4-95ee-b91e-b399-2820fda32227). From general knowledge, UNCONFIRMED against a primary source: an unactivated install runs, installs and receives updates, but has a "Activate Windows" watermark and personalization settings are locked. Installing a key is possible at "I don't have a product key" in setup. Choose Windows 11 Pro there.
- Hyper-V in the guest: needs Pro, Enterprise or Education (Client Hyper-V "is available in Windows Pro editions and greater", requires SLAT): https://learn.microsoft.com/en-us/windows/whats-new/windows-11-requirements. Nothing in the docs ties enabling the feature to activation. UNCONFIRMED by a primary source that Hyper-V can be enabled while unactivated; I expect yes, but verify in the first test VM.

### Not confirmed
- Checkpoint and saved-state behaviour of a running nested host on AMD.
- Which OOBE local-account bypass works on current media; use the answer file.
- Exact unactivated limits and Hyper-V while unactivated.
- Strix Halo specific nested issues.
## Hyper-V file transfer into a guest

Question: Copy-VMFile (Guest Service Interface) versus Enhanced Session clipboard and drive redirection, for copying a git bundle and a setup EXE into a Windows 11 guest VM.

Recommendation: use Copy-VMFile. Enhanced Session redirection needs an interactive VMConnect window and has no scripting interface, so it does not fit an unattended run.

### Copy-VMFile

- Version: the cmdlet is in the Hyper-V module, documented for Windows Server 2016 to 2025 and unchanged since 2016 (page updated 2025-05). No release cadence applies, because it ships with Windows.
  Source: https://learn.microsoft.com/en-us/powershell/module/hyper-v/copy-vmfile
- Signature: `Copy-VMFile -Name <vm> -SourcePath <host path> -DestinationPath <guest path> -FileSource Host [-CreateFullPath] [-Force] [-AsJob]`.
  - A `-VM <VirtualMachine>` parameter set exists as well.
  - `-FileSource` is mandatory, and `Host` is the only accepted value, so the copy only goes host to guest.
  - `-DestinationPath` is the full guest file path, not just a folder. The doc example is `-DestinationPath "C:\Temp\Test.txt"`.
  - `-CreateFullPath` creates missing destination folders in the guest.
  - `-Force` is not described in the Microsoft reference. Petri says to use it when the destination file may already exist, otherwise the copy errors (https://petri.com/copy-vmfile/).
- Example:
  `Copy-VMFile "vm" -SourcePath "C:\x\repo.bundle" -DestinationPath "C:\Temp\repo.bundle" -CreateFullPath -FileSource Host -Force`
- Enabling: the integration service is named "Guest Service Interface". It is off by default.
  - `Enable-VMIntegrationService -Name 'Guest Service Interface' -VMName <vm>`. The cmdlet takes `-Name` and `-VMName` (https://learn.microsoft.com/en-us/powershell/module/hyper-v/enable-vmintegrationservice).
  - Check it with `Get-VMIntegrationService -VMName <vm> -Name 'Guest Service Interface'`. Petri says the Primary Status should be OK, and "No Contact" means the copy cannot work yet.
  - It can be enabled while the VM runs. Petri says this also enables the guest service "Hyper-V Guest Service Interface".
  - The guest needs the integration components. A Windows 11 guest has them built in, and they update through Windows Update.
- Requirements (Petri, Windows Server 2012 R2 or later host): the VM must be running. Saved state does not work (Petri FAQ). The host-side call is made with Hyper-V Administrator rights.
  - The underlying WMI method is `Msvm_GuestFileService.CopyFilesToGuest` (https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/copyfilestoguest-msvm-guestfileservice). Its return codes include Access Denied (32769), Timeout (32772) and Invalid state (32775).
- No guest credentials or network are needed. The transfer goes over the VMBus, so it works before the guest has an IP address or when the network is down.
- No wildcards or folders. Source and destination take one file each, so a bundle plus an EXE means two calls.
- Size: UNCLEAR. Microsoft documents no limit. Petri's FAQ says there is no specific limit and that large files depend on system resources. A multi-GB test was not found. The bundle and EXE are probably tens to hundreds of MB, so test with the real files, and verify the size or hash in the guest after the copy.
- Target folder rights: UNCLEAR. Microsoft does not document the account the guest service uses. It is commonly believed to run as LocalSystem, but that is not confirmed in any source I could read.
  - Practical rule: copy to a path every account can read, for example `C:\Temp` or `C:\ProgramData\<dir>`, with `-CreateFullPath`. Avoid protected folders such as `C:\Windows` or `C:\Program Files`.
  - Files created this way may carry SYSTEM or Administrators ownership and ACLs. That is unconfirmed too. If a later step runs as a normal user, test that it can read the file.
- Pitfalls:
  - The generic error "Failed to initiate copying files to the guest" appears with 0x80004005 or 0x800710DF (device not ready). Causes seen include the service being off, the guest still booting, or the guest service not running (https://learn.microsoft.com/en-ie/answers/questions/440955/hyper-v-2019-server-unable-to-transfer-files-to-gu).
  - Right after a guest boot, wait for the heartbeat or the "Guest Service Interface" status to turn OK, with a deadline, before copying.
  - There is no checksum and no progress reporting. Compare hashes in the guest, for example through PowerShell Direct (`Invoke-Command -VMName`).

### Enhanced Session (clipboard and drive redirection)

- Source: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/use-local-resources-virtual-machine-connection (updated 2025-09).
- Requirements:
  - The host has the "Enhanced session mode policy" and "Enhanced session mode" settings on (`Set-VMHost -EnableEnhancedSessionMode $true`).
  - VMConnect runs on Windows 10 or later.
  - The guest has Remote Desktop Services enabled and runs Windows 10 or later.
- Use: choose local drives in VMConnect under Show options, then Local resources, then More. They appear in the guest under "This PC", in "Redirected drives and folders". Copy and paste of files works through the clipboard, as in RDP.
- This is interactive. The doc describes only the GUI, with no cmdlet or API to transfer files. It needs a user at a VMConnect window and a logged-in guest session.
- It is also unsuitable for the unattended OOBE or first-boot case, because the guest must already be set up with RDP enabled and a user signed in.
- A Windows 11 Home guest cannot accept RDP. This is from general knowledge and was not checked here. Pro or Enterprise is needed.
- Not confirmed: the `Set-VM -EnhancedSessionTransportType HvSocket` details. The fetched doc does not mention them.

### Summary

| Aspect | Copy-VMFile | Enhanced Session |
|---|---|---|
| Scriptable | yes | no (GUI only) |
| Needs guest login, network or RDP | no (Guest Service Interface only) | yes (RDP, signed-in user) |
| Folders and wildcards | no, one file per call | by hand |
| Size limit | none documented | none documented |

### Not confirmed

- Maximum file size for Copy-VMFile.
- The guest account and ACL of the copied file.
- Behavior of `-Force` when the target file is locked.
## Networking inside a nested test VM

Status: partly confirmed from primary sources, the rest unclear and needs a real test on the VM.

1. Address collision between the outer Default Switch and HLab inside the VM: confirmed as no collision for 198.19.0.0/16, a risk for 172.31.1.1.
   - Docs say the NAT internal prefix must not overlap the host's own external addresses. Microsoft's example is internal 172.16.0.0/12 against host 172.17.0.4, which is not allowed. If the host moves to a network that overlaps, the NAT prefix has to change. Source: https://techcommunity.microsoft.com/blog/virtualization/windows-nat-winnat----capabilities-and-limitations/382303
   - The Default Switch picks its subnet from the 172.16.0.0/12 range, and the subnet can change after a host reboot. This is widely reported, and I did not confirm it in a primary doc this session. See the forum thread https://techcommunity.microsoft.com/t5/windows-insider-program/hyper-v-default-switch-ip-address-range-change-ver-1809-build/m-p/852441.
   - 198.19.0.0/16 (RFC 2544 benchmark space) is outside 172.16.0.0/12, so it cannot collide with the Default Switch.
   - 172.31.1.1 is inside 172.16.0.0/12. The Default Switch may hand the VM an address in a subnet that contains 172.31.1.1, for example 172.31.0.0/20. Then the VM's own adapter address and the appliance gateway address 172.31.1.1/32 could clash or route wrongly. I found no source that rules this out. Mitigation: have the test setup read the VM's Default Switch subnet and fail early, or retry, if it covers 172.31.1.1. Alternatively, use an Internal or External switch with a fixed subnet outside 172.16.0.0/12 for the test VM.
2. A second WinNAT inside a Windows 11 VM behind the Default Switch: unclear.
   - Documented: WinNAT supports one NAT, one internal prefix, per host (here, per VM). A second `New-NetNat` leaves the system in an unknown state. Source: https://learn.microsoft.com/en-us/virtualization/hyper-v-on-windows/user-guide/setup-nat-network, section "Multiple NAT networks are not supported". Inside the VM, HLab's single `New-NetNat` for 198.19.0.0/16 is therefore allowed on its own.
   - Not documented: nested virtualization combined with a WinNAT in the guest. Neither Microsoft page covers it. Microsoft's nested example uses a guest-side NAT (192.168.100.0/24 on a dev box), which suggests it works in principle. See https://learn.microsoft.com/en-us/azure/dev-box/how-to-connect-dev-box-virtual-switch.
   - The Default Switch is not a WinNAT instance. I believe it uses ICS/HNS and is not listed by `Get-NetNat`. Forum reports agree, but I did not confirm this at a primary source. If correct, the outer Default Switch and the guest WinNAT do not share the one-NAT limit, because they live on different hosts.
   - The VM needs nested virtualization enabled, `Set-VMProcessor -ExposeVirtualizationExtensions $true`, for Hyper-V inside it. Sources: https://learn.microsoft.com/en-us/virtualization/hyper-v-on-windows/user-guide/nested-virtualization (not fetched this session) and the nested-virtualization search hits.
3. WSL2's NAT inside the development test VM: unclear.
   - WSL2 creates its own HNS NAT network ("WSL" switch) in the guest, next to HLab's WinNAT. One forum report mentions three NATs on one host (Default Switch, custom switch, WSL), which would contradict the one-NAT rule, so Get-NetNat is not a reliable count. Unverified.
   - HLab's architecture already says WinNAT for 198.19.0.0/16 coexists with WSL2 on the real host, as confirmed in E3 to E5 (`.project/ARCHITECTURE.md`, line 1440). The new risk is only the extra nesting layer. WSL2 itself needs nested virtualization in the VM, and I found no Microsoft statement on whether it works under the Default Switch.
   - WSL2 picks its own subnet from 172.16.0.0/12 as well, and it avoids ranges that are in use. That is from memory, not confirmed this session.

Needs a real test on the VM before the plan relies on it: (a) the VM's Default Switch subnet versus 172.31.1.1, (b) `New-NetNat` for 198.19.0.0/16 with WSL2 running, (c) WSL2 start under nested virtualization.
