# Release checks

Before a milestone is released, HLab is checked on Windows machines that
look like a user's: a fresh Windows 11 Pro without the history of the
development machine. Two Hyper-V VMs on the development machine serve for
this. Each has nested virtualization on, so HLab can run its own VMs
inside it. This guide says how to set them up with the repository's
scripts and what runs in which.

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

The script `scripts\release-check-vm.ps1` creates a test VM, from nothing
up to a VM that is off, has Windows 11 Pro with Hyper-V set up and a
checkpoint `fresh`. Both VMs are created with it; the name decides the
sizes:

| VM | Name | vCPU | Memory (static) | Disk |
|---|---|---|---|---|
| Development test VM | `release-check-dev` | 8 | 24 GB | 200 GB |
| User test VM | `release-check-user` | 4 (4 to 8 with `-Cpu`) | 16 GB | 150 GB |

The names avoid the prefix `hlab-`, which HLab uses for its own VMs. Run
the commands of this section on the development machine, in an
administrator PowerShell; the script refuses to run without elevated
rights.

### Get the Windows 11 ISO

Download "Windows 11 Disk Image (ISO) for x64 devices" from
<https://www.microsoft.com/software-download/windows11> (the download link
is valid for 24 hours). The page lists the SHA-256 hash of each language's
ISO; compare it with the downloaded file:

```powershell
Get-FileHash C:\VMs\iso\Win11_x64.iso -Algorithm SHA256
```

### Run the script

