# ROADMAP DONE

Full entries of finished phases, moved here unchanged when a phase closes;
ROADMAP.md keeps one line per finished phase. Newest first. No skill reads
this file; it keeps the details for looking things up.

## Milestone 1: first version

- [x] 20 SMB, WebDAV, FTP and snapshots.
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

- [x] 19 Storage VM and SSH access.
      Requirements: SBOX-03, SBOX-04, SBOX-06, SBOX-08, SBOX-11, SBOX-12,
      SBOX-13, SBOX-21.
      Depends on: 18.
      Success criteria:
      1. The first storage box creates the storage VM; data written over
         SFTP on port 22 can be read back over rsync and Borg on port 23.
      2. A box is reachable from a server, and from host and WSL2 only with
         reachable_externally; the uninstall cleanup removes the storage VM.
      3. Subaccounts see only their home directory; read-only ones write
         nothing; the quota of the type holds, tested with own small
         storage box types from `storage-box-types.custom.json`.
      4. `docs/guide.md`, `docs/deviations.md` and `docs/configuration.md`
         describe storage boxes and their SSH access.

- [x] 18 Storage Box API.
      Requirements: SBOX-01, SBOX-02, SBOX-09, SBOX-10, SBOX-20.
      Depends on: 13.
      Success criteria:
      1. `http://api.hetzner.internal/hetzner/v1` serves storage box types,
         storage boxes, subaccounts and their actions as the frozen Hetzner
         API description says, with the cloud tokens of the project.
      2. `task catalog` writes the storage box types with prices from the
         real API.

- [x] B3 Backlog: console times and labels, server power and delete fixes → phases/B3/

- [x] 17 MCP server.
      Requirements: MCP-01, MCP-02, MCP-03, MCP-04, MCP-05.
      Depends on: 15.
      Success criteria:
      1. An MCP client controls hlab through `hlab mcp`: every tool does
         what its `hlab` command does; while the cloud is stopped,
         `cloud_status`, `logs` and `version` work and every other tool
         says that the cloud is not running.
      2. `cloud_start` and `cloud_stop` return once the cloud has reached
         its state, and a refusal comes back as a tool error.
      3. `docs/commands.md` describes every tool with a configuration
         example for Claude Code and Claude Desktop; the README points to
         it.

- [x] 13 Client matrix and success criterion.
      Requirements: CLI-01, CLI-02, CLI-03, CLI-04.
      Depends on: 14, 15, 12.
      Success criteria:
      1. Every resource type passes its row for hcloud, OpenTofu and
         Ansible.
      2. Each client builds the example environment, it is reachable via
         SSH from host and WSL2, and the client deletes it completely.

- [x] 12 Installer and uninstaller.
      Requirements: INST-01, INST-02, INST-03, INST-04, INST-05, INST-06.
      Depends on: 09.
      Success criteria:
      1. The setup EXE installs hlab on a machine without hlab, including
         WebView2 if missing; the tray starts from the start menu.
      2. After uninstalling with the default answer, no VM, switch, route,
         NRPT rule, service or file of hlab remains.
      3. Installing a newer build over an older one keeps the state.

- [x] 15 Project display names and Hetzner-style project list.
      Requirements: PROJ-06, UI-11.
      Depends on: 14.
      Scope: schema version 2 with `projects.display_name` and its
      migration; `POST /projects` takes the display name and derives the
      name, `GET /projects` adds `display_name` and `resources_by_type`;
      `hlab project create <display-name>` and `list` with the display
      name; the tray's project list as cards (grid/list, sort, resources
      per type, "New project" tile), breadcrumb, project switcher and the
      delete confirmation with the display name. Adjusts the callers of
      `POST /projects` (CLI, tray, test fixtures). Documentation: the CLI
      reference, the guide and the README on projects and their names, the
      projects help text and the README screenshots of the project list.
      Success criteria:
      1. "Web Coding Cat" can be created in the tray and with `hlab`; it is
         listed as `web-coding-cat` with its display name, and a second
         "web coding cat" is refused.
      2. A state of schema version 1 starts with every project showing its
         name as display name.
      3. The project list switches between grid and list, sorts by display
         name and creation date, and a resource count per type opens its
         section.

- [x] 14 Tray console: remaining resources.
      Requirements: UI-02 (part: remaining resource types; completes it),
      UI-09, UI-10, CAT-10.
      Depends on: 11.
      Research: phases/11/RESEARCH.md.
      Scope: the sections Volumes, Load Balancers, Networks, Firewalls,
      Floating IPs, Primary IPs, DNS, Certificates, Placement Groups,
      Images and ISOs on the frame of phase 11; after the manual check the
      console navigation of UI-SPEC.md (grouped sidebar, section tabs)
      and own ISOs in the Administration (change of 2026-11-20).
      Success criteria:
      1. Every remaining resource type can be listed, shown, created,
         configured and deleted in the window; with phase 11, every
         resource type of the API is covered.
      2. The example environment can be created and deleted by hand in the
         console.
      3. The sidebar shows the groups Cloud and Networking and the entry
         Security, every entry with its tabs; own ISOs can be added from a
         URL or a local file in the Administration and attached to a server.
      UI-09 manual check deferred to before the release (2026-11-20).

