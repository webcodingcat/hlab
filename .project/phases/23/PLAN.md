# Phase 23: Server console

## Interfaces

```go
// package platform (internal/platform/platform.go), additive; written by 23-02.

// Console is an optional extension of Platform: hlabd type-asserts it.
// platform/hyperv implements it (23-06).
type Console interface {
	// OpenConsole opens the screen, keyboard and mouse of VM vm; one per
	// open RFB connection. NotFound when the VM does not exist.
	OpenConsole(ctx context.Context, vm string) (ConsoleConn, error)
}

// ConsoleConn is one open console of a VM. Calls are serialized by the
// caller; Close may be called from another goroutine.
type ConsoleConn interface {
	// Screen grabs the current screen at its current resolution. ok false:
	// no picture now (VM off, starting, stopping, saving, restoring), not
	// an error; err only for real failures.
	Screen(ctx context.Context) (f Frame, ok bool, err error)
	// Scancodes types scan code set 1 bytes (make, break = make|0x80,
	// 0xE0 prefix before extended make and break).
	Scancodes(ctx context.Context, codes []byte) error
	// MouseMove moves to x, y in pixels of the current resolution; the
	// caller clamps to the screen, never negative.
	MouseMove(ctx context.Context, x, y int) error
	// MouseButton presses or releases button 1 left, 2 right, 3 middle.
	MouseButton(ctx context.Context, index int, down bool) error
	// MouseScroll scrolls by delta; +120 is one notch up.
	MouseScroll(ctx context.Context, delta int) error
	Close() error
}

// Frame is a screen as RGB565 little endian, Width*Height*2 bytes (the 4
// trailing bytes of the WMI image already dropped).
type Frame struct {
	Width, Height int
	Pix           []byte
}
```

```go
// package console (internal/console)

// input.go, 23-02: pure translation of RFB input to ConsoleConn calls.

// Keyboard turns RFB KeyEvents (keysyms) into scan code set 1 bytes on a
// US layout, correcting Shift where the client's Shift state differs from
// what the character needs on a US keyboard; it tracks held modifiers.
type Keyboard struct{ /* held keys */ }
func (k *Keyboard) Keysym(keysym uint32, down bool) []byte // nil: keysym has no key
// QEMUKey turns a QEMU Extended Key Event keycode (XT, 0xE0 prefix as the
// high bit 0x80 of the high byte, as noVNC's getRFBkeycode) into set 1 bytes.
func QEMUKey(keycode uint32, down bool) []byte

type PointerOp struct {
	Kind   PointerKind // PointerMove, PointerButton, PointerScroll
	X, Y   int         // PointerMove, clamped to 0..w-1, 0..h-1
	Button int         // PointerButton: 1 left, 2 right, 3 middle
	Down   bool        // PointerButton
	Delta  int         // PointerScroll: +120 up (bit 3), -120 down (bit 4)
}
// Pointer returns the operations for an RFB PointerEvent: a move when the
// position changed, button transitions of bits 0/1/2 (to 1/3/2), one
// scroll per press of bit 3 or 4.
func Pointer(prevMask, mask uint8, x, y, w, h int) []PointerOp

// rfb.go, 23-03.

type SessionOptions struct {
	Name     string             // desktop name in ServerInit, the server's name
	Interval time.Duration      // poll interval; 0 = 200 ms (5 frames/s)
	Clock    scheduler.Clock    // nil = real clock
}
// Serve speaks RFB 3.8 on conn: VNC Authentication with password (only the
// first 8 characters count, as at Hetzner), Raw encoding in the client's
// 32-bpp pixel format, DesktopSize, QEMU Extended Key Event; returns when
// the client leaves, ctx ends or src fails.
func Serve(ctx context.Context, conn net.Conn, src platform.ConsoleConn, password string, o SessionOptions) error

// service.go, 23-04.

type Config struct {
	Host     string                // "console.hetzner.internal"
	Cert     tls.Certificate       // issued by the hlab CA at cloud start
	Open     func(ctx context.Context, vm string) (platform.ConsoleConn, error)
	Clock    scheduler.Clock       // nil = real clock; token TTL one minute
	Session  SessionOptions
}
func New(cfg Config) *Service
// Issue returns wss://<Host>/?server_id=<id>&token=<uuid> and a password
// of 32 random letters and digits; the token is valid for one connection
// started within one minute.
func (s *Service) Issue(serverID int64, vm string) (wssURL, password string, err error)
func (s *Service) Handler() http.Handler      // WebSocket upgrade; 403 for a bad token
func (s *Service) Serve(ln net.Listener) error // TLS with Cert around Handler
func (s *Service) CloseServer(serverID int64)  // ends the server's open consoles
func (s *Service) Close() error                // ends listener and all consoles
```

