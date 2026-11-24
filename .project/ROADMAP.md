# ROADMAP: hlab

<!-- A milestone is a version: it ends with all end-to-end tests and a tag.
     A phase is a slice of it that runs and can be checked.
     /wcc:report (or /wcc:phase from FEEDBACK.md) inserts feedback
     phases (F1, ...) for reported bugs,
     /wcc:change backlog phases (B1, ...) for backlog items the user takes
     in, and further milestones before "Later".
     A finished phase shrinks to one line; its full entry moves to
     ROADMAP-DONE.md. -->

## Milestone 1: first version

- [x] 01 Walking skeleton (API-01, API-02, API-10, PROJ-01, PROJ-02, PROJ-05, CAT-01, CAT-06, CAT-07, SRV-01, SRV-03, SRV-04, SRV-05, SRV-06, SRV-16, SRV-19, LIFE-01, LIFE-06, LIFE-10) → phases/01/

- [x] 02 API conventions, catalog and the three clients (API-03, API-04, API-05, API-06, API-07, API-08, API-09, API-11, API-12, API-13, API-14, PROJ-03, PROJ-04, CAT-02, CAT-03, CAT-04, CAT-05, KEY-01, SRV-17, SRV-22, HOST-03, HOST-04) → phases/02/

- [x] 03 Server lifecycle (SRV-02, SRV-07, SRV-08, SRV-09, SRV-10, SRV-18, SRV-20, SRV-21, META-01) → phases/03/

- [x] 04 Images, backups, rescue, ISOs and volumes (SRV-11, SRV-12, SRV-13, SRV-14, SRV-15, CAT-08, VOL-01, VOL-02, VOL-03, VOL-04, VOL-05) → phases/04/

- [x] B1 Backlog: restart recovery, cloud-init and dev-loop bugs → phases/B1/

- [x] 05 Private networks, primary IPs and floating IPs, port isolation (NET-01, NET-02, NET-03, NET-04, NET-05, NET-06, PIP-01, PIP-02, FIP-01, FIP-02, HOST-01) → phases/05/

- [x] 06 Firewalls (FW-01, FW-02, FW-03, FW-04, FW-05) → phases/06/

- [x] 07 DNS zones and reverse DNS (DNS-01, DNS-02, DNS-03, DNS-04, DNS-05, RDNS-01, RDNS-02) → phases/07/

- [x] 08 Load balancers and certificates (LB-01, LB-02, LB-03, LB-04, LB-05, LB-06, LB-07, LB-08, LB-09, CERT-01, CERT-02, CERT-03, CERT-04, CERT-05) → phases/08/

- [x] 09 Cloud lifecycle and persistence (LIFE-02, LIFE-03, LIFE-04, LIFE-05, LIFE-07, LIFE-08, LIFE-09, HOST-02, LB-01, LB-09) → phases/09/

- [x] 10 Tray program: shell, status, settings, server types, projects, logs (UI-01, UI-03, UI-04, UI-05, UI-06, UI-07, UI-08) → phases/10/

- [x] B2 Backlog: VLAN release at project deletion, LB health check, docs and e2e test fixes → phases/B2/

- [x] 11 Tray console: shared frame, servers, SSH keys (UI-02 part) → phases/11/

- [x] 14 Tray console: remaining resources (UI-02, UI-09, UI-10, CAT-10) → phases/14/

- [x] 15 Project display names and Hetzner-style project list (PROJ-06, UI-11) → phases/15/

- [x] 12 Installer and uninstaller (INST-01, INST-02, INST-03, INST-04, INST-05, INST-06) → phases/12/

- [x] 13 Client matrix and success criterion (CLI-01, CLI-02, CLI-03, CLI-04) → phases/13/

- [x] 17 MCP server (MCP-01, MCP-02, MCP-03, MCP-04, MCP-05) → phases/17/

- [x] B3 Backlog: console times and labels, server power and delete fixes → phases/B3/

- [x] 18 Storage Box API (SBOX-01, SBOX-02, SBOX-09, SBOX-10, SBOX-20) → phases/18/

- [x] 19 Storage VM and SSH access (SBOX-03, SBOX-04, SBOX-06, SBOX-08, SBOX-11, SBOX-12, SBOX-13, SBOX-21) → phases/19/

