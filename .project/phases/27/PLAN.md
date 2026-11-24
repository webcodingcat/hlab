# Phase 27: Network, latency and storage box faults

## Interfaces

Additive to `internal/agentapi` (27-01), as in ARCHITECTURE.md "hlab-agent":

```go
package agentapi

type Config struct {
	// ... existing fields ...
	Faults []NetFault // phase 27: network, latency and storage box faults
}

type NetFault struct {
	ID          int64         // the fault's ID
	Kind        string        // "network" | "latency" | "storage_box_unavailable"
	Server      *FaultServer  // network, latency on a server
	Traffic     string        // network: "public" | "private" | "all"
	From, To    []FaultServer // latency between locations: the project's servers in each location
	BoxIP       netip.Addr    // storage_box_unavailable
	LossPercent int           // network: 1-100 (100 cuts)
	Seed        uint64        // network with LossPercent below 100
	DelayMs, JitterMs int     // latency
}
type FaultServer struct {
	ID         int64
	PublicIPv4 netip.Addr     // invalid without one
	Private    []FaultPrivate // always with the VLAN, private ranges may overlap
}
type FaultPrivate struct { VLAN int; IP netip.Addr; AliasIPs []netip.Addr }
```

Additive to `internal/faults` (27-01):

```go
package faults

// NetEffects is the effect of network, latency and storage_box_unavailable.
// It keeps the net faults whose Apply runs or that are listed; Apply adds
// the fault and calls push (an error removes it again and refuses the
// fault), Undo removes it and calls push.
type NetEffects struct{ /* mutex, faults by ID, push */ }

func NewNetEffects(push func(ctx context.Context) error) *NetEffects

// Effects returns the effect for the three kinds, for Options.Effects
// next to Effects(s, v).
func (n *NetEffects) Effects() map[string]Effect

// Faults returns the kept net faults by ID (copies).
func (n *NetEffects) Faults() []Fault
```

New in `internal/cloud` (27-05, Windows only like the package):

```go
// netFaults turns the net faults into agent document entries. doc holds
// the modules' contributions already (servers, networks, floating IPs,
// storage boxes); locations targets take the project's servers per
// location from the store, a box target its IP from the storage box
// record. A fault whose target is gone is left out.
func netFaults(tx store.Tx, doc *agentapi.Config, fs []faults.Fault) ([]agentapi.NetFault, error)
```

New in `internal/agent`, no build tag (27-02, 27-03), used by the Linux
applier (27-04):

```go
package agent

// FaultPlan is what the faults applier enforces for one document.
type FaultPlan struct {
	Rules       []FaultRule             // table inet hlab_fault, every rule drops
	Devices     map[string][]NetemClass // egress device ("eth0", "t<vlan>") -> classes; absent: no fault qdisc
	OffloadsOff bool                    // a loss fault below 100 % is active
}

// FaultRule is one drop rule; empty strings and invalid addresses match
// anything. SdIf is never set in chain "output" (E40).
type FaultRule struct {
	Chain        string // "forward" | "input" | "output"
	IIf, OIf     string // iifname, oifname
	SdIf         string // meta sdifname
	Saddr, Daddr netip.Addr
}

// NetemClass is one HTB class with its netem child and flower filters.
// Key names the flows it shapes (destination server and source group:
// all sources, or the servers of one locations fault), stable across
// documents, so an unchanged class is never touched.
type NetemClass struct {
	Key   string
	Minor uint16 // class 1:<Minor>, netem <Minor>:; set by TcCommands
	Netem Netem
	Flows []Flow
}

// Netem settings; Seed is always set (a netem without loss takes the
// latency fault's ID, E42).
type Netem struct {
	DelayUs, JitterUs int
	LossPercent       int
	Seed              uint64
}

// Flow selects packets to Dst, from Src (invalid: any source; such
// filters get a lower priority than source/destination pairs).
type Flow struct{ Src, Dst netip.Addr }

// PlanFaults builds the plan of cfg.Faults; public is the public device.
func PlanFaults(cfg agentapi.Config, public string) FaultPlan

// TcState is the applied classes per device, with their minors.
type TcState map[string][]NetemClass

// TcCommands returns the tc argument lists (without "tc") that turn have
// into want, and the new state. have nil means unknown (first document
// after the agent started): the root qdisc of every candidate device is
// deleted first. An unchanged class gives no command; a change of delay
// or jitter only is `qdisc change` without seed; a new or changed loss is
// `qdisc change` or `add` with seed (E42).
func TcCommands(have TcState, want map[string][]NetemClass, candidates []string) ([][]string, TcState)
```

Rules of the plan (ARCHITECTURE.md "hlab-agent", E29, E40, E41):