```go
// package servers, 23-07: new dependency in Deps.
type Console interface {
	Issue(serverID int64, vm string) (wssURL, password string, err error)
	CloseServer(serverID int64)
}
```

```go
// package bridge (internal/bridge/console.go), 23-08.
type ConsoleDeps struct {
	CA func(ctx context.Context) ([]byte, error) // hlab CA PEM, GET /mgmt/v1/ca.pem
	// Dial overrides the TCP dial in tests; nil = net.Dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}
func NewConsole(d ConsoleDeps) *Console
// Open starts a one-connection loopback proxy and returns
// ws://127.0.0.1:<port>/<random>; it accepts only origin wails.localhost
// and dials wssURL trusting only the hlab CA, ServerName from the URL.
func (c *Console) Open(wssURL string) (string, error)

// cmd/hlab-tray (windows.go), 23-08: Wails service.
type Windows struct{ /* window map under a mutex */ }
// OpenConsole creates the window "Console: <name>" at
// /#/server-console/<project>/<id>, or shows, restores and focuses the
// open one of that server.
func (w *Windows) OpenConsole(project string, id int64, name string) error
```

```ts
// ui/frontend/src/lib/routes.ts, 23-09: one more Route.
| { area: "server-console"; project: string; server: number } // #/server-console/<project>/<id>
```

## Plans

- [x] 23-01 Console forward and DNS name on the appliance, host firewall port
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CON-01 | Tests: test/e2e/console_test.go (runs at 23-07)
  - Reads: ARCHITECTURE.md "hlab-agent (appliance)" (Config with `Console ConsoleForward`), "Network"; EXPERIMENTS.md E23; CONTEXT.md technical choices
  - Size: ~180 lines in 7 files
  - [x] `agentapi.ConsoleForward{Listen, Backend netip.AddrPort}` and `Config.Console` (additive, schema 1); `transit.Addrs.ConsoleListen` (.2:8781) and `ConsoleProxy` (.1:443) in `internal/agentapi/agentapi.go`, `internal/transit/transit.go`
  - [x] Agent: a plain TCP forward Applier for `Config.Console` (listen, accept, copy both ways, restart on change, nothing while Listen is invalid) in `internal/agent/forward.go` and its registration in `internal/agent/agent.go`; A record `console.hetzner.internal` → `Console.Listen` address in `internal/agent/dns.go`
  - [x] Host: the inbound firewall rule allows TCP 8780 and 8781 in `internal/hostnet/windows/scripts.go` (and `hostnet.go` constant)

- [x] 23-02 Console input translation and platform console interface
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CON-01 | Tests: internal/console/input_test.go
  - Reads: PLAN.md "Interfaces"; EXPERIMENTS.md E25; RESEARCH.md "noVNC and RFB details" (sendCtrlAltDel, QEMU Extended Key Event); CONTEXT.md "Answers" Q1
  - Size: ~380 lines in 3 files
  - [x] `platform.Console`, `platform.ConsoleConn`, `platform.Frame` in `internal/platform/platform.go`
  - [x] `Keyboard.Keysym` (US keysym → set 1 table: letters, digits, symbols, Return, Tab, BackSpace, Escape, Delete, Home/End/PgUp/PgDn/Insert and arrows with 0xE0, F1-F12, Shift/Control/Alt/Super left and right, keypad; Shift correction around a character whose US Shift state differs from the held one), `QEMUKey`, `Pointer` in `internal/console/input.go`

- [x] 23-03 RFB 3.8 session
  - Wave: 2 | Depends on: 23-02 | Review: alone
  - Requirements: CON-01, CON-02 | Tests: internal/console/rfb_test.go
  - Reads: PLAN.md "Interfaces"; SPEC.md "Server console"; RESEARCH.md "noVNC and RFB details"; research/server-console.md section 3; EXPERIMENTS.md E22 (VNC auth bit reversal), E24 (pixel format), E25 (frames)
  - Size: ~400 lines in 2 files
  - [x] `Serve` in `internal/console/rfb.go`: version, security type 2 with DES challenge (key bytes bit-reversed, password cut to 8 characters, comment why), SecurityResult 1 with reason "authentication failed", ClientInit/ServerInit, SetPixelFormat (32-bpp true colour, any shifts, endianness), SetEncodings (remember DesktopSize and QEMU Extended Key Event; answer the latter with its pseudo-rectangle), FramebufferUpdateRequest (full at once; incremental held until a change), KeyEvent, QEMU message 255/0, PointerEvent, ClientCutText ignored
  - [x] Frame loop in `internal/console/frames.go`: grab on the interval only while connected and right after each input event, keep the last frame (black at start) when `ok` is false, RGB565 → client format, changed rectangles by diff, DesktopSize as last rectangle on a size change

