# Phase 23 context: server console

## Confirmed assumptions

- `request_console` works on a server in any state, read-only tokens excepted; it creates a finished `request_console` action in the server's action list and changes nothing on the server.
- The response holds `wss://console.hetzner.internal/?server_id=<id>&token=<uuid>` and a password of 32 random letters and digits, in the form of Hetzner's example (user correction).
- VNC Authentication checks only the first 8 characters of the password (as at Hetzner); this is said in code comments only, not in the response or the user documentation (user correction).
- A token is a UUID, works for exactly one connection and only if it starts within one minute of being issued; an open connection is never cut by token expiry and has no idle timeout.
- A wrong password is refused in the VNC handshake with a reason the client shows; an unknown, expired or foreign token is refused before the connection is set up.
- While the server is saved the console shows its last screen; while it is off, starting, stopping, saving or restoring it shows the last picture it had or black; the connection stays open and the live picture returns when Hyper-V delivers one.
- Open consoles close when the server is deleted or the cloud stops; several consoles on one server are allowed and share keyboard and mouse.
- The picture refreshes at most 5 times per second and only while someone is connected; an input event fetches a new picture straight away.
- When the guest changes its resolution, the console changes size with it.
- A held key repeats in the server as often as the VNC client repeats it; Hyper-V repeats nothing.
- The mouse works with left, right and middle button and the wheel; one wheel step scrolls one notch.
- The console certificate for `console.hetzner.internal` comes from the hlab CA and is issued anew at every cloud start.
- The appliance forwards port 443 to the console and its DNS answers `console.hetzner.internal`; both are reachable from the host and from WSL2.
- "Trust CA" stays off unless the user turns it on.
- In the tray, "Console" in the server's actions menu opens a separate window "Console: <server name>" with the screen scaled to the window, a connection state, "Send Ctrl+Alt+Del" and, after a drop, "Reconnect" (fetches a new URL).
- At most one console window per server; clicking "Console" again brings it to the front.
- Closing a console window closes it for real and ends the connection; the main window still only hides; quitting the tray closes all console windows.
- The console window is not a project view: no sidebar entry, no help text of its own; the server view's help text describes it.
- The graphical console is built (gate passed, decisions/server-console.md, E21, E25); the serial console is not.
- The documentation says one open console costs about 0.7 CPU cores in the Hyper-V service `vmms.exe`, and an own browser or VNC client accepts the URL only while "Trust CA" is on.
- `hcloud server request-console` prints the three lines it prints against Hetzner; the e2e test takes the URL from there.
- The CON-01 e2e test uses its own small VNC client that trusts `/ca.pem`: the screen is not black, a key typed at the login prompt changes the screen and reaches the guest keyboard device, and mouse input reaches the guest's "Microsoft Vmbus HID-compliant Mouse" (read over SSH).
- The UI-15 tray test runs with "Trust CA" off; if it is on, the test turns it off and restores it afterwards.

## Technical choices

- New hlabd package `internal/console`: `Service.Issue(serverID, vm)`, tokens and passwords in memory only, a TLS listener on `198.19.0.2:8781` with a `tls.Certificate` issued in memory at each start (not stored in kv).
- WebSocket via `github.com/coder/websocket` v1.8.15 (`websocket.NetConn`, binary, no subprotocol required); a handwritten RFB 3.8 server: VNC Authentication only, Raw encoding, DesktopSize pseudo-encoding, changed rectangles by a frame diff in Go.
- A bad token gets HTTP 403 without detail before the upgrade; the token is consumed at the upgrade; a test clock is injected for the one-minute limit.
- The graphical source is an additive `platform.Console` extension in `platform/hyperv` (raw `IWbemServices` vtables, one COM connection per open console on a locked OS thread, input calls serialized, RGB565 frames without the 4 trailing bytes converted to 32 bpp); the fake platform gets a scripted screen.
- All keys go through `Msvm_Keyboard.TypeScancodes` with scan code set 1 (down = make, up = break = make | 0x80, 0xE0 prefix where needed); `TypeText` is never used and Home never through a VK (E25).
- Pointer: pixels of the current resolution, clamped, never negative; RFB button bits 0/1/2 map to Hyper-V indexes 1/3/2 via `SetButtonState`, bits 3/4 to `SetScrollPosition(+120/-120)`.
- Frames: a non-zero thumbnail result (32775, 32779) means "no picture now" and the last frame stays; `Msvm_VideoHead` is read before each grab and a change sends a DesktopSize rectangle as the last rectangle of the update; incremental requests wait until something changes; a frame is grabbed right after each input event and on the 5/s timer.
- The server honours `SetPixelFormat` for 32-bpp true colour with the shifts the client sets (noVNC: little endian, red shift 0, blue shift 16).
- A wrong password is answered with SecurityResult 1 plus a reason string.
- `agentapi.Config` gains `Console ConsoleForward{Listen, Backend}` (additive, schema stays 1); the agent runs a plain TCP forward on `198.19.0.1:443` and an A record for `console.hetzner.internal`; `transit.Addrs` gains `ConsoleListen`/`ConsoleProxy`; the hostnet firewall rule opens 8781 besides 8780.
- The servers module gets a `Console` dependency (an interface with `Issue`); `Deviations` loses the `request_console` entry and gains entries for the URL host and the CA certificate if DOC-03 needs them.
- Tray: a Wails service in `cmd/hlab-tray` (`Windows.OpenConsole(project, id, name)`) creates or focuses the window at `#/server-console/<project>/<id>`, outside `views.json`; it keeps its own window map under a mutex, cleared in the window's `WindowClosing` listener (E24).
- The console page calls `request_console` through the project proxy, then `bridge.Console.Open(wssURL)`, which returns a loopback `ws://127.0.0.1:<port>/<random>` URL; that proxy checks TLS against `/ca.pem` with ServerName `console.hetzner.internal` and allows only the origin `wails.localhost`; the password stays in the console page's memory.
- noVNC is loaded with a dynamic `import()`, typed by `src/novnc.d.ts`.
- Tray tests wait for the page whose URL contains the console route, not the first `about:blank` page (E24).
- New dependencies: `@novnc/novnc` 1.7.0 (npm); `github.com/coder/websocket` v1.8.15 promoted to a direct dependency.

## Answers

- Q1 Keyboard layout: (c) both. Physical keys when the VNC client offers the QEMU Extended Key Event (noVNC in the tray always does), passed straight through as scan codes; for clients that do not, characters as typed, translated by a keysym → US set-1 table with Shift corrected.
