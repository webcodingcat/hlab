# EXPERIMENTS: hlab

What the feasibility check tried and what came out of it. The code of the
experiments is deleted; every entry contains what is needed to understand and
repeat the result.

The planning of every phase reads this file, so nothing is found out
twice.

Machine for all entries: Windows 11 Pro 10.0.26300, Windows PowerShell
5.1.26100, Go 1.27.4, WSL 3.0.5.0 (kernel 6.18.58.1), user in Hyper-V
Administrators but not elevated unless stated. Hyper-V switch, VM, port ACL,
PVLAN (also `-ManagementOS`), `Convert-VHD`, `Set-VHD` and
`CreateVirtualDisk` work without elevation for members of Hyper-V
Administrators; host IP addresses, routes, WinNAT and NRPT need elevation.

## E1 Guests on an internal switch can be limited to talking to the appliance

- Result: confirmed with private VLAN; refuted with MAC allow/deny-ANY ACLs
- Checked on: 2026-11-18, Hyper-V on 10.0.26300
- By: experiment
- Status: valid

How it was checked: internal switch, Debian 13 "appliance" plus Ubuntu 24.04
and Debian 13 guests with /32 addresses and on-link gateway 172.31.1.1; a
probe in each guest pinged the appliance, a peer routed via the appliance,
and the peer directly after `ip route replace <peer>/32 dev eth0`, every ~7 s.

```
# refuted: legit traffic drops intermittently
Add-VMNetworkAdapterAcl -VMName g1 -RemoteMacAddress 02-48-4C-00-01-01 -Direction Both -Action Allow
Add-VMNetworkAdapterAcl -VMName g1 -RemoteMacAddress FF-FF-FF-FF-FF-FF -Direction Both -Action Allow
Add-VMNetworkAdapterAcl -VMName g1 -RemoteMacAddress ANY -Direction Both -Action Deny
# confirmed
Set-VMNetworkAdapterVlan -VMName appl -Promiscuous -PrimaryVlanId 10 -SecondaryVlanIdList '200'
Set-VMNetworkAdapterVlan -VMName g1 -Isolated -PrimaryVlanId 10 -SecondaryVlanId 200
Set-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName <switch> -Isolated -PrimaryVlanId 10 -SecondaryVlanId 200
```

Observed: no ACL: direct L2 works. Deny-ANY variant: direct blocked but
appliance pings failed in 3 of 13 rounds, guests' neighbour entry for
172.31.1.1 went FAILED. PVLAN: 43/43 rounds appliance ok, routed ok, direct
blocked. Without isolating the host vNIC too, the host lost the appliance
(0/10). MAC deny-list of only the other guests' MACs (no ANY rule): 0
failures, direct blocked.

Consequence: public segment as PVLAN (appliance promiscuous; host vNIC and
guests isolated); reserve the PVLAN IDs from the private VLAN pool; MAC
deny-lists for private adapters. MACs must be written with dashes in ACLs
(`02-48-4C-00-00-01`; the plain form is accepted by the cmdlet but rejected
by the switch with 0x80070057). Not tested: trunk and PVLAN ports on the same
switch. Measure "routed" before "direct" in tests, the order influenced the
first runs.

## E2 Ubuntu and Debian cloud images boot on Hyper-V Gen2 with NoCloud and a /32 network

- Result: confirmed
- Checked on: 2026-11-18, go-qcow2reader v0.8.0, go-diskfs v1.9.4, go-winio v0.6.4
- By: experiment
- Status: valid

How it was checked:

```
qcow2 -> raw: go-qcow2reader, sparse copy via img.Extent / ReadAt
raw -> fixed VHD: append 512-byte footer ("conectix", type 2, CHS geometry, one's-complement checksum)
Convert-VHD -Path x.vhd -DestinationPath x.vhdx -VHDType Dynamic
New-VM -Generation 2 ...; Set-VMFirmware -EnableSecureBoot On -SecureBootTemplate MicrosoftUEFICertificateAuthority
cidata: diskfs.Create(path, size, diskfs.SectorSize(2048)); close every file; Finalize(iso9660.FinalizeOptions{RockRidge: true, VolumeIdentifier: "cidata"})
network-config v2: match macaddress, set-name eth0, addresses [x/32], routes [{to: 0.0.0.0/0, via: 172.31.1.1, on-link: true}], nameservers 185.12.64.1/.2
```

Observed: Ubuntu 24.04 `.img` (3.5 GiB) converted in 16 s + 7 s, Debian 13
generic raw (3 GiB) 2.6 s + 13 s. Both boot with Secure Boot, cloud-init
reports `nocloud config-disk (/dev/sr0)` (Ubuntu cloud-init 26.1, Debian
25.1.4); both render with netplan (Debian 13: netplan + networkd);
`inet 198.19.0.4/32`, `default via 172.31.1.1 dev eth0 proto static onlink`.
Ubuntu reachable after 42-52 s without a COM port; Debian userspace 7-10 s.

Consequence: no qemu-img needed. Pitfalls: an interface without address in
network-config blocks `systemd-networkd-wait-online` ~2 min; New-VM defaults
to override: DynamicMemory, AutomaticCheckpoints, SecureBootTemplate
MicrosoftWindows, AutomaticStartAction StartIfRunning. PowerShell messages
and integration service names are localized: never parse text. A COM-port
named pipe closes on Save-VM; in the guest open `/dev/ttyS0` per write
(serial-getty's vhangup kills long-held handles).

## E3 Host route and WinNAT reach the servers and give them internet access

- Result: confirmed
- Checked on: 2026-11-18
- By: check run by the user (elevated rights)
- Status: valid

How it was checked:

```
New-NetIPAddress -InterfaceAlias "vEthernet (<switch>)" -IPAddress 198.19.0.2 -PrefixLength 30
New-NetRoute -DestinationPrefix 198.19.0.0/16 -InterfaceAlias "vEthernet (<switch>)" -NextHop 198.19.0.1 -PolicyStore ActiveStore
New-NetNat -Name <name> -InternalIPInterfaceAddressPrefix 198.19.0.0/16
appliance: 198.19.0.1/30 + 198.19.0.4/32 on eth0, default via 198.19.0.2
```

Observed: host ping 198.19.0.4 fails before the route, works after; HTTP
answer shows client 198.19.0.2. Appliance outbound (ping 1.1.1.1, TCP 443)
fails before NetNat, works after, with forwarding on the hlab adapter
disabled. All of it also works with the PVLAN of E1 in place.

Consequence: WinNAT for the public range; no forwarding on the hlab adapter
for NAT. Routes in ActiveStore vanish at reboot; NetNat and NRPT persist and
must be removed explicitly. Windows supports one WinNAT instance. Not
tested: Windows Server.

## E4 WSL2 in NAT mode reaches the servers and resolves hlab names

- Result: confirmed
- Checked on: 2026-11-18, WSL 3.0.5.0, distro Ubuntu, `networkingMode` nat, dnsTunneling on
- By: check run by the user (elevated rights)
- Status: valid

How it was checked: as E3, then from WSL `ping 198.19.0.4`, `curl
http://198.19.0.4/`, `getent hosts www.<zone>`, with and without

```
Set-NetIPInterface -InterfaceAlias "vEthernet (WSL (Hyper-V firewall))" -AddressFamily IPv4 -Forwarding Enabled
Set-NetIPInterface -InterfaceAlias "vEthernet (<switch>)" -AddressFamily IPv4 -Forwarding Enabled
```

Observed: without forwarding 100 % loss; with forwarding on both adapters
replies in ~1.4 ms; the appliance sees the WSL source address
(172.26.63.227), replies return through its default route via the host.
WSL resolves hlab names through dnsTunneling + NRPT. WSL outbound internet
unaffected. Hyper-V firewall for WSL: inbound Block, outbound Allow (no rule
needed). The WSL switch is named `WSL (Hyper-V firewall)`.

Consequence: hostnet enables forwarding on both adapters and restores the
WSL adapter's previous value on `Down` (the experiment's cleanup left it
enabled: restore must be tested). Not tested: whether the WSL subnet and the
forwarding flag survive `wsl --shutdown` (assume not: re-apply on adapter
change). The automatic WSL fallback is VirtioProxy; detect modes with
`wslinfo --networking-mode`.

## E5 NRPT rules route hlab names, zone apexes and the reverse zone to the appliance

- Result: confirmed
- Checked on: 2026-11-18, non-domain Windows 11 Pro
- By: check run by the user (elevated rights)
- Status: valid

How it was checked:

```
Add-DnsClientNrptRule -Namespace '.hlabexp.internal' -NameServers 198.19.0.1 -Comment 'hlabexp'
Add-DnsClientNrptRule -Namespace '.19.198.in-addr.arpa' -NameServers 198.19.0.1 -Comment 'hlabexp'
Add-DnsClientNrptRule -Namespace '.example.net' -NameServers 198.19.0.1 -Comment 'hlabexp'
Resolve-DnsName / [Net.Dns]::GetHostAddresses / curl.exe; Resolve-DnsName -Type PTR 198.19.0.4
```

Observed: Resolve-DnsName, .NET and curl.exe return 198.19.0.4; PTR returns
the name; the suffix rule `.example.net` also answers the apex
`example.net`; the appliance logs the queries from 198.19.0.2. `nslookup`
bypasses NRPT (asks the LAN resolver) - expected.

Consequence: exactly one NRPT rule per zone suffices. Tag rules by
`Comment`. Tests use Resolve-DnsName, never nslookup.

## E6 A chosen VHDX identifier is visible to the Linux guest

- Result: confirmed
- Checked on: 2026-11-18, go-winio v0.6.4 `vhd.CreateVirtualDisk`
- By: experiment
- Status: valid

How it was checked: `CreateVersion2.UniqueID = 12345678-9abc-4def-8123-456789abcdef`,
`Add-VMHardDiskDrive` hot-add at LUN 1, read `/dev/disk/by-id` and
`/sys/block/sdb/device/vpd_pg83` in the guest.

Observed: `Get-VHD` shows the identifier; the guest sees the disk within
~10 s without rescan. VPD 0x83 has a T10 designator `MSFT    ` + full GUID
in Windows byte order (`scsi-14d53465420202020` + `78563412bc9aef4d8123456789abcdef`)
and an NAA-6 designator `6 002248 0 78563412bc9a 456789abcdef`, which drops
GUID bytes 6-9.

Consequence: encode the volume ID in the GUID's last 6 bytes (present in both
designators); the udev rule derives `scsi-0HC_Volume_<id>` from them.
x/sys/windows has no virtdisk wrappers; use go-winio/vhd.

## E7 The server disk stays sda when a rescue disk at LUN 63 is booted

- Result: confirmed
- Checked on: 2026-11-18
- By: experiment
- Status: valid

How it was checked: LUN 0 Ubuntu differencing disk, LUN 63 Debian
differencing disk, `Set-VMFirmware -FirstBootDevice (Get-VMHardDiskDrive
-ControllerLocation 63)`, DVD on SCSI controller 1.

Observed: Debian boots with Secure Boot from `/dev/sdb1`; LUN 0 is `sda`,
LUN 63 `sdb`; the DVD is `sr0` and does not shift sdX. The SCSI host number
varied (0/1). Differencing disks and copies keep the parent's DiskIdentifier.

Consequence: rescue at LUN 63 works; reset the identifier of every disk made
from a base (`Set-VHD -ResetDiskIdentifier -Force`, no elevation needed);
never rely on the SCSI host number.

