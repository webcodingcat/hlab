# Phase 12: Installer and uninstaller

## Interfaces

```go
// internal/prereq: pure decision, no Windows calls (INST-02).
package prereq

// Facts are what sysint/windows reads without elevation and without DISM.
type Facts struct {
	Arch         string // runtime of the OS: "amd64", "arm64", "386"
	Build        int    // CurrentBuildNumber
	Server       bool   // InstallationType "Server" (Desktop Experience)
	EditionID    string // HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\EditionID
	VMMS         bool   // service vmms is registered (Hyper-V platform and services)
	PSModule     bool   // ...\WindowsPowerShell\v1.0\Modules\Hyper-V\Hyper-V.psd1 exists
	Hypervisor   bool   // Win32_ComputerSystem.HypervisorPresent
	FirmwareVirt bool   // Win32_Processor.VirtualizationFirmwareEnabled
}

// Missing returns one English line per missing prerequisite in a fixed
// order (architecture, Windows version, edition, hardware virtualization,
// Hyper-V, Hyper-V PowerShell module, restart pending); nil when nothing
// is missing. Client minimum build 22000, Server minimum 20348; Home is
// EditionID Core, CoreN, CoreSingleLanguage, CoreCountrySpecific.
// "Restart Windows to finish enabling Hyper-V" when VMMS is there but no
// hypervisor runs although the firmware allows it.
func Missing(f Facts) []string
```

```go
// internal/sysint/windows
func Prerequisites(ctx context.Context) (prereq.Facts, error) // registry, files, SCM query, one CIM query via powershell.exe
// RemoveService writes mode to <Paths.State>\uninstall-stop, the folder cloud.halt reads (when the service
// runs), stops the service, waits (uninstallStopWait) and deletes it; a
// missing service is no error. The file is deleted afterwards if it is left.
func RemoveService(ctx context.Context, mode string) error
// StopService stops a running service and waits; the configured
// stop_behaviour applies (used before an upgrade).
func StopService(ctx context.Context) error
```

```go
// internal/cloud: one-shot stop override written by the uninstaller.
const StopOverrideFile = "uninstall-stop" // in Paths.State; content "off" or "shutdown"
// haltBehaviour returns the behaviour halt uses and whether halt is skipped:
// file "off" -> skip (the uninstaller turns the VMs off when it deletes them),
// "shutdown" -> "shutdown", absent or unknown -> the stop_behaviour read as today.
// The file is deleted once read.
func haltBehaviour(stateDir, configured string) (behaviour string, skip bool)
```

```go
// internal/platform: Platform gains
// DeleteSwitch removes the switch with exactly this name; a missing switch is no error.
DeleteSwitch(ctx context.Context, name string) error

// internal/hostnet/windows: HostNet gains (not part of hostnet.HostNet)
func (h *HostNet) Restore(ctx context.Context) error                  // forwarding from hostnet-restore.json, then delete it
func (h *HostNet) RemoveRootTrust(pemData []byte) (had bool, err error) // by thumbprint; absent is no error
func (h *HostNet) RemoveWSLTrust(ctx context.Context, pemData []byte) error // once, no pending retry; ErrNoConsoleUser when nobody is logged on
var ErrNoConsoleUser = errors.New(...)                                 // errNoUser exported
```