- [x] 20 SMB, WebDAV, FTP and snapshots (SBOX-05, SBOX-07, SBOX-14, SBOX-15, SBOX-16, SBOX-17, SBOX-18, SBOX-19) → phases/20/

- [x] 22 SMB from the Windows host (SBOX-22) → phases/22/

- [x] 21 Storage Boxes in the console and the clients (UI-12, UI-13, UI-14, CLI-05, CLI-06, CLI-07) → phases/21/

- [x] 24 Storage box destroy in one run (CLI-06) → phases/24/

- [x] 23 Server console (SRV-17, CON-01, CON-02, UI-15) → phases/23/

- [x] 25 Fault model, API errors and rate limits (FAULT-01, FAULT-02, FAULT-03, FAULT-04, FAULT-12, MCP-06) → phases/25/

- [x] 26 Server and disk faults (FAULT-05, FAULT-06, FAULT-09, FAULT-10) → phases/26/

- [ ] 27 Network, latency and storage box faults.
      Requirements: FAULT-07, FAULT-08, FAULT-11.
      Depends on: 25, 05, 20.
      Adjusts: `agentapi.Config` (additive: `Faults`), hlab-agent's
      appliers, docs/guide.md.
      Success criteria:
      1. A `network` fault cuts a server's public or private traffic, or
         drops about the chosen share of pings, the same pings again with
         the same seed.
      2. A `latency` fault of 100 ms between fsn1 and hel1 raises the ping
         time between servers there by about 100 ms and leaves servers in
         one location unchanged.
      3. A storage box under `storage_box_unavailable` refuses SFTP, SMB
         and WebDAV from a server, the host and WSL2, and serves them again
         after the fault ends.
      4. Ending any of these faults, by hand, by its duration or at cloud
         stop, leaves no `hlab_fault` rule and no netem qdisc on the
         appliance.

- [ ] 28 Faults in the tray.
      Requirements: UI-16, UI-17, UI-18.
      Depends on: 25, 26, 27, 14.
      Adjusts: `views.json` and the help texts (views `faults` and
      `active-faults`), the sidebar, the top bar, the tray menu and
      `TrayModel`, README (tray section), docs/guide.md.
      Success criteria:
      1. In the project console's "Faults", the user creates a fault of
         every kind, sees it in the table with its seed and remaining
         time, and ends it.
      2. While a fault is active, the top bar shows "N faults active",
         which leads to the list of all projects' faults, and "End all"
         there or in the tray menu ends them all.

- [ ] 29 More system images.
      Requirements: CAT-09, SRV-23, SRV-24.
      Depends on: 01, 04, 05.
      Adjusts: `catalog/images.json`, the checksum parser and HTTP client
      of `internal/images`, the network-config of `internal/cidata`, the
      vendor-data and hooks of the servers module, the e2e image matrix,
      README (image list), docs/guide.md, docs/deviations.md (EL10 needs
      an x86-64-v3 host CPU; no default user; debian-11, which Hetzner
      retires on 2026-12-01, is not offered).
      Success criteria:
      1. `hcloud image list --type system` shows the eleven new images
         under Hetzner's names and IDs, fedora-43 as deprecated.
      2. A server from each new image boots, takes SSH keys, password and
         user_data, joins private networks, mounts a volume and resets its
         root password; `cloud-init status` exits with 0 on every image.
      3. On no image, Ubuntu and Debian included, a default user such as
         `ubuntu` or `rocky` exists; root logs in.

- [ ] 16 Release checks in test VMs.
      Requirements: DOC-08.
      Depends on: 13 and every other phase of milestone 1; it stays the
      last phase of the milestone, and phases taken in from "Later" are
      inserted before it.
      Success criteria:
      1. `docs/development/release-checks.md` describes both test VMs,
         and the user has set them up by it.
      2. The elevated installer suite (`task e2e:elevated`) passes in the
         development test VM.
      3. In the user test VM the setup EXE installs HLab without
         development tools, and the manual checks of milestone 1 (INST-03,
         INST-05, UI-09, LIFE-05, DOC-07 and those of later phases) pass
         there.
      Stops for: manual check.

## Later

- Linux host with KVM/libvirt, including the tray
