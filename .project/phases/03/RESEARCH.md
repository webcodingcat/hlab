# Phase 03 research

Researched 2026-11-18. No Hyper-V host commands could be run during this research (no shell available), so everything about live timing and live WMI instance names is marked UNCONFIRMED and needs a first probe in the phase (see "Probes to run first" at the end).

Spec = frozen OpenAPI description `%LOCALAPPDATA%\hlab\openapi\cloud.spec.json` (line numbers refer to that file).

## 1. Reboot: `Msvm_ShutdownComponent.InitiateReboot` (SRV-07)

- Signature (Microsoft Learn, Win32 Hyper-V v2): `uint32 InitiateReboot([in] boolean Force, [in] string Reason)`. Return values: 0 completed, 4096 job started, 32768 Failed, 32769 Access Denied, 32770 Not Supported, 32771 Status unknown, 32772 Timeout, 32773 Invalid parameter, 32774 System in use, 32775 Invalid state, 32776 Incorrect data type, 32777 System not available, 32778 Out of memory, 32779 File not found, 32780 Not ready, 32781 Machine locked, needs Force, 32782 Shutdown in progress. "Zero means the reboot was initiated". Minimum Windows 10 / Server 2016 (InitiateShutdown: Windows 8 / Server 2012).
  https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/msvm-shutdowncomponent-initiatereboot
  https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/msvm-shutdowncomponent-initiateshutdown
- Guest side: Linux `drivers/hv/hv_util.c` handles the shutdown IC message flags: 0 and 1 -> `orderly_poweroff(true)`, 2 and 3 -> `orderly_reboot()` ("Restart request received - graceful restart initiated"), 4 and 5 hibernate. Restart support came with shutdown IC protocol version 3.1 in Linux v5.6 (commit "hv_utils: Support host-initiated restart request", Dexuan Cui, Jan 2020). So every kernel of Ubuntu 22.04 (5.15), 24.04 (6.8), Debian 12 (6.1) and 13 (6.12) has it, provided `hv_utils` is loaded (the Shutdown IC already works for `shutdown`, so it is).
  https://github.com/torvalds/linux/blob/master/drivers/hv/hv_util.c
  https://lkml.iu.edu/hypermail/linux/kernel/2001.3/02499.html
