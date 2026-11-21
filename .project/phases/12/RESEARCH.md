# Phase 12 research: installer and uninstaller

Date of research: 2026-11-21. "Source-verified" means read in the WiX repository at tag v7.0.1, in the
Wails module cache, or in the vendor documentation. "Unverified" means no primary source was found or the
behaviour was not tested; those items need a decision or a check in the elevated suite.

## 1. WiX v7.0.1 MSI authoring

Version and maintenance

- Current stable is v7.0.1 (tag v7.0.1, released 2026-10-26; v7.0.0 was 2026-04-06). It is the pinned
  version and the latest. v7.0.1 is a security re-release (avoids a vulnerable .NET assembly) plus
  `-nohardlinks` and no WIX8004 for identical layout files. Maintained.
  <https://github.com/wixtoolset/wix/releases>, <https://docs.firegiant.com/wix/whatsnew/releasenotes/>
- NuGet has `WixToolset.Util.wixext` 7.0.1 and `WixToolset.BootstrapperApplications.wixext` 7.0.1
  (both latest). <https://api.nuget.org/v3-flatcontainer/wixtoolset.util.wixext/index.json>
- v7 changes that touch this phase: explicit EULA acceptance is required; relative paths in `Files` are
  relative to the source path; `heat` is removed; `ProductSearch/@Result='exists'` was added to Burn
  searches; dual-purpose MSIs in bundles were added (author `Scope="perMachine"` on `Package` to keep the
  MSI per-machine only). No schema renames that affect this phase are listed.

Command line

- Install: `dotnet tool install --global wix --version 7.0.1`.
- Accept the EULA (OSMF v1.1, id `wix7`, fee only above US$10,000 annual revenue). Any of:
  `wix build -acceptEula wix7 ...`, `<AcceptEula>wix7</AcceptEula>` in a .wixproj, once per user and
  machine `wix eula accept wix7`, or `msbuild -t:AcceptEula -p:EulaId=wix7`. Without it the build fails
  (WIX7015). <https://docs.firegiant.com/wix/osmf/>
- Extensions: `wix extension add -g WixToolset.Util.wixext/7.0.1` and
  `wix extension add -g WixToolset.BootstrapperApplications.wixext/7.0.1` (`-g` = current user's
  extension cache; always give the version, the docs warn that omitting it can pick an incompatible
  one). `wix extension list -g` shows them. <https://docs.firegiant.com/wix/tools/wixexe/>
- Build: `wix build -arch x64 -ext WixToolset.Util.wixext -d Version=1.2.3 -b <bindpath> -o bin\hlab-1.2.3.msi installer\Package.wxs`
  and `wix build -arch x64 -ext WixToolset.BootstrapperApplications.wixext -ext WixToolset.Util.wixext -o bin\hlab-setup-1.2.3.exe installer\Bundle.wxs`.
  Options: `-arch x86|x64|arm64` (default x86), `-ext`, `-d name=value`, `-b/-bindpath`, `-o`, `-culture`, `-loc`.
- Schema namespace is still `http://wixtoolset.org/schemas/v4/wxs` (and `.../wxs/util`, `.../wxs/bal`).

Deferred elevated exe custom action with exit-code check

- Native route (no extension): `CustomAction` with `FileRef` (installed file) or `BinaryRef`, `ExeCommand`,
  `Execute="deferred"`, `Impersonate="no"`, `Return="check"` (default). `Impersonate="no"` runs as LocalSystem
  when the install is elevated; a non-zero exit code fails the action (MSI error 1722) and rolls back.
  Stdout and stderr are not logged. <https://docs.firegiant.com/wix/schema/wxs/customaction/>
