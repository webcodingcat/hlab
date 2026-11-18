# ROADMAP DONE

Full entries of finished phases, moved here unchanged when a phase closes;
ROADMAP.md keeps one line per finished phase. Newest first. No skill reads
this file; it keeps the details for looking things up.

## Milestone 1: first version

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
