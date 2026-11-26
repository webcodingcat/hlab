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

Done: phases 01–30, B1–B4; tag wcc-milestone-1; version v0.1.0

## Milestone 2: Linux host

Linux (Ubuntu 24.04 and 26.04 LTS, Debian 13, x86_64) becomes a full host
next to Windows: KVM/libvirt, systemd, systemd-resolved, the tray and a
.deb package. Windows stays fully supported; every phase checks the
Windows build on the Linux machine (`decisions/windows-checks.md`).

- [ ] 31 Linux walking skeleton.
      Requirements: LNX-02, LNX-03, LNX-11, REL-04.
      Depends on: milestone 1.
      Adjusts: `test/e2e` (the harness runs against libvirt and the
      Linux dev service, Ansible on the host), `internal/cloud` (platform-neutral; Hyper-V, hostnet and
      sysint chosen per GOOS), `internal/sysint`, `internal/hostnet`,
      `internal/platform` (additive), `Taskfile.yml` (build per host OS,
      `task check:windows`), `.github/workflows/ci.yml`, CLAUDE.md "Build
      and test commands", `docs/development/setup.md`.
      Success criteria:
      1. `task build` on Ubuntu builds `bin/hlabd`, `bin/hlab`,
         `bin/hlab-agent` for Linux; `task check:windows` builds every
         package and test binary for Windows and lints them.
      2. After `scripts/dev-setup.sh` (run once by the user with sudo) a
         member of group `hlab` starts the dev cloud with `hlab cloud
         start` without a password; the appliance boots under libvirt
         and the host resolves `api.hetzner.internal`.
      3. `hcloud server create` makes an Ubuntu server that boots with
         Secure Boot, takes its SSH key and is reachable via SSH from the
         host; `hcloud server delete` removes its VM and disks.
      4. Without KVM, libvirt, OVMF or systemd-resolved as the resolver,
         the start fails and names what is missing.
      5. `task e2e -- -run 'TestE2E/LNX-02'` passes on Linux.
      6. `ci.yml` runs on pushes to `wcc/**` and has an Ubuntu job (build,
         unit tests, linter) next to the Windows job; it is green once the
         user pushes the branch.

- [ ] 32 Servers, disks and console on libvirt.
      Requirements: LNX-01 (rows SRV, VOL, CON, META, CAT-08, CAT-09).
      Depends on: 31.
      Adjusts: `platform/libvirt`, docs/deviations.md (host-specific
      differences).
      Success criteria:
      1. Every server action of milestone 1 (power, reboot, reset,
         shutdown, rebuild, change type, rescue, reset password, image,
         backups, ISO, protection) passes its end-to-end test on Linux.
      2. Volumes appear in the guest as
         `/dev/disk/by-id/scsi-0HC_Volume_<id>`, attach, detach and grow.
      3. Every system image of the catalog boots on Linux.
      4. `request_console` returns a working wss console on Linux.

- [ ] 33 Networks and host integration on Linux.
      Requirements: LNX-01 (rows NET, PIP, FIP, FW, LB, CERT, DNS, RDNS,
      HOST-01, HOST-02, API-01, SBOX), LNX-05, LNX-07, LNX-08, LNX-09,
      LNX-10.
      Depends on: 32.
      Adjusts: `hostnet/linux`, `platform/libvirt`.
      Success criteria:
      1. Guests reach each other only through the appliance (isolated
         bridge ports, MAC guards), private networks connect.
      2. Firewalls, load balancers, floating IPs, DNS zones, reverse DNS
         and storage boxes pass their end-to-end tests on Linux.
      3. Each zone is a routing domain in systemd-resolved while the
         cloud runs, gone after delete or stop.
      4. With Docker, ufw or firewalld present, host-to-server and
         server-to-internet traffic work.
      5. The trust switch adds and removes the hlab CA in the system store.

- [ ] 34 Lifecycle and faults on Linux.
      Requirements: LNX-01 (rows LIFE, FAULT), LNX-04, LNX-06.
      Depends on: 33.
      Success criteria:
      1. Stop and start with "save" and "shutdown" keep everything; stop
         removes every host object hlab added.
      2. Every fault kind passes its end-to-end test on Linux.
      3. A host reboot with a running cloud follows the stop behaviour
         (manual check).

- [ ] 35 Tray on Linux.
      Requirements: LNX-12.
      Depends on: 34.
      Adjusts: `cmd/hlab-tray`, `test/traylaunch`, the UI end-to-end
      tests (how they reach the window), `ui/frontend` help texts where
      they name Windows.
      Success criteria:
      1. `task build` on Linux also builds `bin/hlab-tray` (GTK4,
         WebKitGTK 6.0); its icon shows the cloud status in GNOME's top
         bar and starts and stops the cloud.
      2. The UI end-to-end tests of milestone 1 pass against the tray on
         Linux.
      3. If the experiment finds no way into the window without a test
         hook in a release build, the phase stops for the user's decision
         instead of weakening the UI tests.

- [ ] 36 Debian package.
      Requirements: LNX-13, LNX-14, LNX-15, LNX-16, LNX-17, REL-05, DOC-09.
      Depends on: 35.
      Adjusts: `.github/workflows/release.yml`, README (Linux
      installation and removal), `docs/development/release-checks.md`.
      Success criteria:
      1. `task package:deb VERSION=x.y.z` builds `hlab_x.y.z_amd64.deb`;
         it installs on fresh Ubuntu 24.04, 26.04 and Debian 13 test VMs
         and the user starts the cloud after a new login.
      2. `apt remove` leaves no host object and keeps the data; `apt
         purge` leaves no file; an upgrade keeps settings and state.
      3. The release workflow adds the .deb to the release and to
         `SHA256SUMS.txt`; the CI's Ubuntu job builds the .deb.
      4. The application menu entry opens only the tray; installed from
         the .deb, the tray looks and works on Ubuntu 26.04 and Debian 13
         GNOME as on Windows (manual check).

- [ ] 37 Clients on Linux and milestone close.
      Requirements: LNX-01 (rows CLI, MCP), LNX-18, LNX-19.
      Depends on: 36; it is the last phase of milestone 2.
      Success criteria:
      1. hcloud, OpenTofu and Ansible (on the host) pass the client matrix
         and build and delete the example environment on Linux.
      2. The full end-to-end suite runs green on Linux.
      3. The example environment is created and deleted by hand in the
         tray on Linux (manual check).
      4. The full Windows end-to-end suite and the elevated installer
         suite pass on the Windows development machine (manual check).

## Later

- none
