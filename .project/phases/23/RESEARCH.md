## noVNC and RFB details

Sources: noVNC v1.7.0 `core/rfb.js` (https://raw.githubusercontent.com/novnc/noVNC/v1.7.0/core/rfb.js), RFC 6143 (https://www.rfc-editor.org/rfc/rfc6143.txt). Installed and pinned version is 1.7.0 (SPEC.md). Last-release date not checked.

### SetEncodings announced by noVNC 1.7.0 (`_sendEncodings`)
- Order: CopyRect; only if color depth is 24: H264 (if WebCodecs H264 decode is supported), Tight, TightPNG, ZRLE, JPEG, Hextile, RRE, Zlib; then Raw, quality level pseudo, compress level pseudo.
- Then these pseudo-encodings, always: DesktopSize, LastRect, QEMUExtendedKeyEvent, QEMULedEvent, ExtendedDesktopSize, Xvp, Fence, ContinuousUpdates, DesktopName, ExtendedClipboard, ExtendedMouseButtons. Only at depth 24: VMwareCursor, Cursor.
- So a server that wants only Raw can rely on Raw being always announced. The numeric values (-223, -308, -258) come from the task and are defined in `core/encodings.js`. I did not re-read that file.

### DesktopSize rectangle (noVNC reaction)
- `case pseudoEncodingDesktopSize: this._resize(w, h); return true;` with the rect's width and height. x and y are ignored.
- `_resize` sets `_fbWidth` and `_fbHeight`, calls `display.resize`, then `_updateClip`, `_updateScale`, `_updateContinuousUpdates` and `_saveExpectedClientSize`. It dispatches no event (no `desktopresize` event in this code path).
- ExtendedDesktopSize is handled the same way via `_handleExtendedDesktopSize`. For x=1,y!=0 (a failed client-requested resize) it only logs a warning. Otherwise it calls `_resize` with the rect's size. With x=1,y=0 it also calls `_requestRemoteResize()` afterwards.
- noVNC sends SetDesktopSize only if `resizeSession` is on, the view is not view-only, and the server supports it, which it learns from an ExtendedDesktopSize rect. A server that never sends ExtendedDesktopSize never gets SetDesktopSize.

### Failed SecurityResult (`securityfailure` event)
- Protocol 3.8 or later: on status != 0 it enters state "SecurityReason" and reads a U32 length, then that many bytes as the reason string. It dispatches `securityfailure` with `detail {status, reason}`, or `{status}` if the length is 0, then calls `_fail(...)`, so a `disconnect` event with `clean=false` follows.
- Protocol before 3.8: dispatches `securityfailure` with `{status}` only, then `_fail("Security handshake failed")`.
- Server wire format for a failure: U32 status = 1, U32 reason-length, reason bytes (RFC 6143 section 7.1.3). I confirmed the noVNC side, not the RFC text.

### sendCtrlAltDel()
- Returns early if not connected or view-only. Otherwise it sends key down Control_L (XK 0xFFE3, code "ControlLeft"), Alt_L (0xFFE9, "AltLeft"), Delete (0xFFFF, "Delete"), then key up Delete, Alt_L, Control_L.
- The hex values are from standard X11 keysyms. The code gives only the names `XK_Control_L`, `XK_Alt_L`, `XK_Delete`.
- With QEMU Extended Key Event active (see below) these go out as extended key events with scancodes. Otherwise they go out as plain KeyEvent messages with these keysyms.

### QEMU Extended Key Event (lower priority)
- Enabling: noVNC announces pseudo-encoding -258 in SetEncodings. If the server answers with a rect of that encoding in a FramebufferUpdate, noVNC sets `_qemuExtKeyEventSupported = true`. There is no payload to handle.
- `sendKey(keysym, code, down)`: if supported and the DOM `KeyboardEvent.code` maps in `XtScancode` (`core/input/xtscancodes.js`), it sends `QEMUExtendedKeyEvent(sock, keysym, down, scancode)`. Otherwise it falls back to plain KeyEvent (message type 4), and does nothing if keysym is 0.
- Wire format: U8 255 (msg-type), U8 0 (sub-type), U16 down-flag, U32 keysym, U32 keycode. The first two bytes are quoted from the code. The remaining field order and widths are from my memory of the QEMU spec and I did not confirm them. The keycode is the XT scancode converted by `getRFBkeycode`, which for the 0xE0 prefix sets the high bit (`lowerByte | 0x80`).
- Not needed for a server that does not announce it. Plain KeyEvent keysyms work without it.

### RFC 6143 framing
- FramebufferUpdate (S->C): U8 type 0, 1 byte padding, U16 number-of-rectangles. Each rectangle: U16 x, U16 y, U16 width, U16 height, S32 encoding-type, followed by the encoding data (7.6.1).
- DesktopSize (7.8.2): a rectangle with encoding -223 and no data. Width and height are the new framebuffer size, x and y are ignored. The RFC says it is sent as the last rectangle of an update. After it, the client no longer has the old framebuffer contents.
- FramebufferUpdateRequest (C->S, type 3): U8 incremental, U16 x, U16 y, U16 w, U16 h (7.5.3).
- Incremental request with no change: the server must not send an empty update. It holds the request and replies only when the area changes ("there may be an indefinite period between the request and the update"). Section 3 says an update must only be sent in response to a request. A non-incremental request needs an update with the full area at once.
- noVNC behaviour: it sends a non-incremental request after ServerInit. After each finished update it sends an incremental request for the whole screen (if ContinuousUpdates is not enabled). So a server needs to track a pending request and answer it on change.

### Status
- noVNC encodings, DesktopSize handling, securityfailure and Ctrl-Alt-Del: confirmed from the 1.7.0 source.
- RFC layouts and incremental semantics: confirmed.
- Unclear: the exact QEMU extended key event field widths after the two header bytes, and the numeric values of the encodings (not read from `encodings.js`).
