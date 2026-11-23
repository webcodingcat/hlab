# Decision: server console

Status: decided (2026-11-18), changed (2026-11-23)

## Question

How should the server console work in hlab? Hetzner's
`POST /servers/{id}/actions/request_console` returns a `wss_url` and a
`password` for a VNC (RFB) console over WebSocket (used by
`hcloud server request-console` and noVNC in the Hetzner web UI). Hyper-V has
no VNC; it offers VMConnect/enhanced session (RDP-based, port 2179), the video
framebuffer via WMI (`GetVirtualSystemThumbnailImage`), keyboard and mouse
injection via WMI (`Msvm_Keyboard`, synthetic mouse) and serial COM ports via
named pipes.

## Decision

The server console is not part of the first version. It moves to a later
version (target vision, not first-version scope).

How the first version answers `request_console` (for example a documented
error response) is left to the spec; it must not pretend a working console.

## Reasons

- No Hetzner client the project targets needs the console to work: the
  OpenTofu provider and the Ansible collection do not use it, and the hcloud
  CLI only prints URL and password.
- A real console needs its own component (RFB-over-WebSocket server,
  framebuffer polling and input injection via WMI, noVNC in the UI) and carries
  the largest feasibility risk (frame rate and CPU load of WMI framebuffer
  polling are unmeasured). Deferring it keeps the first version smaller and
  lower-risk.

## Alternatives considered (for the later version)

- (a) Real wss VNC console via a bridge: framebuffer from WMI, keyboard and
  mouse via WMI, noVNC embedded in the UI, optionally an "Open in VMConnect"
  button. Closest to Hetzner, works for ISO installs and boot problems;
  needs a feasibility experiment first.
- (b) Formally correct but non-functional API response, UI opens VMConnect.
  Rejected for any version: contradicts "everything really works".
- (c) Serial text console (ttyS0 via COM port named pipe), either only in the
  UI (xterm.js) or rendered into an RFB framebuffer behind the real `wss_url`.
  Lower risk than (a), but no graphical GRUB or ISO installers. Candidate
  fallback if (a) proves infeasible.
- Bridging VMConnect's RDP (port 2179) to VNC: rejected as the hardest path
  (needs an RDP client such as FreeRDP).

These options are kept as input for the later version; none of them is chosen
yet.

## Change 2026-11-23: the console moves into milestone 1

The user moved the server console into milestone 1 (phase 23):

- Approach: alternative (a), the graphical console, if an experiment shows
  it is fast enough; otherwise alternative (c), the serial console rendered
  as RFB behind the same `wss_url`. Too slow means any of: fewer than about
  5 frames per second at 1024x768, keyboard input visible later than about
  0.5 s, or hlabd needing more than one CPU core per open console for
  good. The experiment measures exactly these three values and records
  them in `research/EXPERIMENTS.md`.
- Address: `wss://console.hetzner.internal/?server_id=<id>&token=<token>`
  plus a VNC password, built like Hetzner's
  `wss://web-console.hetzner.cloud/...`, reachable from the host and WSL2.
- Certificate: issued by the hlab CA. The tray's console window works
  whether or not the CA is trusted; an own browser or VNC client accepts
  the URL only while "Trust CA" is on, which the documentation and the
  deviations say. hlab never switches trust on by itself.
- The tray opens the console from the server's actions menu (noVNC);
  `unsupported_error` for request_console goes away.
