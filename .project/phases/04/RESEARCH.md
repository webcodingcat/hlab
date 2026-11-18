# Phase 04 research

Checked 2026-11-18. No command could be run during this research (documents
and web sources only), so everything marked "verify" must be confirmed by the
first e2e run or a one-off experiment. Frozen description = the cached
`cloud.spec.json` (OpenAPI 3.1.2).

## Findings that refute or qualify CONTEXT.md

1. The frozen description names NO operation-specific error code for: a volume
   attached elsewhere, a volume in another location, a backup on a server
   without backups, an image larger than the server type disk. CONTEXT says
   "refused with the description's code"; there is none. Only the generic table
   (`invalid_input` 422, `resource_unavailable` 412, `locked` 423, `protected`
   423, `unsupported_error` 422, `service_error` 422, ...) and a few
   operation tables exist (see topic 7). hlab must choose; proposal in topic 7.
2. Debian 13 generic uses `PARTUUID=` (not `UUID=`) for `/` and `/boot/efi` in
   fstab and `root=PARTUUID=` on the kernel line; new GPT partition GUIDs
   therefore must be written into fstab and grub.cfg (topic 3).
3. `xfsprogs` (mkfs.xfs, xfs_admin) is missing from the Debian 13 generic
   image, as are its dependencies `libicu76` and `libinih1`. Everything else
   the agent jobs need is present (topic 4).
4. Neither Debian 13 generic nor Ubuntu 24.04 cloud image ships the Hyper-V VSS
   daemon, so a Production checkpoint always falls back to Standard
   (memory-saving); set `Standard` explicitly (topic 1).
5. Ubuntu live-server: that the live session reads the cidata (NoCloud) ISO is
   documented; that it applies `network-config` (static /32 + on-link route,
   no DHCP in hlab) and injects SSH keys is NOT documented. Unclear; CONTEXT's
   Q2 plan cannot be confirmed or refuted from documents (topic 5).
6. Online resize: the guest sees the new size without a manual rescan only if
   the kernel has the storvsc fix of Nov 2022; likely true for Debian 13
   (6.12) and Ubuntu 24.04 (6.8), unverified for Ubuntu 22.04 (5.15) (topic 2).
7. Debian ISO URLs under `debian-cd/current/` break at the next point release
   (the file moves to `cdimage/archive/<ver>/`); pin both (topic 6).

## 1. Crash-consistent copy of a running VM disk

- Checkpoint types (`Set-VM -CheckpointType`): `Disabled`, `Standard`,
  `Production` (production if the guest supports it, else standard),
  `ProductionOnly` (else the operation fails). Standard saves memory state;
  Production uses VSS or, on Linux, file-system freeze via the guest daemon,
  no memory state.
  https://learn.microsoft.com/en-us/powershell/module/hyper-v/set-vm?view=windowsserver2025-ps
  https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/checkpoints
- The Linux freeze needs `hv_vss_daemon` (Debian package `hyperv-daemons`).
  Package lists: Debian 13 generic amd64 (build 20261116-2664) does not contain
  `hyperv-daemons` or `linux-cloud-tools`
  (https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.json);
  Ubuntu 24.04 cloud image manifest contains neither
  (https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.manifest).
  Ubuntu 22.04 / 26.04 manifests not checked. Consequence: with `Production`
  the result is a Standard checkpoint anyway, possibly after a failed freeze
  attempt of unknown duration. Recommendation: `Set-VM -CheckpointType
  Standard` on every hlab VM at creation (deterministic, no freeze attempt).
  The disk copy is crash-consistent in both cases (guest page cache is not
  flushed), which matches CONTEXT.
- Sequence: `Checkpoint-VM -VM $vm -SnapshotName <name> -Passthru`; the VM's
  disk path now points at an `.avhdx` child and the old `.vhdx` is its
  read-only parent holding the state at checkpoint time. hlab's server disks
  are full copies, not differencing disks (`CreateDisk` = `Copy-Item` +
  `Set-VHD -ResetDiskIdentifier`), so the parent is standalone: `Copy-Item`
  parent -> target, `Set-VHD -Path target -ResetDiskIdentifier -Force`, then
  `Remove-VMCheckpoint -VMSnapshot $cp`. Use the returned object / its Id,
  not the localized default name.
