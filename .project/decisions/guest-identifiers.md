# Guest-visible identifiers (Q2)

- Date: 2026-11-18
- Raised by: phase 01 verifier (open decision Q2 in STATUS.md)
- Affects: SRV-01, SRV-03, SRV-04
- Related: DECISIONS.md "Server MACs, IPv6 and root login", "Volume identifier
  byte order", "Guest-visible identifiers", "IPv6 assigned formally, not
  routed"; EXPERIMENTS.md E6

## Decision

The user approves all four identifiers exactly as phase 01 recorded them
(option 1):

1. **Server MAC** `02:48:4c` followed by `0x1` and the low 20 bits of the
   server ID (`02:48:4c:1x:xx:xx`); the appliance keeps `02:48:4c:00:00:01`.
2. **Formal IPv6** `2001:db8:<server ID as 32 bits>::/64`, reported by the
   API only and not configured in the guest.
3. **Root login**: server vendor-data sets `disable_root: false`, so key
   logins as root work like at Hetzner.
4. **Volume identifier byte order**: `CreateDisk` writes `page83` in GUID
   text byte order, so `484C4142-0000-4000-8000-<volume ID, 12 hex digits>`
   reaches the disk with the volume ID in the last 6 bytes.

## Reasons

- **MAC**: the locally administered prefix `02` never collides with vendor
  MACs; `48:4c` ("HL") marks hlab; the `0x1` nibble keeps servers apart from
  the appliance; the MAC is derived from the ID, so it needs no extra stored
  field. Server IDs start at 1 and are never reused, so the 20 bits wrap after
  1,048,575 servers: server 1,048,577 gets server 1's MAC. A conflict needs
  both servers to exist at once, which a local lab practically never reaches.
  Release and dev profile produce the same MACs, but only one cloud runs and
  each has its own switch.
- **IPv6**: `2001:db8::/32` is the documentation range (RFC 3849), never
  routed, so it cannot collide with a real network; 32 bits of ID suffice.
  The address carries no traffic (known deviation), so the scheme only has to
  be unique and stable.
- **Root login**: Hetzner images allow root logins with the given key, while
  Ubuntu cloud images block them by default; scripts and inventories using
  `root@` must run unchanged (goal: unmodified clients). Multi-user security
  is a non-goal.
- **Volume byte order**: per E6 only the last 6 GUID bytes survive in both
  the T10 and the NAA designator the guest sees, so the volume ID lives there
  and the udev rule derives `scsi-0HC_Volume_<id>` like at Hetzner; text byte
  order keeps `Get-VHD` readable (`484C4142` = "HLAB"). 48 bits of volume ID
  suffice.

## Rejected alternatives

- **Configurable IPv6 prefix** (option 2): an extra setting without effect
  while IPv6 carries no traffic; a routed IPv6 later needs its own decision
  anyway.
- **ULA prefix (`fd00::/8`)**: may collide with ULA networks already used at
  home or in VPNs.
- **Different MAC layout** (option 3), e.g. 32 bits of ID after `02:48`, or a
  random MAC stored per server: removes a wrap-around no lab reaches, at the
  cost of the readable prefix or an extra stored field.
- **Root login blocked**: deviates from Hetzner and breaks scripts that log
  in as root.
- **Other bytes or byte order for the volume ID**: the ID would be lost in
  the NAA designator or be harder to read on Windows, with no benefit.

## Consequences

- No code change; the values in DECISIONS.md stand and are now approved.
- Q2 can be closed in STATUS.md by /wcc:change.