- `network` 100 % cuts. Public addresses are the server's public IPv4
  and the floating IPs in `cfg.FloatingIPs` whose Target it is; private
  addresses are each `FaultPrivate` IP and alias IP with its VLAN.
  - Public: forward `iifname <public> ip saddr A` and
    `oifname <public> ip daddr A`; input `iifname <public> ip saddr A`;
    output `oifname <public> ip daddr A`.
  - Private: forward `iifname "vrf<vlan>" ip saddr A` and
    `oifname "t<vlan>" ip daddr A`; input `iifname "vrf<vlan>"
    meta sdifname "t<vlan>" ip saddr A`; output `oifname "t<vlan>"
    ip daddr A`.
- `network` below 100 %: one class per destination address set on its
  device, `loss random <p>% seed <seed>`, filters `dst_ip` of every
  address of the server for the chosen traffic.
- `latency` on a server: the whole delay and jitter on packets to the
  server on every device it is on, from any source, merged into the class
  of a loss fault on the same server.
- `latency` between locations: for each server in To, a class with half
  the delay and half the jitter for packets from the From servers'
  addresses (public with public, private only within the same VLAN), and
  the same for each server in From with sources in To; it carries the
  destination's loss and server delay as well.
- `storage_box_unavailable`: forward `oifname <public> ip daddr Box` and
  `iifname <public> ip saddr Box`.
- Classes: HTB root `1:` with `default 1`, class `1:1` unshaped, `htb rate
  10gbit quantum 200000`, netem with `limit 100000`; never on `vrf<vlan>`.

## Plans

- [x] 27-01 Fault entries in the agent document and the net effect
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FAULT-07, FAULT-08, FAULT-11 | Tests: internal/faults/effects_test.go
  - Reads: ARCHITECTURE "faults (phase 25)", "hlab-agent" (the `agentapi.Config` block); DECISIONS "2026-11-24 Fault kinds become active by registered effects", "2026-11-24 Undo errors do not keep a fault alive", "2026-11-24 Agent document without JSON tags"; phases/27/CONTEXT.md "Technical choices"; `internal/faults/effects.go`, `manager.go` `Create`
  - Size: ~140 lines in 3 files
  - Adjusts: -
  - [x] `internal/agentapi/agentapi.go`: `Config.Faults`, `NetFault`, `FaultServer`, `FaultPrivate` as in "Interfaces".
  - [x] `internal/faults/neteffects.go` (new): `NetEffects`, `NewNetEffects`, `Effects`, `Faults` as in "Interfaces"; Apply adds the fault before calling push, so the document built during the push holds it.

- [x] 27-02 Agent: fault plan
  - Wave: 2 | Depends on: 27-01 | Review: alone
  - Requirements: FAULT-07, FAULT-08, FAULT-11 (FAULT-02: an empty list plans nothing) | Tests: internal/agent/faults_test.go
  - Reads: REQUIREMENTS FAULT-07, FAULT-08, FAULT-11; ARCHITECTURE "hlab-agent" (Faults paragraph, `agentapi.Config`); EXPERIMENTS E29, E40, E41; phases/27/CONTEXT.md; this file "Interfaces" (rules of the plan); `internal/agent/firewall.go` (floating IPs per server, pure compile pattern)
  - Size: ~300 lines in 1 file
  - Adjusts: -
  - [x] `internal/agent/faultplan.go` (new): `FaultPlan`, `FaultRule`, `NetemClass`, `Netem`, `Flow`, `PlanFaults` by the rules in "Interfaces": cuts, loss, server latency, location latency, box drops, merging per destination and source group with stable keys, the seed of a netem without loss, `OffloadsOff`; rules and classes sorted so the same document gives the same plan.

