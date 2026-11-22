# DECISIONS DONE

Entries from DECISIONS.md that a later entry revoked or changed, moved here
unchanged; the later entry names them under `Replaces:`. Newest first. No
skill reads this file; it keeps the history for looking things up.

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
