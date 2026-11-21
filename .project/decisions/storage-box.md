# Decision: Storage Boxes in milestone 1

- Date: 2026-11-21
- Status: decided by the user (scope, addresses, storage VM, order);
  details decided in `/wcc:change` (see "Details")
- Cost of change: high for the addresses (they end up in users' scripts
  and client configs); medium for the storage VM layout

## Decision

- The Storage Box, planned for version 2, is built in milestone 1, with
  real data over all protocols Hetzner offers: SFTP/SCP (22), SFTP/SCP,
  rsync and BorgBackup (23), SMB (445), WebDAV over HTTPS (443) and
  FTP/FTPS (21).
- Addresses under `hetzner.internal`: the Hetzner API (Storage Boxes) at
  `http://api.hetzner.internal/hetzner/v1`, on the same host and listener
  as the Cloud API; storage boxes at `u<n>.storagebox.hetzner.internal`,
  subaccounts at `u<n>-sub<m>.storagebox.hetzner.internal`. No new NRPT
  zone is needed.
- Data and protocols live in a storage VM of their own, not on the
  appliance.
- The console gets a Storage Boxes section; it is built after phase 13,
  with its own client tests (CLI-05).

## Details

- The clients use their second endpoint setting (hcloud `hetzner_endpoint`
  / `--hetzner-endpoint`, provider `endpoint_hetzner`, Ansible
  `api_endpoint_hetzner`) and the same project token as for the Cloud API.
- Usernames `u<n>` with `n` from 100000 upward, never reused; subaccounts
  `u<n>-sub<m>`. Each box gets a public IPv4 of the range, routed by the
  appliance to the storage VM; the agent DNS answers the box and
  subaccount names with it.
- `reachable_externally` maps to the host and WSL2: without it, a box is
  reachable only from hlab servers.
- btrfs on one dynamic data disk: one subvolume per box with the type's
  size as quota, snapshots as read-only btrfs snapshots; `zfs_enabled`
  shows them under `.zfs/snapshot/<name>`. A box's size counts against the
  disk only as far as it is used (thin, like disks).
- WebDAV and FTPS use a certificate from the hlab CA; clients trust it
  with the existing `trust_ca` switch or skip verification.
- Storage box types and prices come from the real API through
  `task catalog`; the locations are those the types have prices for.
- The storage VM exists while at least one storage box exists, starts and
  stops with the cloud and follows `stop_behaviour`.

## Reasons

- Goal 2 ("every resource behaves for real") asks for real data; backup
  setups (rsync, Borg, SMB mounts) are what users test against Storage
  Boxes.
- Sharing `api.hetzner.internal` needs no second listener, hostname or
  NRPT rule; the path keeps the two APIs apart.
- A VM of its own keeps the appliance, through which every packet passes,
  small, and gives the box services their own addresses on 22 and 443.
- Building after phase 13 keeps the client matrix of the Cloud API
  unchanged.

## Alternatives considered

- Only the SSH protocols, or only the API without data: rejected by the
  user; less effort, but SMB, WebDAV and FTP setups could not be tested.
- Names under their own `.internal` zone (`api.hetzner-com.internal`,
  `*.your-storagebox.internal`): closer to Hetzner's names, but a second
  API host and NRPT zone.
- Services on the appliance: one VM fewer, but a much larger appliance
  whose failure affects all traffic.
- Building before phase 13, so the client matrix includes the boxes:
  rejected by the user.
