# Tools

What a developer installs, and how lint, the tests, the live tests, the
dev loop, the installer and the builds work.

`bin/dev` (Bash, for Linux and WSL2) and `bin/dev.ps1` (PowerShell, for Windows) are
the only entry points. CI runs them the same way a developer does.

What a developer installs:

| What | Where | Why |
|---|---|---|
| Go | `go.mod` names the version; with `GOTOOLCHAIN=auto` (the default) any newer Go fetches it | building, testing |
| gcc | Linux, WSL2 (`apt install gcc`) | only for the race detector of `bin/dev test`; the binaries use no cgo |
| shellcheck, yamllint | Linux, WSL2 (`apt install shellcheck yamllint`) | `bin/dev lint` |
| PSScriptAnalyzer | Windows PowerShell or pwsh: `Install-Module PSScriptAnalyzer -Scope CurrentUser` | `bin/dev lint`, `bin/dev.ps1 lint` |
| hcloud CLI, OpenTofu | Linux, WSL2, in the versions of `compat/versions.yaml` | `bin/dev smoke` |
| Docker | WSL2 (Docker Desktop with WSL integration) or Linux | `bin/dev test --agent` |
| mmdebstrap, uidmap, debian-archive-keyring, e2fsprogs (1.47.1 or newer) with libarchive (`libarchive13t64`), dosfstools, mtools, fdisk, qemu-utils | WSL2, Linux (`apt install …`); an entry for the user in `/etc/subuid` and `/etc/subgid` (Ubuntu creates it) | `bin/dev appliance` |
| Hyper-V; once as administrator `bin/dev.ps1 dev-install`; Smart App Control off (it blocks self-built programs unpredictably) | Windows 11 Pro | `bin/dev itest`, `bin/dev deploy` |

golangci-lint and shfmt need no install: `go tool` builds them from `tools/go.mod`
the first time they run. In WSL2, `bin/dev lint` runs PSScriptAnalyzer in pwsh if it
is installed, otherwise in Windows PowerShell through WSL interop. `bin/dev.ps1 lint`
skips shellcheck and yamllint; they are Linux tools, and CI's Linux job runs them.

**Windows tests from WSL2.** `bin/dev test --windows` compiles each package's tests
with `GOOS=windows go test -c`. It runs the resulting `.exe` through WSL interop in
the package's directory, and the exit code and output come back to WSL2. Windows
needs no Go for this. Tests that need the go command, such as the layer test, are
skipped there; CI's Windows job has Go and runs them.

**Live tests.** `bin/dev itest [--run <pattern>]` runs the tests in `itest/`, which
build only with the tag `live`, against the real platform: the contract tests of
the hypervisor, the host network and the NRPT rules
(`internal/platform/contract`), the appliance's boot with its watchdog, and
the servers (`TestServersBootAnswerAndOutliveTheLab`): the whole lab with
the Cloud API in the test's process creates a Debian and an Ubuntu server.

1. Both answer SSH and serve what their metadata service told them (their
   user data starts a small HTTP server on port 8000) over IPv4 and IPv6.
2. With the lab's key (`ssh.exe`), the guest sees the host as `198.18.0.1`
   and `2001:db8:0:ffff::1`, and as `198.18.0.1` from WSL2 (`wsl.exe`, left
   out without WSL or its ssh). It reaches 8080, 8081 and the Internet, not
   8090, and the private ranges behind the uplink answer EHOSTUNREACH
   (admin-prohibited). A ping from one server to the other has TTL 63: the
   isolated ports let them meet only through the appliance.
3. The lab stops (the host keeps no switch, address, route or NRPT rule of
   it) and starts: the same servers on the same addresses with their disks
   (a mark written before), booted again; the Ubuntu server's VM is removed
   meanwhile and comes back with its disk.
4. A crash: another process of the test binary (`TestLabForACrash`) runs the
   lab and is killed; the servers keep running, and the next start takes
   them over without a boot.
5. The appliance is turned off hard; the Debian server answers again within
   10 s.
6. The appliance's update: another crash, and the lab starts with a copy of
   the release disk that differs only in the VHDX's creator field, so it is
   another release. The new appliance (MAC set B) replaces the old one while
   the Debian server runs: it must take new TCP connections again within 5 s
   (tried every 100 ms), an SSH session that prints every 100 ms must live
   through it, the server must not boot, and the appliance's folder must hold
   only the new release and `system-B.vhdx`.
7. A reset, the guest's shutdown, the deletes, and the host is clean.

The test keeps the system images in `C:\HLabDev\itest\image-cache`, so that
only its first run loads them.

- The host network and the NRPT need administrator rights, without UAC. So the
  tests run through the task "HLab itest" of the Task Scheduler (highest rights,
  S4U: no window, no stored password). `bin/dev itest` compiles them with
  `GOOS=windows go test -c -tags live` into `C:\HLabDev\itest`, copies the
  appliance's disk there if `bin/dev appliance` built a newer one, writes
  `run.cmd` with the test's flags, starts the task with `schtasks /run`, and
  prints `itest.out` as it grows until `itest.rc` holds the exit code.
  `bin/dev.ps1 itest` does the same on Windows with Go.
