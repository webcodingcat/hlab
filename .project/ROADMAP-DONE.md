# ROADMAP DONE

Full entries of finished phases, moved here unchanged when a phase closes;
ROADMAP.md keeps one line per finished phase. Newest first. No skill reads
this file; it keeps the details for looking things up.

## Milestone 1: first version

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
