# ROADMAP: hlab

<!-- A milestone is a version: it ends with all end-to-end tests and a tag.
     A phase is a slice of it that runs and can be checked.
     /wcc:feedback (or /wcc:phase from FEEDBACK.md) inserts feedback
     phases (F1, ...) for reported bugs,
     /wcc:change backlog phases (B1, ...) for backlog items the user takes
     in, and further milestones before "Later".
     A finished phase shrinks to one line; its full entry moves to
     ROADMAP-DONE.md. -->

## Milestone 1: first version

- [x] 01 Walking skeleton (API-01, API-02, API-10, PROJ-01, PROJ-02, PROJ-05, CAT-01, CAT-06, CAT-07, SRV-01, SRV-03, SRV-04, SRV-05, SRV-06, SRV-16, SRV-19, LIFE-01, LIFE-06, LIFE-10) → phases/01/

- [ ] 02 API conventions, catalog and the three clients: shared API behaviour,
      projects and tokens complete, SSH keys, placement groups, pricing,
      server type catalog, WSL2, OpenTofu and Ansible in the harness.
      Requirements: API-03, API-04, API-05, API-06, API-07, API-08, API-09,
      API-11, API-12, API-13, API-14, PROJ-03, PROJ-04, CAT-02, CAT-03,
      CAT-04, CAT-05, KEY-01, SRV-17, SRV-22, HOST-03, HOST-04.
      Depends on: 01.
      Success criteria:
      1. From WSL2 in NAT mode, `ssh root@<server>` works and Ansible's
         `hetzner.hcloud.server_info` lists the servers of a project.
      2. A read-only token can list but not create; a token of another
         project sees nothing.
      3. `hlab catalog update` refreshes the server types; own types from the
         custom file appear in `hcloud server-type list`.
      4. `tofu apply` creates SSH keys and placement groups; `tofu destroy`
         removes them.

- [ ] 03 Server lifecycle: all power actions, rebuild, change type, root
      password, metadata, resource limits, metrics.
      Requirements: SRV-02, SRV-07, SRV-08, SRV-09, SRV-10, SRV-18, SRV-20,
      SRV-21, META-01.
      Depends on: 02.
      Success criteria:
      1. hcloud power actions change the VM visibly (SSH drops, comes back).
      2. A rebuilt server keeps its IP and boots the new image.
      3. `hcloud server reset-password` gives a password that logs in.
      4. A server type larger than the host's free RAM returns
         `resource_unavailable`.

- [ ] 04 Images, backups, rescue, ISOs and volumes.
      Requirements: SRV-11, SRV-12, SRV-13, SRV-14, SRV-15, CAT-08, VOL-01,
      VOL-02, VOL-03, VOL-04, VOL-05.
      Depends on: 03.
      Success criteria:
      1. A snapshot of a server with a marker file boots as a new server
         with that file.
      2. Rescue mode gives an SSH login into a RAM system where the server
         disk is `/dev/sda`.
      3. A volume created with format and automount is mounted in the
         server; after detach and attach to another server its data is
         there.

- [ ] 05 Private networks, primary IPs and floating IPs, port isolation.
      Requirements: NET-01, NET-02, NET-03, NET-04, NET-05, NET-06, PIP-01,
      PIP-02, FIP-01, FIP-02, HOST-01.
      Depends on: 03.
      Success criteria:
      1. Two servers in one network ping each other on their private IPs;
         a server in another network cannot.
      2. A floating IP moved from one server to another answers from the
         new server.
      3. A hand-made route on a guest does not reach another guest
         directly.

- [ ] 06 Firewalls.
      Requirements: FW-01, FW-02, FW-03, FW-04, FW-05.
      Depends on: 05.
      Success criteria:
      1. With a firewall allowing only port 22, SSH works and port 80 is
         blocked; removing the firewall opens port 80.
      2. Applying a firewall by label selector filters a server that gets
         the label later.

- [ ] 07 DNS zones and reverse DNS.
      Requirements: DNS-01, DNS-02, DNS-03, DNS-04, DNS-05, RDNS-01,
      RDNS-02.
      Depends on: 05.
      Success criteria:
      1. An A record in a new zone resolves from a server, the host and
         WSL2; deleting the zone removes its NRPT rule.
      2. A PTR set on a server's IP resolves from the host.

- [ ] 08 Load balancers and certificates.
      Requirements: LB-01, LB-02, LB-03, LB-04, LB-05, LB-06, LB-07, LB-08, LB-09,
      CERT-01, CERT-02, CERT-03, CERT-04, CERT-05.
      Depends on: 06, 07.
      Success criteria:
      1. A load balancer with two private targets alternates between them;
         stopping one target's web server takes it out.
      2. An HTTPS service with a managed certificate for a name in an hlab
         zone answers with that certificate; with the trust switch on,
         `curl https://<name>` succeeds on host and WSL2.

- [ ] 09 Cloud lifecycle and persistence.
      Requirements: LIFE-02, LIFE-03, LIFE-04, LIFE-05, LIFE-07, LIFE-08,
      LIFE-09, HOST-02.
      Depends on: 08.
      Success criteria:
      1. After stop and start with "save", a server's uptime continues and
         every resource still works.
      2. With "shutdown", servers come back after start and every resource
         still works.
      3. While the cloud is stopped no hlab route or NRPT rule exists on the
         host.

- [ ] 10 Tray program: shell, status, settings, server types, projects,
      logs.
      Requirements: UI-01, UI-03, UI-04, UI-05, UI-06, UI-07, UI-08.
      Depends on: 09.
      Success criteria:
      1. The tray icon starts and stops the cloud and shows its state.
      2. Settings, server types, projects with tokens and logs work in the
         window.
      Stops for: manual check.

- [ ] 11 Tray console for all resources.
      Requirements: UI-02, UI-09.
      Depends on: 10.
      Success criteria:
      1. Every resource type can be listed, created, configured and deleted
         in the window.
      2. The example environment can be created and deleted by hand.
      Stops for: manual check.

- [ ] 12 Installer and uninstaller.
      Requirements: INST-01, INST-02, INST-03, INST-04, INST-05, INST-06.
      Depends on: 09.
      Success criteria:
      1. The setup EXE installs hlab on a machine without hlab, including
         WebView2 if missing; the tray starts from the start menu.
      2. After uninstalling with the default answer, no VM, switch, route,
         NRPT rule, service or file of hlab remains.
      3. Installing a newer build over an older one keeps the state.
      Stops for: manual check.

- [ ] 13 Client matrix and success criterion.
      Requirements: CLI-01, CLI-02, CLI-03, CLI-04.
      Depends on: 11, 12.
      Success criteria:
      1. Every resource type passes its row for hcloud, OpenTofu and
         Ansible.
      2. Each client builds the example environment, it is reachable via
         SSH from host and WSL2, and the client deletes it completely.

## Later

- Fault injection (server failure, network disruption, latency, API errors,
  rate limits, storage errors)
- Linux host with KVM/libvirt, including the tray
- Storage Box (version 2)
- Server console with a working wss VNC console
- WSL2 in mirrored networking mode
- More system images and app images