## E8 Responses can be validated against Hetzner's OpenAPI 3.1.2 description

- Result: confirmed
- Checked on: 2026-11-18, libopenapi-validator v0.16.3, libopenapi v0.42.5
- By: experiment
- Status: valid

How it was checked: load `https://docs.hetzner.cloud/cloud.spec.json`
(3,532,416 bytes, SHA-256 `3f90e2a03d37da90cde0b14744419bde101bb20829d0d870c0c3b95c5b3d1225`),
`validator.NewValidator`, `ValidateHttpResponse` for a request to
`http://api.hetzner.internal/v1/locations` with a valid and an invalid body.

Observed: document 23 ms, validator 260 ms, each validation about 1 ms, heap
152 MB; the invalid body was reported with JSON paths. A host other than the
spec's server URL still matches the paths.

Consequence: usable in unit tests; build the validator once per test
binary. `ValidateDocument()` reports the Hetzner spec itself as invalid
against the 3.1.2 meta-schema: do not gate on it.

## E9 Playwright can drive the Wails v3 tray window over CDP

- Result: refuted for the environment variable; confirmed through Wails options
- Checked on: 2026-11-18, Wails v3.1.0, WebView2 157
- By: experiment
- Status: valid

How it was checked: minimal Wails v3.1.0 app started with
`WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9340`; then
with `application.Options{Windows: application.WindowsOptions{AdditionalBrowserArgs:
[]string{"--remote-debugging-port=9340"}, WebviewUserDataPath: ...}}`.

Observed: Wails clears the WebView2 variables
(`internal/webview2/webviewloader/env_create.go`,
`preventEnvAndRegistryOverrides()`), no CDP endpoint. With the option,
`/json/version` answers and `Runtime.evaluate` of a bound Go method
returns its result. Playwright itself was not run.

Consequence: the `e2e` build tag passes the flag through the options
(DECISIONS.md, approved by the user).

## E10 A non-admin interactive user can start and stop the service

- Result: confirmed
- Checked on: 2026-11-18
- By: check run by the user (elevated rights for register/cleanup)
- Status: valid

How it was checked: elevated `sc.exe create` + `sc.exe sdset <svc>
"D:(A;;CCLCSWRPWPLOCRRC;;;IU)(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)"`;
then non-elevated Go: `windows.OpenSCManager(nil, nil, SC_MANAGER_CONNECT)`,
`windows.OpenService(h, name, SERVICE_START|SERVICE_STOP|SERVICE_QUERY_STATUS)`,
`StartService`, `ControlService(STOP)`.

Observed: `StartService: <nil>`, state 4 (running); `ControlService(STOP):
<nil>`, state 1 (stopped). `mgr.Connect()` returns access denied;
`ALL_ACCESS` is always denied.

Consequence: tray and `hlab cloud start|stop` use the raw calls with minimal
rights, never `svc/mgr.Connect`.

## E11 Save, resume and shutdown of Linux guests; Windows shutdown order

- Result: confirmed
- Checked on: 2026-11-18
- By: experiment and documentation
- Status: valid

How it was checked: `Save-VM`/`Start-VM` with a running guest probe;
`Stop-VM -Force`; read `HKLM\SYSTEM\CurrentControlSet\Control\PreshutdownOrder`.

Observed: Save 0.9 s, resume 0.7 s, uptime continuous, appliance reaches the
guest again. `Stop-VM -Force` shuts down through the Shutdown integration
service (Ubuntu 6.4 s, Debian 7.6 s, appliance 2.2 s). `PreshutdownOrder`
starts with `vmms`; the default preshutdown timeout is 10 s
(`SERVICE_CONFIG_PRESHUTDOWN_INFO` raises it).

Consequence: per VM `AutomaticStopAction` = stop behaviour,
`AutomaticStartAction Nothing`; hlabd's preshutdown records state and tears
down host networking. A real Windows shutdown was not tested (LIFE-05 is
manual).

## E12 hcloud CLI v1.70.2 uses the Hetzner API under a path prefix over plain HTTP

- Result: confirmed
- Checked on: 2026-11-21
- By: experiment
- Status: valid

How it was checked: a Python fake server (standard library) answered every
path under `/hetzner/v1` with an example generated from the response
schemas of the frozen `hetzner.spec.json` (SHA-256 `3afa18f1...cb688`) and
logged each request; hcloud ran with `HCLOUD_ENDPOINT=http://127.0.0.1:8799/v1`,
`HETZNER_ENDPOINT=http://127.0.0.1:8799/hetzner/v1` and a dummy token:
`storage-box-type list`, `storage-box list|describe|create|enable-snapshot-plan|delete|folders|rollback-snapshot`,
`storage-box subaccount create`, `storage-box snapshot create`.

Observed: every request went to `/hetzner/v1/...` with the cloud token as
Bearer; lists use `page=1&per_page=50&sort=id:asc`. Waiting for an action
calls the Cloud API, `GET /v1/actions?id=<id>&...`; a 404 there fails the
command. The environment variable is `HETZNER_ENDPOINT` (config key
`hetzner_endpoint`, option `hetzner-endpoint`, flag `--hetzner-endpoint`);
a wrong name sends requests to the real `api.hetzner.com`, so tests point
`--config` at a file of their own.

Consequence: storage box actions live in the one action store with IDs
unique across both APIs and are returned by the Cloud API's action
endpoints (ARCHITECTURE.md "resource").

## E13 OpenTofu provider 1.70.2 ignores `endpoint_hetzner`; `HETZNER_ENDPOINT` works

- Result: refuted for the provider attribute, confirmed for the environment variable
- Checked on: 2026-11-21
- By: experiment and source reading
- Status: valid

How it was checked: the fake server of E12; provider block with `endpoint`
and `endpoint_hetzner`, data sources `hcloud_storage_box_types`,
`hcloud_storage_box` and resource `hcloud_storage_box`, `tofu apply` with
`TF_CLI_CONFIG_FILE=/dev/null`.

Observed: storage box requests went to the cloud endpoint (404). In
`hcloud/plugin_provider.go` (v1.70.2 and `main`) a set `endpoint_hetzner`
assigns `data.Endpoint` instead of `data.EndpointHetzner`; the storage box
resources live in that framework part. Without the attribute and with
`HETZNER_ENDPOINT` set, all requests went to `/hetzner/v1` and the apply
succeeded; the provider also waits through `GET /v1/actions?id=`.

Consequence: documentation and the CLI-06 tests set `HETZNER_ENDPOINT` for
OpenTofu (`decisions/storage-box.md`). The bug is worth reporting upstream.

## E14 `day_of_week` of a snapshot plan is 1 to 7 in the API

- Result: confirmed
- Checked on: 2026-11-21
- By: spec reading and experiment
- Status: valid

Observed: the frozen description gives `day_of_week` as integer or null,
1 Monday to 7 Sunday, null every day, in the box and in
`enable_snapshot_plan`. hcloud sends 7 for `sunday`. The provider maps its
own HCL convention (0 Sunday to 6 Saturday) to the API value. Subaccounts
also have a `name` field besides the username.

Consequence: hlab follows the API strictly and refuses 0.

## E15 Ansible hetzner.hcloud 7.2.0 uses `/hetzner/v1` and waits through the Cloud API

- Result: confirmed
- Checked on: 2026-11-21
- By: experiment
- Status: valid

How it was checked: the fake server of E12 inside WSL; ansible-core 2.22.0,
collection 7.2.0, hcloud-python 2.27.0; `module_defaults` with `api_token`,
`api_endpoint` and `api_endpoint_hetzner`; modules `storage_box_type_info`,
`storage_box_info`, `storage_box`, `storage_box_snapshot`.

Observed: all requests go to `/hetzner/v1/...` with the cloud token;
actions are polled with `GET /v1/actions/{id}` on the Cloud endpoint.
`HETZNER_ENDPOINT` alone gives the same result. `connection: local` needs
`ansible_python_interpreter` set to the uv tool's Python.

Consequence: the Cloud API returns storage box actions through both
`GET /v1/actions?id=` and `GET /v1/actions/{id}`.

## E16 The Windows SMB client talks to Samba without AD (NTLMv2, SMB 3.1.1)

- Result: confirmed from the Windows host; from WSL2 not checked (needs elevated rights)
- Checked on: 2026-11-21, Windows 10.0.26300
- By: experiment
- Status: valid

How it was checked: a throwaway Debian 13 VM (Samba 4.22.11) on the Default
Switch with two box IPs; `server role = standalone server`,
`bind interfaces only = yes`, `server min protocol = SMB2_10`,
`map to guest = never`, `restrict anonymous = 2`,
`include = /etc/samba/ip-%i.conf` with `[backup]` and a read-only
subaccount share per IP. From PowerShell without elevation:
`net use \\<ip>\backup <pw> /user:u100000 /persistent:no` and
`New-SmbMapping` for the subaccount share.

Observed: read, write and list work; the read-only share refuses writes;
`smbstatus -b` shows SMB3_11, signing AES-128-GMAC, NTLMSSP. A box's
credentials at another box's IP are refused. `smbclient -L` does not list
`%i` shares; tools without a client IP read `ip-0.0.0.0.conf` (create it
empty). UNC paths must be written to script files: Git Bash eats `\\`.

Consequence: no change. Risk: if a future Windows build blocks NTLM by
default, SMB from the host breaks (`docs/deviations.md`). SMB from WSL2 is
an assumption (DECISIONS.md) proven by the end-to-end tests of SBOX-04 and
SBOX-14.

## E17 One Debian 13 VM serves several boxes per IP over every protocol

- Result: confirmed
- Checked on: 2026-11-21
- By: experiment
- Status: valid

How it was checked: throwaway VM, Debian 13 (kernel 6.12.117), openssh
10.0p1, samba 4.22.11, apache2 2.4.71 with `libapache2-mpm-itk`, vsftpd
3.0.5, rsync 3.5.0, borgbackup 1.4.0. Box users `u100000`, `u100001`
(UID = number), subaccount `useradd -o -u 100000 -g u100000`, login shell
`/bin/sh`. Per account a root-owned chroot `/srv/chroot/<user>` with `/usr`
bind-mounted read-only, `bin`/`lib` links, `dev/{null,zero,urandom}`, `tmp`,
minimal `etc/passwd`/`group`, and `home` bind-mounted from the box data.
One sshd: `Match ... LocalPort 22` `internal-sftp -d /home` (`-R` read-only);
`LocalPort 23` a ForceCommand wrapper allowing only sftp-server, `rsync
--server` through `rrsync [-ro] /home`, `borg serve --restrict-to-path
/home` and scp. Apache: `Listen <ip>:443`, one vhost per box with SSL,
`Dav On`, basic auth from a file, `AssignUserID <box> <box>`. vsftpd: one
process, chroot to `/srv/chroot/$USER/home`, `pasv_min_port=50000`,
`pasv_max_port=50099`, explicit FTPS, per-user `write_enable=NO` for
read-only subaccounts. A routed hop (network namespaces, nftables policy
drop, accepting established/related and new TCP 21 and 50000-50099) for
passive FTP.

Observed: port 22 SFTP and scp in SFTP mode, no shell, legacy `scp -O`
fails (as at Hetzner); port 23 rsync, `scp -O`, sftp and borg work,
escapes are refused by rrsync and borg, other commands get "Command not
allowed"; read-only subaccounts write nothing over any protocol; WebDAV
PUT/MKCOL/PROPFIND work and files belong to the box's UID; FTP and FTPS
passive work through the hop without a conntrack helper, active mode does
not. Credentials of one box work over SSH and FTP at another box's IP
(landing in their own box); SMB refuses that. `nologin` breaks every
ForceCommand but `internal-sftp`; `sftp -b` needs `-o BatchMode=no` for
passwords; `fwd` is reserved in nftables.

