# Phase 20 context

Confirmed at the start of the phase (2026-11-23).

- The planner's assumptions of step 1, as changed by RESEARCH.md, hold, with the corrections below.
- Q1: one wildcard certificate `*.storagebox.hetzner.internal` from the hlab CA for all accounts (Apache vhosts and vsftpd); the vhosts select the account by SNI.
- Subaccounts without reachable_externally are refused by each service: sshd by Match, Apache by `Require ip` in the vhost, Samba by `hosts allow` in the subaccount's share, vsftpd by `pam_access`.
- Q2: the source and manual evidence for `pam_access` in vsftpd suffices; an end-to-end row (530 from the host or WSL2, login from a server) is the proof; if it fails, the plan stops after three repair attempts.
- The storage VM gets 2048 MB of dynamic memory.
- `day_of_week` and `day_of_month` combine with AND (Ansible documentation, RESEARCH.md section 5); recorded in DECISIONS.md, nothing in `docs/deviations.md`.
- Rollback also ends the sessions of the box's subaccounts; the test checks that a logged-in subaccount sees the old state afterwards.
- Every SHA-256 in RESEARCH.md came through a page summarizer: recompute each before the catalog is committed.