```go
// internal/uninstall: the order of hlabd uninstall-cleanup (CONTEXT.md INST-04).
package uninstall

type Service interface{ RemoveService(ctx context.Context, mode string) error }
type Host interface {
	Restore(ctx context.Context) error
	RemoveRootTrust(pemData []byte) (had bool, err error)
	RemoveWSLTrust(ctx context.Context, pemData []byte) error
	Down(ctx context.Context) error
}
type Hypervisor interface {
	VMs(ctx context.Context, prefix string) ([]string, error)
	DeleteVM(ctx context.Context, name string) error
	DeleteSwitch(ctx context.Context, name string) error
}
type Options struct {
	Tag        string // profile tag: VM names, switch
	DataDir    string // %ProgramData%\<profile>: settings.toml, ca\ca.pem
	KeepData   bool
	Service    Service
	Host       Host
	Hypervisor Hypervisor
}
// Result.Notes: what was skipped without failing (WSL without a console user, a failed distro).
type Result struct{ Notes []string }

// Run: RemoveService ("shutdown" with KeepData, else "off") -> Restore ->
// read ca\ca.pem; RemoveRootTrust; RemoveWSLTrust when trust_ca is on or
// the root store had it -> Down -> DeleteVM for every ProfileVM -> DeleteSwitch(Tag)
// -> files (unless KeepData). Every step runs even after an earlier one
// failed; the joined error names each object that exists and could not be
// removed. Missing objects are no error.
func Run(ctx context.Context, o Options) (Result, error)
// ProfileVM: name is "<tag>-appliance" or "<tag>-server-<digits>".
func ProfileVM(tag, name string) bool
// RemoveFiles deletes dataDir; a data_folder outside it loses only
// disks, volumes, images, isos, cache, appliance, vms and itself when then empty.
func RemoveFiles(dataDir, dataFolder string) error
// RemoveUserData deletes <appData>\hlab-tray.exe and <localAppData>\hlab,
// retrying for up to 10 s while WebView2 processes still hold files.
func RemoveUserData(appData, localAppData string) error
```

hlabd command line (used only by the MSI and dev-setup.ps1):

```
hlabd install [--dev]
hlabd uninstall-cleanup [--keep-data]
hlabd check-prerequisites [--ui]   # exit 0 ok, 1 missing (list on stdout and in %TEMP%\hlab-setup-prerequisites.log, message box with --ui)
hlabd stop-service                 # before an upgrade replaces files
hlabd remove-user-data             # as the uninstalling user
```

MSI properties: `KEEPDATA` (0/1, from the bundle variable `KeepData`), `HLABUI` (1 = show the prerequisite message box, from the bundle).

Added after the tests were written:

- `tools/installer` (package main): `validateVersion(v string) error`.
- `internal/prereq` missing-item texts must contain, case-insensitive: architecture `x64` or `64-bit`; Windows version `22000`, `20348`, `Windows 11` or `Server 2022`; edition `home` or `edition`; hardware virtualization `virtualization`; Hyper-V `hyper-v`; PowerShell module `powershell`; the restart line is exactly "Restart Windows to finish enabling Hyper-V".
- `internal/hostnet/windows` exports `ErrNoConsoleUser`; without `ca.pem` the uninstall calls neither `RemoveRootTrust` nor `RemoveWSLTrust`.
- `tools/installer` (from 12-06): `go run ./tools/installer [-out dir] <version>` writes `<out>\hlab-<v>.msi` and `<out>\hlab-setup-<v>.exe`; it does not build the frontend (`task installer` does via `frontend:build`), so 12-10 runs `task frontend:build` first when it calls the tool directly. Both `wix build` runs get `-d Version=<v>`; Package.wxs has unnamed bind paths `<out>
elease` (hlabd.exe, hlab.exe, hlab-tray.exe, hlab-agent) and the repo root (`catalog\*.json`) with the util extension; Bundle.wxs has `<out>` (`hlab-<v>.msi`) and `%LocalAppData%\hlab-build-cache` (`MicrosoftEdgeWebview2Setup.exe`) with the bal and util extensions.
- `task e2e:elevated` runs `go test -tags e2einstall ./test/install/...` with `-timeout 420m` (above the sum of the row limits in install_test.go).
- The elevated suite expects: `bin\hlab-setup-<v>.exe` from `task installer`; quiet uninstall `/uninstall /quiet KeepData=1`; the prerequisite custom action Id contains "prereq"; the bundle's WebView2 `pv` search variable name contains "WebView" and the WebView2 package Id contains "webview"; bundle Publisher `webcodingcat` and DisplayName starting with "HLab"; shortcuts named `HLab.lnk`.

## Plans