Consequence: subaccounts share the box's UID; WebDAV needs mpm-itk (SPEC.md
"Stack"); the appliance allows TCP 50000-50099 to box IPs besides 21. A
rollback must unmount and rebind the chroot bind mounts of the box and its
subaccounts and end the box's open sessions, or users keep seeing the old
data. The scp branch of the wrapper must exec `scp -t|-f <path>` after
strict parsing, never `sh -c`.

## E18 btrfs qgroups limit a box including its snapshots; snapshots, rollback, `.zfs/snapshot`

- Result: confirmed, with rules
- Checked on: 2026-11-21
- By: experiment
- Status: valid

How it was checked: btrfs-progs 6.14 on a 20 GB dynamic data disk:
`btrfs quota enable`, per box a subvolume `home`, a level-1 qgroup
`1/<n>` with `qgroup assign 0/<home> 1/<n>` and `qgroup limit`; snapshots
with `btrfs subvolume snapshot -r -i 1/<n>`; `.zfs/snapshot` as a read-only
bind mount of the snapshot folder; rollback by `mv home home.old`, a
writable snapshot `-i 1/<n>` as new `home`, `subvolume delete home.old`,
`subvolume sync`, `qgroup clear-stale`.

Observed: the limit holds and counts snapshot data (write after snapshot
and delete fails with "Disk quota exceeded"); snapshots are read-only;
rollback restores data and the new `home` is limited again; `.zfs/snapshot`
is visible and read-only over SFTP and SMB; snapshots taken with the bind
mount in place do not nest. In a first run the accounting turned
"inconsistent" (after assigning a snapshot later and `clear-stale` while a
subvolume was still being deleted) and a write passed the limit.

Consequence: quota on `1/<n>`, never on the subvolume; snapshots only with
`-i 1/<n>`; `subvolume sync` before `clear-stale`; after each job check
`qgroup show` for "inconsistent" and run `quota rescan -w`. Snapshots live
outside the chroot (`/srv/sbox/.snapshots/<user>/<name>`). `stats` and the
change_type check come from `btrfs qgroup show --raw`.

## E19 One stored password works for every service without the plain text

- Result: confirmed
- Checked on: 2026-11-21
- By: experiment
- Status: valid

How it was checked: sha512-crypt (`$6$`) set with `usermod -p` (sshd,
vsftpd through PAM) and in `htpasswd` lines (Apache falls back to crypt(3));
the NT hash (MD4 over UTF-16LE) set with `pdbedit -a -t` and
`pdbedit --set-nt-hash`.

Observed: SSH, SFTP, rsync, Borg, FTP/FTPS, WebDAV and SMB (Windows and
smbclient) all accept the same password; `pdbedit -L -w` shows the
computed NT hash.

Consequence: hlabd computes both hashes and the storage document carries
them per account; the NT hash is unsalted and never logged or returned.
yescrypt (`$y$`) was not tested; stay with `$6$`.

## E20 hcloud 1.70.2 runs every storage box command against hlab unchanged

- Result: confirmed (three client-side quirks)
- Checked on: 2026-11-23
- By: experiment
- Status: valid

How it was checked: hcloud 1.70.2 with context `hlab-docs` (`endpoint`
`http://api.hetzner.internal/v1`, `hetzner-endpoint`
`http://api.hetzner.internal/hetzner/v1`) against the dev cloud, with
`HCLOUD_TOKEN`, `HCLOUD_ENDPOINT` and `HCLOUD_CONTEXT` cleared; a 68-step pass
over every command under `storage-box`, `storage-box subaccount`,
`storage-box snapshot` and `storage-box-type`, plus
`hcloud all list --debug --debug-file <tmp>` (the debug file holds the token
and passwords; write it outside the repo and delete it).

Observed: every command returns rc 0; refusals match Hetzner (protected
delete: `the resource is protected (protected)`; `home_directory` with `..`:
422 `invalid_input`). `all list` sends 14 requests, all to
`api.hetzner.internal`, storage boxes via `/hetzner/v1/storage_boxes`; its
`-o json` is one object of arrays incl. `storage_boxes`. hcloud waits for
storage box actions with `GET /v1/actions?id=<n>` on the Cloud endpoint.
`create -o json` prints the create response (`status` `initializing`, empty
`username`/`server`); read them with a later `describe`. Subaccounts have no
label or protection commands; labels only at create. Timings: first box
create 56 s (storage VM start), rollback 9-24 s, snapshot delete ~9 s,
other actions ~2 s.

Client quirks (no hlab change): hcloud resolves a subaccount argument by ID
or `?name=`, never by username; `describe -o json` prints Sunday as
`day_of_week` 0 (Go `time.Weekday`) while the API holds 7; rollback deletes
newer snapshots (by design, SBOX-18). In Git Bash, arguments starting with
`/` are rewritten unless `MSYS_NO_PATHCONV=1`. `hcloud api` needs
`--endpoint .../hetzner/v1` for storage box paths.

Consequence: CLI-05 rows address subaccounts by name or ID, do not check
Sunday in JSON output, and expect newer snapshots gone after a rollback.

## E21 Graphical server console through Hyper-V WMI: frame rate, key delay, CPU

- Result: frame rate and CPU confirmed (gate passed); key delay unclear
  (estimated well below the limit, direct measurement planned for the start
  of phase 23)
- Checked on: 2026-11-23, Hyper-V on 10.0.26300, AMD Ryzen AI Max+ 395
  (16 cores / 32 threads), while the e2e suite ran other VMs
- By: experiment (read-only WMI calls against running hlab-dev VMs; no key
  was sent to any VM)
- Status: valid for frames and CPU; key delay planned

Gate (`decisions/server-console.md`): graphical only with about 5 frames/s
at 1024x768, a key press visible within about 0.5 s and at most one CPU core
of hlabd per open console.

How it was checked: a Go 1.27.4 program (non-elevated, user in Hyper-V
Administrators) in its own module with `github.com/go-ole/go-ole` v1.3.0 and
`golang.org/x/sys` v0.50.0, against `hlab-dev-server-687` (Ubuntu 24.04 at
the tty1 login prompt, `Msvm_VideoHead` 1024x768, 32 bpp). Lookups:
`Msvm_ComputerSystem WHERE ElementName=<vm>` gives `Name` (GUID);
`Msvm_VirtualSystemSettingData WHERE VirtualSystemIdentifier=<guid> AND
VirtualSystemType='Microsoft:Hyper-V:System:Realized'`;
`Msvm_VideoHead WHERE SystemName=<guid>`;
`Msvm_Keyboard WHERE SystemName=<guid>`. Frame call: `ExecMethod` on the
`Msvm_VirtualSystemManagementService` path, method
`GetVirtualSystemThumbnailImage`, in-parameters `TargetSystem` = VSSD path
(BSTR), `WidthPixels`/`HeightPixels` (VT_I4); one in-parameter object is
reused for every frame. Frames polled back to back, or with a 200 ms start
interval for the 5 frames/s cap. CPU from the localized counter
`\Prozess(*)\Prozessorzeit (%)` (English `\Process(*)\% Processor Time`),
sampled every 2 s for 24 s; readable without elevation, while the CPU of
`vmms.exe` in `Get-Process` is not.

Observed (1024x768 unless stated):

| Run | frames/s | ms per frame p50 / p95 | probe process | vmms.exe | vmwp.exe (all) |
|---|---|---|---|---|---|
| idle baseline | - | - | - | 0.11 cores | 0.02 cores |
| 1 console, uncapped | 7.3-7.7 | 126-133 / 145-151 | 0.03 cores | 0.96 cores | 0.09 cores |
| 1 console, capped at 5/s | 5.0 | 131 / 147 | 0.03 cores | 0.69 cores | 0.07 cores |
| 3 consoles at once, uncapped | 5.3-5.6 each | 178-186 / 197-203 | 0.07 cores (all three) | 2.73 cores | 0.16 cores |
| 1 console, 640x480 | 21.7 | 45 / 53 | - | - | - |

- The frame is RGB565 little endian (`r = v>>11`, `g = v>>5&63`,
  `b = v&31`), 1024*768*2 + 4 bytes: four trailing zero bytes. The decoded
  frame showed the real login screen.
- `Msvm_Keyboard.IsKeyPressed` (read-only, same call path as `TypeKey`):
  4.2-4.8 ms p50, max 6 ms.
- The cost is CPU in `vmms.exe` (about 0.13 core-seconds per frame,
  roughly proportional to the requested pixel count), not in the caller.
  The calling process (the later hlabd) stays near 0.03 cores per console.

Key delay (not measured, no input was sent to dev VMs): estimate = `TypeKey`
call (~5 ms) + guest echo (ms) + wait for the next frame (at most one frame
interval, 200 ms at 5/s) + one capture (~130 ms) + a raw frame over the
loopback (<20 ms), about 0.15-0.35 s, below the 0.5 s limit.

Pitfalls:
- Do not use the WbemScripting automation layer (`SWbemLocator`, `oleutil`
  on `SWbemObject`) for frames: it returns `uint8[]` as a SAFEARRAY of
  VARIANTs (VT 12); building and freeing 1.5 million VARIANTs per frame cost
  330 ms and 0.7-0.8 cores in the caller (measured 1.6-2.1 frames/s). Use the
  raw `IWbemServices` / `IWbemClassObject` vtables:
  `CoCreateInstance(CLSID_WbemLocator {4590F811-1D3A-11D0-891F-00AA004B2E24},
  CLSCTX_INPROC_SERVER, IID_IWbemLocator {DC12A687-737F-11CF-884D-00AA004B2E24})`,
  `IWbemLocator.ConnectServer` (vtable index 3),
  `CoSetProxyBlanket(svc, 10, 0, nil, 3, 3, nil, 0)`, IWbemServices
  `GetObject` 6 / `ExecMethod` 24, IWbemClassObject `Get` 4 / `Put` 5 /
  `SpawnInstance` 15 / `GetMethod` 19. `ImageData` then is VT_ARRAY|VT_UI1
  and is copied with `SafeArrayAccessData` in one step; `VariantClear` it
  and release the out-parameter object every frame.
- go-ole's `SafeArrayConversion.ToByteArray` calls `SafeArrayGetElement`
  per byte; equally unusable for frames.
- Parallel consoles share vmms; with three, each drops to about 5.5
  frames/s. Cap the poll rate (5/s), poll only while a client is connected,
  and request a frame right after a key or pointer event instead of waiting
  for the timer.
- The dev host's CPU baseline jumps by several cores while the e2e suite
  runs; per-process counters are needed, a system-wide idle delta is too
  noisy.

Planned for the start of phase 23 (key delay, needs input to a VM): on a
test VM of the phase at a text login prompt (not a dev-service VM in use),
call `Msvm_Keyboard.TypeKey(0x41)` through the raw vtable path five times.
Before each press, record every screen hash seen during 1.5 s (the blinking
cursor gives two states); after the press, poll frames back to back until a
hash appears that was not seen before; then send Backspace (0x08). Pass: the
median of the five delays is at most 0.5 s minus one frame interval at the
chosen cap (about 0.3 s at 5 frames/s, since the check polls uncapped), and
`TypeKey` returns within 100 ms. Repeat the 5 frames/s run of the table on
the same VM: pass if it reaches 5 frames/s at 1024x768 and the probe plus
`vmms.exe` stay at or below one core. Record the values here.