- WixQuietExec (util extension): runs a command line without a console window, logs the command line and its
  output into the MSI log; "if the exit code of the command line is nonzero, WixQuietExec returns
  ERROR_INSTALL_FAILURE". The deferred form takes the command from the property with the same Id as the
  custom action. <https://docs.firegiant.com/wix/tools/wixext/quietexec/>
  Source-verified in v7.0.1 (`src/ext/caDecor.wxi`, `src/ext/Util/wixlib/UtilExtension_Platform.wxi`): binary
  `Wix4UtilCA_<X86|X64|A32|A64>` (the prefix is still `Wix4`), entry points `WixQuietExec` and `WixSilentExec`
  (no logging of command and output). `WixQuietExec64`/`WixSilentExec64` still exist but are documented as
  removed in the next major version; with `-arch x64` use the plain entry point.

```xml
<CustomAction Id="HlabCleanup" BinaryRef="Wix4UtilCA_$(sys.BUILDARCHSHORT)" DllEntry="WixQuietExec"
              Execute="deferred" Impersonate="no" Return="check" />
<SetProperty Id="HlabCleanup" Sequence="execute" Before="HlabCleanup"
             Value="&quot;[INSTALLFOLDER]hlabd.exe&quot; uninstall-cleanup [CLEANUPARGS]"
             Condition="REMOVE=&quot;ALL&quot; AND NOT UPGRADINGPRODUCTCODE" />
<InstallExecuteSequence>
  <Custom Action="HlabCleanup" Before="StopServices" Condition="REMOVE=&quot;ALL&quot; AND NOT UPGRADINGPRODUCTCODE" />
</InstallExecuteSequence>
```

  The `util` namespace must be declared on the root. `$(sys.BUILDARCHSHORT)` yields the suffix; check the
  exact case in a first build. The action must run while `hlabd.exe` still exists, so schedule it before
  `StopServices` (at the latest before `RemoveFiles`). The `SetProperty` with `Sequence="execute"` formats
  `[INSTALLFOLDER]` and `[KEEPDATA]` at that point and hands the result to the deferred action. The snippet
  is not built or tested here.
- Conditions (standard Windows Installer behaviour; not re-fetched): first install `NOT Installed`;
  uninstall `REMOVE="ALL"`; the old product's removal during a major upgrade has `UPGRADINGPRODUCTCODE` set,
  so "real uninstall only" is `REMOVE="ALL" AND NOT UPGRADINGPRODUCTCODE`; maintenance
  `Installed AND NOT REMOVE`; the new product sees `WIX_UPGRADE_DETECTED` when MajorUpgrade finds an older one.
- The condition that skips cleanup lives in the OLD msi. It works only for products built with it, which is
  the case from the first release on.

MajorUpgrade

- `<MajorUpgrade DowngradeErrorMessage="..." />`. `Schedule` default is `afterInstallValidate` (old product is
  removed completely, then the new one installed; if the new install fails neither remains). Others:
  `afterInstallInitialize` (old product restored on failure), `afterInstallExecute`, `afterInstallExecuteAgain`,
  `afterInstallFinalize`. `AllowDowngrades` default no (the message is shown on downgrade).
  `IgnoreRemoveFailure` default no. <https://docs.firegiant.com/wix/schema/wxs/majorupgrade/>
- The cleanup action is skipped for the old product purely by its `NOT UPGRADINGPRODUCTCODE` condition; the
  schedule only decides ordering. `afterInstallValidate` keeps "old product fully uninstalled (service stopped,
  files removed) before the new files are laid down", which fits "stop through the service, replace files".
- MSI properties given to the new install (for example KEEPDATA) are not passed to the old product's removal.
- Burn level (older WiX material, unverified for v7): a newer bundle with the same UpgradeCode installs first
  and uninstalls the old bundle at the end of the chain (`-uninstall -quiet -burn.related.upgrade`), which
  mainly removes the old registration and cache. Test upgrade 0.0.1 to 0.0.2 through the bundle and read both
  MSI logs.

ServiceControl on a service the MSI did not install

- `<ServiceControl Id="StopHlabd" Name="hlabd" Stop="both" Wait="yes" />` inside a component. Attributes:
  `Name` (required), `Start`/`Stop`/`Remove` (`install`, `uninstall`, `both`), `Wait` (default yes). Stopping
  needs no `ServiceInstall`. <https://docs.firegiant.com/wix/schema/wxs/servicecontrol/>
