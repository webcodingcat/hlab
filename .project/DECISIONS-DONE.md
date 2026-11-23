# DECISIONS DONE

Entries from DECISIONS.md that a later entry revoked or changed, moved here
unchanged; the later entry names them under `Replaces:`. Newest first. No
skill reads this file; it keeps the history for looking things up.

## 2026-11-23 Storage box firewall chains only drop or return

- Type: decision
- Phase: phase 19
- What: Each box IP gets its own nftables chain `box<i>`, jumped to from the forward chain, that drops external sources of boxes not reachable externally and ports other than TCP 22/23 and returns otherwise.
- Why: Allowed traffic must still pass the server's own outbound firewall (FW-03, review of 19-05).
- Cost of change: low

## 2026-11-23 Only SSH ports reach box IPs in phase 19

- Type: decision
- Phase: phase 19
- What: The appliance lets only TCP 22 and 23 through to box IPs (subject to the server firewalls) and drops the rest, so a box IP never reaches the storage agent on 9100; phase 20 adds its ports to `storageBoxPorts`.
- Why: Phase 19 serves only SSH, and the agent port must stay internal.
- Cost of change: low

## 2026-11-21 WebDAV for subaccounts by name-based virtual hosts

- Type: assumption
- Phase: change
- What: subaccounts reach WebDAV on the box's IP through one name-based virtual host per account name with its own hlab CA certificate (SNI); not tried in E17.
- Why: Hetzner gives each subaccount its own hostname on the box's address; Apache with mpm-itk serves per-vhost users (E17).
- Cost of change: medium

## 2026-11-21 Storage VM interfaces and catalog

- Type: decision
- Phase: change
- What: the storage VM has the fixed address `Addrs.Storage` (default `198.19.255.253`) with `PUT /v1/storage` and `POST /v1/jobs`, the appliance document gets `Storage` and `StorageBoxes`, the storage modules register under `/hetzner/v1` and the management API forwards `/projects/{name}/api/hetzner/v1/{rest}`; the storage VM's Debian packages are pinned in `catalog/storage-debs.json` like the appliance's; `task catalog` reads storage box types from `https://api.hetzner.com/v1` with the context's token; SBOX-08 is a unit test of the uninstall cleanup, while INST-04 in the elevated suite covers removal on a real machine.
- Why: the spec review found these interfaces missing; each choice reuses an existing pattern of the appliance, the catalog or the uninstall cleanup.
- Cost of change: medium

## 2026-11-21 Storage Boxes in milestone 1

- Type: change
- Phase: change
- What: the Storage Box moves from the target vision into milestone 1 as phases 18 to 20 after phase 13 (API, storage VM with real data over SFTP/SCP, rsync, Borg, SMB, WebDAV and FTP/FTPS, console section and client tests), with requirements SBOX-01 to SBOX-08, UI-12 and CLI-05, addresses under `hetzner.internal` and the details in `decisions/storage-box.md`; UI-02 and CLI-01 to CLI-03 now name the Cloud API.
- Why: the user wants Storage Box setups testable in the first version, with real data as goal 2 asks.
- Cost of change: medium (additions: three phases, a second VM role of hlab-agent, a second router under `/hetzner/v1`; no rebuild of existing code)

## 2026-11-22 Shutdown retry as a testable function

- Type: decision
- Phase: phase B3
- What: `shutdown(ctx, name, timeout, interval, ask)` in hyperv.go wraps `untilAccepted`, and `Power(PowerShutdown)` calls it with `guestReadyTimeout` and 2 s like reboot.
- Why: the PowerShell runner cannot be faked, so the test passes the ask function.
- Cost of change: low

## 2026-11-21 All manual checks wait for the release, in a dedicated test VM

- Type: approval
- Phase: change
- What: no phase stops for a manual check any more, phase 12 included; every open manual check, the elevated installer suite and the INST checks of phase 12 among them, stays in "Open manual checks" and is done before the release of milestone 1 in a dedicated Hyper-V VM with Windows and nested virtualization, not on the development machine.
- Why: the user has no time for manual checks now and wants the phases to go on; installer and uninstaller checks need a clean machine without the development setup.
- Cost of change: low

An entry that revokes or changes an earlier one states in full what holds
now, names the earlier entry under `Replaces:`, and the earlier entry moves
unchanged to DECISIONS-DONE.md, so this file holds only what is valid.

Keep entries short and their titles telling: phases find the entries
they need by their titles and do not read this file in full. Details belong
in ARCHITECTURE.md or the plan.

## 2026-11-19 CA common name per profile

- Type: decision
- Phase: phase 08
- What: the CA's CN is "<profile tag> CA" (`hlab CA`, `hlab-dev CA`).
- Why: the trust store entries of the two profiles can be told apart.
- Cost of change: medium

## 2026-11-18 Hidden token prompt through x/sys/windows

- Type: decision
- Phase: phase 02
- What: the hidden prompt of `hlab catalog update` uses the console mode of `x/sys/windows`, with new `cli.IO.ReadSecret` and `IO.Getenv`.
- Why: no new module.
- Cost of change: low

## 2026-11-18 Vendor script syncs before first boot ends

- Type: decision
- Phase: 03
- What: the vendor shell part ends with `sync`; poweroff stays a hard cut.
- Why: a hard poweroff within about 30 s of the first boot left zero-length SSH host keys and hook files, so sshd never came back.
- Cost of change: low
