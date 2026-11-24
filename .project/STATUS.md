STATUS: BLOCKED

<!-- Allowed values: IDEA, IDEA_DONE, SPEC_REVIEW, READY, IN_PROGRESS, MANUAL_CHECK, BLOCKED, DONE -->

## Current state

<!-- One or two sentences per line; details belong in the phase folder. -->

- Milestone: 1
- Phase: 29 More system images (blocked at 29-05, branch wcc/phase-29)
- Base branch: main
- Last done: phase 28 merged into main (2026-11-24): the console creates, lists and ends faults per project, the top bar shows "N faults active" leading to all active faults, and the tray menu offers "End all faults"
- Next: answer Q25, then /wcc:phase in a new session resumes phase 29 at 29-05; phase 16 (release checks) stays the last phase and stops for the manual checks

## Open manual checks

<!-- Requirements verified as "manual" that the user still has to look at,
     one line each: "- UI-04 (03): <requirement>". The guide is in
     phases/<id>/CHECKS.md. /wcc:report walks through them. -->

- DOC-07 (01): README and guides are understandable for their audience, help texts name only existing views and buttons
- LIFE-05 (09): a Windows shutdown or restart with the cloud running applies the stop behaviour
- INST-01 to INST-06 (12): the elevated installer suite passes in the development test VM
- INST-03 (12): the setup creates shortcuts that open only the tray program, and "Launch" starts it without admin rights
- INST-05 (12): uninstalling asks whether to keep the data; with the default answer no file of hlab remains
- UI-09 (14): the example environment can be created and deleted by hand in the console
- COMPACT (19): a deleted box gives its space on the host back after a cloud stop (data.vhdx compaction)
- All of them, and those of later phases, are done in phase 16: the suite in the development test VM, the others in the user test VM (SPEC.md "Test VMs for the release checks").

## Open decisions

<!-- Questions only the user can answer: findings that would change
     REQUIREMENTS.md, SPEC.md or ARCHITECTURE.md, and wishes not yet taken
     up. One question each, with context, options and a recommendation.
     /wcc:phase asks those of its phase at its start, /wcc:change takes
     answers any time. -->

- Q25 (phase 29, 29-05): How should hlab get `cloud-init status` to exit 0 on Fedora, where cloud-init sets the hostname too early in a race?
  - Context: on fedora-44 (2 of 2 runs) and fedora-43 (1 of 2) cloud-init's local stage calls `hostnamectl set-hostname` for the seed's meta-data `local-hostname` before D-Bus is up; it logs a WARNING, `cloud-init status` exits 2 ("degraded done"), although the hostname is then set correctly. All other SRV-23/SRV-24 checks pass on all 16 images. SRV-23 promises exit 0 on every image.
  - Options: 1. For Fedora images, leave `local-hostname` out of the seed's meta-data and set the hostname through vendor-data (`hostname`) in the init stage, like the `on-link` switch by `os_flavor`; changes ARCHITECTURE.md "cidata". 2. The same for all images; one seed for all, but all 16 images need re-checking. 3. Accept exit 2 on Fedora as a documented deviation in docs/deviations.md and let SRV-23 accept "degraded done" there only for this warning; changes REQUIREMENTS.md SRV-23.
  - Recommendation: 1, because it removes the cause where it occurs, follows the existing per-flavor pattern, and leaves the 14 green images untouched.
  - Affects: SRV-23, ARCHITECTURE.md "cidata", "Guest integration"

## If BLOCKED

- Reason: plan 29-05 stopped: fedora-44 ends `cloud-init status` with exit 2 because of a cloud-init hostname race on Fedora; fixing it changes the seed contract
- Decision needed: Q25 (how Fedora gets exit 0, or whether exit 2 there is accepted)