- The docs do not say how a missing service is handled (cleanup deletes the service earlier on uninstall).
  Unverified; test an uninstall after the service is already gone. `Wait="yes"` waits for the stop, which can
  take as long as hlabd needs to stop the cloud.

Environment PATH

- `<Environment Id="HlabPath" Name="PATH" Value="[INSTALLFOLDER]" Action="set" Part="last" System="yes" Permanent="no" />`
  in a component. `Part="last"` appends, `Separator` default `;`, `System="yes"` = machine PATH. Removal at
  uninstall happens unless `Permanent`; removal matches name and value, so keep the value identical between
  versions. <https://docs.firegiant.com/wix/schema/wxs/environment/>

Shortcuts

- Standard `Shortcut` elements in the Start menu and Desktop folders with `Target="[INSTALLFOLDER]hlab-tray.exe"`;
  each shortcut component needs a `RegistryValue` key path (ICE rules). Not re-fetched for v7.0.1; no breaking
  change is listed in the release notes.

## 2. WixStdBA in v7

All answers from `src/ext/Bal/stdbas/WixStandardBootstrapperApplication.cpp` and
`src/libs/dutil/WixToolset.DUtil/thmutil.cpp` at tag v7.0.1 unless stated.

Uninstall started from "Apps"

- With display full and action Uninstall the BA does not skip to plan. It shows the Modify page
  (`DeterminePageId`: DETECTED with Modify, Repair or Uninstall maps to the Modify page); the user clicks the
  Uninstall button, then Progress, then Success. A custom theme must contain Loading, Help, Install, Modify,
  Progress, ProgressPassive, Success and Failure pages. Quiet or passive uninstall skips straight to plan.
- `bal:Condition` entries are evaluated at detect for every action from Uninstall to Repair, so a prerequisite
  condition also blocks uninstall unless it is written to let it pass (see section 3).

Checkbox bound to a Burn variable

- thmutil binds a `Checkbox` whose `Name` equals a Burn variable name: a click writes the numeric variable
  (1 or 0), and when the page is shown the box is initialised from that variable (`OnButtonClicked`,
  `ShowControl`, flag `fAutomaticValue`). WixStdBA switches the automatic behaviour off only for its own named
  controls (InstallButton, EulaAcceptCheckbox, LaunchButton, ...). A custom control with another name keeps
  it, on any page including Modify. The old advice that only Options-page checkboxes work is for v3.
- Authoring:

```xml
<Variable Name="KeepData" Type="numeric" Value="0" bal:Overridable="yes" />
<!-- theme.xml, Modify page: <Checkbox Name="KeepData" X="11" Y="..." Width="..." Height="23" TabStop="yes" FontId="3">#(loc.KeepDataLabel)</Checkbox> -->
<MsiPackage SourceFile="hlab.msi">
  <MsiProperty Name="KEEPDATA" Value="[KeepData]" />
</MsiPackage>
```

  Use a numeric variable with default 0 so the MSI property is always `0` or `1`, never empty. That Burn
  passes `MsiProperty` values on uninstall too is from Burn's design (the properties are put on every MSI
  command line) but is not stated in the v7 docs; check the MSI log.
- Quiet uninstall: `hlab-setup-1.2.3.exe /uninstall /quiet KeepData=1`. A command-line variable is accepted
  only when declared `bal:Overridable="yes"`; otherwise WixStdBA logs "Ignoring attempt to set non-overridable
  variable". <https://docs.firegiant.com/wix/schema/wxs/variable/>
- BA element: `<bal:WixStandardBootstrapperApplication Theme="none" ThemeFile="theme.xml" LocalizationFile="theme.wxl" />`
  (attributes `Theme` required, `ThemeFile`, `LocalizationFile`, `LogoFile`, `SuppressOptionsUI`, `SuppressRepair`,
  `ShowVersion`, `LaunchTarget`, `LaunchArguments`, `LaunchHidden`, `LaunchWorkingFolder`, `LaunchTargetElevatedId`,
  `LicenseFile`/`LicenseUrl`, `SupportCacheOnly`, `SuppressDowngradeFailure`).
  <https://docs.firegiant.com/wix/schema/bal/wixstandardbootstrapperapplication>

