# DECISIONS DONE

Entries from DECISIONS.md that a later entry revoked or changed, moved here
unchanged; the later entry names them under `Replaces:`. Newest first. No
skill reads this file; it keeps the history for looking things up.

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
