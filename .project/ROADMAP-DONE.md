# ROADMAP DONE

Full entries of finished phases, moved here unchanged when a phase closes;
ROADMAP.md keeps one line per finished phase. Newest first. No skill reads
this file; it keeps the details for looking things up.

## Milestone 1: first version

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