LaunchTarget

- `LaunchTarget="[ProgramFiles64Folder]hlab\hlab-tray.exe"` (Burn variables are expanded; the MSI folder is not
  known to the bundle, so use the fixed path). The Success page Launch button starts it with ShellExecute in the
  unelevated BA process (the elevated launch needs `LaunchTargetElevatedId`). It is enabled only if the target
  exists and the planned action is not Uninstall.

Closing a running hlab-tray

- `util:CloseApplication` (WixCloseApplications, immediate action before InstallFiles; InstallFiles is always in
  the sequence, so it should run for install, upgrade and uninstall, but the docs do not say so explicitly):
  `<util:CloseApplication Id="CloseTray" Target="hlab-tray.exe" CloseMessage="yes" TerminateProcess="1" RebootPrompt="no" Timeout="5" />`
  Attributes: `Target` (file name only), `CloseMessage`, `EndSessionMessage`, `ElevatedCloseMessage`,
  `TerminateProcess` (terminate after the messages, value = exit code, needs `RebootPrompt="no"`), `Timeout`
  (default 5 s), `PromptToContinue`, `Condition`, `Property`.
  <https://docs.firegiant.com/wix/schema/util/closeapplication/>
- A tray app often hides on window close, so `TerminateProcess` is the reliable part. Restart Manager
  (`RegisterRestartResources`, FilesInUse) needs UI or `MSIRESTARTMANAGERCONTROL` handling and is less
  predictable under Burn; CloseApplication is simpler. The cleanup action can also end the tray itself.
- msedgewebview2.exe children can keep the WebView2 data folder locked for a moment after the tray exits (see 5).

## 3. Showing a prerequisite failure under Burn

- Unverified (no primary source): an immediate custom action in a per-machine MSI run by Burn. What is known:
  Burn elevates its engine and runs the MSI from it in the same session; Windows Installer runs immediate actions
  in the installing client's user context, not as SYSTEM (only deferred actions with `Impersonate="no"` get
  LocalSystem). A message box from it should reach the desktop in an interactive run but would block an
  unattended run; `MsiProcessMessage` text is suppressed because Burn runs the MSI without UI.
  <https://learn.microsoft.com/en-us/windows/win32/msi/custom-action-security>
- Failed package in WixStdBA (source-verified): the Failure page shows `0xHHHHHHHH - <system message>` for the
  failing HRESULT plus a log link; there is no per-package custom text. Quiet runs show nothing and return the
  HRESULT as exit code.
- `bal:Condition` (source-verified): evaluated at detect, before the UAC prompt and before any package runs. On
  failure the Failure page shows exactly the condition's `Message` (no HRESULT prefix); quiet runs log it and
  return `E_WIXSTDBA_CONDITION_FAILED`. Nothing is installed. It is the better carrier: it works in full UI and
  honours quiet mode. Its limit is what Burn searches can express.
  `<bal:Condition Message="...">...</bal:Condition>` <https://docs.firegiant.com/wix/schema/bal/condition/>
- Uninstall pitfall: the condition is also evaluated for uninstall. Write it as
  `WixBundleInstalled OR (<prerequisite expression>)`, or test `WixBundleAction = 3`. Built-ins: `WixBundleInstalled`
  (non-zero if the bundle is installed, set at engine start), `WixBundleAction` (BOOTSTRAPPER_ACTION; Uninstall = 3
  from memory of the header, verify), `VersionNT64` (version type, `VersionNT64 >= v10.0.19041`).
  <https://docs.firegiant.com/wix/tools/burn/builtin-variables/>