- [x] 12-01 Prerequisite check
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: INST-02 | Tests: internal/prereq/prereq_test.go
  - Reads: REQUIREMENTS INST; SPEC "Machine prerequisites"; ARCHITECTURE "Installer", "system integration"; RESEARCH.md §3, §4; CONTEXT.md INST-02
  - Size: ~250 lines in 4 files
  - [x] `internal/prereq/prereq.go`: `Facts`, `Missing` as in Interfaces
  - [x] `internal/sysint/windows/prereq.go`: `Prerequisites` from registry (EditionID, CurrentBuildNumber, InstallationType), the module file, an SCM query of vmms, one `Get-CimInstance Win32_ComputerSystem, Win32_Processor` call through powershell.exe; no Win32_OptionalFeature, no DISM, nothing that needs elevation
  - [x] `cmd/hlabd/main.go`: `check-prerequisites [--ui]`: prints the list, writes it to `%TEMP%\hlab-setup-prerequisites.log`, with `--ui` shows it in one MessageBoxW (MB_ICONERROR|MB_TOPMOST|MB_SETFOREGROUND), exits 1 when anything is missing; a failure to read the facts is exit 1 with the reason

- [x] 12-02 Platform: delete the switch
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: INST-04 | Tests: (used by internal/uninstall/uninstall_test.go)
  - Reads: ARCHITECTURE "platform"; RESEARCH.md §6
  - Size: ~70 lines in 3 files
  - [x] `internal/platform/platform.go`, `internal/platform/hyperv/hyperv.go`: `DeleteSwitch` matching `$_.Name -eq $name` exactly; missing is no error; if `Remove-VMSwitch -Force` fails, remove the management-OS adapter of that name first and retry (PVLAN on the host adapter is unverified, RESEARCH §6)
  - [x] `internal/platform/platformtest/fake.go`: switches in the fake (created by EnsureSwitch, removed by DeleteSwitch, failure injection like the other calls)

- [x] 12-03 Host network: uninstall helpers
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: INST-04 | Tests: -
  - Reads: ARCHITECTURE "hostnet", "Network" (WSL2); DECISIONS "Q9", "WSL trust runner", "Host settings restored after a crash"; RESEARCH.md §7
  - Size: ~90 lines in 3 files
  - [x] `internal/hostnet/windows/hostnet.go`: exported `Restore` (the existing `restore`)
  - [x] `internal/hostnet/windows/trust.go`, `trust_wsl.go`: `RemoveRootTrust` (thumbprint lookup of `trustRoot`, reports whether it was there), `RemoveWSLTrust` (one `applyWSL(off)`, returns `ErrNoConsoleUser`, never sets `wslPending` or starts `watchLogon`), `ErrNoConsoleUser` exported from `errNoUser`

- [x] 12-04 Cloud: stop override for the uninstaller
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: INST-04 | Tests: internal/cloud/halt_test.go
  - Reads: ARCHITECTURE "Cloud start and stop"; CONTEXT.md INST-04 (stopping at uninstall)
  - Size: ~80 lines in 2 files
  - [x] `internal/cloud/lifecycle.go`: `StopOverrideFile`, `haltBehaviour`; `halt` uses it: "off" skips halting the servers, "shutdown" overrides the configured behaviour (60 s per guest as today); the file is deleted when read

- [x] 12-05 Uninstall orchestration
  - Wave: 2 | Depends on: 12-02 | Review: alone
  - Requirements: INST-04, INST-05 | Tests: internal/uninstall/uninstall_test.go
  - Reads: ARCHITECTURE "Installer", "Profiles", "Files and formats"; CONTEXT.md INST-04, INST-05; DECISIONS "Release and dev profile side by side"; `internal/discard/discard.go` as a pattern
  - Size: ~300 lines in 2 files
  - [x] `internal/uninstall/uninstall.go`: `Options`, `Run`, `ProfileVM`, `RemoveFiles`, `RemoveUserData` as in Interfaces; data_folder from `settings.Load(<DataDir>\settings.toml)` read before anything is deleted (default when unreadable); a Hyper-V that is not installed lists no VMs (no error); WSL `ErrNoConsoleUser` and distro failures become `Result.Notes`

