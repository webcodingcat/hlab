# Fault injection: research (2026-11-24)

Status legend: CONFIRMED = stated by a primary source; UNCONFIRMED = no primary source found, needs a test on the real machine (nothing here was executed; no shell was available to the researcher).

## 1. Hetzner rate limiting

Source of truth: the frozen specs `%LocalAppData%\hlab\openapi\cloud.spec.json` and `hetzner.spec.json` (section "Rate Limiting" in the top-level `description`), same text as https://docs.hetzner.cloud/reference/cloud and https://docs.hetzner.cloud/reference/hetzner.

- CONFIRMED: all requests, authenticated or not, are rate limited. When the limit is reached the request gets `429 Too Many Requests`. "Burst requests are allowed."
- CONFIRMED: default limit 3600 requests per hour and per Project. Remaining requests "increase gradually"; example in the spec: with 3600/h the remaining count rises by 1 every second (token bucket, capacity 3600, refill 1/s).
- CONFIRMED headers (names, prose only):
  - `RateLimit-Limit`: total number of requests per hour.
  - `RateLimit-Remaining`: requests remaining in the current time frame.
  - `RateLimit-Reset`: "a UNIX timestamp of the point in time when your rate limit will have recovered, and you will have the full number of requests available again" (absolute epoch seconds, not a delta; the spec does not say seconds vs. milliseconds, seconds is the plain reading of "UNIX timestamp"). So Reset = now + (Limit - Remaining) seconds under the 1/s refill.