- [ ] 27-03 Agent: tc commands from the applied state
  - Wave: 3 | Depends on: 27-02 | Review: alone
  - Requirements: FAULT-07, FAULT-08 (FAULT-02: ending undoes the effect) | Tests: internal/agent/faults_test.go
  - Reads: ARCHITECTURE "hlab-agent" (Faults paragraph); EXPERIMENTS E29 (syntax), E41, E42; phases/27/CONTEXT.md; this file "Interfaces"
  - Size: ~200 lines in 1 file
  - Adjusts: -
  - [ ] `internal/agent/faulttc.go` (new): `TcState`, `TcCommands`: with have nil, `qdisc del dev <d> root` for every candidate (errors for a missing qdisc are the applier's to ignore); a device leaving want loses its root qdisc; a device entering want gets root HTB, class `1:1`; per class by Key: new → class, netem (with seed), flower filters (pairs prio 1, destination only prio 2); gone → filters, class deleted; changed flows → filters replaced, netem untouched; changed netem → `qdisc change` per E42; minors kept per Key, new ones from `0x10` upward without reuse of a live minor.

- [ ] 27-04 Agent: faults applier on the appliance
  - Wave: 4 | Depends on: 27-03, 27-05 | Review: alone
  - Requirements: FAULT-07, FAULT-08, FAULT-11 | Tests: test/e2e/fault_net_test.go
  - Reads: ARCHITECTURE "hlab-agent"; EXPERIMENTS E29, E40, E43; phases/27/CONTEXT.md; `internal/agent/firewall_linux.go`, `firewall_other.go`, `isolation_linux.go` (nftables table handling), `cmd/hlab-agent/main.go` `runAppliance`
  - Size: ~300 lines in 4 files
  - Adjusts: -
  - [ ] `internal/agent/faults_linux.go` (new) and `faults_other.go` (new, stub like `firewall_other.go`): Applier `faults`: `PlanFaults`; table `inet hlab_fault` (chains forward, input, output at priority -10, policy accept) replaced whole through google/nftables, deleted when the plan has no rule (also a leftover one); candidates = the public device and every `t<vlan>` link; runs `TcCommands` through `tc` (exec, combined output in errors, "No such file" on a delete ignored); `/usr/sbin/ethtool -K <dev> gro off gso off tso off lro off` on the public and trunk adapters when `OffloadsOff` turns on, `on` again when it turns off; keeps `TcState` and the offload state in memory.
  - [ ] `cmd/hlab-agent/main.go`: register the applier after `NewFirewall`.

- [x] 27-05 Net faults in the cloud
  - Wave: 2 | Depends on: 27-01 | Review: alone
  - Requirements: FAULT-07, FAULT-08, FAULT-11 | Tests: internal/cloud/faults_test.go
  - Reads: ARCHITECTURE "faults (phase 25)" (the network paragraph), "hlab-agent" (`agentapi.Config`), "Cloud start and stop"; phases/27/CONTEXT.md; `internal/cloud/faults.go`, `internal/cloud/cloud.go` `PushAgent`, `newFaults` callers; `internal/resources/servers/servers.go` `Contribute`, `internal/resources/networks/contribute.go`, `internal/resources/storageboxes` `Load`
  - Size: ~200 lines in 2 files
  - Adjusts: -
  - [x] `internal/cloud/faults.go`: per run a `faults.NewNetEffects(c.PushAgent)` kept beside the manager, its `Effects()` merged into `Options.Effects`; `netFaults` as in "Interfaces" (server target: public IPv4 from `doc.Servers`, private from the members of `doc.Networks` with VLAN, IP and alias IPs; locations: the project's servers in the store by location, then their entries as for a server; box: the record's IP; parameters and seed from the fault).
  - [x] `internal/cloud/cloud.go`: `PushAgent` sets `doc.Faults` from the run's net effects inside its store transaction, after the modules; none while stopped. Check that no caller of `endFaults` holds `pushMu`.

- [ ] 27-06 Documentation
  - Wave: 5 | Depends on: 27-01, 27-02, 27-03, 27-04, 27-05 | Review: alone
  - Requirements: DOC-01, DOC-03, DOC-04, DOC-07 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation" (D1, D4, D6) and "Accounts and secrets" (hlab-docs context); REQUIREMENTS FAULT-07, FAULT-08, FAULT-11, DOC-01 to DOC-07; decisions/fault-injection.md; EXPERIMENTS E17, E44; phases/27/CONTEXT.md
  - Size: ~150 lines in 3 files
  - Adjusts: -
  - [ ] `docs/guide.md`, "Fault injection": the sentence naming `network`, `latency` and `storage_box_unavailable` as not available goes; one example each with `hlab fault create` (a cut, a loss with seed, latency on a server and between fsn1 and hel1, a box); loss hits packets to the server, the delay is added once per round trip, jitter may reorder, what keeps a seed's pattern repeatable, that a locations latency hits only server-to-server traffic of the project, that a cut also stops DNS, metadata and the API while the console keeps working, and the SMB delay on the Windows host after a box fault.
  - [ ] `docs/deviations.md` (a blocked box is still reachable with its credentials over SSH and FTP at another box's address) and `docs/development/components.md` (the agent's faults applier, plan and tc state).

## Order of the e2e rows

The new rows run in the sequential FAULT block of `test/e2e/e2e_test.go`
after FAULT-05 and before the restart rows. Their fixture reuses the
fault fixture's debian-13 server as server A (fsn1) and adds server B
(fsn1), server C (hel1), one private network with all three, and one
storage box with SSH, Samba and WebDAV on and reachable from outside, so
the block stays within the three server slots. The shared restart of
`faultFixture.restart` also creates a `network` loss fault (private, on
A) and a `latency` fault between fsn1 and hel1 before the stop, checked
by FAULT-07/restart and FAULT-08/restart; the net fixture goes with
`releaseFaultFixture`.