- Using the real `hlabd check-prerequisites`: an `ExePackage` first in the chain (`PerMachine="yes"`,
  `Permanent="yes"`, install only, `ExitCode` non-zero mapped to `error`) runs after elevation; WixStdBA shows
  only the generic failure page and the log, so the failure list would not reach the user. Use `bal:Condition`
  for what Burn can express (OS build, edition by registry, hypervisor and vmms by search) and keep the exe as
  a second line. The choice goes to DECISIONS.md.
- Home edition by search: `util:RegistrySearch Root="HKLM" Key="SOFTWARE\Microsoft\Windows NT\CurrentVersion"
  Value="EditionID" Variable="EditionId"`; Home values are `Core`, `CoreN`, `CoreSingleLanguage`,
  `CoreCountrySpecific` (from memory; unverified).

## 4. Detecting Hyper-V parts without elevation or DISM

- Requirements (docs): Windows 10/11 Pro or Enterprise (Hyper-V "can't be installed on Home"), 64-bit CPU with
  SLAT, VM Monitor Mode extensions, firmware virtualization on, 4 GB RAM.
  <https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/host-hardware-requirements>
- Feature names. Client (DISM, Enable-WindowsOptionalFeature): `Microsoft-Hyper-V` and
  `Microsoft-Hyper-V-Management-PowerShell` are documented; `Microsoft-Hyper-V-All`, `-Hypervisor`, `-Services`,
  `-Tools-All` are not confirmed in a primary source (run `Get-WindowsOptionalFeature -Online -FeatureName *Hyper-V*`
  once on the target systems). Server (Server Manager): `Hyper-V`, `Hyper-V-PowerShell`, `Hyper-V-Tools`; Server
  Core with `-IncludeManagementTools` installs only the PowerShell module.
  <https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/get-started/install-hyper-v>
- `Get-WindowsOptionalFeature` and DISM need elevation. `Win32_OptionalFeature` (root\cimv2; `InstallState`
  1 enabled, 2 disabled, 3 absent, 4 unknown) is the usual unelevated route; whether it works unelevated on
  Windows 11 and lists the Server features is unverified.
  <https://learn.microsoft.com/en-us/windows/win32/cimwin32prov/win32-optionalfeature>
- File and service checks that need no elevation (not a documented contract; unverified on Server): service
  `vmms` (SCM query or `HKLM\SYSTEM\CurrentControlSet\Services\vmms`), `%SystemRoot%\System32\vmms.exe`, the
  module `%SystemRoot%\System32\WindowsPowerShell\v1.0\Modules\Hyper-V\Hyper-V.psd1`, and `...\System32\hvix64.exe`
  (all present on this Windows 11 Pro machine, build 26300). A Burn `bal:Condition` can use `util:RegistrySearch`
  and `util:FileSearch` for them.
- Running hypervisor: `Win32_ComputerSystem.HypervisorPresent` (Windows 8/Server 2012 and later; true for any
  hypervisor, so also with only WSL2 or VBS). `Win32_Processor.VirtualizationFirmwareEnabled`,
  `VMMonitorModeExtensions`, `SecondLevelAddressTranslationExtensions` exist since Windows 8. With a hypervisor
  running, `systeminfo` prints "A hypervisor has been detected. Features required for Hyper-V will not be
  displayed" and the firmware flag is commonly reported false (not in the docs). Rule: firmware flag true OR
  HypervisorPresent true.
  <https://learn.microsoft.com/en-us/windows/win32/cimwin32prov/win32-computersystem>,
  <https://learn.microsoft.com/en-us/windows/win32/cimwin32prov/win32-processor>
- Edition: Home is excluded by the docs; detect with `EditionID` (section 3) or `GetProductInfo`.
- Reboot pending after enabling features: the docs only say a restart completes the installation (the hypervisor
  does not run before it). Usual markers: `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`,
  `...\WindowsUpdate\Auto Update\RebootRequired`, `PendingFileRenameOperations`; not confirmed in a primary source.
  Robust rule: Hyper-V Services present AND `HypervisorPresent` true; features enabled but no hypervisor means
  "restart first".

## 5. WebView2 Evergreen bootstrapper and the Wails data folder