- Once, as administrator: `bin/dev.ps1 dev-install` creates `C:\HLabDev`,
  which only the developer, the administrators and SYSTEM may write (whatever
  lands there runs elevated; Hyper-V's VMs may read it), the tasks "HLab itest"
  and "HLab dev" (dev loop, below), the firewall rule "HLab dev" and the
  agent's hv_sock service (the registry key of `VsockServiceID(5000)` under
  `GuestCommunicationServices`). From WSL2: `powershell.exe -ExecutionPolicy
  Bypass -File \\wsl.localhost\<distribution>\<repo>\bin\dev.ps1 dev-install`
  in a PowerShell run as administrator. Running it again is safe.
- HLab must not run meanwhile: the tests take its switches, ranges and VM name.
- Disks go to a temporary folder on the Windows disk: Hyper-V cannot use files in
  WSL2's file system.
- The tests remove what they create. Their VMs and disks are named
  `hlab-contract-…` and their VMs carry the notes `hlab-contract`; after an aborted
  run, `Get-VM hlab-contract-* | Stop-VM -TurnOff -Force -Passthru | Remove-VM
  -Force` removes the VMs. The appliance test uses the lab's own names
  (`hlab-appliance-a`, `HLab-Link`, `HLab-Public`).
- The live tests stay out of CI: GitHub's Windows runners have no nested Hyper-V.
  All of them take about 5 minutes: the hypervisor's 30 s, the host network's 6 s,
  the appliance's 30 s, the servers' 240 s with the images cached (the
  appliance and the servers skip without the appliance's disk).

**Agent tests.** The tests of `internal/agent` with the build tag `netns` change
links, addresses, routes, nftables and sysctls for real, so they run in a network
namespace of their own, never on the developer's machine as it is:

- `bin/dev test --agent` builds them and runs them in a container without network
  (`--network none`) with `NET_ADMIN` and `NET_RAW`. `/proc/sys` is read-only in a
  container unless `systempaths=unconfined`; `NET_ADMIN` still limits writes to the
  container's own `net.*`. The image (`internal/agent/testdata/Dockerfile`) has
  nft, dnsmasq, ip and busybox's `udhcpc`, which plays a server asking for its
  address.
- `bin/dev test --agent --host` runs them with sudo in a new namespace
  (`unshare --net`); CI's job `agent` does that on `ubuntu-latest`.
- They take a few seconds. The NICs are veth pairs: the agent renames one end by its
  MAC address, the test watches the other.
- The tests without the tag (the API, the metadata service) run with `bin/dev test`.

**Smoke tests.** At the end of a phase, `bin/dev smoke` runs the real tools against
the lab, from Linux or WSL2:

1. It starts `hlabd --platform fake` with a fresh data folder on free ports. Its
   catalog takes `debian-13` from a local HTTP server (python3), as a small
   qcow2 of the tests: the fake machine's servers need no real image.
2. It runs the hcloud CLI with the commands of the phases so far.
3. It runs OpenTofu (`apply`, `plan`, `destroy`) on every `examples/tofu-<phase>`.

The smoke tests pass when nothing fails and the `plan` after `apply` shows no
changes. Each phase adds its commands to `smoke` in `bin/dev` and its configuration
as `examples/tofu-<phase>`. The smoke tests need the provider from the OpenTofu
registry, so they stay out of CI.

**The dev loop.** `bin/dev deploy` runs a freshly built `hlabd` on Windows from
WSL2, without UAC:

1. It builds everything (`bin/dev build`).
2. If an `hlabd.exe` runs, it stops it with `hlab stop` (the servers shut
   down, as at every stop) or, with `--keep-vms`, ends the task with `schtasks
   /end` as a crash would end it: the servers and the appliance keep running,
   and the start takes them over. Then it waits until the process is gone.
3. It copies `hlabd.exe`, `hlab.exe` and a newer appliance's disk to
   `C:\HLabDev\bin`, starts the task "HLab dev" (`hlabd.exe --data
   C:\HLabDev\data`, highest rights, S4U as "HLab itest": no window, so its log
   is `C:\HLabDev\data\logs\hlabd.log`) and waits until `hlab status`
   answers.

It prints how long that took. Measured on a Ryzen AI Max+ 395: about 29 s
with the appliance made anew (stop 9 s, start 20 s), 9 s with `--keep-vms`.
The CLI in WSL2 finds the dev loop's lab by itself (instance file). The task's
firewall rule lets WSL2 reach 8080, 8081 and 8090 on `198.18.0.1`, the servers
8080 and 8081 through the appliance. An installed HLab must not run meanwhile.

**Installing.** `install/install.ps1` installs HLab for users (see its help and
the README), from the folder that `bin/dev package` fills (`build/package`:
`hlabd.exe`, `hlab.exe`, the Linux `hlab`, the appliance's disk, the script).
There is no Windows service and no autostart: HLab runs when the user starts
it (the start menu's link, whose "Run as administrator" flag the script sets
in the `.lnk`, or `hlab start`). `-Uninstall` first stops the installed
`hlabd` (`hlab stop`) and refuses while one from another folder runs. It
removes the VMs with the notes `hlab` (with `-KeepData` their disks stay, and
the next start gives each server a new VM with its disk), the switches `HLab-*` with the host's NIC, the NRPT rules
with the comment `hlab`, and the rest the install added; the Hyper-V feature
stays on. The firewall rule "HLab" is separate from the dev loop's, so both
can be there.

**Builds.** `bin/dev build` builds with `CGO_ENABLED=0` and `-trimpath`, and sets
`main.version` from `git describe`. `hlabd --version` (and the same flag of the other
binaries) prints it.