In the repository root, once per VM:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\release-check-vm.ps1 -Name release-check-dev -Iso C:\VMs\iso\Win11_x64.iso -Lang de-DE
powershell -ExecutionPolicy Bypass -File .\scripts\release-check-vm.ps1 -Name release-check-user -Iso C:\VMs\iso\Win11_x64.iso -Lang de-DE
```

A fresh Windows does not run script files under its default execution
policy; `-ExecutionPolicy Bypass` lifts that for this one call only and
changes no setting. Every script in this guide is called this way.

| Parameter | Meaning |
|---|---|
| `-Name` | `release-check-dev` or `release-check-user` (required) |
| `-Iso` | path of the Windows 11 ISO (required) |
| `-Lang` | language of the ISO, for example `de-DE` or `en-US` (required); Windows setup stops if it does not match |
| `-Cpu` | vCPU count; `release-check-user` accepts 4 to 8, `release-check-dev` only 8 |
| `-VmRoot` | folder for the VM and its disk, default `C:\VMs`; it needs room for the disk |
| `-User` | name of the local administrator account in the VM, default `tester` |

The script asks for the account's password twice before it creates
anything. It writes the password in plain text into the answer files, so
use one for this VM only; the files are deleted before the script ends.

When the script has started the VM, it opens the VM's console. Press a
key there when it shows "Press any key to boot from CD or DVD"; that is
the only input setup needs. Then wait: setup installs Windows and
restarts the VM several times. The script prints a line every 30 seconds
while it waits and gives up after 90 minutes.

A successful run ends with the line that the VM "is set up, shut down
and has the checkpoint 'fresh'". Sign in to the VM with the account and
password you gave.

### What the script does

Each step prints a line `[n/9]`:

1. It stops if a VM of that name exists already; it only creates new VMs.
2. It creates the VM in `<VmRoot>\<name>` with the settings below.
3. It writes the answer files to `<VmRoot>\<name>\unattend`, checks that
   `autounattend.xml` was written, builds `<VmRoot>\<name>\unattend.iso`
   from them and adds it to the VM as a second DVD. The Windows ISO stays
   the first boot device.
4. It opens the console with `vmconnect` and starts the VM.
5. It waits until Windows answers over PowerShell Direct with the account
   and Hyper-V's management service runs inside the VM, that is, until
   setup is done and Hyper-V is enabled.
6. It empties both DVD drives and deletes the answer folder and its ISO.
   This step also runs when an earlier step fails or you stop the script
   with Ctrl+C.
7. It runs [the subnet check](#check-the-subnet-before-the-first-cloud-start)
   inside the VM and stops on an overlap.
8. For `release-check-dev` it copies `scripts\release-check-dev-tools.ps1`
   to `C:\Temp` in the VM.
9. It shuts the VM down and takes the checkpoint `fresh`.

The ISO is built with Windows' own image mastering interface (IMAPI), so no
extra tool is needed.

### The VM settings and why

- Generation 2 with Secure Boot (template `MicrosoftWindows`) and a
  virtual TPM: Windows 11 requires them. The TPM's key protector is bound
  to this development machine; the VM does not start on another host
  without a new one.
- Static memory: while Hyper-V runs inside a VM, its memory cannot change.
- Nested virtualization (`-ExposeVirtualizationExtensions $true`) is on.
  It can only be set while the VM is off. On an AMD processor the VM's
  configuration version must be 9.3 or higher; `Get-VM <name> |
  Select-Object Name, Version` shows it (a Windows 11 host creates 12.x).
- "Default Switch" gives the VM internet access through the development
  machine. HLab builds its own switch and NAT inside the VM, as on a real
  machine.
- The integration service "Guest Service Interface" lets `Copy-VMFile` copy
  files from the development machine into the VM. The script selects it by
  its ID, so it works in any host language.
- No automatic checkpoints, and the VM shuts down instead of saving its
  state when the development machine shuts down (see
  [Shut a VM down, do not save it](#shut-a-vm-down-do-not-save-it)).

### What the answer file does

The answer file runs the whole Windows setup without a question, in this
order:

- Setup language, keyboard and region come from `-Lang`, during setup
  and in the installed Windows.
- Disk 0 is wiped and gets the layout a Generation 2 VM boots from: an
  EFI partition of 100 MB (FAT32), a 16 MB reserved partition and the rest
  for Windows (NTFS).
- The product key `W269N-WFGWX-YVC9B-4J6C9-T83GX` is Microsoft's published
  KMS client setup key for Windows 11 Pro. It only selects the edition:
  Hyper-V needs Pro, Enterprise or Education. Windows stays unactivated,
  which is enough for these checks (a watermark, and personalization is
  locked). The licence terms are accepted.
- The script `account.ps1` from the same ISO runs while Windows configures
  itself (the specialize pass). It creates the local account and adds it
  to the group with the well-known SID `S-1-5-32-544`, which is the
  Administrators group in every Windows language ("Administratoren" in
  German, for example). It then marks the account as created by the answer
  file, so the setup screens do not ask for one.
- The setup screens for the licence, online accounts, wireless networks
  and privacy settings are skipped; privacy settings keep Windows'
  defaults.
- The account signs in once by itself. Its first sign-in enables
  `Microsoft-Hyper-V` with all its parts and restarts the VM. The account
  is an administrator under User Account Control, like on most users'
  machines.

### When the script stops

The script stops at the first error and says why. Before it creates the
VM, it stops without changing anything when it runs without elevated
rights, the ISO is missing, `-Cpu` is out of range, the two passwords
differ or are empty, or the VM exists already.

After the VM exists:

- **Wrong password, or setup takes too long.** While it waits, a line
  `not reachable: ...` about credentials means the account does not
  accept the password; stop the script with Ctrl+C. After 90 minutes the
  script stops by itself. Look at the VM's console, then remove the VM as
  below and run the script again.
- **Overlap in the subnet check.** Windows and Hyper-V are set up, but
  the VM is running and has no checkpoint. Follow
  [Check the subnet before the first cloud start](#check-the-subnet-before-the-first-cloud-start)
  until every line says `ok`, then finish by hand on the development
  machine (the `Copy-VMFile` line only for `release-check-dev`):

  ```powershell
  Copy-VMFile -Name 'release-check-dev' -SourcePath C:\path\to\hlab\scripts\release-check-dev-tools.ps1 `
    -DestinationPath C:\Temp\release-check-dev-tools.ps1 -FileSource Host -CreateFullPath -Force
  Stop-VM -Name 'release-check-dev'
  Checkpoint-VM -Name 'release-check-dev' -SnapshotName 'fresh'
  ```

- **Answer files left behind.** If the script cannot delete them, it
  prints a warning naming them; they hold the password. Remove the answer
  ISO from the VM and delete the folder and the ISO:

  ```powershell
  $Name = 'release-check-dev'                  # or 'release-check-user'
  $Dir  = "C:\VMs\$Name"                       # <VmRoot>\<name>
  Get-VMDvdDrive -VMName $Name | Where-Object Path -eq "$Dir\unattend.iso" | Remove-VMDvdDrive
  Remove-Item -Recurse -Force "$Dir\unattend", "$Dir\unattend.iso"
  ```