- CONFIRMED error: the error table lists HTTP 429, code `rate_limit_exceeded`, "Error when sending too many requests." Body is the normal error envelope `{"error":{"code":"rate_limit_exceeded","message":"...","details":null}}` (envelope per the spec's error schema; hcloud-go `schema.Error` has code, message, details). The exact `message` text is not documented. hlab already maps the code: `internal/api/errors.go:34`.
- UNCONFIRMED: `Retry-After` is not mentioned anywhere in either spec (0 hits). Whether 429 responses themselves carry the three headers, and whether failed requests count, is not documented.
- Storage Boxes (`api.hetzner.com/v1`, `hetzner.spec.json`): the spec contains the identical "Rate Limiting" text (3600/h per Project, same three headers, 1/s refill), so documented as the same. Real-world behaviour of that host not verified.
- Repo / OpenAPI declaration: `rg RateLimit` finds nothing in the repository. In both spec files the headers appear only in prose: no `components.headers`, no `RateLimit-*` response header objects, and no `"429"` response is declared on any operation (only listed in the error-code table). Conclusion: schema-driven conformance tests cannot check the headers; the contract is the prose above.

## 2. Hyper-V pause (Suspend-VM / Resume-VM)

- CONFIRMED: `Suspend-VM -Name X` "suspends, or pauses" a VM; `Resume-VM` returns it to Running. (https://learn.microsoft.com/powershell/module/hyper-v/suspend-vm). Get-VM state name is `Paused`; WMI `Msvm_ComputerSystem.EnabledState` 32768 = Paused per the docs (corrected by E26: a paused VM reports 9) (https://learn.microsoft.com/windows/win32/hyperv_v2/msvm-computersystem). `OnTimeInMilliseconds` excludes paused time. `Paused-critical` (32785) is the low-host-disk-space variant, not the same as a user pause.
- CONFIRMED: a paused VM keeps its memory allocated (https://learn.microsoft.com/windows-server/virtualization/hyper-v/best-practices-analyzer/avoid-pausing-a-virtual-machine).
- UNCONFIRMED (docs silent; standard behaviour is that vCPUs stop, so guest executes nothing, no guest-initiated disk or network I/O, guest clock jumps on resume and timesync/chrony must step it; in-flight I/O and packets queued at the host side are not specified): test with a ping and a `dd` loop, and look at `dmesg` for soft-lockup/RCU warnings after resume.
- UNCONFIRMED: `Stop-VM -TurnOff` on a Paused VM and `Save-VM` on a Paused VM. Docs only say TurnOff is "equivalent to disconnecting the power" and Save "similar to hibernating". Nothing says either is rejected or accepted from Paused. Test both; plan the runner to `Resume-VM` first if TurnOff fails (safe fallback). `Stop-VM` has no pause-specific text.
- Pitfall: the `hlab-agent` appliance is a router; pausing it stops all routing for the VMs behind it, which is the intended fault but also stops hlab's own guest agent channel.

## 3. Hyper-V disk QoS (`Set-VMHardDiskDrive -MaximumIOPS / -MinimumIOPS`)

- CONFIRMED parameters (UInt64): "-MaximumIOPS: maximum normalized IOPS ... Hyper-V calculates normalized IOPS as the total size of I/O per second divided by 8 KB" (same for -MinimumIOPS). https://learn.microsoft.com/powershell/module/hyper-v/set-vmharddiskdrive. Addressing: `-VMName X -ControllerType SCSI -ControllerNumber n -ControllerLocation m`, or pipe a `Get-VMHardDiskDrive` object.
- CONFIRMED: Storage QoS "requires that the Hyper-V role is installed"; per-VHD limits are defined without any policy; a disk without a max/min defaults to 0, so 0 = no limit (https://learn.microsoft.com/previous-versions/windows/it-pro/windows-server-2012-r2-and-2012/dn282281(v=ws.11)). Not available for shared VHDX. The 8 KB normalization size is only changeable on a storage cluster (Storage QoS overview, Windows Server docs); on a standalone host it is the fixed default.
- Minimum IOPS is only a notification threshold (event when not met), not a reservation or guarantee. Do not use it for fault injection; use Maximum only.
- UNCONFIRMED: applied live on a running VM (docs do not say; Hyper-V Manager exposes QoS for running VMs and the 2012 R2 docs show no restart requirement; verify by running a fio/dd test before and after with the VM running). UNCONFIRMED: Windows 10/11 client Hyper-V support: the cmdlet is in the same Hyper-V module, the docs list no client restriction, but no source states it. Test on this host (Windows 11, build 10.0.26300).
- Pitfall: because of 8 KB normalization a 64 KB I/O costs 8 IOPS; set the limit with block size in mind.

## 4. Hot-removing a SCSI VHDX from a running Linux guest

- UNCONFIRMED by primary docs: no Microsoft page describes the Linux guest's view. Known from kernel/vendor reports (indirect): the guest's `sd` device goes away or "offline", writes fail with I/O errors, XFS logs a shutdown and ext4 follows `errors=` (default continue/remount-ro). Red Hat KB 3980771 and bug 1502601 show "rejecting I/O to offline device" followed by XFS shutdown, requiring umount and repair. A Microsoft Azure troubleshooting page (https://learn.microsoft.com/troubleshoot/azure/virtual-machines/linux-kernel-panics-upgrade) links a kernel panic on old RHEL/CentOS guests to removing a SCSI disk from a running VM (SCSI locking bug); Debian 13 / 6.12 is far newer, but it is a risk marker.
- Re-adding at the same controller/location: UNCONFIRMED. Expected: a new SCSI device is registered (same LUN address, possibly a new `sdX` name); the old mounted filesystem is bound to the dead device and does not recover; it must be `umount -l`'d and remounted, and the fs may need `fsck`/xfs_repair. Mount by UUID or `/dev/disk/by-path`, never by `sdX`.
- Pitfall for the plan: this fault is destructive to a mounted filesystem by design. Use only on a data disk with a throwaway or recoverable filesystem, never on the boot disk. Test the full cycle (remove, observe, re-add, remount) on a real VM before promising it.

## 5. Linux tc netem and nftables on Debian 13

Versions on trixie (https://manpages.debian.org): kernel 6.12, iproute2 6.15.0, nftables 1.1.3.

### netem
- CONFIRMED syntax (https://man7.org/linux/man-pages/man8/tc-netem.8.html): `delay TIME [JITTER [CORRELATION]] [distribution uniform|normal|pareto|paretonormal]`, times in ms (suffix `ms` accepted); `loss random PERCENT [CORRELATION]`, `loss state ...`, `loss gemodel ...`; `limit COUNT`.
- SURPRISE, CONFIRMED: netem loss and corruption can be seeded. tc-netem(8) on trixie: "seed: Specifies a seed to guide and reproduce the randomly generated loss or corruption events." Kernel attribute `TCA_NETEM_PRNG_SEED`, patch series from Aug 2023 (https://lkml.iu.edu/hypermail/linux/kernel/2308.1/08993.html: same seed gives the same loss pattern; example: seed 42424242, 10% loss drops 5th, 12th, 24th of 25 packets). The kernel release is not stated in the sources; patches were netdev/net-next in Aug 2023, so it is expected from 6.6, hence present in 6.12 (UNCONFIRMED exact version; check with `tc qdisc add ... netem loss 10% seed 1` on the appliance). Seed covers loss/corruption only, not delay jitter. Determinism is per qdisc instance and per packet count seen by that qdisc.
- Delay jitter reorders packets; the man page says attach a `pfifo` child to netem if reordering is unwanted. Man page also warns netem combined with other qdiscs "may not always work".
- `limit` defaults to 1000 packets; delay x throughput above that drops packets: raise `limit` for delay faults.

### Per-address delay with filters (standard pattern, not executed)
- Both directions: apply on egress of every router interface. A->B leaves via B's interface and B->A via A's interface, so egress shaping with `src_ip`/`dst_ip` filters on both interfaces covers both directions. `ifb` is only needed to queue on ingress (mirred redirect to an ifb device); not needed here. VRF slave devices accept qdiscs normally.
- Flower syntax (https://man7.org/linux/man-pages/man8/tc-flower.8.html): `tc filter add dev IF parent 1: protocol ip prio 1 flower src_ip A dst_ip B/len classid 1:10`; `src_ip/dst_ip` need `protocol ip` (or ipv6), no `ip_proto` required. Filters need `cls_flower` (or `cls_u32`); `u32` form: `u32 match ip dst A/32 match ip src B/32 flowid 1:10`.
- prio: `tc qdisc add dev IF root handle 1: prio bands N priomap ...` with netem on `parent 1:k`; max 16 bands, strict priority; make default priomap point at a band without netem. HTB (recommended for many pairs): `tc qdisc add dev IF root handle 1: htb default 1`, `tc class add dev IF parent 1: classid 1:1 htb rate 10gbit`, per fault `tc class add dev IF parent 1:1 classid 1:10 htb rate 10gbit`, `tc qdisc add dev IF parent 1:10 handle 10: netem delay 50ms 10ms`, filter as above. Pitfall: an HTB rate lower than the real traffic throttles everything, set it high; the default class must be unshaped.
- Pitfalls: (a) GRO/GSO/TSO: qdiscs and netfilter see aggregated skbs, so "per packet" loss/delay counts skbs, not wire packets; turn offloads off (`ethtool -K IF gro off gso off tso off`) on the router interfaces if exact per-packet behaviour matters (reasoned, UNCONFIRMED for hv_netvsc/tap). (b) Modules `sch_netem`, `sch_htb`, `sch_prio`, `cls_flower` must exist in the appliance kernel (stock Debian `linux-image-amd64` has them as modules; the cloud kernel variant not checked). (c) Replacing a qdisc resets its netem state and seed sequence.
- nftables interaction: tc egress runs after the netfilter postrouting hook, so nft drops happen before netem; nft can also classify: `meta mark set 0x10` then `tc filter ... handle 0x10 fw classid 1:10`. VRF: the kernel VRF doc says PREROUTING/INPUT rules can match either the real ingress device or the VRF device as `skb->dev` (https://docs.kernel.org/networking/vrf.html), which suggests some hooks are traversed with two devices; the doc does not say hooks run twice for forwarded traffic. UNCONFIRMED: to keep a counter-based rule from being evaluated twice per packet, use the `forward` hook (or pin `iifname`) and verify with `nft monitor trace` or a counter.

### Deterministic drop pattern with nftables numgen
- CONFIRMED syntax (nft(8) trixie, nftables 1.1.3): `numgen { inc | random } mod NUM [offset NUM]`. Documented uses: `dnat to numgen inc mod 2 map { 0 : A, 1 : B }` and `numgen random mod 100 vmap { 0-49 : jump c1, 50-99 : jump c2 }` (https://wiki.nftables.org/wiki-nftables/index.php/Load_balancing). A comparison form is known to work: `numgen random mod 100 < 5 drop` (https://serverfault.com answer mirrored at kiwix, numgen since Linux 4.9 / nft 0.7).
- CONFIRMED kernel behaviour (net/netfilter/nft_numgen.c): `inc` is one atomic counter per rule expression, initialised to `modulus-1`, so the first evaluation returns 0, then wraps `(old+1 < modulus) ? old+1 : 0`, result plus `offset`. The counter advances only when the expression is evaluated, so put the address matches before `numgen` in the same rule (left to right). It is shared by all flows matching that rule, and reset when the rule is (re)loaded; changing set/map elements does not reset it.
- Proposed rule (UNCONFIRMED, not run on nft 1.1.3; the vmap form is the one closest to documented usage, the plain-set form is likely also valid):
  - vmap: `ip saddr 10.0.1.5 ip daddr 10.0.2.7 numgen inc mod 1000 vmap { 3 : drop, 17 : drop, 42 : drop }` (a miss in the vmap should fall through to the next rule).
  - set: `ip saddr 10.0.1.5 ip daddr 10.0.2.7 numgen inc mod 1000 { 3, 17, 42 } counter drop`.
  - named map: `map pat { typeof numgen inc mod 1000 : verdict; elements = { 3 : drop, 17 : drop } }` and rule `numgen inc mod 1000 vmap @pat` (typeof support for such maps reported from nft 0.9.4, still to verify on 1.1.3; ranges need `flags interval`).
  - hook: `table inet fault { chain fwd { type filter hook forward priority 0; policy accept; ... } }`.
- Pitfalls: determinism is per skb evaluated (see GRO above); a pattern of K drops per N packets is exact only if every packet of the stream passes this one rule once; do not mix `random` mode; large modulus values are fine (u32); nft rejects `mod 0`.

## Surprises
1. netem has a `seed` for loss and corruption (iproute2 6.15 on trixie), so seeded netem loss is possible after all; nftables numgen is still the safest exact-pattern tool.
2. RateLimit headers are not declared as headers or a 429 response in either OpenAPI file, only described in prose; Storage Box spec carries the identical text.
3. Docs say nothing on Save-VM/TurnOff from Paused, live QoS change, Windows 11 QoS support, or the Linux guest's view of a hot-removed disk: all need a real-host test.
