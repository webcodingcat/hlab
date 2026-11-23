# Research: server console (research date 2026-11-23)

Legend: [C] confirmed from the cited source, [M] from memory or general knowledge, not re-verified, [U] unclear / not found.

## 1. Hetzner `request_console`

Source: frozen OpenAPI https://docs.hetzner.cloud/cloud.spec.json (operationId `request_server_console`, tag Server Actions); hcloud-go schema https://github.com/hetznercloud/hcloud-go/blob/main/hcloud/schema/server.go ; hcloud CLI https://github.com/hetznercloud/cli/blob/main/internal/cmd/server/request_console.go ; docs https://docs.hetzner.com/cloud/servers/getting-started/vnc-console

- [C] `POST /servers/{id}/actions/request_console`, no request body, bearer token. Description: "Requests credentials for remote access via VNC over websocket to keyboard, monitor, and mouse for a Server. The provided URL is valid for 1 minute, after this period a new url needs to be created to connect to the Server." The spec also says the open connection is not subject to that timeout.
- [C] Success response (spec says 201): `{"action": <Action>, "wss_url": string, "password": string}`, all three required. `wss_url`: "websocket proxy URL. It includes a token that is valid for a limited time only." `password`: "VNC password. It only works with a `wss_url` that has a valid token."
- [C] Action object: `id` int64, `command` string, `status` running|success|error, `started`, `finished` (nullable), `progress` 0-100, `resources` [{id,type}], `error` {code,message} or null. The real command name is `request_console`; not stated in the spec excerpt [U], confirm against a real response if needed.
- [C] Errors: generic envelope `{"error":{"code","message","details"}}`; 4XX example `unauthorized`, 5XX example `timeout`. The spec lists no console-specific error codes [U]. Generic codes from the spec header include 401 `unauthorized`, 423 `locked`, 429 `rate_limit_exceeded`; a missing server is the usual 404 `not_found` [M].
- [C] `hcloud server request-console <id|name>` fetches the server, calls RequestConsole, waits for the action, then prints exactly:
  ```
  Console for Server <id>:
  WebSocket URL: <wss_url>
  VNC Password: <password>
  ```
  With `-o json|yaml` it prints an object with only `wss_url` and `password`.
- [U] The exact real URL shape (host, path, token form) is not documented. The planned `wss://console.hetzner.internal/?server_id=<id>&token=<token>` is the project's own choice; clients only treat it as opaque.
- [C] The Hetzner docs page for the console says nothing on session length or password type. The VNC password type (RFB "VNC Authentication") is implied by "VNC password"; noVNC passes it as `credentials.password` [M].
- Implication: token single-use is not documented. A 1 minute TTL on the token for the handshake, with the open session unaffected afterwards, matches the spec.

## 2. Hyper-V WMI (`root\virtualization\v2`)

Sources: Microsoft Learn Win32 Hyper-V v2 reference pages (linked per item); all classes list "Windows 8 / Server 2012" as minimum.