- Merge: "When you delete a checkpoint, Hyper-V merges the .avhdx and .vhdx
  files"; do not delete `.avhdx` files by hand (same Learn page). Online
  (live) merge of a running VM is supported by Hyper-V since 2012 R2; the
  cmdlet returns before the merge ends. Wait for completion by condition with a
  deadline: the VM's `Get-VMHardDiskDrive` path no longer ends in `.avhdx`
  (never parse status text). Duration is proportional to the data written
  since the checkpoint (small for a short snapshot) plus the `.vmrs`
  memory-state write (about VM RAM size) at checkpoint time. No documented
  figure; measure in the e2e.
- Parent copy while the VM runs: the parent is opened read-only by the VM
  worker process; reading it is the normal path of backup tools. Not stated
  in Microsoft's cmdlet docs: verify `Copy-Item` has no sharing violation.
- Elevation: all cmdlets above go through VMMS and need Hyper-V
  Administrators only (E7 already confirmed `Set-VHD -ResetDiskIdentifier`
  without elevation). Reading the parent file needs the hlabd account to have
  read access through the data-folder ACL (VMMS adds an ACE for the VM SID but
  keeps inherited ACEs). Not confirmed for `.vhdx` created by hlab; verify.
- Automatic checkpoints: `Set-VM -AutomaticCheckpointsEnabled $false` is
  already set in `CreateVM`. It does not block manual `Checkpoint-VM`; only
  `CheckpointType Disabled` does.
- Pitfalls: (a) a checkpoint covers every disk of the VM including volumes
  (they get `.avhdx` children until the merge); `Resize-VHD` on a disk with a
  checkpoint fails, so the action lock of the server must also exclude volume
  resize for the whole snapshot; (b) a crash between checkpoint and removal
  leaves a checkpoint: on start-up remove checkpoints named with hlab's prefix;
  (c) stopped or saved VM: no checkpoint needed, copy the disk directly (a
  saved VM's disk is not open); (d) the copy keeps the source's GPT GUIDs,
  PARTUUIDs and filesystem UUIDs; harmless except when two such disks meet in
  one guest (rescue is prepared separately, topic 3).

## 2. Online Resize-VHD of a hot-added SCSI VHDX

- Host: `Resize-VHD -Path <vhdx> -SizeBytes <n>` expands VHD/VHDX and can be
  done online only if the disk is on the VM's SCSI chain (documented).
  `-SizeBytes` is UInt64; no elevation beyond Hyper-V Administrators expected
  (not stated; verify). Fails while the disk has a checkpoint (see topic 1).
  https://learn.microsoft.com/en-us/powershell/module/hyper-v/resize-vhd?view=windowsserver2025-ps
  The existing `ResizeDisk` already issues this (internal/platform/hyperv/disks.go).
- Guest: storvsc logs "Capacity data has changed". Older kernels do not
  revalidate the disk, so `lsblk` keeps the old size until
  `echo 1 > /sys/block/sdX/device/rescan` (Red Hat bug 2139728, reproduced on
  Ubuntu guests too). Upstream commit b8a5376c321b "scsi: storvsc: Fix handling
  of srb_status and capacity change events" (Michael Kelley, merged
  2022-11-25) makes capacity-change events trigger an immediate rescan.
  https://bugzilla.redhat.com/show_bug.cgi?id=2139728
  https://github.com/torvalds/linux/commit/b8a5376c321b4669f7ffabc708fd30c3970f3084
  Kernels: Debian 13 6.12 and Ubuntu 24.04 6.8 are newer than the fix; Ubuntu
  22.04 GA 5.15 depends on a stable backport (not checked); a Red Hat
  recurrence on 4.18.0-553 (Nov 2024) was reported but not explained. So: do
  not assume it; any test that checks the guest size must tolerate a rescan
  (use the rescan write in the test, or wait for the condition with a
  deadline). hlab does not grow the guest filesystem (CONTEXT), so the product
  needs no guest action.
- UNIT_ATTENTION is also raised when a differencing disk is added; unrelated.

## 3. rescue-prepare on a copy of the Debian 13 generic image

Source: Debian cloud-images build config (salsa.debian.org cloud-team/
debian-cloud-images, `config_space/13`), not the image itself (could not read
the disk).

- fstab (AMD64 class): `PARTUUID={%PARTUUID_ROOT%} / ext4
  rw,discard,errors=remount-ro,x-systemd.growfs 0 1` and
  `PARTUUID={%PARTUUID_ESP%} /boot/efi vfat defaults,umask=077 0 2`.
- Kernel: `GRUB_DISABLE_LINUX_UUID=true`, `GRUB_DISABLE_LINUX_PARTUUID=`
  (empty), so `root=PARTUUID=<root partuuid>` on every `linux` line.