- [x] 23-04 Console service: tokens and WebSocket listener
  - Wave: 3 | Depends on: 23-03 | Review: alone
  - Requirements: CON-01, CON-02 | Tests: internal/console/service_test.go
  - Reads: PLAN.md "Interfaces"; SPEC.md "Server console"; ARCHITECTURE.md "Server console"; research/server-console.md section 3 (coder/websocket); EXPERIMENTS.md E22
  - Size: ~250 lines in 3 files
  - [x] `New`, `Issue` (UUID v4 token from crypto/rand, 32-character alphanumeric password, TTL one minute from Clock, kept in memory), `Handler` (query `server_id` and `token`; unknown, used, expired or foreign → 403 without body detail, before the upgrade; token consumed at the upgrade; `websocket.Accept` without origin check for outside clients, no subprotocol required; `websocket.NetConn` binary; `Open` then `Serve`), `Serve(ln)` with TLS, `CloseServer`, `Close` in `internal/console/service.go`
  - [x] `github.com/coder/websocket` v1.8.15 as a direct dependency in `go.mod`/`go.sum`

- [x] 23-05 Raw WMI access for Hyper-V
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CON-01 | Tests: test/e2e/console_test.go (runs at 23-07)
  - Reads: EXPERIMENTS.md E21 (pitfalls: raw vtables, indexes, CoSetProxyBlanket, SafeArrayAccessData), E25
  - Size: ~300 lines in 2 files
  - [x] `internal/platform/hyperv/wmi_windows.go`: COM init on a locked OS thread, `IWbemLocator.ConnectServer` to `root\virtualization\v2`, `CoSetProxyBlanket`, `IWbemServices` `ExecQuery`/`GetObject`/`ExecMethod`, `IWbemClassObject` `Get`/`Put`/`SpawnInstance`/`GetMethod`, VARIANT helpers for BSTR, I4, BOOL, VT_ARRAY|VT_UI1 in and out (one `SafeArrayAccessData` copy), Release and VariantClear on every path, through `golang.org/x/sys/windows` only

- [x] 23-06 Hyper-V console connection
  - Wave: 2 | Depends on: 23-02, 23-05 | Review: alone
  - Requirements: CON-01 | Tests: test/e2e/console_test.go (runs at 23-07)
  - Reads: PLAN.md "Interfaces"; EXPERIMENTS.md E21, E25; ARCHITECTURE.md "platform (interface) and platform/hyperv"
  - Size: ~280 lines in 2 files
  - [x] `Platform.OpenConsole` in `internal/platform/hyperv/screen_windows.go` (plus a `_other.go` stub if the package builds elsewhere): lookups by VM name (`Msvm_ComputerSystem`, realized VSSD, `Msvm_VideoHead`, `Msvm_Keyboard`, `Msvm_SyntheticMouse`), one worker goroutine per conn owning the COM connection; `Screen` reads VideoHead then `GetVirtualSystemThumbnailImage` at that size (ReturnValue 32775/32779 → ok false, trailing bytes dropped), `Scancodes` via `TypeScancodes` (VT_ARRAY|VT_UI1), `MouseMove` via `SetAbsolutePosition`, `MouseButton` via `SetButtonState`, `MouseScroll` via `SetScrollPosition`; a missing VM is `platform.Error{Kind: NotFound}`

- [ ] 23-07 request_console and the console in the cloud
  - Wave: 4 | Depends on: 23-01, 23-04, 23-06 | Review: alone
  - Requirements: SRV-17, CON-01 | Tests: internal/resources/servers/servers_test.go, test/e2e/console_test.go
  - Reads: PLAN.md "Interfaces"; SPEC.md "Server console"; frozen OpenAPI (`%LocalAppData%\hlab\openapi\cloud.spec.json`, operationId `request_server_console`); ARCHITECTURE.md "Server console", "ca"; CONTEXT.md
  - Size: ~260 lines in 7 files
  - Adjusts: `internal/apitest` `ServersDeps` (gives a console), `internal/resources/servers/servers_test.go` and `internal/resources/conventions_test.go` environments if they build `servers.Deps` without a console; the old SRV-17 subtest is replaced by the test writer
  - [ ] `servers.Console` in `Deps`; `requestConsole` in `internal/resources/servers/actions.go`: load the server, start and finish a `request_console` action in one transaction, `Issue`, answer 201 `{action, wss_url, password}`; the delete path calls `CloseServer`; `consoleMessage` removed
  - [ ] Cloud: at start issue the `console.hetzner.internal` certificate from the hlab CA (in memory), build `console.New` with `Open` through the `platform.Console` assertion and the server's VM name, listen on `addrs.ConsoleListen`, close at stop; `Config.Console` in the agent document; in `internal/cloud/cloud.go`, a new `internal/cloud/console.go`
  - [ ] `api.Deviations`: the `request_console` entry describes `console.hetzner.internal`, the hlab CA certificate and the Hyper-V screen instead of `unsupported_error`, in `internal/api/deviations.go`

