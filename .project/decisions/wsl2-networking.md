# Decision: supported WSL2 networking modes

Status: decided (2026-11-18)

## Question

Which WSL2 networking modes does hlab support? The success criterion requires
SSH to the servers and resolution of hlab names (API, user DNS zones) from
WSL2, where Ansible runs.

- NAT (default, `networkingMode=nat`): WSL runs in its own subnet behind the
  `vEthernet (WSL)` Hyper-V switch; the Windows host is its gateway.
- Mirrored (`networkingMode=mirrored` in `%UserProfile%\.wslconfig`, opt-in):
  WSL mirrors the host's interfaces and routes.
- In both modes `dnsTunneling` (default `true` since Windows 11 22H2) sends
  DNS queries from WSL to the Windows DNS client.

## Decision

- The first version supports WSL2 in NAT mode only, with `dnsTunneling`
  enabled (the default).
- hlab detects when WSL runs in mirrored mode (or another non-NAT mode) and
  reports clearly that it is not supported, instead of failing silently.
- Mirrored mode moved to a later version (target vision); on 2026-11-24 the
  user removed it from the plans altogether.
- hlab does not change `.wslconfig` and changes nothing inside WSL
  distributions. It checks the prerequisites, reports what is missing (for
  example `dnsTunneling=false`), and the documentation lists them.
- How far hlab must control WSL networking in NAT mode is clarified by an
  experiment before the spec settles it (see "Open: experiment").

## Reasons

- NAT is the default. A fresh Windows 11 Pro, as the success criterion
  describes it, works without the user changing WSL settings.
- One mode keeps the first version and its end-to-end test matrix smaller.
- Adding mirrored mode later is an extension, not a rebuild, as long as the
  NAT-specific host setup lives in its own component: mirrored mode needs
  less from hlab (WSL already uses the host's routes, and packets carry the
  host's source address).
- `.wslconfig` belongs to the user, applies only after `wsl --shutdown` (which
  stops all running distributions) and would have to be restored exactly at
  uninstall. Checking and reporting avoids that and keeps "uninstalling
  leaves nothing behind" simple.

## Open: experiment (feasibility, before the spec)

Find out what hlab must set up, keep up to date and remove for WSL in NAT
mode to reach the servers and resolve hlab names:

- Do packets from WSL to the hlab public range reach the servers through the
  host? Is `Forwarding Enabled` on `vEthernet (WSL)` and the hlab switch
  needed, and does it survive a WSL restart (the adapter is recreated)?
- Return path: does the appliance need a route to the WSL subnet via the
  host, or is there a simpler way (for example address translation on the
  host)? Does the WSL subnet change between WSL or Windows restarts?
- Does the Hyper-V firewall (on by default for WSL) block this traffic, and
  which rule is needed?
- Do NRPT rules for hlab names apply inside WSL with `dnsTunneling`
  (Microsoft documents only that queries go through Windows; NRPT is
  confirmed only by third-party sources)?
- What happens with the `consomme` mode WSL falls back to when NAT fails
  (WSL 2.3.25 and later), and can hlab detect it?
- What must the hlab service watch (WSL start and stop) and what must it
  remove at stop and uninstall?

## Alternatives considered

- Support both NAT and mirrored in the first version: rejected for now. It
  needs two network paths and double end-to-end runs. Kept as the target for
  a later version.
- Mirrored only: rejected. Users would have to change `.wslconfig`, which
  contradicts a fresh Windows 11 setup. There is also an open WSL issue where
  Hyper-V VMs on an internal switch are unreachable from mirrored mode
  (microsoft/WSL#11494).
- hlab edits `.wslconfig` itself: rejected. See the reasons above.

## Sources

- https://learn.microsoft.com/en-us/windows/wsl/networking
- https://learn.microsoft.com/en-us/windows/wsl/wsl-config
- https://techcommunity.microsoft.com/blog/itopstalkblog/windows-subsystem-for-linux-2---addressing-traffic-routing-issues/1764074
- https://github.com/microsoft/WSL/issues/11494