- `/boot/grub/grub.cfg` is generated at build time with a stub grub-probe and
  sed placeholders: `##DEV##` -> `PARTUUID=<root partuuid>`, `##FSUUID##` ->
  root filesystem UUID (used in `search --fs-uuid`), `##PARTUUID##` -> root
  partuuid. grub-install ran with `--no-nvram --no-bootsector --force
  --force-extra-removable --skip-fs-probe --modules="ext2 part_gpt"`; the root
  filesystem UUID is a fixed value (`mkfs.ext4 -U $FSUUID_ROOT`).
  https://salsa.debian.org/cloud-team/debian-cloud-images/-/tree/master/config_space/13
- So three places hold identifiers: fstab (root + ESP PARTUUID),
  `/boot/grub/grub.cfg` (root PARTUUID and root FS UUID), and the ESP's
  `EFI/debian/grub.cfg` (expected: `search.fs_uuid <root FS UUID> root ...`,
  then `configfile` of `/boot/grub/grub.cfg`; written by grub-install, the
  stock Debian behaviour; NOT confirmed for this image, verify). If the ESP
  stub is left unchanged, the firmware boot of the rescue disk searches the
  old root FS UUID and, with the server disk (same UUID) at LUN 0, can load
  the SERVER's grub.cfg and kernel. This is the main reason rescue-prepare
  exists.
- Procedure in the agent job (disk found by SCSI LUN, E7): read old values
  with `blkid -s UUID/PARTUUID/LABEL` before changing anything; `sgdisk -G
  /dev/sdX` (randomizes disk GUID and all partition GUIDs; `-u N:R`, `-U R`
  are the single forms); `partprobe` or re-read; `tune2fs -U <new>` on the
  unmounted ext4 root (needs e2fsprogs; Debian 13 mkfs creates `metadata_csum`
  filesystems: changing the UUID rewrites checksums and tune2fs wants a clean
  `e2fsck -f` first, or enable `-O metadata_csum_seed` first, as tune2fs
  documents for changing the UUID of a mounted fs; exact refusal text not
  confirmed, run `e2fsck -f -p` before); `tune2fs -L`, `fatlabel` for labels
  if the image has any (unknown which; fstab does not use labels); optionally
  `fatlabel -i` for the vfat volume ID (dosfstools 4.2 in trixie; option name
  from memory, verify). Then mount ESP and root and replace every occurrence
  of the OLD values with the NEW ones in: `/etc/fstab`, `/boot/grub/grub.cfg`,
  `EFI/debian/grub.cfg` (and any other `grub.cfg` under the ESP; use
  `grep -rl <old>` rather than a fixed list). Generate UUIDs in Go and pass
  them down (`uuidgen` is present via `uuid-runtime`, but Go is simpler).
  Do not run `update-grub` (it needs the stub grub-probe the build used).
- Boot at LUN 63 with Secure Boot next to a Debian server disk at LUN 0: E7
  proved the firmware boots the disk set by `FirstBootDevice` and that LUN 0
  stays `sda` (E7 used an unprepared Debian copy while LUN 0 was Ubuntu). The
  prepared disk boots through the removable path
  (`EFI/BOOT/BOOTX64.EFI` = shim, present because of
  `--force-extra-removable`) without NVRAM entries; shim and signed grub are
  identical on both disks, only the identifiers differ. Not yet tested: both
  disks Debian; the e2e SRV-11 covers it.
- Secure Boot template: shims in current distribution media are dual-signed
  with the Microsoft UEFI CA 2011 and 2023 per vendor reports; the
  `MicrosoftUEFICertificateAuthority` template reportedly puts only the UEFI CA
  into db. A shim signed only with the 2023 CA may fail on a VM whose db lacks
  it (toggle the template to refresh). Not seen in E2 (both images booted
  2026-11-18); watch for it with new ISOs.
  https://wiki.debian.org/SecureBoot/CAChanges
  https://docs.citrix.com/en-us/provisioning/current-release/advanced-concepts/secure-boot-cert-expiration-ca-updates.html
- The rescue disk is a differencing disk of the prepared base; reset its
  identifier (`Set-VHD -ResetDiskIdentifier`, E7). Never write to the base
  after children exist.
- Rescue seed (cidata in slot 0) needs its own instance-id, root password
  hash (`chpasswd` with `ssh_pwauth` true) and keys; the server's own seed is
  restored by `disable_rescue`.