- [x] 12-06 Installer build tool and task
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: INST-01, INST-06 | Tests: tools/installer/installer_test.go
  - Reads: SPEC "Stack" (WiX, WebView2), "Machine prerequisites"; ARCHITECTURE "Profiles", "Installer"; RESEARCH.md §1 (command line), §5 (download URL); CONTEXT.md "Build and package"
  - Size: ~250 lines in 3 files
  - [x] `tools/installer/main.go`, `version.go`: validates VERSION (x.y.z, major and minor ≤ 255, patch ≤ 65535); builds the four binaries with profile `release` and the version into `bin\release\`; fetches `MicrosoftEdgeWebview2Setup.exe` from `https://go.microsoft.com/fwlink/p/?LinkId=2124703` into `%LocalAppData%\hlab-build-cache\` (outside `%LocalAppData%\hlab`, which uninstall deletes) when missing or older than 7 days (a failed download with a cached copy uses the copy and says so); runs `wix build -acceptEula wix7 -arch x64` for `installer\Package.wxs` then `installer\Bundle.wxs` with `-d Version=`, `-b` paths and the util/bal extensions; checks that both extensions 7.0.1 are in the global cache and names the `wix extension add -g` command otherwise
  - [x] `Taskfile.yml`: `installer` (VERSION required, `go run ./tools/installer`)

- [ ] 12-07 MSI package
  - Wave: 2 | Depends on: 12-06 | Review: alone
  - Requirements: INST-01, INST-03, INST-04, INST-05, INST-06 | Tests: test/install/install_test.go (via 12-10)
  - Reads: ARCHITECTURE "Installer", "Files and formats" (shipped catalogs); RESEARCH.md §1, §2 (CloseApplication); CONTEXT.md all sections; the hlabd command line in Interfaces
  - Size: ~220 lines in 2 files
  - [ ] `installer/Package.wxs`: per-machine (`Scope="perMachine"`), fixed UpgradeCode (new GUID committed once), Manufacturer webcodingcat, ARPSYSTEMCOMPONENT, `%ProgramFiles%\hlab` with hlabd.exe, hlab.exe, hlab-tray.exe, hlab-agent, `catalog\*.json`; `Environment` PATH `[INSTALLFOLDER]` (Part last, System); Start menu and Desktop shortcuts "HLab" to hlab-tray.exe; `MajorUpgrade` (afterInstallValidate, DowngradeErrorMessage); `util:CloseApplication` hlab-tray.exe (TerminateProcess, RebootPrompt no)
  - [ ] Custom actions in `installer/Package.wxs`: `check-prerequisites [--ui when HLABUI=1]` immediate from a Binary of hlabd.exe, before InstallValidate, condition `NOT REMOVE`; `install` deferred, Impersonate no, after InstallFiles, `NOT REMOVE`; rollback of it `uninstall-cleanup --keep-data` on a first install; `stop-service` deferred, Impersonate no, before RemoveFiles, `REMOVE="ALL" AND UPGRADINGPRODUCTCODE`; `uninstall-cleanup [--keep-data when KEEPDATA=1]` through WixQuietExec, deferred, Impersonate no, before StopServices, `REMOVE="ALL" AND NOT UPGRADINGPRODUCTCODE`; `remove-user-data` deferred, Impersonate yes, after the cleanup and before RemoveFiles, same condition plus `KEEPDATA<>1`. No ServiceControl element

- [x] 12-08 hlabd uninstall commands
  - Wave: 3 | Depends on: 12-01, 12-03, 12-04, 12-05 | Review: alone
  - Requirements: INST-04, INST-05, INST-06 | Tests: test/install/install_test.go (via 12-10)
  - Reads: ARCHITECTURE "hlabd", "system integration"; CONTEXT.md INST-04 to INST-06; Interfaces
  - Size: ~200 lines in 4 files
  - Adjusts: -
  - [x] `internal/sysint/windows/install.go`: `RemoveService(ctx, mode)` (writes `<Paths.State>\uninstall-stop` (the folder cloud.halt reads), then the existing stop-wait-delete), `StopService`; `Installer.Uninstall(keepData)` keeps its contract through them; the stale comment about phase 12 goes
  - [x] `cmd/hlabd/main.go`: `uninstall-cleanup` builds `uninstall.Options` with `hostnet/windows`, `platform/hyperv` and `RemoveService`, prints the notes and the error list; `stop-service`; `remove-user-data` (from `%AppData%` and `%LocalAppData%`); usage text names all commands