Consequence: the console is graphical (alternative (a)) unless the key
check fails. hlabd's console source calls WMI through raw COM vtables
(go-ole v1.3.0 is already an indirect dependency; `golang.org/x/sys/windows`
alone suffices), caps polling at 5 frames/s, and diffs frames in Go before
sending. Most CPU of one console is in `vmms.exe` (about 0.7 cores at 5/s);
the documentation should say so.

## E22 The tray's WebView2 connects to a loopback WebSocket and runs noVNC 1.7.0

- Result: confirmed
- Checked on: 2026-11-23
- By: experiment
- Status: valid

How it was checked: a minimal Wails v3.1.0 app (`application.New`,
`AssetFileServerFS`, one window) with a Go listener on `127.0.0.1:0` using
`github.com/coder/websocket` v1.8.14 (`websocket.Accept` with
`OriginPatterns: []string{"wails.localhost"}`, then
`websocket.NetConn(ctx, c, websocket.MessageBinary)`). First a plain page
(`new WebSocket("ws://127.0.0.1:<port>/<token>")`, `binaryType =
"arraybuffer"`, binary round trip); then a Svelte 5.58.0 page built by Vite
8.3.8 with the project's `vite.config.ts` (no `build.target`) that does
`import RFB from "@novnc/novnc"` (1.7.0) and `new RFB(el,
"ws://127.0.0.1:<port>/tok", { credentials: { password } })`; the Go side
spoke the RFB 3.8 handshake with VNC Authentication.

Observed: WebView2 `Edg/157.0.0.0`; request `Origin: http://wails.localhost`;
binary bytes round-trip unchanged; noVNC sends no `Sec-WebSocket-Protocol`,
answers `RFB 003.008\n`, picks security type 2, and its response equals
DES-ECB of the challenge with the 8-byte password whose key bytes are
bit-reversed (Go `crypto/des`). No mixed-content block and no local network
prompt. `vite build` succeeds with the default target (211 kB, 66 kB gzip);
the top-level await of noVNC's `core/util/browser.js` stays in the bundle
and runs in WebView2. `svelte-check --fail-on-warnings` fails with TS7016
("Could not find a declaration file for module '@novnc/novnc'"); a local
`src/novnc.d.ts` with `declare module "@novnc/novnc" { export default class
RFB extends EventTarget { ... } }` fixes it (`@types/novnc__novnc` exists
only for 1.6.0).

Pitfalls: Wails creates a WebView2 data folder `%APPDATA%\<exe name>`
(delete it after an experiment); the page origin on Windows is
`http://wails.localhost`, which must be in `OriginPatterns` (coder/websocket
rejects foreign origins by default); the server must not require a
subprotocol.

Consequence: the tray's loopback ws-to-wss proxy works as planned; the
frontend needs a noVNC type declaration file; no Vite target change. Loading
noVNC with a dynamic `import()` in the console window keeps its top-level
await out of the main window's start.

## E23 Port 443 on the appliance address 198.19.0.1 is free

- Result: confirmed
- Checked on: 2026-11-23
- By: code reading of `internal/agent` (not run)
- Status: valid

Observed: in the root namespace the agent binds `Config.API.Listen`
(`198.19.0.1:80`), DNS on `198.19.0.1:53` and `185.12.64.1/2:53`, and the
metadata service `169.254.169.254:80` in the VRFs. HAProxy binds `:<port>`
(also 443) only inside the per-load-balancer netns `lb<ID>`. Port 443 in
`firewall_linux.go` is the storage boxes' WebDAV, filtered by the boxes'
own routed /32 addresses. The nftables `input` chain of `inet hlab` has
policy accept and only drops metadata spoofing. The DNS zone
`hetzner.internal` holds only `api.hetzner.internal` (from
`Config.API.Listen`) and storage box names.

Consequence: the console forward can listen on `198.19.0.1:443`; the
agent's DNS needs a new A record for `console.hetzner.internal` (from
`Config.Console`), as ARCHITECTURE.md already says.

## E24 The tray opens, focuses and closes a second window and Playwright drives it

- Result: confirmed
- Checked on: 2026-11-23
- By: experiment (phase 23), Wails v3.1.0, WebView2 157, playwright-core 1.65.1, noVNC 1.7.0
- Status: valid

Observed: a bound service creates a `WebviewWindow` at runtime with
`URL: "/#/console/<id>"` and a title; the hash route loads from the bundled
asset server and noVNC connects. A second `Open` shows, restores and
focuses the existing window. `Close()` and the window's X close it for real
(page gone in under 50 ms, RFB connection ends); the main window's
`WindowClosing` hide hook is per window and still only hides the main
window. Every window builds its own WebView2 environment from the
app-wide `AdditionalBrowserArgs`/`WebviewUserDataPath` of the e2e build
without error. Playwright `connectOverCDP` sees the console page in the
single context, reads canvas pixels with `getImageData` and sends keys,
`Control+Alt+Delete` included, which reach the RFB server as keysyms.
noVNC sends `SetPixelFormat` 32 bpp, little endian, red shift 0, blue
shift 16. Pitfalls: `Close()` is asynchronous and `GetByName` returns the
first match, so the service keeps its own window map under a mutex and
clears it in the window's `WindowClosing` listener; the new page appears
first as `about:blank`, so tests wait for a page whose URL holds
`#/console/<id>`; one run of six hung after `app.Run()` returned, so the
launcher stops the tray hard after a deadline. Not tested: focusing from a
pure background state (Windows may refuse `SetForegroundWindow`).

Repeat: a Wails v3.1.0 probe with a hide-on-close main window and a bound
`Console` service (`Open`, `Close`), a loopback RFB 3.8 server with a
two-colour 320x240 screen that logs KeyEvents, and a Node script that
attaches over CDP, opens, focuses and closes the windows and reads pixels.

## E25 Hyper-V console input, frames in every VM state, and guest input devices

- Result: confirmed (key delay gate passed; two WMI methods unusable)
- Checked on: 2026-11-23
- By: experiment (phase 23) on Windows 11 Pro 10.0.26300, Ubuntu 24.04.5 cloud image (kernel 6.8.0-147), Go raw WMI vtables as in E21, no elevation
- Status: valid

Observed:
- Key delay: `Msvm_Keyboard.TypeKey(0x41)` at the tty1 login prompt was
  visible after a median of 135 ms (max 179 ms) when frames are grabbed
  right after the input; with frames only from a 200 ms timer the median
  was 258 ms (max 311 ms). One input call takes about 5 ms (first call in
  a process about 40 ms).
- 5 frames/s at 1024x768, three runs: 5.01 frames/s, p95 144 ms per frame,
  no errors, caller 0.01-0.02 cores, `vmms.exe` 0.59-0.61 cores (max 0.81),
  idle 0.03.
- Keyboard: `PressKey`/`ReleaseKey`/`TypeKey(UInt32 KeyCode)` with held
  Shift, Ctrl and Alt (left and right VKs) arrive in order. VK_HOME 0x24
  never sends its key-up (the guest keeps HOME held); `TypeText` produces
  garbage; VK_OEM_102, VK_CLEAR and 0x15 return 32773. Hyper-V emulates no
  autorepeat. `TypeScancodes(UInt8Array Scancodes)` takes scan code set 1:
  break = make | 0x80, the 0xE0 prefix precedes make and break, make and
  break may go in separate calls; it releases HOME correctly.
- Mouse: `SetAbsolutePosition(SInt32, SInt32)` takes pixels of the current
  resolution (clamped above, negative values rejected with 32773);
  `SetButtonState(UInt32 ButtonIndex, Boolean IsDown)` index 1 left,
  2 right, 3 middle; `SetScrollPosition(SInt32)` in steps of 120 (+120
  wheel up), smaller deltas do nothing. Each call 6-8 ms.
- `GetVirtualSystemThumbnailImage`: ReturnValue 0 with a frame while
  running and when saved (the last screen); 32775 while off, starting,
  shutting down or saving; 32779 while restoring; never a job. After a
  guest resolution change (1024x768 to 1280x800, `Msvm_VideoHead`
  changes) a request at the old size returns a scaled image and one at the
  new size w·h·2+4 bytes.
- Guest: the stock image loads `hid_hyperv` and `hyperv_keyboard`;
  "Microsoft Vmbus HID-compliant Mouse" (ABS 0..32767, wheel) and
  "AT Translated Set 2 keyboard" appear in `/proc/bus/input/devices` with
  `/dev/input/event*` readable by root over SSH.
- Invalid values come back as ReturnValue 32773 with HRESULT 0; `UInt8Array`
  parameters are built as VT_ARRAY|VT_UI1.

Consequence: the graphical console passes the gate (decisions/server-console.md).
Keys go through `TypeScancodes` with a keysym → set-1 table (down = make,
up = break, client autorepeat forwarded as further makes), never
`TypeText`, and HOME never through a VK. Frames are grabbed right after
an input event as well as on the poll timer. A non-zero thumbnail
ReturnValue means "no picture now": keep the connection, show the last or
a black frame. A changed `Msvm_VideoHead` triggers a DesktopSize update.
RFB button bits 0/1/2 map to Hyper-V indexes 1/3/2, buttons 4/5 to scroll
±120. The e2e test finds the guest input devices by name in
`/proc/bus/input/devices`.

Repeat: a throwaway project and an Ubuntu 24.04 cx23 server in the dev
service; a Go probe that calls the methods above through raw WMI vtables
and hashes frames until one changes; a root `python3` reader of
`/dev/input/event*` over SSH; `Get-Counter` on `vmms.exe`; `Save-VM`,
`Start-VM` and `hcloud server poweroff/poweron` for the VM states.

## E26 Pause, save and hard stop of a paused Hyper-V VM

- Result: confirmed (`Save-VM` and `Stop-VM -TurnOff` both accept a paused VM)
- Checked on: 2026-11-24, Hyper-V on 10.0.26300, Debian 13 server (kernel 6.12.117+deb13-amd64) of a throwaway project in the dev service, no elevation
- By: experiment
- Status: valid

How it was checked: the PowerShell cmdlets the platform uses
(`internal/platform/hyperv`), state read from `Get-VM` and
`Msvm_ComputerSystem.EnabledState`; a guest loop writing `date +%s.%N`
every 0.2 s.

```
Suspend-VM -Name $n                      # 0.46 s; State Paused, EnabledState 9
Suspend-VM -Name $n                      # again: warning "already in this state", no error
Save-VM -Name $n                         # on Paused: 1.35 s, State Saved (EnabledState 6)
Resume-VM -Name $n                       # on Saved or Off: error "cannot be performed in the current state"
Start-VM -Name $n                        # from Saved: 0.77 s, Running
Stop-VM -Name $n -TurnOff -Force         # on Paused: 0.22 s, State Off (EnabledState 3)
Stop-VM -Name $n -Force                  # graceful, on Paused: returns without error, State Off
```

Observed: a 30 s pause left a 30.4 s gap in the guest's tick log; after
`Resume-VM` the guest clock was right at once (no step needed), no
soft-lockup or RCU warning in the kernel log. After Paused, Save, Start
the guest time matched the host within 1 s.