- Download (Microsoft's WiX sample): `https://go.microsoft.com/fwlink/p/?LinkId=2124703`
  (`MicrosoftEdgeWebview2Setup.exe`, about 2 MB, always current; installs the runtime matching the device).
  <https://raw.githubusercontent.com/MicrosoftEdge/WebView2Samples/main/SampleApps/WV2DeploymentWiXBurnBundleSample/Bundle.wxs>
- Silent install: `MicrosoftEdgeWebview2Setup.exe /silent /install`. Elevated means per-machine, not elevated
  means per-user (replaced by per-machine if a per-machine Edge updater exists). Standalone:
  `MicrosoftEdgeWebView2RuntimeInstaller{X64|X86|ARM64}.exe /silent /install`.
  <https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution>
- Detection: value `pv` (REG_SZ) under `Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`.
  64-bit Windows: `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{...}` (per-machine) and
  `HKCU\Software\Microsoft\EdgeUpdate\Clients\{...}` (per-user). Installed means one of them exists with a version
  above `0.0.0.0` (missing, empty, null or `0.0.0.0` = not installed). Burn: two `util:RegistrySearch` elements
  (`Root="HKLM"` with `Key="SOFTWARE\Microsoft\EdgeUpdate\Clients\{...}"` and `Bitness="always32"` which maps to
  WOW6432Node, or the WOW6432Node key with `Bitness="always64"`; `Root="HKCU"`), `Value="pv"`, into variables
  `WV2Machine` and `WV2User`; `InstallCondition` on the `ExePackage` (`PerMachine="yes"`, `Permanent="yes"`,
  `InstallArguments="/silent /install"`): not installed when neither variable is set or both are `0.0.0.0`.
  Microsoft's own sample tests the value name `EBWebView` instead of `pv`; follow the documented `pv`.
- Redistribution: Microsoft documents packaging the bootstrapper with the app as an option. The license
  (<https://developer.microsoft.com/microsoft-edge/api/eula/webview2>) allows copying and distributing the object
  code; requirements: add significant primary functionality, require end users to agree to terms that protect
  Microsoft, acquire the code directly from Microsoft (including from a Microsoft URL), indemnify Microsoft;
  restrictions: no Microsoft trademarks suggesting endorsement, no Microsoft download sites or shortcuts for the
  software offered to third parties. Embedding the unmodified file fetched from the fwlink fits the text; this is
  not legal advice.
- Wails v3.1.0 (the pinned version) WebView2 user data folder: with `Options.Windows.WebviewUserDataPath` empty
  (the default; `cmd/hlab-tray/options_test.go` asserts it stays empty) the folder is `%AppData%\<name of the
  running exe>`, that is the roaming profile: `%AppData%\hlab-tray.exe\` (source:
  `internal/webview2/pkg/edge/chromium.go`, `filepath.Join(os.Getenv("AppData"), currentExeName)`). localStorage
  lives in a subfolder of it (WebView2 documents only "a subfolder of userDataFolder"; the name `EBWebView` is
  from memory). Uninstall can delete the whole folder `%AppData%\hlab-tray.exe`. The e2e build uses another path
  (`HLAB_E2E_WEBVIEW_DIR`).
  Cautions: the tray and its msedgewebview2.exe children must have exited (the docs say to wait for browser
  processes); Microsoft says the folder is never removed automatically for Win32 hosts; a deferred
  `Impersonate="yes"` action runs as the elevated installing user, which under UAC with other admin credentials
  is not the person who ran the tray, so both `%AppData%` and `%LocalAppData%` may belong to another profile.
  <https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/user-data-folder>

## 6. Hyper-V removal

- `Remove-VM` "deletes the virtual machine's configuration file, but does not delete any virtual hard drives";
  snapshots are deleted and merged into the VHDs afterwards. `-Force` only suppresses the confirmation prompt.
  <https://learn.microsoft.com/en-us/powershell/module/hyper-v/remove-vm>
- Running VM: not directly. The WMI method behind it (`DestroySystem`) requires "the virtual machine must be in the
  powered off or saved state". A Japanese test article reports Running and Paused VMs error out while Saved and
  Off ones are removed. So Saved works; Running needs `Stop-VM -TurnOff -Force` first.
  <https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/destroysystem-msvm-virtualsystemmanagementservice>
- Not confirmed: whether the saved-state files (`.vmrs`, `.vmgs`) and the VM's own folder are deleted by
  `Remove-VM`; the doc says only "configuration file". The cleanup deletes the data folder afterwards, but with
  KEEPDATA leftovers next to the kept disks should be checked in the elevated suite.
- `Remove-VMSwitch -Name x -Force`: the docs only say it deletes a virtual switch; nothing about PVLAN. PVLAN is a
  per-adapter port setting (`Set-VMNetworkAdapterVlan -Isolated|-Community|-Promiscuous`, `-ManagementOS` for the
  host adapter) that should go away with the adapter or switch, but this is unverified. Test it: create a switch
  with a host adapter carrying PVLAN settings and remove it; if that fails remove the host adapter first
  (`Remove-VMNetworkAdapter -ManagementOS -Name ...`), then the switch.
  <https://learn.microsoft.com/en-us/powershell/module/hyper-v/remove-vmswitch>

## 7. WTSQueryUserToken plus CreateProcessAsUser from an MSI deferred action

- Documented requirement: the caller must run in the context of LocalSystem and hold `SE_TCB_NAME`.
  <https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/nf-wtsapi32-wtsqueryusertoken>
- A deferred action with `Impersonate="no"` in an elevated per-machine install runs as LocalSystem (the custom
  action security page words it loosely, "may run with elevated privileges"), so the call sequence in
  `internal/hostnet/windows/runas.go` (`WTSGetActiveConsoleSessionId`, `WTSQueryUserToken`, `token.Environ`,
  `CreateProcessAsUser` on `winsta0\default`) should work from the action's hlabd process. No source says it
  fails and none confirms it for the Windows Installer custom-action host: unverified.
- Expect: no console user (Remote Desktop only, nobody logged on) gives `errNoUser`, as in the service; the result
  does not depend on whether the uninstall starts from Apps or the command line as long as the action is
  deferred and not impersonated. Check in the elevated suite by looking for the CA file in the distro after an
  uninstall driven through the bundle.

## Maintenance summary

| Item | Version | Last release | Maintained |
|---|---|---|---|
| WiX Toolset | 7.0.1 (pinned = latest) | 2026-10-26 | yes |
| WixToolset.Util.wixext, BootstrapperApplications.wixext | 7.0.1 | with WiX | yes |
| WebView2 Evergreen bootstrapper | no own version, always current | rolling | yes |
| Wails v3 | v3.1.0 (pinned, in module cache) | not re-checked | n/a |

## Could not confirm

- Elevation and message-box behaviour of an immediate custom action inside a Burn-run MSI (reasoned only).
- `MsiProperty` on uninstall and the `Bitness` search keys in the v7.0.1 docs (behaviour follows v4 to v6; test in e2e).
- Burn related-bundle upgrade order in v7; ServiceControl on a missing service; CloseApplication on uninstall.
- `Win32_OptionalFeature` unelevated and its Server feature list; client sub-feature names other than
  `Microsoft-Hyper-V` and `-Management-PowerShell`; reboot-pending registry markers.
- Remove-VM leftovers (saved-state files), Remove-VMSwitch with PVLAN, the CreateProcessAsUser path from an MSI CA.
- Value of `BOOTSTRAPPER_ACTION_UNINSTALL` (3) and the exact case of `$(sys.BUILDARCHSHORT)` in the CA binary name.

## Correction from plan 12-01

- On Windows 11 the Hyper-V PowerShell module is at `Modules\Hyper-V\<version>\Hyper-V.psd1` (here `2.0.0.0`), not directly in `Modules\Hyper-V`; any file search for it (also a Burn `util:FileSearch`) must allow the version folder.
