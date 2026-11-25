# DECISIONS DONE

Entries from DECISIONS.md that a later entry revoked or changed, moved here
unchanged; the later entry names them under `Replaces:`. Newest first. No
skill reads this file; it keeps the history for looking things up.

## 2026-11-21 Manual checks and the installer suite move to a last phase in two test VMs

- Type: change
- Phase: change
- What: no phase stops for a manual check any more; the new phase 16, always the last of milestone 1 with phases taken in from "Later" inserted before it, runs the elevated installer suite in a development test VM and every open manual check in a user test VM without development tools, both fresh Windows Hyper-V VMs with nested virtualization that the user sets up by hand from `docs/development/release-checks.md` (DOC-08).
- Why: the user has no time for manual checks now, wants to pull items from "Later" forward, and wants the installation tested in the user's own flow on a clean machine.
- Cost of change: low
- Replaces: 2026-11-21 All manual checks wait for the release, in a dedicated test VM

## 2026-11-25 Windows in the test VMs installed fully by the answer file

- Type: decision
- Phase: phase 16
- What: the answer file of `docs/development/release-checks.md` runs the whole Windows setup of a test VM (windowsPE and oobeSystem passes: language from a variable matching the ISO, disk layout for a Generation 2 VM, Windows 11 Pro without activation, licence terms, local account, privacy screens) and, through a one-time automatic sign-in and first-logon commands, enables Hyper-V and restarts; "Domain join instead" and setup without network are dropped from the guide.
- Why: the user asked for it in /wcc:report: one way, easier to follow and to repeat than three routes and manual setup screens.
- Cost of change: low
- Replaces: 2026-11-24 Answer-file ISO built with IMAPI2 (the ISO is still built with IMAPI2); 2026-11-24 Local account at Windows setup left to the guide

## 2026-11-25 SRV-08 accepts a minor release of the image's major version

- Type: decision
- Phase: phase 16
- What: the SRV-08 end-to-end test accepts `/etc/os-release` naming a minor release of the image's `os_version` (for example "rocky 8.10" for "8"), never another major.
- Why: the catalog gives rocky-8 only its major release, as Hetzner does, while the guest reports 8.10.
- Cost of change: low

## 2026-11-25 Seed swap waits for a hold only while the VM runs

- Type: decision
- Phase: phase 16
- What: a network attach or detach right after a boot finishes at once; the seed ISO swap waits until 90 s after the boot hlab started unless the VM is off, then it swaps at once; a waiting swap is noted as `seed_due` on the server and finished at the next cloud start if hlabd restarts.
- Why: E48 showed an early slot-0 swap leaves a booting guest without hostname and public IP, but an Off VM reads no seed, and the full e2e run showed the hold kept a primary IP assigned right after create from taking effect (PIP-02).
- Cost of change: low
- Replaces: 2026-11-24 Seed swap waits for a hold after a boot hlab starts (Q26)

## 2026-11-24 Answer-file ISO built with IMAPI2

- Type: decision
- Phase: phase 16
- What: the guide builds the answer-file ISO (oobeSystem pass only, local account `tester`, password asked with `Read-Host`, folder deleted afterwards) with Windows' own IMAPI2FS interface.
- Why: a fresh development machine has no oscdimg.
- Cost of change: low

## 2026-11-24 Local account at Windows setup left to the guide

- Type: assumption
- Phase: phase 16
- What: the guide names the answer file, "Domain join instead" and setup without network as routes to a local account; none is confirmed on current media, and corrections come back through `/wcc:report`.
- Why: RESEARCH.md found the older bypasses removed and the remaining reports contradictory.
- Cost of change: low

## 2026-11-24 Seed swap waits for a hold after a boot hlab starts (Q26)

- Type: decision
- Phase: phase B4
- What: a network attach or detach right after a boot finishes at once, and the seed ISO swap waits until 90 s after the boot hlab started, is noted as `seed_due` on the server and is finished at the next cloud start if hlabd restarts.
- Why: E48 showed an early slot-0 swap leaves the guest without hostname and public IP, and the user chose no waiting in the attach action.
- Cost of change: low

## 2026-11-24 A box fault with an unparsable IP is left out

- Type: decision
- Phase: phase 27
- What: a storage box whose stored IP cannot be parsed counts as gone, and its fault is left out of the agent document.
- Why: a broken record must not stop the push for every other fault and resource.
- Cost of change: low

## 2026-11-22 Every storage box action locks the box

- Type: decision
- Phase: phase 18
- What: a second concurrent action on a box, subaccount actions included, returns 423 `locked`; evidence exists only for parallel subaccount and snapshot creates (provider PR #1307), the rest is without evidence.
- Why: the simplest rule, and safe for clients that retry.
- Cost of change: low

## 2026-11-18 Server protection and console details

- Type: decision
- Phase: phase 02
- What: `change_protection` writes the flags in the transaction that starts its action; delete checks protection before the lock (protected and locked answers `protected`); resource action lists accept `id` and `status`; `request_console` answers `unsupported_error` for any server ID.
- Why: the simplest order that the description allows; the console is unsupported regardless of the server.
- Cost of change: low

## 2026-11-18 request_console and vswitch subnets

- Type: decision
- Phase: spec
- What: request_console returns `unsupported_error` ("server console is not supported by hlab yet"); vswitch subnets return `invalid_input` on field `type`.
- Why: neither may pretend to work (decisions/server-console.md); vSwitches couple to dedicated servers that hlab has no equivalent for.
- Cost of change: low

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
