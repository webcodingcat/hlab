# Phase 23 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| SRV-17 | request console answers 201 with a finished `request_console` action that the server's action list shows, a `wss_url` `wss://console.hetzner.internal/?server_id=<id>&token=<uuid>` and a password of 32 letters and digits; nothing else on the server changes | unit | internal/resources/servers/servers_test.go: TestServers/SRV-17 | 23-07 |
| CON-01 | a keysym becomes scan code set 1 bytes on a US layout (table: letter, capital with Shift held, symbol whose US Shift state differs from the held Shift, digit, Return, BackSpace, Home and arrows with 0xE0, F5, Control_L/Alt_L/Delete as sent by Ctrl+Alt+Del, unknown keysym gives nothing); key up gives the break code | unit | internal/console/input_test.go: TestInput/CON-01/keysym | 23-02 |
| CON-01 | a QEMU Extended Key Event keycode passes through as set 1 make and break, with the 0xE0 prefix for extended keys | unit | internal/console/input_test.go: TestInput/CON-01/qemu_key | 23-02 |
| CON-01 | a pointer event maps button bits 0/1/2 to Hyper-V buttons 1/3/2 as press and release transitions, bits 3/4 to scroll +120/-120, and clamps the position to the screen | unit | internal/console/input_test.go: TestInput/CON-01/pointer | 23-02 |
| CON-01 | with the right password the RFB 3.8 handshake (VNC Authentication) succeeds, ServerInit carries the screen size and name, and a full update request returns the screen as Raw in the client's 32-bpp pixel format (noVNC's red shift 0, blue shift 16, and the server default) | unit | internal/console/rfb_test.go: TestRFB/CON-01/session | 23-03 |
| CON-01 | an incremental update request is answered only after the screen changes, with only the changed rectangle; a key or pointer event triggers a new grab before the next poll | unit | internal/console/rfb_test.go: TestRFB/CON-01/updates | 23-03 |
| CON-01 | a resolution change sends a DesktopSize rectangle as the last rectangle and then the screen at the new size | unit | internal/console/rfb_test.go: TestRFB/CON-01/resize | 23-03 |
| CON-01 | while the source has no picture the connection stays open and shows the last frame (black before the first), and the live picture returns once the source delivers one | unit | internal/console/rfb_test.go: TestRFB/CON-01/no_picture | 23-03 |
| CON-01 | key events (plain and, after the QEMU pseudo-rectangle, extended) and pointer events reach the source as scan codes and mouse calls (one case each) | unit | internal/console/rfb_test.go: TestRFB/CON-01/input | 23-03 |
| CON-02 | a wrong password gets SecurityResult 1 with a reason and the connection ends without any screen data | unit | internal/console/rfb_test.go: TestRFB/CON-02/wrong_password | 23-03 |
| CON-01 | an issued URL connects over TLS (certificate of a test CA) and WebSocket to an RFB session of its server, and the open connection keeps working after the token's minute has passed (test clock) | unit | internal/console/service_test.go: TestService/CON-01/connect | 23-04 |
| CON-01 | ending a server's consoles (server deleted) closes its open connections and leaves those of other servers open | unit | internal/console/service_test.go: TestService/CON-01/close_server | 23-04 |
| CON-02 | a connection is refused with 403 before the WebSocket upgrade for a token that is unknown, expired (test clock past one minute), or issued for another server | unit | internal/console/service_test.go: TestService/CON-02/token | 23-04 |
| CON-01 | the URL and password printed by `hcloud server request-console` give a VNC session (own RFB client, `/ca.pem` loaded, "Trust CA" untouched) whose screen is not black | e2e | test/e2e/console_test.go: TestE2E/CON-01/screen | 23-07 |
| CON-01 | keys typed in that session change the screen at the login prompt and arrive at the guest's keyboard device (read over SSH) | e2e | test/e2e/console_test.go: TestE2E/CON-01/keyboard | 23-07 |
| CON-01 | pointer moves, a click and a wheel step in that session arrive at the guest's "Microsoft Vmbus HID-compliant Mouse" (found in `/proc/bus/input/devices`, read over SSH) | e2e | test/e2e/console_test.go: TestE2E/CON-01/mouse | 23-07 |
| UI-15 | the loopback proxy relays binary messages both ways to a wss server whose certificate comes from the given CA although the system does not trust it; it refuses a server certificate of another CA and a WebSocket from an origin other than `wails.localhost` | unit | internal/bridge/console_test.go: TestConsoleProxy/UI-15 | 23-08 |
| UI-15 | with "Trust CA" off, the actions menu entry "Console" of a running server opens the window "Console: <name>", its screen shows a non-black picture, and typed keys change it | e2e | ui/frontend/tests/e2e/serverconsole.spec.ts: "UI-15 console window shows the screen and sends keys" (run by test/e2e/ui_test.go: TestE2E/UI-15, which switches "Trust CA" off and restores it) | 23-09 |

DOC-01 to DOC-06 keep their existing tests in `test/docs/docs_test.go`
and `ui/frontend/tests/unit/help.test.ts`; DOC-07 is manual.

## Writers

1. console: `internal/console/input_test.go`, `internal/console/rfb_test.go`,
   `internal/console/service_test.go` (in-package fake `platform.ConsoleConn`
   with a scripted screen and a recorded input log, and a small RFB test
   client, shared by the three files), ~750 lines.
2. servers and tray proxy: `internal/resources/servers/servers_test.go`
   (SRV-17 subtest replaced), `internal/bridge/console_test.go`, ~250 lines.
3. e2e: `test/e2e/console_test.go` (RFB client over wss with `/ca.pem`,
   guest input device reader over SSH, one shared server and session,
   registered in `test/e2e/e2e_test.go`), `test/e2e/ui_test.go` (UI-15
   runner), `ui/frontend/tests/e2e/serverconsole.spec.ts`, ~550 lines.