- [ ] 12-09 Setup bundle
  - Wave: 3 | Depends on: 12-07 | Review: alone
  - Requirements: INST-01, INST-03, INST-05 | Tests: test/install/install_test.go (via 12-10)
  - Reads: SPEC "Stack" (WebView2); ARCHITECTURE "Installer"; RESEARCH.md §2, §3, §5; CONTEXT.md INST-01, INST-03, INST-05
  - Size: ~300 lines in 3 files
  - [ ] `installer/Bundle.wxs`: same version, own fixed UpgradeCode; chain WebView2 `ExePackage` (embedded, PerMachine, Permanent, `/silent /install`, `InstallCondition` from two `util:RegistrySearch` of `pv` (HKLM WOW6432Node, HKCU): missing or `0.0.0.0`), then the MSI with `MsiProperty KEEPDATA=[KeepData]` and `HLABUI`; numeric `KeepData` default 0 and `bal:Overridable`; `HLABUI` from Burn's display level if v7 exposes one as a built-in variable, else an overridable numeric `HlabUI` default 1 (record which in DECISIONS.md); `LaunchTarget="[ProgramFiles64Folder]hlab\hlab-tray.exe"`
  - [ ] `installer/theme.xml`, `installer/theme.wxl`: WixStdBA theme with all required pages and the checkbox `KeepData` "Keep projects, servers and settings" on the Modify page, unchecked by default

- [ ] 12-10 Elevated installer suite
  - Wave: 4 | Depends on: 12-06, 12-08, 12-09 | Review: alone
  - Requirements: INST-01, INST-04, INST-05, INST-06 | Tests: test/install/install_test.go
  - Reads: SPEC "Machine prerequisites", "Accounts and secrets"; CONTEXT.md "Tests and documentation"; RESEARCH.md "Could not confirm"; test/e2e/main_test.go (refusal, dev cloud restore, logging, deadlines) and test/e2e/cert_test.go, wsl_test.go (CA in WSL)
  - Size: ~400 lines in 4 files
  - [ ] `test/install/harness_test.go` (build tag `e2einstall`): refuses unelevated, while service `hlab` exists or an HLab bundle is registered (naming it), while the release cloud runs; stops a running dev cloud and starts it again at the end; builds 0.0.1 and 0.0.2 with `tools/installer` into the run's temp folder; runs bundles with `/quiet /log` and a deadline; seeds `C:\ProgramData\hlab\data` image and appliance caches from the dev data folder; helpers that bring the machine to a state (installed version, fixture with project, token, one server, trust on and one WSL distro, tray running) so `-run` of one row still works; each row logged at start and end with its limit
  - [ ] `test/install/host_test.go`: probes for VMs, switches, NRPT rules by comment, NAT, firewall group, routes via the adapter, forwarding vs. the recorded values, service, Root store thumbprint, the WSL CA file, folders and the PATH entry
  - [ ] `Taskfile.yml`: `e2e:elevated` (`go test -tags e2einstall -count=1 -timeout 90m ./test/install/... {{.CLI_ARGS}}`)
  - The suite settles what RESEARCH.md could not confirm: the prerequisite action runs under Burn (INST-01 log), MsiProperty on uninstall (INST-05 keep), CloseApplication on uninstall and the WebView2 folder (INST-05), Remove-VMSwitch with PVLAN and WSL removal from the deferred action (INST-04), Burn upgrade order (INST-06). A failure there goes back to the plan that owns the part

- [ ] 12-11 Documentation
  - Wave: 5 | Depends on: 12-10 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC; CONTEXT.md "Tests and documentation"
  - Size: ~200 lines in 4 files
  - [ ] `README.md`: installation with the setup EXE (SmartScreen for unsigned packages, WebView2, PATH), uninstallation (keep-data checkbox, quiet `KeepData=1`, other accounts' tray folders stay, WSL CA removal needs a logged-on user), remove the "no installer yet" text and the `ca trust off` workaround
  - [ ] `docs/development/build-and-test.md`: `task installer VERSION=x.y.z`, `task e2e:elevated` and what it refuses; `docs/development/setup.md`: `wix extension add -g` for both extensions 7.0.1 and `wix eula accept wix7`; `docs/development/components.md`: installer and `internal/uninstall`, `internal/prereq`
