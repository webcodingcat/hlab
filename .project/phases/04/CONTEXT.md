# Phase 04 context

Confirmed with the user at the start of the phase (2026-11-18).

## Assumptions confirmed

- Snapshots and backups are project resources of kind `images`, served by the `images` module together with the catalog system images (list/get, PUT description/labels/type backup→snapshot, DELETE, change_protection); IDs from `store.NextID`, never colliding with catalog IDs; disks at `<data>\images\image-<id>.vhdx`.
- `create_image` locks the server and calls `platform.Snapshot` (running or stopped); `disk_size`, `image_size`, `created_from`, `bound_to` as at Hetzner.
- Servers can be created and rebuilt from snapshots and backups (closes the phase-03 "rebuild from system images only" decision); a new server gets a new instance-id so cloud-init runs again and the marker file survives.
- A server type with a disk smaller than the image's `disk_size` is refused on create and rebuild with the frozen description's code.
- `disable_backup` and server deletion delete the server's backups; snapshots stay. Manual and daily backups share 7 slots; the oldest beyond 7 is deleted. `create_image type=backup` without backups enabled is refused with the description's code.
- Daily backup: a `servers` scheduler job checks hourly on the injected Clock, one backup per server per UTC day while the cloud runs; missed days are not made up; `backup_window` is a fixed string hlab ignores.
- Snapshots of running servers are crash-consistent via a Hyper-V checkpoint; every copy gets a new disk identifier; `hyperv.Snapshot` is implemented in this phase.
- Rescue runs from its own disk at LUN 63 (ARCHITECTURE/DECISIONS); the roadmap's "RAM system" means "does not run from the server disk"; success criterion 2 is checked as: SSH into rescue, `/` is not on `/dev/sda`, `/dev/sda` is the server disk.
- `images.EnsureRescue` prepares the rescue base once from the Debian 13 image via the agent job `rescue-prepare`; the agent gets `POST /v1/jobs` (mkfs, rescue-prepare); disks are found by SCSI LUN, never by host number (E7).
- `enable_rescue` returns the root password and does not reboot; it uses a fresh differencing disk at LUN 63, a rescue seed ISO in DVD slot 0 with its own instance-id, keys and password hash, and puts LUN 63 first in the boot order.
- `disable_rescue` restores the boot order and the server's own seed at once; hlab removes the rescue disk at the next boot it drives or when the VM is seen off.
- ISOs: hand-written `catalog/isos.json` in Hetzner's ISO shape plus `_hlab.source`, pinned public Secure-Boot ISOs (Ubuntu 22.04/24.04/26.04 live-server, Debian 12/13 netinst); `images.EnsureISO` caches in `<data>\isos\`; `attach_iso` downloads on first use, inserts into DVD slot 1 and boots it first, no reboot; `detach_iso` ejects and restores disk-first boot.
- Volumes: new module `internal/resources/volumes`; 10–10240 GB; thin VHDX `<data>\volumes\volume-<id>.vhdx` with page83 `484C4142-0000-4000-8000-<id>`; hot-added at the lowest free LUN from 1 (at most 16, never 63); `linux_device` `/dev/disk/by-id/scsi-0HC_Volume_<id>`.
- VOL-05: attaching a volume attached elsewhere, or to a server in another location, is refused with the description's codes.
- Formatting: hot-add to the appliance, agent job `mkfs` (ext4/xfs), detach, attach to the server.
- Vendor-data gains the udev rule for `scsi-0HC_Volume_<id>` (E6); for volumes attached at server create with automount, a cloud-config part is merged into the cidata user-data as MIME multipart; metadata keeps serving the user's original user_data.
- Deleting a server detaches its volumes and keeps their files; deleting a project deletes volumes, snapshots and backups.
- Volume create/resize, snapshots and backups count toward the SRV-18 disk rule; exceeding it gives `resource_unavailable`.
- Resize only grows (shrink gives `invalid_input`), online on attached volumes; the guest filesystem is not grown.
- Tests: SRV-13, SRV-14, CAT-08, VOL-01, VOL-05 as unit tests (fake platform, clock, agent client); SRV-11, SRV-12, SRV-15, VOL-02, VOL-03, VOL-04 as e2e rows in the shared harness.
- Documentation: `deviations.md` (rescue on its own disk, ISO catalog, automount/udev only on hlab-created disks, crash-consistent snapshots, backups only while the cloud runs); `commands.md`/`configuration.md` get the isos catalog file.

## Corrections by the user

- Rescue follows the frozen description: it is disabled automatically when the server first boots into it, or after 60 minutes without such a boot; after that the server boots from its own disk again (`disable_rescue` still disables it at once).
- `enable_rescue` takes `ssh_keys` (key IDs) and injects them into the rescue system.
- Volume attach takes `assignee` `{id, type: "server"}` (required in the description) and still accepts the deprecated `server` field.
- The mkfs tools (and what rescue-prepare needs) are not installed at every appliance boot; they are built into the appliance image or cached locally.

## Answers

- Q1 Automount on a later attach: the agent document and metadata get a per-server list of automount volumes at a new guest path `GET /hlab/v1/volumes`; the udev-triggered unit asks it and mounts at `/mnt/HC_Volume_<id>` with an fstab entry (like Hetzner).
- Q2 SRV-15 e2e proof: boot an Ubuntu live-server ISO whose live session reads the cidata in slot 0, log in by SSH and check that `/` is the live system; fall back to the indirect proof (installed system stops answering, Hyper-V shows the DVD first holding the ISO; after detach and reset SSH works again) only if research refutes it.