Consequence: `server_crash` on a frozen server can run `PowerStop`
directly, and `PowerSave` (cloud stop with stop behaviour "save") works on
paused VMs; neither needs `Resume-VM` first. `PowerResume` works only on a
Paused VM (it fails on Saved and Off), so ending a freeze checks the state
first. Paused is `EnabledState` 9 on this host (not 32768 as
`research/fault-injection.md` says); the platform already maps 9 and 32788.

## E27 Hyper-V `MaximumIOPS` applies live on Windows 11 client Hyper-V

- Result: confirmed
- Checked on: 2026-11-24, same VM as E26, no elevation (member of Hyper-V Administrators)
- By: experiment
- Status: valid

How it was checked: in the running guest
`dd if=/dev/zero of=<file> bs=8k count=1000 oflag=direct` on the volume
(LUN 1) and on the system disk (LUN 0) before, with and after the limit.

```
Set-VMHardDiskDrive -VMName $n -ControllerType SCSI -ControllerNumber 0 -ControllerLocation 1 -MaximumIOPS 100   # 0.71 s
Set-VMHardDiskDrive -VMName $n -ControllerType SCSI -ControllerNumber 0 -ControllerLocation 0 -MaximumIOPS 200
Get-VMHardDiskDrive -VMName $n | Set-VMHardDiskDrive -MaximumIOPS 0                                              # removes both
```

Observed: volume 0.16 s (51.7 MB/s) unlimited, 9.2 s (892 kB/s) at 100;
system disk 0.12 s unlimited, 4.0 s at 200; 200 writes of 64 KiB at 100
took 15.1 s (8 KiB normalisation: 1600 / 100 = 16 s expected); 500 direct
8 KiB reads at 100 took 4.9 s (reads are limited too); after
`-MaximumIOPS 0` back to 0.13 s. The VM stayed Running throughout.

Consequence: `SetDiskIOPS` as planned; the limit takes effect within a
second without any guest action. The first second runs slightly above the
limit (about 110 IOPS at 100). The unit is 8 KiB I/Os, so UI and
documentation should say so.

## E28 Hot-removing and re-adding a data VHDX on a running Debian 13 guest