## 4. Tools in the Debian 13 generic image

From the image's package list (build 20261116-2664):

| Needed | Package | In image |
|---|---|---|
| mkfs.ext4, tune2fs, e2fsck | e2fsprogs | yes |
| sgdisk | gdisk | yes |
| sfdisk, blkid, lsblk, mount, wipefs | util-linux (+ fdisk) | yes |
| mkfs.fat, fatlabel, fsck.fat | dosfstools | yes |
| uuidgen | uuid-runtime | yes |
| mkfs.xfs, xfs_admin | xfsprogs 6.13.0-2+deb13u1 | NO |
| xfsprogs deps | libicu76, libinih1 | NO (libdevmapper1.02.1, libedit2, liburcu8t64, python3, python3-dbus, libblkid1, libuuid1 yes) |

Also not in the image: `haproxy`, `nftables` (agent uses netlink), `rsync`.
So the closure to add for XFS is exactly three packages: `xfsprogs`,
`libicu76` (about 10 MB), `libinih1`. xfsprogs 1.1 MB, sha256
79e00160bfc45d25a46d2709edd364afb41723d9b96713de8f6c80b6a17f2d74,
http://deb.debian.org/debian/pool/main/x/xfsprogs/xfsprogs_6.13.0-2+deb13u1_amd64.deb
(https://packages.debian.org/trixie/amd64/xfsprogs/download).

Appliance facts: no image build step exists (Windows host, image converted
from the published qcow2/raw); `appliance/user-data.yaml` installs no
packages; the cidata instance-id is the stable `applianceName()`, so
per-instance cloud-init modules run once per appliance disk.

Options:
- A. Build into the image: not possible without a Linux builder; the nearest
  equivalent is cloud-init `packages: [xfsprogs]` at first boot of the
  appliance disk (once per instance, apt resolves dependencies, needs the
  internet via the host NAT at that moment and re-runs for a new appliance disk).
- B. Cache the .debs locally: `catalog/appliance-debs.json` with URL and
  sha256 for the three .debs; hlabd downloads once into
  `<data>\cache\debs\`, puts them on the appliance cidata ISO as extra files
  (the seed already carries the agent binary); the install script runs
  `dpkg -i` only if `mkfs.xfs` is absent. Offline at boot, idempotent, no apt.
  Weakness: `pool/` keeps only the current version of a package, so a pinned
  URL breaks after a security update of xfsprogs/libicu76; add a
  `snapshot.debian.org` timestamped URL as second source (permanent). The same
  mechanism will be needed for HAProxy in the load-balancer phase.

Recommendation: B (user excluded per-boot installation and asked for cached
files; it stays offline and testable with a fake download). Fall back to A
only if B's maintenance becomes a burden. Either way the `mkfs` job must fail
with a clear message when `mkfs.xfs` is missing. ext4 needs nothing.

## 5. Catalog ISO from SCSI DVD slot 1 on Gen2

- Secure Boot: Ubuntu live-server and Debian netinst boot via a Microsoft-
  signed shim, which is what the `MicrosoftUEFICertificateAuthority` template
  is for; both were not tested here (E2 tested the cloud images only). Verify
  in the first SRV-15 run. Boot order: `Set-VMFirmware -FirstBootDevice` to the
  slot 1 `DvdDrive`; ISO insert/eject through `Set-VMDvdDrive -Path` works on a
  running VM.
- Does the Ubuntu live-server session read the cidata ISO in slot 0? Yes for
  user-data: cloud-init runs in the live session so installs can be completed
  over SSH; NoCloud data on a filesystem labelled `CIDATA` (or
  `ds=nocloud-net;s=...`) is the documented delivery of autoinstall data, and
  the default cloud.cfg generates a random password for the installer account.
  https://discourse.ubuntu.com/t/cloud-init-and-the-live-server-installer/14597
  https://canonical-subiquity.readthedocs-hosted.com/en/latest/tutorial/providing-autoinstall.html
- NOT documented, so unclear: (a) whether the live session applies
  `network-config` from the seed (hlab has no DHCP: static /32 with on-link
  default route; the live session defaults to DHCP on all interfaces; subiquity's
  network controller does not read cloud-init files itself, it takes whatever
  netplan holds); (b) whether `ssh_authorized_keys` / meta-data `public-keys`
  reach a usable account in the live session (the documented route is the
  generated installer password shown on the console, which a test cannot read
  without the serial pipe). Community threads report mixed results on 20.04.
  Result: CONTEXT Q2's primary proof is plausible but unconfirmed; run it once
  and keep the indirect proof as the fallback (do not block the plan).
  Possible hardening, all unverified: add a user with `ssh_authorized_keys` and
  `chpasswd` in user-data (hlab already writes vendor-data with `chpasswd`);
  an `ssh` check of `/proc/cmdline` or `findmnt /` showing a casper/overlay
  root proves "live system".
- Debian netinst has no cloud-init; no SSH proof is possible. Booting is
  observable only through the DVD state in Hyper-V (and the serial pipe if the
  installer prints to ttyS0).
- Size warning: the Ubuntu 24.04.5 live-server ISO is 3.8 GB, 26.04.1 2.7 GB,
  22.04.5 2.0 GB, Debian netinst about 0.76 GB; cache time and disk space for
  the SRV-18 disk rule.

## 6. ISO sources (checked 2026-11-18)

Checksum files list several point releases per directory, so look up by file
name. Ubuntu SHA256SUMS are signed (`SHA256SUMS.gpg`), Debian has
`SHA256SUMS.sign` and `SHA512SUMS`.

| ISO | URL | SHA256 |
|---|---|---|
| ubuntu-22.04 live-server | https://releases.ubuntu.com/22.04/ubuntu-22.04.5-live-server-amd64.iso (2024-09-11) | 9bc6028870aef3f74f4e16b900008179e78b130e6b0b9a140635434a46aa98b0; sums: https://releases.ubuntu.com/22.04/SHA256SUMS |
| ubuntu-24.04 live-server | https://releases.ubuntu.com/24.04/ubuntu-24.04.5-live-server-amd64.iso (2026-09-09, 3.8 GB) | 97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8; sums: https://releases.ubuntu.com/24.04/SHA256SUMS (also lists 24.04.3, 24.04.4) |
| ubuntu-26.04 live-server | https://releases.ubuntu.com/26.04/ubuntu-26.04.1-live-server-amd64.iso (2026-08-26, 2.7 GB) | cc8a95cde20f6ced61a322420de00f10cc3c90ced545daa46cb9c1a117f1d927; sums: https://releases.ubuntu.com/26.04/SHA256SUMS (also 26.04) |
| debian-12 netinst | https://cdimage.debian.org/cdimage/archive/12.15.0/amd64/iso-cd/debian-12.15.0-amd64-netinst.iso | cd4462c06aa8892e692c0c4b9c17802f38c8ab8690e85cbfb5ccaa5956e9af17; sums: https://cdimage.debian.org/cdimage/archive/12.15.0/amd64/iso-cd/SHA256SUMS |
| debian-13 netinst | https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/debian-13.7.0-amd64-netinst.iso (2026-09-12, 756 MB) | a7ef94ac2fb9a7fec454552abd629b7cc9d5155c886165a45649f5ce6167e355; sums: https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/SHA256SUMS |

Pitfalls: `debian-cd/current/` holds only the newest 13.x file; at 13.8.0 the
13.7.0 file moves to `https://cdimage.debian.org/cdimage/archive/13.7.0/amd64/iso-cd/`.
Give `_hlab.source` an ordered URL list (current, then archive) or a
resolver; the existing `images.json` uses one `url` plus `checksum_url`, so a
single URL needs a fallback field. The `releases.ubuntu.com` directories keep
older point releases (24.04.3, .4, .5 present), which are safer to pin than
Debian's `current`. Debian 12 12.15.0 (2026-07-11) is the latest bookworm
point release in the archive listing (https://cdimage.debian.org/cdimage/archive/).

ISO object in the frozen description (`GET /isos`, `GET /isos/{id}`; list
filters `name`, `architecture`, `include_architecture_wildcard`, `page`,
`per_page`): required fields `id` (int64), `name` (string|null, only set for
public ISOs), `description` (string), `type` (`public`|`private`|null),
`deprecation` (object `{unavailable_after, announced}` or null),
`architecture` (`x86`|`arm`|null). `attach_iso` takes `iso` (ID or name);
a previously attached ISO is detached first; "Servers with attached ISOs
have a modified boot order: they try the ISO first before falling back to
hard disk" (description of `attach_iso`).

## 7. Hetzner behaviour per the frozen description

- `enable_rescue` (POST `/servers/{id}/actions/enable_rescue`): body `type`
  (`linux64`, default) and `ssh_keys` (array of int64 key IDs "injected into
  the rescue system"); 201 `{root_password, action}`. "Rescue Mode is
  automatically disabled when you first boot into it or if you do not use it
  for 60 minutes." "Enabling rescue mode will not reboot your Server". Server
  object `rescue_enabled`: "True if rescue mode is enabled. Server will then
  boot into rescue system on next reboot." No error table. Design hint (hlab's
  choice): "first boot into it" needs a signal; options are the VM start
  hlab itself drives, or the agent seeing the rescue seed's first metadata
  request from the server's MAC.
- `create_image` (POST `/servers/{id}/actions/create_image`): body
  `description` (auto-generated if unset), `type` (`snapshot`|`backup`,
  default `snapshot`), `labels`; 201 `{image, action}`. "If the Server is
  currently running you must make sure that its disk content is consistent."
  Backup images "are only available when the backup option is enabled for the
  Server"; snapshots are independent and survive server deletion; backups are
  "bound to the Server and ... deleted when the Server is deleted". Image
  fields (all required): `id`, `type` (`system|app|snapshot|backup`), `status`
  (`available|creating|unavailable`), `name` (null except system),
  `description`, `image_size` (GB number or null, "for snapshot Images the
  value relevant for costs"), `disk_size` (GB number), `created`,
  `created_from` (`{id, name}` or null), `bound_to` (int or null, only for
  backups), `os_flavor`, `os_version`, `rapid_deploy`, `protection{delete}`,
  `deprecation`, `deleted`, `labels`, `architecture`. Backups: enable_backup
  gives "seven slots" and "Backups are automatically created daily";
  `disable_backup` "immediately removes all existing backups"; `backup_window`
  is "Time window (UTC) ... or null if the backups are not enabled".
- `rebuild` (POST `/servers/{id}/actions/rebuild`): body `image` (string: ID
  or name; names only for system images; may be a snapshot or backup) and
  `user_data`; "automatically powered off before the rebuild"; destroys all
  data. No error table.
- Volume create (POST `/volumes`): required `size` (int GB; 10 to 10240
  documented in the description text), `name` (unique per project, 64 chars);
  optional `labels`, `format` (`xfs`|`ext4`), `automount` ("`server` must be
  provided"), `location` (name or ID; "can be omitted if Server is
  specified"), `assignee {id, type}` (required members id and type) and the
  deprecated `server`. "Specifying the Server during Volume creation will
  automatically attach the Volume ... In that case, the `next_actions` key
  in the response is an array which contains a single `attach_volume`
  action." Operation error: `422 no_space_left_in_location`. Combinations the
  description implies: `location` alone = unattached; `server`/`assignee` =
  created in the server's location, attached; `automount` without a server is
  invalid (code unspecified; use `invalid_input`); both `location` and a
  server: unspecified (real API behaviour unknown; use `invalid_input` when
  they differ). Server create has `volumes` ("must be in the same Location")
  and `automount` ("Auto-mount Volumes after attach").
- Volume attach (POST `/volumes/{id}/actions/attach`): required `assignee
  {id, type}` (type example `server`), optional deprecated `server` (int),
  `automount`; 201 `{action}` with resources `server` and `volume`. "Works
  only if the Resource is in the same Location as the Volume." No error
  table. Volume object: `server` deprecated (int|null), `assignee`
  (object|null), `location`, `linux_device` (see volume schema), `format`.
- Error codes: the description's tables contain none for the four requested
  cases. Present operation codes: `no_space_left_in_location` (volume
  create), `server_not_stopped` (422, several), `invalid_server_type` (422,
  change type: "does not fit for the given server or is deprecated"),
  `placement_error`, `primary_ip_*` (server create). The official Go client
  also defines `volume_already_attached`
  (https://github.com/hetznercloud/hcloud-go/blob/main/hcloud/error.go), but
  this is not in the frozen description and its HTTP status is not documented.
  Proposal (validators treat `code` as a free string, so any choice passes
  response validation): attached elsewhere `422 volume_already_attached`;
  volume in another location, backup without backups enabled, image larger
  than the server type disk: `422 invalid_input` with `details.fields`
  (`volume`/`assignee`, `type`, `image`) and a clear message. Real Hetzner
  behaviour for these four could not be confirmed (no document found).
  Generic codes remain from the table: `resource_unavailable` 412 (as CONTEXT
  uses for the disk rule), `locked` 423 (action running), `protected` 423.
- Pagination note in the description: `Link` header deprecated (changelog
  2026-10-12), `per_page` max 50.