- [ ] 23-08 Tray: console proxy and console windows
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-15 | Tests: internal/bridge/console_test.go
  - Reads: PLAN.md "Interfaces"; ARCHITECTURE.md "hlab-tray (tray program)"; EXPERIMENTS.md E22, E24
  - Size: ~300 lines in 5 files
  - [ ] `bridge.Console` in `internal/bridge/console.go`: `Open` gets the CA through `CA`, listens on `127.0.0.1:0` for one connection on a random path, `websocket.Accept` with `OriginPatterns: wails.localhost`, dials the `wss_url` with `tls.Config{RootCAs: hlab CA only, ServerName: URL host}`, copies binary messages both ways, closes both sides when one ends and the listener after a minute unused
  - [ ] `Windows` service in `cmd/hlab-tray/windows.go`: window map under a mutex cleared in each window's `WindowClosing` listener, create "Console: <name>" at `/#/server-console/<project>/<id>` (about 1100x800) or show, restore and focus; register `bridge.Console` (with `client` for `/ca.pem`) and `Windows` in `cmd/hlab-tray/options.go` and `main.go`; quitting closes the console windows

- [ ] 23-09 Tray: console window and the "Console" menu entry
  - Wave: 2 | Depends on: 23-08 | Review: alone
  - Requirements: UI-15 | Tests: ui/frontend/tests/e2e/serverconsole.spec.ts
  - Reads: UI-SPEC.md "Building blocks" (Server console), "Behaviour", "Language of the interface"; PLAN.md "Interfaces"; EXPERIMENTS.md E22, E24; RESEARCH.md "noVNC and RFB details" (securityfailure, sendCtrlAltDel)
  - Size: ~350 lines in 8 files
  - Adjusts: `ui/frontend/tests/unit/serverActions.test.ts` (the console entry is no longer disabled; the case expecting `disabledHint` uses the new entry instead), `ui/frontend/tests/unit/routes.test.ts` only if it enumerates every route kind
  - [ ] `@novnc/novnc` 1.7.0 in `ui/frontend/package.json` and `package-lock.json`; `ui/frontend/src/novnc.d.ts` (RFB class: constructor, `scaleViewport`, `sendCtrlAltDel`, `disconnect`, events)
  - [ ] Route `server-console` in `ui/frontend/src/lib/routes.ts`; `App.svelte` renders `ui/frontend/src/views/ServerConsole.svelte` for it without the shell: top bar with the connection state ("Connecting", "Connected", "Disconnected" with the reason) and "Send Ctrl+Alt+Del", the screen filling the window (`scaleViewport`), "Reconnect" after a drop; on load and on Reconnect it runs `request_console` through the project proxy, calls `Console.Open(wss_url)` and starts noVNC (dynamic `import()`) with the password
  - [ ] The "Console" entry in `ui/frontend/src/lib/serverActions.ts` is enabled and opens the window through `Windows.OpenConsole(project, id, name)` in `ui/frontend/src/views/servers/ServerActions.svelte` (and the row menu where the server actions appear)

- [ ] 23-10 Documentation
  - Wave: 5 | Depends on: 23-07, 23-09 | Review: alone
  - Requirements: DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC.md "Documentation", "Server console"; REQUIREMENTS.md "Documentation (`DOC`)"; CONTEXT.md; EXPERIMENTS.md E21, E25 (CPU of `vmms.exe`)
  - Size: ~150 lines in 7 files
  - [ ] `docs/deviations.md` (servers: the console works; the certificate from the hlab CA, an own browser or VNC client needs "Trust CA"; about 0.7 cores in `vmms.exe` per open console; no serial console), `docs/development/api-coverage.md` (request_console no longer `unsupported_error`; URL host, token, certificate), `docs/development/build-and-test.md` (CON-01 and UI-15 rows, `hcloud server request-console` no longer listed as uncovered)
  - [ ] `README.md` and `docs/guide.md` (Console in the server's actions menu, `hcloud server request-console` with a VNC client and "Trust CA"), `ui/frontend/src/help/servers.md` (Console opens the console window, Send Ctrl+Alt+Del, Reconnect); screenshots only if the guide shows the console window