- Result: confirmed with a correction: what the guest sees depends on whether the filesystem is busy
- Checked on: 2026-11-24, same VM as E26, ext4 volume mounted by systemd from `/etc/fstab` (`/dev/disk/by-id/scsi-0HC_Volume_<id>`, `nofail`)
- By: experiment
- Status: superseded (phase 26 observed that an idle volume with `automount` is mounted again by hlab's udev rule once the disk is re-added; "Nothing was remounted automatically" holds only for a plain fstab mount as used here, and a busy volume still needs the remount, see ARCHITECTURE.md; 2026-11-24)

How it was checked: a guest loop appending to a file on the volume, then

```
Get-VMHardDiskDrive -VMName $n -ControllerType SCSI -ControllerNumber 0 -ControllerLocation 1 | Remove-VMHardDiskDrive   # 0.95 s, as the user
Add-VMHardDiskDrive -VMName $n -ControllerType SCSI -ControllerNumber 0 -ControllerLocation 1 -Path <volume vhdx>
#   as the user: AccessDenied 0x80070005 "no permission to open the attachment"
# re-add therefore through hlabd (LocalSystem): hcloud volume detach, hcloud volume attach
```

Observed:
- Not busy (the loop opened and closed the file per write): the kernel
  logged `device offline error` and `Aborting journal`, then systemd
  unmounted the filesystem by itself (the fstab mount unit is bound to the
  device). Later writes to the mount point succeeded silently on the root
  disk (`rc=0`), not with an I/O error.
- Busy (a process with an open file and its working directory on the
  volume): unmount failed, the filesystem stayed mounted on the dead
  device, `EXT4-fs (sdb): shut down requested`, every access returned
  `Input/output error`.
- Re-adding at the same LUN: the guest registered the disk within about
  10 s, as `sdb` again when the old one was unmounted, else as the next
  free name `sdc`. `/dev/disk/by-id/scsi-0HC_Volume_<id>` pointed to the
  new device in both cases. Nothing was remounted automatically.
  `mount /mnt/HC_Volume_<id>` worked at once, ext4 replayed its journal
  ("recovery complete"), data written before the removal was there. In the
  busy case the new mount stacks on the dead one, which goes away only
  after its users end (`umount -l` or ending the processes).
- `Remove-VMHardDiskDrive` deletes the VM's own access entry on the VHDX;
  `Add-VMHardDiskDrive` then needs a caller with write access to the file
  (hlabd as LocalSystem has it; the interactive user only has read on
  `C:\ProgramData\hlab-dev\data\volumes`). hlab's `DetachDisk` of a disk
  already missing on the VM completed without error.

Consequence: `volume_unavailable` works as planned; `Restore` re-adds at
the same LUN and the by-id name stays right. The user documentation must
say that the guest either loses the mount (writes then land on the system
disk under the empty mount point) or keeps a dead mount with I/O errors,
and that after the fault the volume is mounted again (`umount -l` first in
the busy case). Tests of this fault run the platform calls through hlabd,
not as the interactive user.

## E29 netem with seed, tc on VRF interfaces and an extra nftables forward table on Debian 13

- Result: confirmed, with corrections (the forward hook sees the VRF device as `iifname`; the agent's netlink library has no netem seed)
- Checked on: 2026-11-24, Debian 13 VM kernel 6.12.117+deb13-amd64 (Debian `generic` image like the appliance), iproute2 6.15.0, nftables 1.1.3 (installed in the throwaway VM; the agent itself uses google/nftables v0.3.0 and vishvananda/netlink v1.3.1)
- By: experiment
- Status: valid

How it was checked: network namespaces in the VM modelled on the
appliance: router `r` with `p0` in the main table (public) and `t20`
enslaved to `vrf20` (table 1020, private); two hosts behind each on a
bridge, each with a `/32` and an on-link gateway, so host-to-host traffic
enters and leaves the same router interface; `send_redirects=0`. Loss
measured as the missing `icmp_seq` of `ping -c 200 -i 0.005`.

```
tc qdisc replace dev p0 root netem loss random 10% seed 42
tc qdisc add dev t20 root handle 1: htb default 1
tc class add dev t20 parent 1: classid 1:1 htb rate 10gbit
tc class add dev t20 parent 1: classid 1:10 htb rate 10gbit
tc qdisc add dev t20 parent 1:10 handle 10: netem loss random 10% seed 42      # or: delay 50ms 10ms limit 100000
tc filter add dev t20 parent 1: protocol ip prio 1 flower dst_ip 10.0.0.7 classid 1:10
tc filter add dev t20 parent 1: protocol ip prio 2 flower src_ip 10.0.0.7 classid 1:10
# prio variant: root handle 1: prio bands 3 priomap 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1, netem on parent 1:3
table inet hlab_fault { chain fw_chain { type filter hook forward priority -10; policy accept;
  ip saddr 10.0.0.5 ip daddr 10.0.0.7 numgen inc mod 10 { 2, 5 } counter drop;
  ip saddr { 198.19.4.10 } ip daddr { 198.19.4.20 } meta mark set 0x10 counter; } }
tc filter add dev p0 parent 1: protocol ip prio 1 handle 0x10 fw classid 1:10   # mark from forward -> netem child
```

Observed:
- Seed: with the netem child behind flower filters for exactly one flow,
  every run with seed 42 lost the same 41 packets (7 14 19 20 ... 193), on
  HTB and prio parents, on `p0` and `t20`, and after a reboot of the VM.
  Seed 43 and no seed gave other patterns. As root qdisc on `p0` (which
  also sees ARP and IPv6 neighbour traffic) runs agreed only for the first
  ~90 packets, then shifted by one. Without `seed`, `tc qdisc show`
  reports the seed the kernel chose. Seeds up to 2^64-1 are accepted. A
  fixed delay in the same netem leaves the loss pattern unchanged; delay
  with jitter changes it (jitter draws from the same generator), and it is
  again identical run to run.
- Latency: `delay 50ms 10ms` on egress of both router interfaces, filtered
  on the server's address (public) or the address pair (VRF): RTT
  86-114 ms, avg 100 ms, public and private alike; other traffic unchanged
  (0.1 ms); deleting the root qdisc restored 0.1 ms. Flower filters on a
  VRF slave work normally.
- nftables: `inet hlab` (forward priority 0, prerouting), `inet hlab_fw`
  (forward priority 10, `ct state established,related accept`) and
  `inet hlab_fault` (forward priority -10) load and work together;
  deleting `hlab_fault` leaves the others intact. The forward chain ran
  once per packet (36 counts for 20 requests and 16 replies). For VRF
  traffic the forward hook sees `iifname "vrf20"` (not `t20`), `oifname
  "t20"` and `meta sdifname "t20"`; prerouting runs twice (with `t20` and
  with `vrf20`). `numgen inc` after the address match dropped exactly
  packets 3, 6, 13, 16. A mark set in the forward hook is seen by a tc
  `fw` filter on egress (80 ms added).
- Offloads: `hv_netvsc` has GRO, GSO, TSO and LRO on (LRO is the
  vSwitch's receive coalescing); all four switch off with
  `ethtool -K eth0 gro off gso off tso off lro off` and back on; VLAN
  devices accept `gro off` too. The kernel logs "htb: netem qdisc 10: is
  non-work-conserving?" when netem sits under HTB (harmless; none with
  prio); HTB at 10gbit warns about a big quantum (harmless, set `quantum`).
- Syntax: netem has no `jitter` keyword (`delay 50ms 10ms`); `tc qdisc
  replace` resets every parameter not given (`limit` back to 1000).
- Libraries: vishvananda/netlink v1.3.1 `Netem` has no seed field (no
  `TCA_NETEM_PRNG_SEED`) and no delay distribution; `Flower`, `Fw`, HTB and
  prio are there. google/nftables v0.3.0 has `expr.Numgen`.

Consequence: per-fault netem children selected by flower filters (or by a
mark set in `hlab_fault`) give repeatable loss for the same packet
sequence; the agent sets the seed through `tc` (iproute2 is in the
appliance image) or adds the raw seed attribute itself. Rules in
`hlab_fault` for private traffic match `oifname`/`meta sdifname t<vlan>`
or `iifname vrf<vlan>` together with the address, because private ranges
of different projects may overlap. A flow reaches only one netem child, so
a flow hit by a loss and a latency fault needs one netem with both
settings. GRO, GSO, TSO and LRO go off on the appliance's physical
adapters (public and trunk), not only on VLAN subinterfaces. Forward-hook
rules do not touch traffic between a server and the appliance's own
services (DNS, metadata, API proxy, console); a cut that should stop those
too needs input and output chains.

Repeat: a throwaway project with a Debian 13 cx23 server and a 10 GB ext4
volume in the dev service (`hcloud` with
`HCLOUD_ENDPOINT=http://api.hetzner.internal/v1` and a project token), SSH
as root; the namespace topology above built with `ip netns` and
`ip link add ... type veth|bridge|vrf`; `apt-get install nftables` inside
the VM only for the nft CLI.

## E30 Download, checksum and qcow2 conversion of the EL, Fedora and openSUSE cloud images through hlab's pipeline

- Result: confirmed for conversion; refuted for the checksum parser (BSD format) and partly for downloads (a Fedora mirror blocks Go's User-Agent)
- Checked on: 2026-11-24, Windows 11 Pro 26300, Hyper-V, Go 1.27.4, go-qcow2reader v0.8.0, hlab `internal/images` and `hyperv.ConvertToDynamic` at commit b3441e9
- By: experiment
- Status: valid

How it was checked: a small `package main` inside the module
(`.project/research/experiments/images/conv`, gitignored) builds
`images.New(images.Options{Catalog: {name: {URL, ChecksumURL, Format:
"qcow2"}}, Convert: hyperv.New(...).ConvertToDynamic})` and calls `Ensure`;
a second pass served local GNU-format copies of the BSD checksum files
through a `file` transport. The qcow2 headers were read from the first 112
bytes (HTTP range request).

```
ensure rocky-10: err=images: rocky-10: checksum file .../Rocky-10-GenericCloud-Base.latest.x86_64.qcow2.CHECKSUM: no entry for Rocky-10-GenericCloud-Base.latest.x86_64.qcow2
ensure fedora-44: err=... GET https://download.fedoraproject.org/.../Fedora-Cloud-44-1.7-x86_64-CHECKSUM: 403 Forbidden
ensure alma-10: base=...\alma-10.vhdx err=<nil> (1m32s)
curl -A Go-http-client/1.1 https://mirror.dogado.de/fedora/.../Fedora-Cloud-44-1.7-x86_64-CHECKSUM -> 403; -A hlab/0.1 -> 200
```

Observed:
- Checksum files: GNU format (`hash  name`, works today) for Alma (one
  `CHECKSUM` per folder, listing the `-latest` names) and openSUSE
  (`.sha256`); BSD format (`SHA256 (name) = hash`, fails in
  `images.findSum`) for Rocky `.CHECKSUM`, CentOS Stream `.SHA256SUM` and
  Fedora `-CHECKSUM` (PGP clear-signed, with `#` lines). With GNU copies
  all 11 images downloaded, verified and converted.
- qcow2: all 11 are version 3, 64 KiB clusters, incompatible features 0
  (no extended L2, no external data file, no zstd, no backing file);
  go-qcow2reader v0.8.0 needs no change. Virtual sizes 10 GiB (Rocky,
  Alma, CentOS Stream), 5 GiB (Fedora), 1.43 GiB (openSUSE); conversion
  1.5 to 5 min per image.
- Downloads: `download.fedoraproject.org` redirects to a random mirror;
  `mirror.dogado.de` answers 403 to Go's default User-Agent and 200 to any
  other; one of 11 pipeline runs failed this way.
- Sources (2026-11-24): Rocky
  `https://dl.rockylinux.org/pub/rocky/<8|9|10>/images/x86_64/Rocky-<v>-GenericCloud-Base.latest.x86_64.qcow2`
  with `<same>.CHECKSUM`; Alma
  `https://repo.almalinux.org/almalinux/<8|9|10>/cloud/x86_64/images/AlmaLinux-<v>-GenericCloud-latest.x86_64.qcow2`
  with `CHECKSUM` in the folder; CentOS Stream
  `https://cloud.centos.org/centos/<9|10>-stream/x86_64/images/CentOS-Stream-GenericCloud-<v>-latest.x86_64.qcow2`
  with `<same>.SHA256SUM`; Fedora
  `https://download.fedoraproject.org/pub/fedora/linux/releases/<43|44>/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-<43-1.6|44-1.7>.x86_64.qcow2`
  with `Fedora-Cloud-<43-1.6|44-1.7>-x86_64-CHECKSUM`; openSUSE
  `https://download.opensuse.org/distribution/leap/16.0/appliances/Leap-16.0-Minimal-VM.x86_64-Cloud.qcow2`
  with `<same>.sha256`. Rocky, Alma, CentOS Stream and openSUSE prune old
  builds, so their `latest` file and the current checksum file are used;
  Fedora's release tree is frozen.
- Real API values (`hcloud image list --type system -a x86`, read-only):
  rocky-8 45780948, centos-stream-9 59752342 (os_version "Stream 9"),
  rocky-9 76766499, alma-8 104788983, alma-9 104788996, centos-stream-10
  210163277 ("Stream 10"), alma-10 245249013, rocky-10 245249370,
  fedora-43 342035601 (deprecation announced 2026-11-12, unavailable_after
  2027-02-09T00:00:00Z), opensuse-16 342262088, fedora-44 387893188; all
  disk_size 5, rapid_deploy true; created dates and descriptions from the
  same call (kept in `experiments/images/real-images.json`).

Method of E31-E34: each server was created through the dev API from
debian-12 with `start_after_create: false` (cx23, fsn1, private network
attached at create, user_data with `write_files` and `runcmd` markers,
with or without an SSH key); its system disk was then replaced by a copy
of the converted base (`Resize-VHD` to 40 GB, `Set-VMHardDiskDrive` at
SCSI 0/0), so the VM is otherwise hlab's own (Gen2, Secure Boot template
MicrosoftUEFICertificateAuthority, COM1, hlab's cidata). After poweron:
SSH and a check script, `reset_password` and a password login, a 10 GB
ext4 volume with automount attached while running, `attach_to_network` of
a second network while running, `reboot` through the API and the checks
again.

Consequence: `findSum` also parses BSD lines and skips PGP and `#` lines;
the image HTTP client sends an `hlab/<version>` User-Agent and retries
once on a non-200 answer.

Repeat: scripts in `.project/research/experiments/images/tools/`
(`run.py`, `guestcheck.sh`); set `MSYS_NO_PATHCONV=1` in Git Bash; the
swapped disk copy has to be deleted by hand.

## E31 rocky-9/10, alma-9/10, centos-stream-9/10 on Hyper-V Gen2 with hlab's seed and hooks

- Result: confirmed, no change needed
- Checked on: 2026-11-24, Rocky 9.9 / 10.3, AlmaLinux 9.9 / 10.3, CentOS Stream 9 (5.14.0-765) / 10 (6.12.0-284), cloud-init 24.4; dev cloud at b3441e9
- By: experiment (method in E30)
- Status: valid

Observed:
- Boot with Secure Boot on; `hv_vmbus`, `hv_storvsc`, `hv_netvsc` in the
  initramfs; `console=ttyS0,115200n8`; SSH 13 to 38 s after poweron. EL10
  is built for x86-64-v3 (AVX2) and boots on this host (processor
  compatibility off).
- NoCloud: hostname, user_data, root SSH key, create-time root password,
  root disk grown to 40 GB.
- Network: EL9 renders sysconfig, EL10 NetworkManager keyfiles; both get
  the /32, `172.31.1.1 scope link` and the default route; outbound, DNS
  and SSH from the host work; the private network at create gets its /32
  and range route.
- Hooks with SELinux enforcing, 0 AVC denials: password reset, volume
  symlink and automount with fstab line, hot-added adapter through the
  `ip` path (networkd inactive), sshd drop-in (`permitrootlogin yes`),
  `hlab-sync`; after the API reboot the seed re-renders the hot-added
  adapter and fstab mounts the volume.
- `cloud-init status` ends "degraded done" with exit code 2: the
  network-config v2 schema of cloud-init 24.4 to 25.3 rejects `on-link`
  in routes; without it the file validates, and the NetworkManager and
  sysconfig renderers ignore the key anyway.
- cloud-init creates the distro's default user (`rocky`, `almalinux`,
  `cloud-user`) with the SSH key and passwordless sudo.

Consequence: these six images work with the existing seed and hooks.

## E32 rocky-8 and alma-8: hlab's hooks fail on Python 3.6, fixed in the vendor script

- Result: refuted as is; confirmed with a hook change
- Checked on: 2026-11-24, Rocky 8.10, AlmaLinux 8.10, cloud-init 23.4, platform-python 3.6.8
- By: experiment (method in E30)
- Status: valid

```
journalctl -u hlab-guest: TypeError: __init__() got an unexpected keyword argument 'text'
hlab-volume@199: TypeError: __init__() got an unexpected keyword argument 'capture_output'
rocky-8 hlab-guest.service: ExecMainStatus=203 (no /usr/bin/python3)
API reset_password: server_error "the server did not confirm the password reset within 2m0s"
```

Observed: boot, Secure Boot, drivers, the sysconfig /32 network, the
private network at create, create-time password, user_data, sshd drop-in
and `hlab-sync` work; password reset, volume automount and hot-added
adapters fail (on rocky-8 also the volume symlink). With
`stdout=subprocess.PIPE, stderr=subprocess.PIPE, universal_newlines=True`
instead of `capture_output=True, text=True` (and `universal_newlines=True`
instead of `text=True`) in `hlab-guest` and `hlab-volume`, and the
interpreter chosen by the vendor script (`/usr/bin/python3`, else
`/usr/libexec/platform-python`) in the shebangs, `ExecStart=` lines and
the udev `PROGRAM=`, every check passes, also after a reboot, with 0 AVC
denials. The changed scripts run with exit 0 under Python 3.14 (Fedora
44) with `-W error::DeprecationWarning`.

Consequence: the hooks in `vendordata.go` use only Python 3.6-compatible
arguments and the interpreter the vendor script finds.

## E33 fedora-43 and fedora-44 on Hyper-V Gen2 with hlab's seed and hooks

- Result: confirmed, no change needed
- Checked on: 2026-11-24, Fedora 43 (6.17.1, cloud-init 25.2), Fedora 44 (6.19.10, cloud-init 25.3, Python 3.14.3), Cloud Base Generic qcow2
- By: experiment (method in E30)
- Status: superseded (phase 29 29-05): with `on-link` gone, fedora-44 still ends `cloud-init status` with exit 2 in two of two e2e runs and fedora-43 in one of two: the local stage runs `hostnamectl set-hostname` for meta-data `local-hostname` before D-Bus is up ("Failed to connect to system scope bus via local transport"), a WARNING in recoverable_errors; the init stage then sets the hostname correctly

Observed: Secure Boot, drivers, serial console, SSH 21 s after poweron;
NoCloud, NetworkManager /32 network, outbound, private network at create
and hot-added, volume automount, password reset, sshd drop-in, `hlab-sync`
and the reboot all pass with SELinux enforcing (one AVC on fedora-43 from
`reboot`, unrelated to hlab). `cloud-init status` exit code 2 as in E31.

Consequence: both work as is once the checksum parser and User-Agent of
E30 are in.

## E34 opensuse-16 (Leap 16.0 Minimal-VM Cloud) on Hyper-V Gen2 with hlab's seed and hooks

- Result: confirmed, no change needed
- Checked on: 2026-11-24, openSUSE Leap 16.0, kernel 6.12.0-160000.41-default, cloud-init 25.1.3
- By: experiment (method in E30)
- Status: valid

Observed: Secure Boot (SUSE key), hv drivers in the initramfs, SSH 61 s
after poweron (the slowest image); cloud-init renders NetworkManager
keyfiles (wicked inactive); all network, volume, password reset, hot-add,
`hlab-sync` and reboot checks pass with SELinux enforcing and 0 AVCs;
there is no `/etc/ssh/sshd_config` (vendor config in `/usr/etc`), but
`00-hlab-root.conf` in `/etc/ssh/sshd_config.d/` is honoured.
`cloud-init status` exit code 2 as in E31.

Consequence: opensuse-16 works with the existing seed and hooks.

## E35 Disk, NIC, DVD and checkpoint operations on a Paused VM

- Result: confirmed, no change needed
- Checked on: 2026-11-24, client Hyper-V, persistent runspace, not elevated
- By: experiment (throwaway Gen2 VM without OS, user-owned VHDX/ISO files)
- Status: valid

Observed: after `Suspend-VM`, `Add-`/`Remove-VMHardDiskDrive`,
`Set-VMHardDiskDrive -MaximumIOPS`, hlab's AttachNIC sequence,
`Remove-VMNetworkAdapter`, DVD eject/insert/change (eject poll 5.3 s),
`Add-VMDvdDrive` and the checkpoint-based snapshot (checkpoint, copy,
`Remove-VMSnapshot`, merge 4.5 s) all succeed in under a second each (waits
aside), none hangs, and the VM stays Paused; `Resume-VM` then runs normally.
`Add-VMNetworkAdapterAcl -RemoteMacAddress` needs the dashed MAC form in
every state.

Consequence: no action has to end a freeze or refuse on a Paused VM other
than the power actions and rebuild; `Snapshot` needs no change; the cloud
start cleanup may resume, reattach and clear limits in any order. Attaching
on hlab's real data folder still needs hlabd (LocalSystem, E28).

## E36 MaximumIOPS belongs to the drive, not to the file

- Result: confirmed, consequence for the design
- Checked on: 2026-11-24, client Hyper-V, not elevated
- By: experiment (method in E35)
- Status: valid

Observed: `Set-VMHardDiskDrive -MaximumIOPS 300` works on an Off VM and reads
back 300 after Start-VM, a hard and a graceful stop, and after the VHDX at
the drive's path is deleted and recreated as a full copy (rebuild) or as a
differencing child (rescue); the VM starts normally on the new file.

Consequence: a limit is never cleared by rebuild, power cycle or crash; only
an explicit `SetDiskIOPS(...,0)` (fault end, cloud start cleanup) clears it;
deleting the server removes it with the VM.

## E37 Cost of reading and clearing disk limits at cloud start

- Result: confirmed, no change needed
- Checked on: 2026-11-24, persistent runspace, warm Hyper-V module
- By: experiment (median of 20 runs)
- Status: valid

Observed: `Get-VMHardDiskDrive -VMName` about 20 ms per VM (16 ms Off);
`Get-VM | Get-VMHardDiskDrive` for two VMs 53 ms; a Set to 0 on a drive
already at 0 about 35 ms; Get then filtered Set with nothing set 20 ms.

Consequence: the cleanup reads all drives of a VM in one call and sets only
those with `MaximumIOPS -gt 0`, about 20 ms per VM with nothing to fix.

## E38 fio installs in debian-13 and ubuntu-24.04 servers through hlab's NAT

- Result: confirmed, with a correction
- Checked on: 2026-11-24, dev cloud, cx23 in fsn1, debian-13 (fio 3.39) and ubuntu-24.04 (fio 3.36)
- By: experiment
- Status: valid

Observed: `apt-get install -y fio` alone fails on both ("Unable to locate
package"); after `cloud-init status --wait` and `apt-get update`,
`apt-get install -y --no-install-recommends fio` works: about 36 s in all on
Debian, 15 s on Ubuntu.

Consequence: the e2e test waits for cloud-init, runs `apt-get update` first
and gives update plus install about 2 minutes.

## E39 Measuring IOPS with fio at a Hyper-V limit

- Result: confirmed
- Checked on: 2026-11-24, same servers as E38, limit set with `Set-VMHardDiskDrive -MaximumIOPS`
- By: experiment
- Status: valid

Observed: command (read-only, raw device):
`fio --name=iops --filename=<dev> --readonly --rw=randread --bs=8k --direct=1 --ioengine=libaio --iodepth=16 --ramp_time=2 --runtime=5 --time_based --output-format=json`;
IOPS is `.jobs[0].read.iops`. Volume device:
`/dev/disk/by-id/scsi-0HC_Volume_<id>`; system disk:
`/dev/$(lsblk -n -o PKNAME "$(findmnt -n -o SOURCE /)")` (sda/sdb order
differs between distributions). Unlimited 130k to 170k IOPS; at 200 with
ramp 197 to 200, without ramp 213 to 239 (first-second burst as in E27);
`--bs=64k` gives 27.5 (8 KiB normalisation). Laying out a file under the
limit is slow.

Consequence: an IOPS check uses `--ramp_time=2`, `--bs=8k`, the raw device
with `--readonly`, accepts the limit within about 10 % and checks
"unlimited" as far above it (more than 5 times).

## E40 nftables input/output hooks for private (VRF) traffic to and from the appliance

- Result: confirmed with a correction: the output hook supports neither `meta sdifname` nor `iifname`
- Checked on: 2026-11-24, Debian 13 cx23 server (`debian-13`, the appliance image), kernel 6.12.117, iproute2 6.15.0, nftables 1.1.3 (CLI only in the throwaway VM)
- By: experiment
- Status: valid

Observed: namespace model of `vrf_linux.go` (router with `p0` in main and
trunk VLAN `t20` enslaved to `vrf20`, table 1020; a private server
10.0.0.2 on VLAN 20; HTTP listener bound to `vrf20` on 169.254.169.254;
kernel default `ip rule`, `tcp/udp_l3mdev_accept` 0). Traffic from the
server to the appliance's own addresses (gateway ping, metadata HTTP) and
the appliance's own `ping -I vrf20` show in input `iifname "vrf20"` with
`meta sdifname "t20"` (`iifname "t20"` never matches); output and
postrouting run twice, first with `oifname "vrf20"`, then `"t20"`. Rules
that work: input `iifname "vrf<vlan>" meta sdifname "t<vlan>" ip saddr <ip> drop`,
output `oifname "t<vlan>" ip daddr <ip> drop`; each alone stops all
three. `meta sdifname` or `iifname` in an output chain fails with
"Operation not supported" and aborts the whole transaction. Private
servers also reach the appliance's public addresses (185.12.64.1,
198.19.0.1) through the local table, so an input cut must match every
packet from the server, not only gateway and metadata addresses. A
`numgen` in output would advance twice per VRF packet.

## E41 Egress tc on p0 and t<vlan> also delays packets the appliance sends itself

- Result: confirmed
- Checked on: same VM and model as E40
- By: experiment
- Status: valid

Observed: `tc qdisc add dev <dev> root handle 1: htb default 1`, classes
1:1 and 1:10 (`htb rate 10gbit quantum 200000`), `netem delay 50ms limit
100000` as child 10: of 1:10, `flower dst_ip <server>` to 1:10. Ping to
gateway, DNS over UDP, HTTP to the API address and metadata rose from
0.1 to 0.5 ms to 50 ms (HTTP 100 ms: SYN-ACK and response each pass) on
`p0` (public) and on `t20` (private, also for the appliance's own
`ping -I vrf20`); deleting the root qdisc restored 0.05 ms. Egress
shaping delays only the direction toward the server: a service round trip
gets the delay once, server-to-server traffic through two shaped egresses
twice. `vrf<vlan>` must keep its default `noqueue`; a qdisc on the VRF
device sends local packets another way.

## E42 netem seed sequence across filter and class changes and `tc qdisc change`

- Result: confirmed for filters and sibling classes; `tc qdisc change` or `replace` with `seed` restarts the sequence, without `seed` it continues
- Checked on: same VM and model as E40
- By: experiment
- Status: valid

Observed: tree of E41 with `netem loss random 10% seed 42 limit 100000`,
`ping -c 100 -i 0.005` runs, lost `icmp_seq` compared. Identical loss
lists to one continuous run of 200 after: adding and deleting a sibling
class with its own netem and flower filters, `tc class change` of 1:1,
deleting and re-adding the flow's own filter. `tc qdisc change` or
`replace` with `seed 42` (also with `delay 1ms` added) restarts at the
first loss of a fresh fault; `change` without `seed` continues the
sequence and `tc qdisc show` still reports `seed 42`; changing to 20 %
with `seed 42` equals a fresh 20 % fault. Consequence: pass `seed` when a
fault's netem parameters change, and skip `tc qdisc change` when nothing
changed. A seed-less `add` gets a random seed. Loss draws come only from
packets entering that netem.

## E43 ethtool in the appliance image, and offloads through SIOCETHTOOL

- Result: confirmed: ethtool 1:6.14.2-1 is preinstalled in `debian-13`; the SIOCETHTOOL ioctl from Go works too
- Checked on: 2026-11-24, fresh `debian-13` server (Debian 13.7, kernel 6.12.117, `hv_netvsc`)
- By: experiment
- Status: valid

Observed: `dpkg-query -W ethtool iproute2 nftables` shows ethtool and
iproute2 installed, nftables not installed. `/usr/sbin/ethtool -K <dev>
gro off gso off tso off lro off` is the simplest way. Alternative with
golang.org/x/sys v0.50.0: `ioctl(fd, unix.SIOCETHTOOL, &ifreq)` on an
AF_INET datagram socket with an own ifreq `{Name [16]byte; Data
unsafe.Pointer}` padded to 40 bytes (not `unix.Ifreq`), commands
`ETHTOOL_GGRO/SGRO`, `GGSO/SGSO`, `GTSO/STSO`, and `GFLAGS/SFLAGS` with
`ETH_FLAG_LRO = 1<<15` (no constant in x/sys). Off and on round-tripped
on eth0; a VLAN subinterface has LRO `[fixed]` off; unknown interface
gives `no such device`. `STSO off` also clears the TSO sub-features.

## E44 Windows SMB client while a storage box IP is dropped, and its recovery

- Result: confirmed: while dropped, `net use` fails after 21 to 84 s and a TCP 445 dial after 21 s; after the drop ends the box is reachable at once, but the first `net use` to the same name can fail for up to about 50 s
- Checked on: 2026-11-24, Windows 11 Pro 10.0.26300 (German UI), dev service `hlab-dev`, hcloud 1.70.2, PowerShell 5.1 unelevated
- By: experiment
- Status: valid

Observed: drop made by `storage-box update-access-settings
--reachable-externally=false|true` (plain nftables `drop` in the box IP's
chain before `ct state established`, no reject; in place when the action
ends, 2 to 3 s). Host: MaxSynRetransmissions 4, SMB SessionTimeout 60,
KeepConn 600. While dropped: TCP dial to `<ip>:445` fails after 21.0 s
(with a 3 s timeout: no answer); a fresh `net use` fails after 42 s (IP)
or 59 s (FQDN, also tries 139) with error 64 or 67; with an old or
half-dead SMB connection to the server still open, `net use` either
succeeds in 0.04 s without traffic or fails after 84 s; a write over an
earlier mapping blocks 82 s, then "network path not found". After the
drop ends: dial connects at once; the first `net use` to the same name
fails 0 to 2 times after 21 s each with error 67 (the WebDAV redirector
tries port 80) and then succeeds; `net use /delete` before does not help;
a failure under the IP does not block the FQDN. An old mapping recovers
only after about 82 s (then "access denied" for about 53 s). After
`net use /delete` the TCP connection stays ESTABLISHED 10 to 20 s.
Consequence for tests: no SMB connection to the box before the fault
(wait with a deadline until `netstat -n -p tcp` shows none to
`<ip>:445`); prove the drop with a short-timeout TCP dial, or a `net use`
failing within 2 min without checking the error code; prove recovery by
retrying a new `net use` for at least 90 s. `Get-SmbConnection` needs
elevation; match `net.exe` by exit code, its messages are localized.

## E45 `cloud-init status` on ubuntu-22.04/24.04/26.04 and debian-12/13 with hlab's seed, vendor-data and `users: []`

- Result: confirmed, no change needed for Ubuntu and Debian
- Checked on: 2026-11-24, dev cloud at 9c1dea1, Hyper-V Gen2, cx23; cloud-init 26.1 (Ubuntu 22.04.5, 24.04.5), 26.2 (Ubuntu 26.04.1), 22.4.2 (Debian 12), 25.1.4 (Debian 13)
- By: experiment
- Status: valid

Method: one server per image via hcloud 1.70.2 with SSH key, private
network at create and an ext4 volume with automount, user_data
`#cloud-config` with `users: []` and a runcmd marker (the default merge
keeps `users` wherever it sits); then `cloud-init status --wait --long`,
`--format json`, `cloud-init schema --system --annotate`, the log grepped
for WARNING/DEPRECAT, `getent passwd` for UID 1000-59999; API reboot and
status again.

Observed:
- Exit 0, status done, `errors: []`, `recoverable_errors: {}` on all five,
  on first boot and after reboot; volume mounted, marker written, no user
  with UID >= 1000.
- Ubuntu and Debian 13 validate network-config v2 with netplan's Python
  API, which accepts `on-link`; cloud-init's own JSON v2 schema still
  rejects `on-link` in 26.1 and 26.2 too (E31's range 24.4-25.3 is too
  narrow), so images without netplan (EL, Fedora, openSUSE) end
  "degraded" (exit 2) for exactly this key.
- Debian 12 (22.4) does not validate network-config v2; its cloud-config
  schema rejects `users: []` ("[] is too short") with a log WARNING only,
  status stays done with exit 0, and no default user is created.
- No deprecated key in the vendor-data on any image.
- Routes rendered by netplan: `default via 172.31.1.1 onlink` and
  `10.0.0.0/16 via 10.0.0.1 onlink`. Not proven: that networkd sets
  onlink by itself if `on-link` is dropped from the seed.

Pitfalls: scripts written on Windows carry CRLF (dash fails on
`2>&1\r`); `cloud-init schema -t network-config` does not exist in 22.4.

## E46 Hostname from vendor-data instead of meta-data `local-hostname` (Q25)

- Result: refuted with `hostname` alone (EL and Fedora get the reverse-DNS name); confirmed with `hostname` and `fqdn` both set to the server name. Also refuted: an empty seed user-data `#cloud-config` keeps `cloud-init status` at 0
- Checked on: 2026-11-24, dev cloud at c566f4b, Hyper-V Gen2, cx23; ubuntu-24.04 (cloud-init 26.1), debian-12 (22.4.2), debian-13 (25.1.4), rocky-8 (23.4-7.el8_10), rocky-9 (24.4-8.el9_8.1), fedora-43 (25.2), fedora-44 (25.3), opensuse-16 (25.1.3)
- By: experiment
- Status: valid

Method: per image a server created without user_data; the seed copied
from `/dev/sr0` to `/var/lib/cloud/seed/nocloud/` with `local-hostname`
removed from meta-data and `hostname` (A) or `hostname` + `fqdn` (B) added
to vendor-data's cloud-config part; `datasource: {NoCloud: {fs_label:
null}}` keeps cloud-init off the DVD (else the DVD's files win);
`/etc/hostname` and `/etc/hosts` restored to the image's content; a probe
unit (`DefaultDependencies=no`, between cloud-init-local and cloud-init)
logs the hostname; `cloud-init clean --logs`, reboot, then status, log and
journal. Fedora ran A and B twice.

Observed:
- Local stage: no hostname set and no `hostnamectl` call on any image; the
  Fedora D-Bus warning is gone.
- Between local and init stage the guest carries the image's own name
  (`ubuntu` on Ubuntu, `localhost` elsewhere, from an empty
  `/etc/hostname`); on NetworkManager images (Fedora, EL, openSUSE)
  NetworkManager sets transient `localhost.localdomain` and then the
  reverse-DNS name `static.<ip>.clients.your-server.de`, as it already did
  with today's seed on Fedora.
- A: `cc_set_hostname` takes the fqdn from the current system name; with
  `prefer_fqdn` in `rhel.py` rocky-8, rocky-9, fedora-43 and fedora-44 end
  with the reverse-DNS name; Ubuntu, Debian and openSUSE end with the
  server name. Status exit 0 everywhere.
- B: final static hostname and `/etc/hostname` are the server name on all
  eight; status exit 0 on all, Fedora twice each. No `/etc/hosts` entry
  (no image sets `manage_etc_hosts`).
- Seed user-data `#cloud-config\n` alone (server without user_data): exit
  2 "empty cloud config" on ubuntu-24.04, debian-13, opensuse-16, fedora;
  rocky-9 crashes in init-local (`TypeError` in
  `_should_wait_via_user_data`, exit 1). `#cloud-config\n{}\n` gives exit 0
  on all tested images.
