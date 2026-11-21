# Phase 12 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| INST-02 | `Missing` names exactly what is missing, in fixed order, and nothing when all is there (table: all present; arm64; build 19045; Server build 17763; each Home EditionID; no firmware virtualization and no hypervisor; hypervisor present with firmware flag false is fine; no vmms; no PowerShell module; vmms present, firmware on, no hypervisor gives "restart Windows"; several missing at once lists each) | unit | internal/prereq/prereq_test.go: TestPrereq/INST-02 | 12-01 |
| INST-04 | `ProfileVM` and `Run` delete only `<tag>-appliance` and `<tag>-server-<digits>` and the switch named exactly `<tag>`: with tag `hlab`, `hlab-dev-server-1`, `hlab-dev-appliance`, `hlab-server-x`, `hlab-server-1-old`, a foreign VM and the switch `hlab-dev` stay (table) | unit | internal/uninstall/uninstall_test.go: TestUninstall/INST-04/profile | 12-05 |
| INST-04 | `Run` keeps the order service, forwarding restore, CA trust, host network, VMs, switch, files; reads `ca.pem` before deleting files; removes WSL trust only when `trust_ca` is on or the root store had the CA; asks the service to stop with "off" by default and "shutdown" with KeepData | unit | internal/uninstall/uninstall_test.go: TestUninstall/INST-04/order | 12-05 |
| INST-04 | already missing objects (no service, no VMs, no switch, no `ca.pem`) are no error; a VM that exists and cannot be deleted fails the run naming it after every other step ran; no console user for WSL ends in a note, not an error | unit | internal/uninstall/uninstall_test.go: TestUninstall/INST-04/errors | 12-05 |
| INST-04 | `haltBehaviour`: file "off" skips halting, "shutdown" overrides a configured "save", no or unknown file keeps the configured behaviour; the file is gone after reading | unit | internal/cloud/halt_test.go: TestHalt/INST-04 | 12-04 |
| INST-05 | `RemoveFiles` deletes the data folder; a `data_folder` outside it loses only disks, volumes, images, isos, cache, appliance, vms and itself only when then empty (other files of the user stay) | unit | internal/uninstall/uninstall_test.go: TestUninstall/INST-05/data-folder | 12-05 |
| INST-06 | VERSION is accepted only as x.y.z with major and minor at most 255 and patch at most 65535 (table: 0.0.1, 255.255.65535 accepted; 1.2, 1.2.3.4, v1.2.3, 256.0.0, 1.0.65536, dev refused) | unit | tools/installer/installer_test.go: TestInstaller/INST-06 | 12-06 |
| INST-01 | the quiet bundle install on a machine without hlab installs the files to `%ProgramFiles%\hlab`, registers service `hlab` (manual, LocalSystem, the service DACL), creates `%ProgramData%\hlab`, puts the folder on the machine PATH; the Burn log shows the prerequisite action passed and the WebView2 package detected as present and skipped; `hlab version` prints the built version | e2e | test/install/install_test.go: TestInstall/INST-01 | 12-10 |
| INST-04 | after the default uninstall of an installed cloud that ran with a server, trust on and a WSL distro: no VM `hlab-appliance` or `hlab-server-*`, no switch `hlab`, no route via its adapter, no NRPT rule with comment `hlab`, no NAT `hlab`, no firewall group `hlab`, forwarding of the WSL adapter as before the install, no service `hlab`, no hlab CA thumbprint in LocalMachine\Root, no `/usr/local/share/ca-certificates/hlab-ca.crt` in the distro | e2e | test/install/install_test.go: TestInstall/INST-04 | 12-10 |
| INST-04 | uninstalling the release leaves the dev profile's switch `hlab-dev` and VM `hlab-dev-appliance` and the dev service in place | e2e | test/install/install_test.go: TestInstall/INST-04/dev-profile | 12-10 |
| INST-05 | after the default quiet uninstall with the tray running, no file of hlab remains: `%ProgramFiles%\hlab`, `%ProgramData%\hlab`, the uninstalling user's `%AppData%\hlab-tray.exe` and `%LocalAppData%\hlab` are gone, the PATH entry and the shortcuts are gone, the bundle is no longer registered | e2e | test/install/install_test.go: TestInstall/INST-05 | 12-10 |
| INST-05 | uninstall with `KeepData=1` keeps `%ProgramData%\hlab` (settings.toml, state.db, server disk, ca) and removes VMs, switch, NRPT rules and the service; a reinstall and cloud start shows the project and recreates the server's VM | e2e | test/install/install_test.go: TestInstall/INST-05/keep | 12-10 |
| INST-06 | upgrading 0.0.1 to 0.0.2 with the cloud running stops the cloud, leaves it stopped, and keeps settings (a changed `stop_behaviour`), projects, tokens (the token still authenticates after start) and the server; `hlab version` prints 0.0.2 | e2e | test/install/install_test.go: TestInstall/INST-06 | 12-10 |
| INST-06 | installing 0.0.1 over 0.0.2 is refused and leaves 0.0.2 installed | e2e | test/install/install_test.go: TestInstall/INST-06/downgrade | 12-10 |

INST-03 (none) has no row; the shortcuts are checked as part of INST-05's
removal and in the manual check. The keep-data checkbox in the bundle's
uninstall page is a dialog and is looked at in the manual check; INST-05
proves the variable path quietly. DOC-01 to DOC-06 keep their rows in
test/docs/docs_test.go (TestDocs/DOC-0n), which 12-11 keeps green; DOC-07
is manual.

The TestInstall rows share one build of both installers and the dev cloud
stop/restore; each row brings the machine to the state it needs (installed
version, fixture) through shared helpers, so a `-run` filter selects one
row and still works. They run one after another (one machine, one release
installation), each with a deadline of its own.