To remove a half-built VM and start again, turn it off, remove it and
delete its folder, which holds its configuration and its disk:

```powershell
$Name = 'release-check-dev'                    # or 'release-check-user'
Stop-VM -Name $Name -TurnOff -Force
Remove-VM -Name $Name -Force
Remove-Item -Recurse -Force "C:\VMs\$Name"     # <VmRoot>\<name>
```

### Enable Hyper-V in the VM

The answer file enables Hyper-V, and the script waits for it. To check
it by hand, run in an administrator PowerShell inside the VM; the line
`State` must say `Enabled`:

```powershell
Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V
```

If it is not `Enabled`, nested virtualization is off: shut the VM down,
check `Get-VMProcessor -VMName <name> | Select-Object
ExposeVirtualizationExtensions` on the development machine, and set it
again while the VM is off. Then enable Hyper-V in an administrator
PowerShell inside the VM with
`Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All`
and restart it.

### Check the subnet before the first cloud start

HLab's public range is `198.19.0.0/16`, and its servers use `172.31.1.1`
as their gateway. The "Default Switch" gives the VM an address from
`172.16.0.0/12` and may choose another subnet after the development machine
restarts. If the VM's subnet contains `172.31.1.1` or overlaps
`198.19.0.0/16`, HLab's addresses inside the VM clash with it. The script
checks this once; after every restart of the development machine, check
again before the first cloud start in each VM, in a PowerShell inside the
VM:

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
Stop-VM -Name 'release-check-user'
```

Never use `Save-VM` or "Save" in Hyper-V Manager for these VMs.

### Checkpoint the fresh VM

The script ends with the checkpoint `fresh` of the VM while it is off.
Each check run can start again from that clean state; shut the VM down
first:

```powershell
Restore-VMCheckpoint -VMName 'release-check-user' -Name 'fresh' -Confirm:$false
```

For the development test VM, `fresh` comes before its tools. Once the
tools and the clone below are in, shut it down and take a checkpoint of
its own, so a later run need not install them again:

```powershell
Checkpoint-VM -Name 'release-check-dev' -SnapshotName 'tools'
```

## Development test VM

The development test VM runs only the elevated installer suite. The
script `release-check-dev-tools.ps1`, which `release-check-vm.ps1` copied
to `C:\Temp` in the VM, gives it the tools the suite needs and a clone of
the repository, nothing else.

### Install the tools

Start the VM, sign in, and run in an administrator PowerShell inside it:

```powershell
powershell -ExecutionPolicy Bypass -File C:\Temp\release-check-dev-tools.ps1
```

It installs, and skips each part that is already there:

- with winget: Git, Go, Node.js LTS, Task and the .NET SDK 8,
- WiX 7.0.1 with its licence accepted and the extensions
  `WixToolset.Util.wixext` and `WixToolset.BootstrapperApplications.wixext`,
- Wails v3.1.0,
- WSL2 with `Ubuntu-24.04`.

It refreshes `PATH` between the steps, so no new terminal is needed, and
prints the versions of Go, Node.js, Task and WiX at the end; they must
match [Developer setup](setup.md#tools). If winget is not available yet
right after the first sign-in, the script says so; wait a few minutes and
run it again.

When it has installed WSL2, its last lines ask for a restart: restart the
VM (`Restart-Computer`), open "Ubuntu" from the Start menu once and create
its user. The suite needs no hcloud, OpenTofu or Ansible.

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

Inside the VM, run the script again with the bundle and the printed hash:

```powershell
powershell -ExecutionPolicy Bypass -File C:\Temp\release-check-dev-tools.ps1 -Bundle C:\Temp\hlab.bundle -Hash <sha256>
```

`-Bundle` and `-Hash` go together. The script skips the tools that are
in, compares the hash, stops if it differs, and clones `main` to
`C:\src\hlab`; it skips the clone if that folder exists. It ends with
`All tools are set up.` once WSL2 and Ubuntu are set up as well.

### Register the service and start the dev cloud once

In an administrator PowerShell inside the VM, in `C:\src\hlab`:

```powershell
task build
powershell -ExecutionPolicy Bypass -File .\scripts\dev-setup.ps1
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
cmd /c "task e2e:elevated -- -v 2>&1" | Tee-Object -FilePath C:\Temp\e2e-elevated.log
```

The suite builds the setup programs 0.0.1 and 0.0.2 from the clone, then
runs the rows `INST-01`, `INST-04`, `INST-05` and `INST-06` with their
subtests one after another. Each row logs
`start TestInstall/<row>` and `end TestInstall/<row>: ok` with its time;
with `-v`, `go test` also prints `=== RUN` and `--- PASS` (or `--- FAIL`)
for every row and subtest.
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

### The checks in order

Each check below says where it starts, what it needs, its steps and what
you should see. Run them in this order; each one builds on the state the
one before leaves behind:

1. [Install and launch](#install-and-launch-inst-03)
2. [Names of the first cloud start](#names-of-the-first-cloud-start)
3. [Example environment in the window](#example-environment-in-the-window-ui-09)
4. [Update over an older version](#update-over-an-older-version-inst-06)
5. [Windows shutdown with a running cloud](#windows-shutdown-with-a-running-cloud-life-05)
6. [Storage box space after a delete](#storage-box-space-after-a-delete-compact)
7. [Uninstall](#uninstall-inst-05)

To run them again from the start, shut the VM down, go back to its
checkpoint `fresh` (see [Checkpoint the fresh VM](#checkpoint-the-fresh-vm))
and copy the setup programs in again. Note each result, with what you saw
where it differs from "Expected".

### Install and launch (INST-03)

- Start: Explorer in the user test VM, folder `C:\Temp`.
- Setup: no HLab installed (a fresh VM).
- Steps:
  1. Double-click `hlab-setup-0.1.0.exe`. SmartScreen may warn because the
     setup program is not signed: **More info**, then **Run anyway**.
  2. Click **Install** and confirm the administrator prompt.
  3. On the last page click **Launch**.
  4. Open Task Manager, **Details**, right-click a column heading,
     **Select columns**, tick **Elevated**, and look at `hlab-tray.exe`.
  5. Open the Start menu and type `HLab`; look at the desktop.
  6. In Task Manager open **Startup apps**.
- Expected: **Launch** opens the HLab window and the tray icon without
  another administrator prompt, and `hlab-tray.exe` shows **Elevated**
  "No". The Start menu and the desktop each have one shortcut "HLab",
  which opens the window and nothing else. HLab is not among the startup
  apps.

### Names of the first cloud start

- Start: the HLab window from the check above.
- Setup: run [the subnet check](#check-the-subnet-before-the-first-cloud-start)
  in a PowerShell inside the VM; every line must end in `ok`.
- Steps:
  1. Click **Start cloud** in the top bar and wait until the window shows
     the cloud running; the first start takes several minutes.
  2. Open Hyper-V Manager inside the VM and confirm its administrator
     prompt.
  3. Double-click the VM `hlab-appliance` to open its console.
- Expected: Hyper-V Manager lists the VM `hlab-appliance`, and its console
  shows the prompt `hlab-appliance login:`.

### Example environment in the window (UI-09)

This builds the example environment by hand, two servers in a private
network behind a load balancer and a firewall, with a volume, a floating
IP and a DNS zone, and deletes it again. On the way it checks the help
texts of the views it opens.

- Start: the HLab window with the cloud running.
- Setup: an SSH key in the VM, copied to the clipboard. In a PowerShell
  inside the VM:

  ```powershell
  ssh-keygen -t ed25519
  Get-Content $HOME\.ssh\id_ed25519.pub | Set-Clipboard
  ```

- Steps:
  1. **+ New project** with the display name `demo`, and open it. In
     **Security**, tab **SSH Keys**, **Add SSH key** and paste the key.
  2. **Networks**: **Create network** `demo-net` with the IP range
     `10.0.0.0/16`; on its page, **Add subnet** `10.0.1.0/24`.
  3. **Firewalls**: **Create firewall** `demo-fw` with inbound TCP rules
     for port 22 and port 80.
  4. **Servers**: **Create server** twice, `web1` and `web2`, each with
     your SSH key, the network `demo-net` and the firewall `demo-fw`. Wait
     until both run, then run `ssh root@<IPv4 of web1> hostname` in
     PowerShell.
  5. **Volumes**: **Create volume** `demo-vol`, 10 GB, server `web1`;
     detach it and attach it again from its actions menu.
  6. **Load Balancers**: **Create load balancer** `demo-lb` with the
     network `demo-net`, the targets `web1` and `web2` and the HTTP
     service on port 80; look through its tabs.
  7. **IPs**, tab **Floating IPs**: **Create floating IP** (IPv4) for
     `web1`; **Unassign** it, then **Assign** it to `web2`.
  8. **DNS**: **Add zone** `demo.internal`; in **Records** add an `A`
     record `www` with the floating IP; open **Zone file**.
  9. On `web1`'s page, tab **Snapshots**, **Create snapshot**; delete it
     in **Servers**, tab **Snapshots**.
  10. Switch Windows between light and dark mode (Settings >
      Personalization > Colors) and look over the pages you used.
  11. In every view you opened, click `?`.
  12. Delete everything by hand: the load balancer, the floating IP, the
      volume, the zone and the firewall, then the servers, then the
      network and the SSH key. Keep the project `demo`.
- Expected: every step works from the window alone. Each action ends in
  a toast naming the resource and the action, and errors appear at their
  field. `ssh` prints `web1`. Each help text names only views, tabs and
  buttons that exist. At the end every section of the project is empty.

### Update over an older version (INST-06)

- Start: the HLab window with the cloud running, 0.1.0 installed.
- Setup: in the project `demo`, add the SSH key again and create one
  server `upd1` with it. Change a setting: **Administration** >
  **Settings**, the log level `debug`, **Save settings**.
- Steps:
  1. Run `C:\Temp\hlab-setup-0.1.1.exe`, click **Install** and confirm
     the administrator prompt. Setup closes the tray program and stops
     the cloud.
  2. On the last page click **Launch**.
  3. Open Settings > Apps > Installed apps and find "HLab".
  4. In the window click **Start cloud** and wait until it runs; then, in
     a new PowerShell:

     ```powershell
     hlab cloud status
     ssh root@<IPv4 of upd1> hostname
     ```

- Expected: Apps lists "HLab" once, with version 0.1.1. `hlab cloud
  status` shows `state: running` and `version: 0.1.1`. The project `demo`,
  the server `upd1` and the log level `debug` are still there, and `ssh`
  prints `upd1`.

### Windows shutdown with a running cloud (LIFE-05)

- Start: a PowerShell inside the VM, the cloud running, `upd1` running.
- Setup: the stop behaviour under **Administration** > **Settings** is
  **Save the VMs' state**, the default. Note the server's boot ID and boot time:

  ```powershell
  ssh root@<IPv4 of upd1> 'cat /proc/sys/kernel/random/boot_id; uptime -s'
  ```

- Steps:
  1. While the cloud runs, shut the user test VM down from its Start
     menu (Power > Shut down).
  2. Start the VM again from the development machine and sign in.
  3. Open HLab from the Start menu. Click **Start cloud** and wait until
     it runs.
  4. Run the `ssh` command of the setup again.
  5. Optional: set the stop behaviour to **Shut the VMs down**, **Save settings**,
     then **Stop cloud** and **Start cloud** once (Hyper-V takes the new
     value only at a cloud start), and repeat steps 1 to 4.
- Expected: after the restart the window shows the cloud stopped until
  you start it. With **Save the VMs' state**, `upd1` runs again with the same boot ID: it
  was saved, not restarted. With **Shut the VMs down**, it runs again with a new boot
  ID and a later boot time: it was shut down and powered on.
  **Administration** > **Logs** shows no error about restoring servers.

### Storage box space after a delete (COMPACT)

When a box is deleted and the cloud stops, HLab compacts the storage VM's
data disk, so the drive gets the space back.

- Start: the HLab window with the cloud running, project `demo`.
- Setup:
  1. **Administration** > **Settings**: set the stop behaviour to
     **Shut the VMs down** and **Save settings**; with **Save the VMs' state** the disk is not
     compacted.
  2. **Storage Boxes**: **Create Storage Box** twice. Box A with a
     password, **Samba** and **reachable externally** on; box B with the
     defaults. Box B keeps the storage VM: deleting the last box removes
     the VM with its disk. Note box A's host name and user name from its
     **Overview**.
  3. Create a file of 2 GB of random data, in a PowerShell inside the VM:

     ```powershell
     $buf = New-Object byte[] (64MB)
     $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
     $f = [IO.File]::Create('C:\Temp\random.bin')
     try { for ($i = 0; $i -lt 32; $i++) { $rng.GetBytes($buf); $f.Write($buf, 0, $buf.Length) } } finally { $f.Close() }
     ```

- Steps:
  1. In Hyper-V Manager, open the console of the VM `hlab-storage`.
  2. Press Win+R, enter `\\<host name of box A>\backup`, sign in with
     box A's user name and password, and copy `C:\Temp\random.bin` into
     the share. The same from PowerShell:

     ```powershell
     net use \\<host name of box A>\backup /user:<user name of box A> *
     Copy-Item C:\Temp\random.bin \\<host name of box A>\backup\
     ```

  3. **Stop cloud**, then read the size of the data disk in an
     administrator PowerShell:

     ```powershell
     (Get-Item C:\ProgramData\hlab\data\storage\data.vhdx).Length / 1GB
     ```

  4. **Start cloud**, delete box A (type its name to confirm) and wait
     until it is gone from the list.
  5. **Stop cloud** and read the size again.
  6. Afterwards set the stop behaviour back to **Save the VMs' state**, start the cloud
     and delete box B.
- Expected: Hyper-V Manager lists `hlab-storage`, and its console shows
  `hlab-storage login:`. After step 3 the disk is about 2 GB larger than
  an empty one; after step 5 it is clearly smaller again, roughly as
  before the copy. **Administration** > **Logs** shows "free blocks of
  the storage VM's data file system discarded" and then "data disk of
  the storage VM compacted", each with its duration.

### Uninstall (INST-05)

- Start: Settings > Apps > Installed apps, "HLab", **Uninstall**.
- Setup: HLab 0.1.1 installed, the tray running.
- Steps:
  1. Start the uninstall and confirm the administrator prompt.
  2. Look at the page that opens, leave its checkbox unchecked and click
     **Uninstall**.
  3. Afterwards, in a PowerShell inside the VM:

     ```powershell
     'C:\Program Files\hlab', 'C:\ProgramData\hlab',
       "$env:APPDATA\hlab-tray.exe", "$env:LOCALAPPDATA\hlab" |
       ForEach-Object { '{0}: {1}' -f $_, (Test-Path $_) }
     Get-ChildItem -Recurse -Filter HLab.lnk -ErrorAction SilentlyContinue `
       "$env:ProgramData\Microsoft\Windows\Start Menu\Programs",
       "$env:APPDATA\Microsoft\Windows\Start Menu\Programs",
       "$env:PUBLIC\Desktop", "$HOME\Desktop"
     ```

  4. Open Hyper-V Manager and its Virtual Switch Manager.
- Expected: the page shows the checkbox "Keep projects, servers and
  settings", unchecked. The tray closes and "HLab" is gone from Apps.
  Every line of step 3 ends in `False` and no shortcut is listed. Hyper-V
  Manager shows no VM `hlab-appliance`, `hlab-storage` or `hlab-server-<n>`,
  and no switch `hlab`.

## Reading the documentation

On the development machine, read the documents as their readers would,
in a Markdown preview in light and dark theme:

- `README.md`, as a developer who tests Hetzner setups and has never seen
  HLab: the banner, badges and link line, the screenshots that follow the
  theme, and whether installation and first steps can be followed without
  asking anyone.
- [docs/guide.md](../guide.md), [docs/commands.md](../commands.md),
  [docs/configuration.md](../configuration.md) and
  [docs/deviations.md](../deviations.md), as a user: is each resource,
  command and setting clear?
- The guides in this folder, as an outside contributor who wants to set
  up, build, test and extend HLab.

Nothing may be unclear, missing, or describe what is not built. The help
texts of the window are checked in the user test VM, in
[Example environment in the window](#example-environment-in-the-window-ui-09).