- rocky-8 always lists `skipping .../scripts/vendor as its not executable`
  in recoverable_errors with exit 0; debian-12 keeps E45's `users: []`
  warning with exit 0.

Pitfalls: with the cidata DVD attached a seed directory alone is not
enough (`fs_label: null`); after `cloud-init clean` `/etc/hostname` hides
the pre-init name; `cloud-init clean` makes new host keys.

## E47 Private network drops after resume from saved state (phase B4)

- Result: confirmed and explained; the cause is in Hyper-V's restore and the guest's `hv_netvsc` driver, no hlab step removes it. Refuted: "up to about 5 minutes" (the drops end 11 to 25 s after the first loss)
- Checked on: 2026-11-24, dev cloud at a9c685a, `stop_behaviour = "save"`, ubuntu-24.04 (kernel 6.8.0-147, systemd 255), cx23, two servers in fsn1 and a third in hel1, one network, no faults active
- By: experiment
- Status: valid

Method: each guest ran `ping -D -O -i 0.01 -W 1 <peer private IP>` (started
before the stop, it resumes with the VM) and logged eth1 carrier, address
and route every 50 ms; full cycle `hlab cloud stop` / `hlab cloud start`,
and guest-only cycle `Save-VM` / `Start-VM` (no elevation) with the cloud
running; `tcpdump 'icmp or arp'` on eth1 of both guests in one run. Loss
counted by `icmp_seq` without reply, not by guest time (the clock steps by
about 3 s after resume).