- [x] 11 Tray console: shared frame, servers, SSH keys.
      Requirements: UI-02 (part: shared frame, servers, SSH keys; done
      after phase 14).
      Depends on: 10.
      Research: phases/11/RESEARCH.md (field order of the Hetzner create
      forms; also used by phase 14).
      Scope: the shared frame every resource section builds on - API client
      over `bridge.Call`, action polling with a toast, detail and tab frame,
      Labels and Protection tabs, form errors from `details.fields`,
      confirmation dialogs, routes, help IDs - and the sections Servers and
      SSH Keys completely.
      Success criteria:
      1. Servers and SSH keys can be listed, shown, created, configured and
         deleted in the window, including every server action the API
         offers.
      2. A running action shows its progress and ends in a toast; a
         rejected form shows the error at its field from `details.fields`;
         deleting asks for confirmation.
      3. Labels and protection of a server and an SSH key can be changed in
         their tabs, and every new view opens its help text.
      Stops for: manual check (look and operation of the frame, before the
      remaining sections follow the same pattern).

- [x] 10 Tray program: shell, status, settings, server types, projects,
      logs.
      Requirements: UI-01, UI-03, UI-04, UI-05, UI-06, UI-07, UI-08.
      Depends on: 09.
      Success criteria:
      1. The tray icon starts and stops the cloud and shows its state.
      2. Settings, server types, projects with tokens and logs work in the
         window.
      Stops for: manual check.

- [x] 09 Cloud lifecycle and persistence.
      Requirements: LIFE-02, LIFE-03, LIFE-04, LIFE-05, LIFE-07, LIFE-08,
      LIFE-09, HOST-02; changes LB-01, LB-09 (Q14).
      Depends on: 08.
      Success criteria:
      1. After stop and start with "save", a server's uptime continues and
         every resource still works.
      2. With "shutdown", servers come back after start and every resource
         still works.
      3. While the cloud is stopped no hlab route or NRPT rule exists on the
         host.
      4. A load balancer without an IPv6 primary IP shows
         `2001:db8:<0xA0000000 | ID>::1` (Q14) and a PTR record set on its
         old formal IPv6 is kept on the new address; LB-01 and LB-09 tests
         and docs/user/deviations.md follow.

- [x] 07 DNS zones and reverse DNS.
      Requirements: DNS-01, DNS-02, DNS-03, DNS-04, DNS-05, RDNS-01,
      RDNS-02.
      Depends on: 05.
      Success criteria:
      1. An A record in a new zone resolves from a server, the host and
         WSL2; deleting the zone removes its NRPT rule.
      2. A PTR set on a server's IP resolves from the host.

- [x] 06 Firewalls.
      Requirements: FW-01, FW-02, FW-03, FW-04, FW-05.
      Depends on: 05.
      Success criteria:
      1. With a firewall allowing only port 22, SSH works and port 80 is
         blocked; removing the firewall opens port 80.
      2. Applying a firewall by label selector filters a server that gets
         the label later.

- [x] B1 Backlog: restart recovery, cloud-init and dev-loop bugs → phases/B1/

- [x] 04 Images, backups, rescue, ISOs and volumes.
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

- [x] 03 Server lifecycle: all power actions, rebuild, change type, root
      password, metadata, resource limits, metrics.
      Requirements: SRV-02, SRV-07, SRV-08, SRV-09, SRV-10, SRV-18, SRV-20,
      SRV-21, META-01.
      Depends on: 02.
      First plan: the servers module implements `resource.Reconciler` and
      replaces `servers.Deps.Machines` and `internal/cloud/servers.go`
      (decisions/resource-reconciler.md); SRV-01 and SRV-16 run again
      end-to-end after it.
      Success criteria:
      1. hcloud power actions change the VM visibly (SSH drops, comes back).
      2. A rebuilt server keeps its IP and boots the new image.
      3. `hcloud server reset-password` gives a password that logs in.
      4. A server type larger than the host's free RAM returns
         `resource_unavailable`.

- [x] 02 API conventions, catalog and the three clients: shared API behaviour,
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

- [x] 01 Walking skeleton: hlabd as dev service, appliance, one server
      created by hcloud boots and is reachable via SSH from the host;
      end-to-end harness against real Hyper-V and the real hcloud CLI.
      Its first plan writes the shared type packages `internal/agentapi`,
      `internal/platform` and `internal/hostnet` (types and interfaces from
      ARCHITECTURE.md, no logic), so later plans build in parallel against them.
      Requirements: API-01, API-02, API-10, PROJ-01, PROJ-02, PROJ-05,
      CAT-01, CAT-06, CAT-07, SRV-01, SRV-03, SRV-04, SRV-05, SRV-06,
      SRV-16, SRV-19, LIFE-01, LIFE-06, LIFE-10.
      Success criteria:
      1. `hlab cloud start` as a user without administrator rights starts the cloud
         without an admin prompt; `hlab cloud stop` stops it.
      2. `hlab project create` and `hlab token create` give a token that
         hcloud accepts with `HCLOUD_ENDPOINT=http://api.hetzner.internal/v1`.
      3. `hcloud server create --image ubuntu-24.04 --ssh-key ...` returns a
         server that answers `ssh root@<ip>` from the host and reaches the
         internet; `hcloud server delete` removes VM and disk.
      4. `task e2e` runs the end-to-end suite against Hyper-V and passes.

- [x] 05 Private networks, primary IPs and floating IPs, port isolation.
      Requirements: NET-01, NET-02, NET-03, NET-04, NET-05, NET-06, PIP-01,
      PIP-02, FIP-01, FIP-02, HOST-01.
      Depends on: 03.
      Also: remove the unused `Metrics(ctx, vm)` from `platform.Platform`,
      the hyperv implementation and the fake; `MetricsAll` stays (Q7).
      Success criteria:
      1. Two servers in one network ping each other on their private IPs;
         a server in another network cannot.
      2. A floating IP moved from one server to another answers from the
         new server.
      3. A hand-made route on a guest does not reach another guest
         directly.

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

- [x] B2 Backlog: VLAN release at project deletion, LB health check, docs and e2e test fixes → phases/B2/