- The BACKLOG "not supported" answer came from a VM without a guest OS (no IC connected); that is expected there and says nothing about Linux guests.
- UNCONFIRMED: no source found that states the return value or the time until the guest is back for Ubuntu 24.04 / Debian 13 on Hyper-V. Expected: return 0 (or 4096) at once; the guest reboots by itself; the VM state stays `Running` the whole time (never `Off`), so `platform.State` cannot show completion; the guest is typically back in tens of seconds, but this is an estimate, not a source.
- Existing code (`internal/platform/hyperv/hyperv.go`) already calls `InitiateReboot` with `Force=$true, Reason='hlab'` and accepts 0 and 4096 only; it checks the IC is enabled first. Good as is.
- Recommended design for the plan: try `InitiateReboot`; on any other return value (especially 32770) fall back inside `hyperv` to `InitiateShutdown`, wait for `StateOff` with a deadline (systemd's default stop timeout is 90 s, so use at least 120 s), then `Start-VM`; then deadline-wait for the guest's return (agent status/heartbeat, not VM state). Do not decide by distro, decide at run time. The SRV-07 e2e row proves which path works and must measure the time. If the fallback is ever used, `docs/user/deviations.md` notes it (already in CONTEXT).
- Pitfall: `Restart-VM` (used for `reset`) is documented as a hard restart ("like powering the computer down, then back up"); the kernel commit message mentions `Restart-VM -Type Reboot`, but the cmdlet has no `-Type` parameter in the current docs; ignore that text.
  https://learn.microsoft.com/en-us/powershell/module/hyper-v/restart-vm?view=windowsserver2025-ps

## 2. Metrics source (SRV-21)

Measure-VM / resource metering is NOT suitable: it reports average CPU in MHz over the metering period (not cumulative CPU time), average/min/max memory, total disk allocation (capacity, no IO), and network only as MB per `NetworkAdapterAcl` (needs ACLs, per direction, no packets). It has no IOPS, no disk bytes, no packets, and needs `Enable-VMResourceMetering` per VM.
https://learn.microsoft.com/en-us/powershell/module/hyper-v/measure-vm?view=windowsserver2025-ps

Use the raw performance counter classes in `root\cimv2` (class and property names are English and not localized; the display names of counters are, which is why not to use `Get-Counter` paths). Class names below are from memory of the standard Hyper-V WMI performance providers; I found no Microsoft page listing the properties, so verify with `Get-CimClass` first (see probes):

| Need | Class | Properties (cumulative, raw) | Instance `Name` |
|---|---|---|---|
| CPU | `Win32_PerfRawData_HvStats_HyperVHypervisorVirtualProcessor` | `PercentTotalRunTime` (also `PercentGuestRunTime`, `PercentHypervisorRunTime`), 100 ns units; with `Timestamp_Sys100NS` of the same row | `<VMName>:Hv VP <n>`, plus `_Total` |
| Disk | `Win32_PerfRawData_Counters_HyperVVirtualStorageDevice` | read/write bytes and read/write operations counts (names such as `ReadBytesPersec`, `WriteBytesPersec`, `ReadCount`, `WriteCount`; the `...Persec` ones are raw cumulative totals in the Raw class) | one instance per virtual disk: the VHDX path with separators turned into `-` (UNCONFIRMED), plus `_Total` |
| NIC | `Win32_PerfRawData_NvspNicStats_HyperVVirtualNetworkAdapter` | `BytesReceivedPersec`, `BytesSentPersec`, `PacketsReceivedPersec`, `PacketsSentPersec` (raw cumulative) | `<VMName>_<adapter name>--<adapter GUID>` form (Microsoft's guidance: "virtual machine name name<GUID>"), UNCONFIRMED exact separators |

Evidence: instance format `testVM:Hv VP 0` (SolarWinds KB, Veeam reference); the counter objects are "Hyper-V Hypervisor Virtual Processor", "Hyper-V Virtual Storage Device", "Hyper-V Virtual Network Adapter"; Microsoft's `Win32_PerfRawData` base gives `Timestamp_Sys100NS`, `Frequency_Sys100NS`, `Timestamp_PerfTime`, `Frequency_PerfTime`.
https://helpcenter.veeam.com/docs/mp/hyperv_reference/vm.html
https://learn.microsoft.com/en-us/archive/blogs/tvoellm/how-to-get-processor-utilization-for-hyper-v-via-wmi (same technique for the logical processor class: two samples)
https://learn.microsoft.com/en-us/windows/win32/cimwin32prov/win32-perfrawdata

How to use:
- CPU percent = delta(sum of `PercentTotalRunTime` over the VM's `Hv VP n` rows) / delta(`Timestamp_Sys100NS`) * 100, as 100 ns ticks; this is per vCPU summed (100 = one vCPU fully busy), which matches Hetzner's definition (value per vCPU, 100 = full use of a 1-vCPU server; spec line 61714). So the API value can be the raw sum with no division by cores. The Raw class already holds cumulative ticks, so `Sample.CPUPercent` is better replaced by a cumulative CPU-time counter and the percentage derived from two samples (the existing `Sample.CPUPercent` field is a computed value, not cumulative).
- Mapping to a VM: the VM name `hlab-server-<id>` is under hlab's control and is part of every instance name. Match with an exact delimiter (`<name>:` for VP, `<name>_` for NIC), never a bare prefix (`hlab-server-1` is a prefix of `hlab-server-10`). Disk: match through the VHDX path of the server's disk (hlab knows it), or sum all rows whose path is under the VM's folder; skip `_Total` and `_Checkpoint Disks`.
- Counters reset when the VM stops/starts (new instance); handle a decrease as a counter reset (restart delta at zero). Instances exist only while the VM runs.
- Query once per sample from the existing long-running PowerShell (`Get-CimInstance -ClassName ...` with a `-Filter "Name LIKE 'hlab-server-5:%'"`), return JSON with numbers only.

## 3. Hetzner metadata service (META-01)

Official text (spec `info.description`, section "Server Metadata", at `/hetzner/v1/metadata`): only six items are documented.

| Data | Format | Contents |
|---|---|---|
| hostname | text | name of the server |
| instance-id | number | ID of the server |
| public-ipv4 | text | primary public IPv4 |
| private-networks | yaml | private networks the server is attached to |
| availability-zone | text | e.g. `hel1-dc2` |
| region | text | network zone, e.g. `eu-central` |

Official `GET http://169.254.169.254/hetzner/v1/metadata` (YAML, keys sorted alphabetically):
```yaml
availability-zone: hel1-dc2
hostname: my-server
instance-id: 42
public-ipv4: 1.2.3.4
region: eu-central
```
Single values: `GET /hetzner/v1/metadata/hostname` -> `my-server`; `/instance-id` -> `42`; `/public-ipv4` -> `1.2.3.4`; `/availability-zone` -> `hel1-dc2`; `/region` -> `eu-central` (plain text, no quotes, trailing newline as curl shows).
`GET /hetzner/v1/metadata/private-networks` (YAML list; official example, field names exactly):
```yaml
- ip: 10.0.0.2
  alias_ips: [10.0.0.3, 10.0.0.4]
  interface_num: 1
  mac_address: 86:00:00:2a:7d:e0
  network_id: 1234
  network_name: nw-test1
  network: 10.0.0.0/8
  subnet: 10.0.0.0/24
  gateway: 10.0.0.1
- ip: 192.168.0.2
  alias_ips: []
  interface_num: 2
  mac_address: 86:00:00:2a:7d:e1
  network_id: 4321
  network_name: nw-test2
  network: 192.168.0.0/16
  subnet: 192.168.0.0/24
  gateway: 192.168.0.1
```
Note: `mac_address` is unquoted in the official example; a YAML 1.1 parser may read `86:00:00:2a:7d:e0` as a string (it has hex letters) but an all-digit MAC such as `86:00:00:12:34:56` can be read as a sexagesimal integer by YAML 1.1 parsers (PyYAML). cloud-init uses PyYAML. Emit the MAC quoted if the generator allows it; quotes are valid YAML and read back as the same string. (Judgement, not from Hetzner.)

hcloud-go (`hcloud/metadata`, `Endpoint = "http://169.254.169.254/hetzner/v1/metadata"`) reads exactly `/hostname`, `/instance-id` (parsed as int64), `/public-ipv4` (net.ParseIP), `/region`, `/availability-zone`, `/private-networks` (returns the YAML unparsed). Nothing else.
https://github.com/hetznercloud/hcloud-go/blob/main/hcloud/metadata/client.go
cloud-init's Hetzner datasource (`DataSourceHetzner.py`, `helpers/hetzner.py`) reads `http://169.254.169.254/hetzner/v1/metadata` (YAML dict, parsed with `util.load_yaml`), `.../metadata/private-networks` (YAML list), and `http://169.254.169.254/hetzner/v1/userdata` (raw, base64 decoded if possible); it also tries IPv6 `http://[fe80::a9fe:a9fe%<nic>]/hetzner/v1/`. It checks reachability with `metadata/instance-id`. Keys used from the root YAML: `instance-id` (required), `hostname` (required, becomes local-hostname), `network-config` (optional), `public-keys` (optional list of strings), `private-networks` (default []), `vendor_data` (optional). HTTP 204 is treated as "empty".
https://github.com/canonical/cloud-init/blob/main/cloudinit/sources/DataSourceHetzner.py
- `public-keys`, `userdata`, `network-config`, `vendor_data`: NOT in Hetzner's public documentation. REQUIREMENTS META-01 names `public-keys` and `userdata`. So the shape is: root YAML key `public-keys:` as a list of SSH public key strings (what cloud-init consumes; unconfirmed against a real Hetzner server), the path `/hetzner/v1/metadata/public-keys` is by analogy with the documented per-key paths (unconfirmed), and `/hetzner/v1/userdata` returns the raw user_data (as cloud-init expects; unconfirmed on the Hetzner side). Treat these as "compatible with cloud-init's reader", which is the only verifiable contract.
- PITFALL: cloud-init's Hetzner datasource only activates if SMBIOS `system-manufacturer` is `Hetzner` and `system-serial-number` equals the metadata `instance-id`. On Hyper-V that never matches (manufacturer is Microsoft), so cloud-init on these guests will use the NoCloud seed, not the metadata service. The metadata service is for hcloud-aware tools and `curl`, not for first boot. Do not try to make cloud-init use it.
- Hetzner removed its EC2-compatible routes (`/2009-04-04/...`, `/latest/...`) on 2026-08-01 (changelog 2026-06-30 and 2026-08-01); do not implement them.
  https://docs.hetzner.cloud/changelog
- Everything above beyond the six documented keys is derived from client code, not documentation.

## 4. OpenAPI: actions and metrics (frozen spec)

All eight action paths are `POST /servers/{id}/actions/<name>`, answer `201` with `{ "action": Action }` (title `ActionResponse`; Action requires `id, command, status (running|success|error), progress, started, finished (nullable), resources[{id,type}], error (nullable {code,message})`). `4xx` and `5xx` bodies are `{ "error": {code, message, details|null} }`.

| Path | operationId | action `command` (example in spec) | request body | extra response / operation errors |
|---|---|---|---|---|
| `/actions/poweron` (59050) | | `start_server` | none | none listed |
| `/actions/poweroff` (58759) | | `stop_server` | none | none listed |
| `/actions/shutdown` (61131) | | `shutdown_server` | none | none; text: the action status "only reflects whether the action was sent", use `poweroff` to be sure |
| `/actions/reboot` (59341) | | `reboot_server` | none | none; "gracefully by sending an ACPI request" |
| `/actions/reset` (60547) | | `reset_server` | none | none; hard |
| `/actions/rebuild` (59632) | `rebuild_server` | `rebuild_server` | `{image: string (required), user_data?: string}` | adds `root_password` (string or null: "New root password when not using SSH keys"); `user_data` limited to 32 KiB, "If not specified, the Server's previous user_data value will be re-used (if any was set)"; server is powered off first; no operation errors listed (generic `protected` 423 applies for rebuild protection) |
| `/actions/change_type` (56069) | `change_server_type` | `change_server_type` | `{server_type: string (ID or name), upgrade_disk: boolean}`, both required | `422 invalid_server_type` ("does not fit for the given server or is deprecated"), `422 server_not_stopped`. Text: server must be powered off; copies the disk and starts it again; only same `storage_type` and equal or bigger disks; "If the disk gets upgraded, the Server type can not be downgraded any more" (use `upgrade_disk: false` to keep downgrading possible) |
| `/actions/reset_password` (60838) | `reset_server_password` | `reset_password` | none | `201` adds `root_password` (string, required in practice). Text: "Only works for Linux systems that are running the qemu guest agent. Server must be powered on (status `running`)". NO error code is listed for a non-running server (the spec has an "Operation specific errors" table only for change_type and a few others). |

Generic codes relevant (info.description error table): `423 locked` (an action is running), `423 protected`, `422 invalid_input`, `422 unsupported_error`, `412 resource_unavailable`, `500 server_error`, `404 not_found`, `422 service_error`.
- reset_password for a non-running server: UNCONFIRMED which code Hetzner returns. The spec lists none; `422 server_not_stopped` has an opposite twin only for placement-group/change_type text. Choose a generic code (suggest `422 invalid_input`... or `422 service_error`) and note it in decisions; there is no spec value to violate. The spec's response validation will accept any `4xx` shape.
- Rebuild: the spec says nothing about disk-size or image errors beyond generic ones; `image` accepts ID or name (system images by name only).

`GET /servers/{id}/metrics` (61710; operationId `get_server_metrics`):
- Query: `type` (required, array of enum `cpu|disk|network`, comma separated per text, e.g. `cpu,disk`), `start` (required, RFC 3339 string), `end` (required, RFC 3339), `step` (optional string, "Resolution of results in seconds").
- `200`: `{ "metrics": { "start": date-time, "end": date-time, "step": number, "time_series": { "<name>": { "values": [[<number timestamp, unix seconds, fractional>, "<value as string>"], ...] } } } }`; all four of `start, end, step, time_series` required; each value pair has exactly 2 items (number, string).
- Series names: `cpu` -> `cpu` (percent; per vCPU, 100 = one full vCPU, not normalised); `disk` -> `disk.0.iops.read`, `disk.0.iops.write`, `disk.0.bandwidth.read`, `disk.0.bandwidth.write` (iop/s, bytes/s); `network` -> `network.0.pps.in`, `network.0.pps.out`, `network.0.bandwidth.in`, `network.0.bandwidth.out` (packets/s, bytes/s).
- Step: "If you do not provide the step argument we will automatically adjust it so that a maximum of 200 samples are returned. We limit the number of samples returned to a maximum of 500 and will adjust the step parameter accordingly." So: no step -> step = ceil((end-start)/200) (floor of the data resolution, here 10 s); a given step that would give more than 500 samples is raised to ceil((end-start)/500). The exact rounding is not specified. Metrics available for the last 30 days (hlab keeps 24 h in memory, deviation already planned). Error codes for bad `type`/time format: none listed; use `422 invalid_input` (spec lists only the generic 4xx shape).

## 5. cloud-init: root password, ssh_pwauth, growpart

Versions (distro packages): Debian 12 bookworm 22.4.2, Debian 13 trixie 25.1.4, forky/sid 26.2 (packages.debian.org). Ubuntu cloud images ship current cloud-init through updates (version for 26.04 not checked). `chpasswd.users` needs cloud-init >= 22.3, so Debian 12's 22.4.2 is the lowest that works; the old `chpasswd.list` form is deprecated.
https://docs.cloud-init.io/en/latest/reference/modules.html (module "Set Passwords")

- chpasswd with a hash: `chpasswd: {expire: false, users: [{name: root, password: "$6$...", type: hash}]}`. `type` defaults to `hash`; hashes are applied via the distro's chpasswd (hashed mode). `root` is not filtered out in `cc_set_passwords.py`. Pitfall: `expire` defaults to `true` and since 22.3 also applies to hashed passwords (`EXPIRE_APPLIES_TO_HASHED_USERS`), which forces a password change at first root login. Set `expire: false` explicitly unless a forced change is wanted (Hetzner's real root password is expired on first login; decide and put it in the spec).
  https://github.com/canonical/cloud-init/blob/main/cloudinit/config/cc_set_passwords.py
- `ssh_pwauth: true`: boolean; cloud-init sets `PasswordAuthentication yes` via `update_ssh_config`. If the main `/etc/ssh/sshd_config` has a line starting exactly with `Include /etc/ssh/sshd_config.d/*.conf` it writes `/etc/ssh/sshd_config.d/50-cloud-init.conf` (mode 0600), otherwise the main file. sshd takes the FIRST value it reads and reads drop-ins in lexical order, so `50-cloud-init.conf` beats Ubuntu's `60-cloudimg-settings.conf` (`PasswordAuthentication no`; the file was renamed from `10-` to `60-` exactly for this, Launchpad bug 2049860, fixed). A config you add must sort before `50-` (use `00-` or `01-`) to win. Restart: only if sshd is active (on Ubuntu 24.04 with socket-activated ssh no restart is needed because sshd is not resident).
  https://github.com/canonical/cloud-init/blob/main/cloudinit/ssh_util.py
  https://bugs.launchpad.net/cloud-images/+bug/2049860
- ROOT LOGIN OVER SSH is NOT enabled by chpasswd or `ssh_pwauth`: the OpenSSH default `PermitRootLogin prohibit-password` (Ubuntu and Debian ship it) still refuses a root password. cloud-init's `disable_root: true` only changes root's `authorized_keys` prefix (forced command), it does not touch sshd. For the SRV-02/SRV-10 "root password login" rows over SSH, vendor-data must also drop `/etc/ssh/sshd_config.d/00-hlab-root.conf` with `PermitRootLogin yes` (via `write_files`), and, if user-data/SSH keys disable password auth elsewhere, `PasswordAuthentication yes`; and `disable_root: false` is not needed for password login. UNCONFIRMED by test on the five images (Ubuntu 22.04/24.04/26.04, Debian 12/13); the e2e row will show it. The Debian cloud images' sshd defaults were not checked.
- User-data vs vendor-data: "User-supplied cloud-config is merged over cloud-config from vendor-data" (user wins on conflicting keys; lists are replaced by default merge, so a user `chpasswd` replaces the vendor one); users can disable vendor-data entirely. So a user's `ssh_pwauth: false` or `chpasswd` wins, and hlab's vendor-data root password is a default, not a guarantee. Keep root_password semantics: it is returned by the API, not guaranteed to work if user-data overrides.
  https://docs.cloud-init.io/en/latest/explanation/vendordata.html
- `set_passwords` has per-instance frequency: on a booting disk with the same instance-id it does NOT rerun. That is why reset_password needs the guest hook (as decided), and why a rebuild (fresh disk, no `/var/lib/cloud`) reruns everything even with the same instance-id.
- growpart: module `cc_growpart` frequency "always" (runs on every boot), defaults `mode: auto`, `devices: ["/"]`, enabled by default; only grows the LAST partition of an MBR/GPT disk; `/etc/growroot-disabled` blocks it. `cc_resizefs` (always, `resize_rootfs: true`) grows the filesystem afterwards. In Ubuntu and Debian cloud images the root partition (p1) is the last one on disk (ESP p15/BIOS p14 sit in front), so after `Resize-VHD` and a normal boot the root filesystem grows. Not tested here on the specific images; an e2e check is the proof (change_type with `upgrade_disk: true`).
  https://docs.cloud-init.io/en/latest/reference/modules.html (Growpart, Resizefs)

## 6. SHA-512 crypt test vectors (Drepper, "Unix crypt using SHA-256 and SHA-512")

Source: https://www.akkadia.org/drepper/SHA-crypt.txt (the `tests2` array of `sha512crypt.c`). Parameters: default rounds 5000, `rounds=` clamped to 1000..999999999, salt at most 16 characters (longer is truncated), `rounds=N$` appears in the output only if `rounds=` was given in the input (even `rounds=5000`), and the output shows the clamped value and the truncated salt. Alphabet `./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz`; byte order for the 64 digest bytes in groups of three (b2,b1,b0 per Drepper's table): (0,21,42) (22,43,1) (44,2,23) (3,24,45) (25,46,4) (47,5,26) (6,27,48) (28,49,7) (50,8,29) (9,30,51) (31,52,10) (53,11,32) (12,33,54) (34,55,13) (56,14,35) (15,36,57) (37,58,16) (59,17,38) (18,39,60) (40,61,19) (62,20,41), last group (0,0,63) -> 2 chars.

Vectors (input setting, password, expected output):
1. `$6$saltstring`, `Hello world!` -> `$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1`
2. `$6$rounds=10000$saltstringsaltstring`, `Hello world!` -> `$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v.`
3. `$6$rounds=5000$toolongsaltstring`, `This is just a test` -> `$6$rounds=5000$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0`
4. `$6$rounds=1400$anotherlongsaltstring`, `a very much longer text to encrypt.  This one even stretches over morethan one line.` (two spaces after "encrypt."; the two C string pieces join WITHOUT a space: "...over more" + "than one line.") -> `$6$rounds=1400$anotherlongsalts$POfYwTEok97VWcjxIiSOjiykti.o/pQs.wPvMxQ6Fm7I6IoYN3CmLs66x9t0oSwbtEW7o7UmJEiDwGqd8p4ur1`
5. `$6$rounds=77777$short`, `we have a short salt string but not a short password` -> `$6$rounds=77777$short$WuQyW2YR.hBNpjjRhpYD/ifIw05xdfeEyQoMxIXbkvr0gge1a1x3yRULJ5CCaUeOxFmtlcGZelFl5CxtgfiAc0`
6. `$6$rounds=123456$asaltof16chars..`, `a short string` -> `$6$rounds=123456$asaltof16chars..$BtCwjqMJGx5hrJhZywWvt0RLE8uZ4oPwcelCjmw2kSYu.Ec6ycULevoBK25fs2xXgMNrCzIMVcgEJAstJeonj1`
7. `$6$rounds=10$roundstoolow`, `the minimum number is still observed` -> `$6$rounds=1000$roundstoolow$kUMsbe306n21p9R.FRkW3IGn.S9NPN0x50YhH1xhLsPuWGsUSklZt58jaTfF4ZEQpyUNGc0dqbpBYYBaHHrsX.`

Caveat: the vectors were read through a summarising fetch tool, not copied byte for byte by me; re-check the hash strings against the Drepper file (or `openssl passwd -6 -salt saltstring 'Hello world!'` / `mkpasswd -m sha-512`) before freezing them in the test. Vector 1 is also the well-known `openssl passwd -6` example.

## Probes to run first (cheap, on the dev host / one VM)

1. `Get-CimClass root\cimv2 -ClassName Win32_PerfRawData_HvStats_HyperVHypervisorVirtualProcessor, ...HyperVVirtualStorageDevice, ...NvspNicStats_HyperVVirtualNetworkAdapter` (property lists) and `Get-CimInstance` of each with a running VM (instance names).
2. `InitiateReboot` on a running Ubuntu 24.04 and Debian 13 guest: return value and wall time to agent heartbeat.
3. Effective sshd settings on each image after vendor-data: `sshd -T | grep -Ei 'permitrootlogin|passwordauthentication'`.

## Not confirmed

- Real return value and timing of InitiateReboot on Ubuntu 24.04 / Debian 13 (only the kernel and API documentation support it).
- Exact WMI class property names and instance-name formats for the three perf classes.
- Real Hetzner YAML for `public-keys`, `userdata`, `network-config`, `vendor_data` (not documented; derived from cloud-init's reader).
- Error code for reset_password on a non-running server (not in the spec).
- Ubuntu 26.04 and Debian 12/13 sshd drop-in defaults for root password login.