Observed:
- Full cycle: one or two blocks of about 90 to 540 lost pings in a row,
  sometimes a few percent in the following 10 s, then no loss in 6 minutes.
  The same pattern with only one guest saved and resumed, so the appliance
  boot, agent document push, deny lists, VLANs and start order are not the
  cause; VLAN and ACL settings are unchanged, the appliance answers ARP at once.
- On restore Hyper-V sends each NIC several disconnect/connect events within
  about 100 ms, twice (port connect and vmbus resume; VmSwitch-Operational
  event 220, localized text). `hv_netvsc` handles link changes at most every
  2 s, so eth0 and eth1 lose carrier two or three times for 2 to 3 s each
  over 10 to 20 s; public traffic drops too.
- While the carrier is off the guest drops all outgoing packets (the peer's
  echo requests arrive, no reply leaves); after carrier on the gateway's ARP
  entry is gone (`arp_evict_nocarrier=1`).
- Lost pings in guest-only runs: baseline 371 and 600; networkd
  `ConfigureWithoutCarrier=yes` + `IgnoreCarrierLoss=yes` 603;
  `arp_evict_nocarrier=0` 370; adapters disconnected while saved and
  `Connect-VMNetworkAdapter` after `Start-VM` 323 (one carrier-off window
  only); VLAN and 4 ACL entries survive the reconnect. A gratuitous ARP
  cannot help, flushing appliance tables does not touch the cause.
- Side effect: while the carrier is off networkd removes the static private
  address; the guest hook (`has_ip` in `internal/resources/servers/vendordata.go`)
  then treats the link as hot-added and writes `05-hlab-<mac>.network`, which
  sorts before the seed's `10-netplan-eth1.network`, so the user's netplan
  settings for that link are not applied until the next reboot.
- Waiting condition that is stable: `/sys/class/net/eth{0,1}/carrier_changes`
  unchanged for 8 s (deadline 2 min); 100 good pings can fall between two
  drop windows.

Pitfalls: `ping -O` reports "no answer yet" after 1 s although late replies
arrive; Ubuntu `sh` is dash; `hlab logs` returns only the last 1000 lines.

## E48 cloud-init seed timing and the slot-0 swap on an early network attach (phase B4)

- Result: confirmed: a seed swap inside the window leaves the guest with the image's default hostname and no public IP on all five images; a swap after it does no harm; later stages use the cache. Refuted: that an hlabd restart or a cloud start rewrites stale seeds
- Checked on: 2026-11-24, dev cloud at a9c685a, Hyper-V Gen2, cx23, fsn1, idle host; ubuntu-24.04 (cloud-init 26.1), debian-13 (25.1.4), rocky-10 (24.4-7.el10), fedora-44 (25.3), opensuse-16 (25.1.3)
- By: experiment and code reading
- Status: valid

Method: servers created stopped, then `hcloud server poweron`; a PowerShell
watcher swapped slot 0 like `SetDVD` (`Set-VMDvdDrive -Path $null`, poll
until empty, insert the same ISO) at a given `(Get-VM).Uptime`; firmware
offset = VM uptime minus guest `/proc/uptime`; evidence from
`/var/log/cloud-init.log`, `journalctl -b <n>`; real path: `hcloud server
create` then `attach-to-network` at once, as OpenTofu does.

Observed (VM time from start):

| image | seed unmounted | local stage done |
|---|---|---|
| ubuntu-24.04 | 7.2-7.5 s | 7.6-8.1 s |
| debian-13 | 9.7-9.9 s | 10.4-10.7 s |
| rocky-10 | 16.4-16.7 s | 16.5-16.9 s |
| fedora-44 | 12.2-12.5 s | 12.3-12.6 s |
| opensuse-16 | 36.4-36.9 s (first boot only; later boots about 6.5 s guest time) | 36.6-37.1 s |

- The window opens at about VM start + 4 s: ds-identify (systemd generator)
  looks for `cidata` right after switch-root; an empty drive then disables
  cloud-init for that boot.
- `Set-VMDvdDrive -Path $null` takes 5.1 to 5.4 s until the drive is empty,
  insert 0.2 s: every `rewriteSeed` on a running VM empties the drive for
  at least about 5.3 s.
- Swap inside the window: 5 of 5 failed ("No local datasource found", DHCP
  fallback, wait-online timeout 60-120 s, `DataSourceNone`, default
  hostname, no public IP, unreachable). Real path: attach finished at VM
  uptime 11-14 s; ubuntu 2/2 and debian 1/1 failed, rocky-10 1/1 passed.
  `hcloud server reset` repairs a failed server.
- Swap after the window: 5 of 5 fine. Network, config and final stages log
  `restored from cache` and never mount `/dev/sr0`.
- Every later boot (power-on, reboot, reset) reads the seed again in the
  local stage, so the window returns at every boot. A later boot with an
  empty drive keeps hostname and IPs from the earlier boot (ubuntu-24.04).
- Code: `servers.reconcile` builds the seed only when the file is missing and
  runs at create and rebuild only; cloud start (`recoverModules`, `restore`)
  rewrites seeds only in `primaryips.Recover` for rebuilt primary IPs. A stale
  seed stays until the server's next `SyncAdapters`; if hlabd ends between
  eject and insert in `rewriteSeed`, slot 0 stays empty and nothing puts it back.
- Measured worst case 37 s on an idle host; a 90 s hold covers it. A reboot
  inside the guest does not reset Hyper-V uptime and stays invisible to hlab.

Pitfalls: Get-VM Uptime includes firmware (add 2 to 4.3 s to guest `Up`);
PowerShell starts in 1 to 5 s; scripts need LF endings; a failed guest's
`cloud-init.log` may be gone after reset (use the persistent journal).
