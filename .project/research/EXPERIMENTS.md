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
