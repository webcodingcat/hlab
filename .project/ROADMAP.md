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

- [ ] 18 Storage Box API.
      Requirements: SBOX-01, SBOX-02, SBOX-09, SBOX-10, SBOX-20.
      Depends on: 13.
      Success criteria:
      1. `http://api.hetzner.internal/hetzner/v1` serves storage box types,
         storage boxes, subaccounts and their actions as the frozen Hetzner
         API description says, with the cloud tokens of the project.
      2. `task catalog` writes the storage box types with prices from the
         real API.

- [ ] 19 Storage VM and SSH access.
      Requirements: SBOX-03, SBOX-04, SBOX-06, SBOX-08, SBOX-11, SBOX-12,
      SBOX-13.
      Depends on: 18.
      Success criteria:
      1. The first storage box creates the storage VM; data written over
         SFTP on port 22 can be read back over rsync and Borg on port 23.
      2. A box is reachable from a server, and from host and WSL2 only with
         reachable_externally; the uninstall cleanup removes the storage VM.
      3. Subaccounts see only their home directory; read-only ones write
         nothing; the quota of the type holds.
      4. `docs/guide.md`, `docs/deviations.md` and `docs/configuration.md`
         describe storage boxes and their SSH access.

- [ ] 20 SMB, WebDAV, FTP and snapshots.
      Requirements: SBOX-05, SBOX-07, SBOX-14, SBOX-15, SBOX-16, SBOX-17,
      SBOX-18, SBOX-19.
      Depends on: 19.
      Success criteria:
      1. Data written over SMB, WebDAV or FTPS can be read back over SFTP;
         SMB, WebDAV and port 23 answer only while their access setting
         is on.
      2. Snapshots, the snapshot plan and a rollback behave as at Hetzner.
      3. Data, accounts and snapshots survive a stop and start of the
         cloud.
      4. `docs/guide.md` and `docs/deviations.md` describe the protocols and
         snapshots.

- [ ] 21 Storage Boxes in the console and the clients.
      Requirements: UI-12, UI-13, CLI-05, CLI-06, CLI-07.
      Depends on: 20, 17.
      Success criteria:
      1. The console manages storage boxes, their subaccounts and
         snapshots.
      2. hcloud, OpenTofu and Ansible pass their storage box rows with only
         their endpoints changed.
      3. The README names the Hetzner endpoint setting of each client, and
         the help text of the new view exists.

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

- Fault injection (server failure, network disruption, latency, API errors,
  rate limits, storage errors)
- Linux host with KVM/libvirt, including the tray
- Server console with a working wss VNC console
- WSL2 in mirrored networking mode
- More system images and app images
- MCP tools that read and change Hetzner resources (`decisions/mcp-server.md`)