- Framebuffer: `Msvm_VirtualSystemManagementService.GetVirtualSystemThumbnailImage(TargetSystem=<Msvm_VirtualSystemSettingData ref>, WidthPixels uint16, HeightPixels uint16, out ImageData uint8[])`. [C] Output "raw RGB 565" (2 bytes/pixel, little endian [M]). May return 0 or job 4096 (poll the job). The parameters are uint16, so max 65535 each, no documented lower cap. https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/getvirtualsystemthumbnailimage-msvm-virtualsystemmanagementservice
- [C] Full resolution works: pass the current `Msvm_VideoHead.CurrentHorizontalResolution/CurrentVerticalResolution`; smaller values are scaled down by Hyper-V (Ben Armstrong, archived MS blog, https://learn.microsoft.com/en-us/archive/blogs/virtual_pc_guy/capturing-a-hyper-v-vm-screen-to-a-file). This is the same call Hyper-V Manager uses for thumbnails.
- `Msvm_VideoHead` [C]: `CurrentHorizontalResolution`, `CurrentVerticalResolution`, `CurrentBitsPerPixel` (read-only). Reach it via VM -> `Msvm_SyntheticDisplayController` (assoc `Msvm_SystemDevice`) -> `Msvm_VideoHead` (assoc `Msvm_VideoHeadOnController`). https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/msvm-videohead  (the ClickButton sample shows the traversal).
- No faster documented path [U]. Nothing in the docs gives frame rate or latency; I found no published figures for polling this API [U]. Must be measured on the target host (the project's earlier decision already notes this). The data is a full frame per call (e.g. 1024x768x2 = 1.5 MB), so diff on the Go side to emit only changed rectangles (Raw/Hextile/ZRLE) [M].
- Keyboard `Msvm_Keyboard` [C]: one instance per VM, associated by `Msvm_SystemDevice`. Methods: `TypeKey(keyCode)`, `PressKey(keyCode uint32)`, `ReleaseKey(keyCode)`, `TypeScancodes(Scancodes uint8[])`, `TypeText(string)`, `TypeCtrlAltDel()`, `IsKeyPressed`, `Reset`. `PressKey` takes Windows virtual-key codes; VK_MENU/CONTROL/SHIFT are mapped to the left variants (164/162/160). `TypeScancodes` takes an array of scan codes, details of the encoding (set 1 vs 2, extended 0xE0 prefix, break codes) are not documented [U]. `Layout` property is "00000409" (US) [C]. `UnicodeSupported` property exists. https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/msvm-keyboard
  - Mapping X11 keysyms (RFB KeyEvent) to VK codes needs a table written in hlabd; layout is US only.
  - Each call is a WMI method call (a round trip); expect latency per key. Not measured [U].
- Mouse `Msvm_SyntheticMouse` [C]: `SetAbsolutePosition(horizontalPosition sint32, verticalPosition sint32)` ("in pixels"; the sample uses `CurrentVerticalResolution - 1`), `ClickButton(buttonIndex uint32)` (1-based), `SetButtonState`, `GetButtonState`, `SetScrollPosition`. `AbsoluteCoordinates = True`, `NumberOfButtons = 5`. https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/msvm-syntheticmouse
  - [U] Whether the pixel value is really scaled to the guest's screen or to a 0..65535 range is not documented; test on a real guest.
  - `ClickButton` is press+release; for drag and hold use `SetButtonState`; its value semantics are not verified [U]. RFB pointer events give button masks, so map to SetButtonState transitions.
  - Class docs only mention a synthetic mouse; there is no relative-motion method (absolute only) [C].
- Pitfalls [M unless noted]:
  - Gen2 VMs have no legacy VGA; UEFI firmware and OS drivers use synthetic video (typically 1024x768 until the guest driver sets a mode). Thumbnail returns black or stale image when the VM is off or saved; handle error codes 32775 invalid state.
  - Resolution can change at any time (guest driver, VM connect); read VideoHead before every frame and send the RFB DesktopSize/ExtendedDesktopSize pseudo-encoding when it changes.
  - WMI access needs admin/LocalSystem (hlabd runs as LocalSystem, fine) and the WMI provider is slow per call; use one long-lived COM/WMI connection per VM, and serialize input calls.
  - Enhanced session mode / a running VMConnect session does not block the thumbnail call [U].
  - Serial fallback: Hyper-V COM port named pipe (`\\.\pipe\<name>`), configured via `Msvm_SerialPortSettingData` / `Set-VMComPort`; guest needs `console=ttyS0`. Not researched in detail [U].

## 3. RFB protocol and Go libraries

Spec: https://github.com/rfbproto/rfbproto/blob/master/rfbproto.rst (read up to message definitions; later sections not read).

- [C] Handshake: server sends `RFB 003.008\n` (12 bytes); client answers with its version (noVNC speaks 3.8 and accepts 3.3/3.7). For 3.7+/3.8: server sends security-type count + list; client picks 1 byte. Type 1 = None, type 2 = VNC Authentication: server sends 16 random bytes; client returns the 16 bytes encrypted with DES-ECB using the password truncated or zero-padded to 8 bytes, with the bits of every key byte reversed. SecurityResult U32: 0 OK, 1 failed (3.8: followed by reason string). ClientInit: 1 byte shared flag. ServerInit: U16 width, U16 height, 16-byte PIXEL_FORMAT, U32 name length + name.
- [C] Client->server messages every server must handle: 0 SetPixelFormat, 2 SetEncodings, 3 FramebufferUpdateRequest, 4 KeyEvent (down-flag, 2 pad, U32 keysym), 5 PointerEvent (button mask, x U16, y U16), 6 ClientCutText. Raw (0) may always be sent. DesktopSize pseudo-encoding is -223 (ExtendedDesktopSize -308 [M]).
- [M] Server->client FramebufferUpdate (type 0): 1 pad, U16 rect count, per rect x,y,w,h (U16 each) + S32 encoding + data. Raw data uses the pixel format negotiated (server may suggest 32 bpp, depth 24, little endian, true colour, shifts 16/8/0; client may call SetPixelFormat). Minimal server: Raw only, DesktopSize optional, ignore unknown encodings and SetPixelFormat variants beyond 32bpp true-colour (noVNC always sends 32bpp). Convert RGB565 from Hyper-V to 32 bpp in Go.
- [M] noVNC decodes Raw, CopyRect, RRE, Hextile, Tight/TightPNG, ZRLE, H.264 (new in 1.6+), pseudo-encodings Cursor, DesktopSize, ExtendedDesktopSize, DesktopName, QEMU extended key event, clipboard. A server that offers only Raw is accepted. It supports security types None, VNC (2), Tight, VeNCrypt, XVP, ARD, plain; with type 2 it uses `credentials.password`, otherwise it fires `credentialsrequired`. These come from memory; the noVNC API page confirmed only the options and events in section 4.
- WebSocket framing for noVNC: RFB bytes in binary WebSocket messages; noVNC offers subprotocol `binary` only if `wsProtocols` is set (default empty, so none) [C: API.md says default empty]. Server must not insist on a subprotocol.
- DES: Go stdlib `crypto/des` exists (deprecated-style but present); reverse the bits of each key byte yourself.
- Go libraries:
  - `github.com/coder/websocket` latest v1.8.15 (Jun 15 2026, pkg.go.dev; GitHub releases list v1.8.15, 14, 13, 12); no v2 yet; project already has v1.8.14 as indirect dependency (go.mod). Active (formerly nhooyr). `websocket.Accept(w, r, &AcceptOptions{OriginPatterns, Subprotocols})`, `websocket.NetConn(ctx, c, websocket.MessageBinary)` wraps the conn as `net.Conn` so an RFB server loop can run over it directly. Default Accept rejects cross-origin requests: add the tray origin to `OriginPatterns` (do not use `*`). https://pkg.go.dev/github.com/coder/websocket
  - `github.com/gorilla/websocket` v1.5.3 (Jun 14 2024); older versions are flagged by Go vuln DB GO-2026-6278 (weak mask-key PRNG), v1.5.3 not flagged. Has `Conn.NetConn()`, no stdlib-style `net.Conn` adapter. Prefer coder/websocket (already in go.mod). https://pkg.go.dev/github.com/gorilla/websocket?tab=versions
  - No maintained Go RFB server library was evaluated [U]. The protocol subset above is small; writing it in-house (about 300 lines) is the realistic route. I did not verify specific candidates (e.g. amitbet/vnc2video, mitchellh/go-vnc is a client only) [M].

## 4. noVNC

Sources: https://github.com/novnc/noVNC/releases , https://raw.githubusercontent.com/novnc/noVNC/master/docs/API.md , https://unpkg.com/@novnc/novnc@1.7.0/package.json , https://registry.npmjs.org/@novnc/novnc

- [C] Latest release and npm `latest` tag: 1.7.0 (GitHub release dated 28 Apr; the year is not shown on the page, assumed 2026). Release notes: "The NPM bundle has been converted to ES-module format." License MPL-2.0 [M]. The master branch already contains 1.8.0 work (issue #2060 is "fix version v1.8.0", closed Sep 1 2026), 1.8.0 not yet on npm as of today [C by npm latest tag; GitHub release list not checked past the latest page].
- [C] 1.7.0 `package.json`: `"type": "module"`, `"exports": "./core/rfb.js"`, no `main`/`module`. So `import RFB from '@novnc/novnc'` works with Vite 8 (default export is `RFB`); the old deep import `@novnc/novnc/lib/rfb` is not available through `exports`.
- [C] Top-level await: `core/util/browser.js` in 1.7.0 still has `supportsWebCodecsH264Decode = await _checkWebCodecsH264DecodeSupport();` at module level. Upstream replaced it in PR #2053 (merged May 23 2026, after 1.7.0, so in unreleased 1.8.0); issue #1943 "cannot be bundled due to top-level await" is closed. WebView2 (Chromium) supports top-level await at runtime. Build risk is only the bundler target: Vite fails with "Top-level await is not available in the configured target environment" if `build.target` is below Chrome 89. Vite 8 default target `baseline-widely-available` should be above that, but I did not verify this against the project's vite 8.3.8 config [U]. Safe settings: `build.target: 'esnext'` (or `'chrome120'`) in `vite.config`; the `vite-plugin-top-level-await` plugin is not needed for WebView2. Verify with a real `vite build` early.
- Also check: svelte-check strictness (`--fail-on-warnings`) - noVNC ships no TypeScript types in the package [M]; add a `declare module '@novnc/novnc'` shim or `@types/novnc__novnc` [M, unchecked].
- [C] API (docs/API.md): `new RFB(target: HTMLElement, urlOrChannel: string | WebSocket | RTCDataChannel, options)`; options `shared` (default true), `credentials {username, password, target}`, `repeaterID`, `wsProtocols` (default `[]`). Events: `connect`, `disconnect` (`detail.clean`), `credentialsrequired` (`detail.types`), `desktopname`. Properties: `scaleViewport` (default false), `resizeSession` (default false), `clipViewport` (default false). Methods such as `sendCtrlAltDel()`, `disconnect()`, `sendCredentials()` exist [M].
- Example:
  ```ts
  import RFB from '@novnc/novnc';
  const rfb = new RFB(el, wssUrl, { credentials: { password } });
  rfb.scaleViewport = true;
  rfb.addEventListener('disconnect', e => { /* e.detail.clean */ });
  ```
- Pitfall: the `target` element needs explicit size; `resizeSession` only works if the server supports ExtendedDesktopSize SetDesktopSize [M] (the hlab bridge will not, so keep it false).

## 5. WebView2 / Wails v3 and the hlab CA

Installed: `github.com/wailsapp/wails/v3 v3.1.0`, `@wailsio/runtime 3.1.0`, vite 8.3.8, svelte 5.58.0 (project manifests). Current Wails v3 stable not looked up [U].

- [C] WebView2 has `ICoreWebView2_14.ServerCertificateErrorDetected` (host app can `ALWAYS_ALLOW` after checking the certificate, e.g. thumbprint). It "will raise for all web resources", follows `WebResourceRequested`, and decisions are cached per host+cert until `ClearServerCertificateErrorActions`. https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2_14 It is documented for web resource loads (navigation and requests); whether it also covers a JavaScript `new WebSocket('wss://...')` handshake is not documented [U]; treat as untested.
- [C] Wails v3.1.0 contains internal COM bindings for it (`internal/webview2/pkg/webview2/ICoreWebView2_14.go`), but nothing in `pkg/application` wires the event, and the package is `internal`, so it is not usable from hlab-tray without forking or patching Wails [C by grep].
- [C] Wails v3.1.0 does expose `WindowsWindow.AdditionalBrowserArgs` (used in `webview_window_windows.go`), so `--ignore-certificate-errors` (flag exists, see https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/webview-features-flags) is possible but it disables validation for all the tray's traffic, and Microsoft says production apps should not use browser flags. `--ignore-certificate-errors-spki-list` is not listed in the WebView2 flag page [U]. Not recommended.
- Recommended design (technical, safe to skip): keep the TLS check in Go. hlab-tray (or hlabd's existing local API) opens a loopback listener (`127.0.0.1:<random>`, plain `ws://`) with a one-time path token; the Svelte page connects noVNC to `ws://127.0.0.1:<port>/<token>`; the Go side dials `wss://console.hetzner.internal/?...` with `tls.Config{RootCAs: hlab CA pool, ServerName: ...}` and pipes frames both ways (coder/websocket on both sides, `NetConn` or message copy). This works whether or not Windows trusts the CA, and needs no WebView2 certificate feature. Origins: set `OriginPatterns` for the Wails origin (`http://wails.localhost` on Windows [M]) on the loopback listener. Check the app's CSP and `connect-src` if any [U].
- Not verified: whether a Wails v3 asset-server handler can accept WebSocket upgrades (would avoid a second port) [U]; sending binary frames over Wails events/bindings works but base64 JSON overhead is large [M]. The loopback listener is the simplest choice.
- Browsers outside the tray (hcloud CLI users pasting the URL into noVNC/Hetzner tooling) still need the hlab CA trusted; that is the "CA trust" setting, not the tray.

## Summary and risks

1. API shape is clear and small: 201 with `action`, `wss_url`, `password`; URL token valid 1 minute for connecting; CLI prints three lines. Exact URL form is free for hlab to choose.
2. Biggest risk stays performance and input fidelity of WMI: framebuffer only via `GetVirtualSystemThumbnailImage` (full-frame RGB565 per call, no documented frame rate), key input via per-call WMI (VK codes, US layout), mouse coordinate semantics undocumented. Needs a measurement spike on the real host before committing; serial console fallback remains the safer plan for headless/boot logs.
3. RFB server: implement in-house, Raw encoding + VNC auth (DES with bit-reversed key) + DesktopSize; no mature Go RFB server library was found. WebSocket: coder/websocket v1.8.15 (already indirect in go.mod, v1.8.14).
4. noVNC 1.7.0 (npm latest): ESM only via `exports`, one top-level await in `browser.js`; set Vite `build.target` high enough and run an early `vite build` + `svelte-check` (no bundled types).
5. Tray with CA trust off: do not rely on WebView2 certificate hooks (not exposed in Wails v3.1.0, unclear for WebSockets); use a Go-side loopback ws -> wss proxy with the hlab CA in its own `tls.Config`.
