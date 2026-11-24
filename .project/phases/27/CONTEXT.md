# Phase 27 context

Confirmed by the user on 2026-11-24. Experiments: E40 to E44 in research/EXPERIMENTS.md.

## Assumptions

- Packet loss below 100 % hits only packets going to the server; the server's outgoing packets pass, so a ping loses about the chosen share.
- A `latency` delay is added once per round trip; between two locations each direction gets half; on a single server the whole delay goes on packets to the server, so DNS and metadata answers are delayed too.
- Jitter varies each delay around the set value (netem default); packets may arrive out of order.
- A latency fault between two locations hits only traffic (public and private) between that project's servers in those locations; load balancers, storage boxes, the host and other projects keep normal times.
- A `network` fault also covers the server's floating IPs and alias IPs and follows addresses or networks added while it is active.
- The same seed repeats the same drops as long as the same packets arrive in the same order and the fault's own settings stay the same; adding or ending a latency fault on the server continues the loss sequence, jitter changes which packets are hit from then on; the guide says this.
- `storage_box_unavailable` blocks the box's own address (and so its subaccounts' names); API actions and scheduled snapshots keep working.
- A blocked box's credentials still log in over SSH and FTP at another box's address (E17); docs/deviations.md names it.
- On the Windows host SMB returns with a delay after the fault ends (a first `net use` can fail for about 50 s, an old mapping about 80 s); docs/guide.md says so; SFTP, WebDAV, WSL2 and servers recover at once (E44).

## Technical choices

- `faults` gets one net effect for the three kinds with its own list of active net faults; Apply adds and pushes the agent document (on push failure the fault is removed and refused); Undo removes and pushes; registered next to `faults.Effects`.
- `Cloud.PushAgent` fills `doc.Faults` after the modules: server ID → `FaultServer` from `doc.Servers` and network members; locations target → the project's servers per location from the store; box ID → box IP via the storage box module. `NetFault` unchanged; the agent finds floating IPs from `cfg.FloatingIPs`.
- The agent splits into a pure planner (Config → nft rules and tc tree per interface; no build tag; unit tests) and a Linux applier `faults_linux.go` (replaces table `hlab_fault` whole through google/nftables, runs `tc`).
- Private cuts (E40): input `iifname "vrf<vlan>" meta sdifname "t<vlan>" ip saddr <ip>`, matching every packet from the server; output `oifname "t<vlan>" ip daddr <ip>`; never `sdifname` or `iifname` in output. Public cuts: `ip saddr` in input, `ip daddr` in output.
- Shaping (E41): tc trees only on `p0` and `t<vlan>` egress, never on `vrf<vlan>`; between two locations both directions shaped with half the delay; `htb rate 10gbit quantum 200000`.
- Netem changes (E42): skip `tc qdisc change` when settings are unchanged; delay or jitter only changes go without `seed`; a new or changed loss always sets its seed; a netem without loss still gets an explicit seed from hlab.
- Offloads (E43): `/usr/sbin/ethtool -K <dev> gro off gso off tso off lro off` while a loss fault is active.
- The applier removes a leftover `hlab_fault` table and its own root qdiscs when the list is empty, also on the first document after a cloud start.
- Ending a net fault while a server is deleted calls PushAgent; `pushMu` must not already be held on that path.
- e2e rows join the sequential FAULT block in `test/e2e/fault_net_test.go`: two servers in fsn1, one in hel1, a private network and a storage box; repeatability measured on private traffic; DNS and metadata during a cut checked by a script started on the server before the cut, its log read after the fault ends.
- e2e from the host for the box fault (E44): before the fault wait with a deadline until `netstat -n -p tcp` shows no connection to `<box ip>:445`; during the fault a TCP dial to 445 with a 3 s timeout fails; after the fault retry a new `net use` for up to about 2 min, judged by exit code only.

## Answers

- Q1: success criterion 4 is proven by unit tests of the agent's planner (an empty or smaller list removes every rule and qdisc, leftovers included) plus e2e checks that ping time and loss return to normal after ending by hand, by duration and at cloud stop. No new status interface.
- Q2: the fault blocks only the box's own address; the side path over another box's address (E17) is named in docs/deviations.md.
