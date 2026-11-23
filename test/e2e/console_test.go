//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/des" //nolint:gosec // VNC Authentication is DES by protocol
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The CON-01 rows share the shared server and one VNC session to it,
// opened with the URL and password `hcloud server request-console` prints.
// The session is the test's own RFB 3.8 client over WebSocket and TLS that
// trusts only the hlab CA (`/ca.pem` through `hlab ca export`); "Trust CA"
// is not touched. What reaches the guest is read over SSH from the guest's
// input devices, found by name in /proc/bus/input/devices.

// Guest input device names (E25).
const (
	guestKeyboard = "AT Translated Set 2 keyboard"
	guestMouse    = "Microsoft Vmbus HID-compliant Mouse"
)

// Linux input event types and codes the rows look for.
const (
	evKey     = 1
	evRel     = 2
	evAbs     = 3
	keyBack   = 14
	keyA      = 30
	keyB      = 48
	keyH      = 35
	keyL      = 38
	btnLeft   = 0x110
	relWheel  = 8
	absX      = 0
	absY      = 1
	keysymBS  = 0xff08
	rfbButton = 1 << 0 // left
	rfbWheel  = 1 << 3 // wheel up
)

// testCON01 opens the console session and runs the three CON-01 rows on it
// one after the other: they share the session and the guest's screen.
func testCON01(t *testing.T) {
	e, srv := sharedServer(t)
	wssURL, password := requestConsole(t, e, srv)
	c, err := dialConsole(wssURL, password, hlabCA(t))
	if err != nil {
		t.Fatalf("VNC session to %s: %v", redactToken(wssURL), err)
	}
	t.Cleanup(func() { _ = c.Close() })
	t.Logf("VNC session to %s: %dx%d, desktop %q", srv.Name, c.initW, c.initH, c.name)

	t.Run("screen", logged(func(t *testing.T) { testCON01Screen(t, c) }))
	t.Run("keyboard", logged(func(t *testing.T) { testCON01Keyboard(t, e, srv, c) }))
	t.Run("mouse", logged(func(t *testing.T) { testCON01Mouse(t, e, srv, c) }))
}

// testCON01Screen: the session delivers a screen that is not black.
func testCON01Screen(t *testing.T, c *rfbClient) {
	if err := c.waitFrame(2*time.Minute, "a screen that is not black", func(f rfbFrame) bool {
		return f.lit() > 0
	}); err != nil {
		t.Fatal(err)
	}
}

// testCON01Keyboard: keys typed at the login prompt change the screen and
// arrive at the guest's keyboard device. The typed text is erased again.
func testCON01Keyboard(t *testing.T, e *env, srv *server, c *rfbClient) {
	in := startGuestInput(t, e, srv, guestKeyboard, "keyboard")
	before := c.current()
	if before.n == 0 {
		t.Fatal("the session has delivered no screen yet")
	}

	typed := "hlab"
	for _, r := range typed {
		c.tap(t, uint32(r))
	}
	t.Cleanup(func() {
		for range typed {
			c.tap(t, keysymBS)
		}
	})

	// A blinking cursor changes one character cell; four typed characters
	// change a region at least three cells (24 pixels) wide.
	if err := c.waitFrame(30*time.Second, "the typed text on the screen", func(f rfbFrame) bool {
		_, _, w, _ := f.diff(before)
		return w >= 24
	}); err != nil {
		t.Error(err)
	}

	want := []int{keyH, keyL, keyA, keyB}
	in.wait(t, 30*time.Second, "key presses and releases of h, l, a, b", func(evs []inputEvent) bool {
		for _, code := range want {
			if !hasEvent(evs, evKey, code, func(v int) bool { return v == 1 }) ||
				!hasEvent(evs, evKey, code, func(v int) bool { return v == 0 }) {
				return false
			}
		}
		return true
	})
}

// testCON01Mouse: pointer moves, a left click and one wheel step arrive at
// the guest's Hyper-V mouse.
func testCON01Mouse(t *testing.T, e *env, srv *server, c *rfbClient) {
	in := startGuestInput(t, e, srv, guestMouse, "mouse")
	f := c.current()
	if f.n == 0 {
		t.Fatal("the session has delivered no screen yet")
	}
	x1, y1 := f.w/4, f.h/4
	x2, y2 := f.w/2, f.h/2
	c.pointer(t, 0, x1, y1)
	c.pointer(t, 0, x2, y2)
	c.pointer(t, rfbButton, x2, y2)
	c.pointer(t, 0, x2, y2)
	c.pointer(t, rfbWheel, x2, y2)
	c.pointer(t, 0, x2, y2)

	in.wait(t, 30*time.Second, "absolute moves, left button down and up, one wheel step", func(evs []inputEvent) bool {
		anyValue := func(int) bool { return true }
		moved := hasEvent(evs, evAbs, absX, anyValue) || hasEvent(evs, evAbs, absY, anyValue)
		down := hasEvent(evs, evKey, btnLeft, func(v int) bool { return v == 1 })
		up := hasEvent(evs, evKey, btnLeft, func(v int) bool { return v == 0 })
		wheel := hasEvent(evs, evRel, relWheel, func(v int) bool { return v != 0 })
		return moved && down && up && wheel
	})
}

var (
	consoleURLRe      = regexp.MustCompile(`wss://\S+`)
	consolePasswordRe = regexp.MustCompile(`(?im)password:\s*(\S+)\s*$`)
	consoleTokenRe    = regexp.MustCompile(`token=[^&\s]+`)
)

// requestConsole runs `hcloud server request-console` and takes the URL and
// password from what it prints. The password is never logged.
func requestConsole(t *testing.T, e *env, srv *server) (wssURL, password string) {
	t.Helper()
	r, err := e.hcloudRun(context.Background(), 2*time.Minute, "server", "request-console", srv.Name)
	u := consoleURLRe.FindString(r.Stdout)
	m := consolePasswordRe.FindStringSubmatch(r.Stdout)
	if m != nil {
		r = redact(r, m[1])
	}
	r.Stdout = redactToken(r.Stdout)
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hcloud server request-console %s: %v\n%s", srv.Name, err, r)
	}
	if u == "" || m == nil {
		t.Fatalf("hcloud server request-console %s printed no URL or password:\n%s", srv.Name, r)
	}
	return u, m[1]
}

// redactToken hides the token of a console URL in messages.
func redactToken(u string) string {
	return consoleTokenRe.ReplaceAllString(u, "token=<token>")
}

// rfbClient is a minimal RFB 3.8 client: VNC Authentication, Raw encoding
// in 32 bpp (blue in the lowest byte), DesktopSize. One goroutine reads the
// server's messages into the frame buffer and asks for the next
// incremental update after each one.
type rfbClient struct {
	conn  net.Conn
	r     *bufio.Reader
	wmu   sync.Mutex // one message write at a time
	name  string
	initW int
	initH int

	mu  sync.Mutex
	fb  rfbFrame
	err error // why the reader stopped
}

// rfbFrame is a copy of the frame buffer: 4 bytes per pixel, blue, green,
// red, unused; n counts the updates received.
type rfbFrame struct {
	w, h int
	pix  []byte
	n    uint64
}

// lit counts the pixels that are clearly not black.
func (f rfbFrame) lit() int {
	n := 0
	for i := 0; i+3 < len(f.pix); i += 4 {
		if f.pix[i] > 16 || f.pix[i+1] > 16 || f.pix[i+2] > 16 {
			n++
		}
	}
	return n
}

// diff returns the bounding box of the pixels that differ from old; a
// changed size counts as the whole screen.
func (f rfbFrame) diff(old rfbFrame) (x, y, w, h int) {
	if f.w != old.w || f.h != old.h {
		return 0, 0, f.w, f.h
	}
	minX, minY, maxX, maxY := f.w, f.h, -1, -1
	for py := 0; py < f.h; py++ {
		row := py * f.w * 4
		for px := 0; px < f.w; px++ {
			i := row + px*4
			if f.pix[i] != old.pix[i] || f.pix[i+1] != old.pix[i+1] || f.pix[i+2] != old.pix[i+2] {
				minX, maxX = min(minX, px), max(maxX, px)
				minY, maxY = min(minY, py), max(maxY, py)
			}
		}
	}
	if maxX < 0 {
		return 0, 0, 0, 0
	}
	return minX, minY, maxX - minX + 1, maxY - minY + 1
}

// dialConsole opens the WebSocket over TLS with only the hlab CA as root,
// does the RFB handshake and starts the reader.
func dialConsole(wssURL, password string, caPEM []byte) (*rfbClient, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("the exported hlab CA is no PEM certificate")
	}
	hc := &http.Client{
		// Bounds the handshake only; the open connection has no limit.
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	ws, resp, err := websocket.Dial(context.Background(), wssURL, &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("WebSocket dial: %w (HTTP %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("WebSocket dial: %w", err)
	}
	// A full screen is one message of several MB.
	ws.SetReadLimit(-1)
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	c := &rfbClient{conn: conn, r: bufio.NewReaderSize(conn, 1<<16)}

	// The handshake has its own deadline; on expiry the connection closes.
	done := make(chan error, 1)
	go func() { done <- c.handshake(password) }()
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		err = errors.New("RFB handshake did not finish within 30s")
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	go c.readLoop()
	if err := c.request(false); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *rfbClient) Close() error { return c.conn.Close() }

func (c *rfbClient) handshake(password string) error {
	ver := make([]byte, 12)
	if _, err := io.ReadFull(c.r, ver); err != nil {
		return fmt.Errorf("read ProtocolVersion: %w", err)
	}
	if !bytes.HasPrefix(ver, []byte("RFB 003.")) {
		return fmt.Errorf("ProtocolVersion %q", ver)
	}
	if _, err := c.conn.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	n, err := c.r.ReadByte()
	if err != nil {
		return fmt.Errorf("read security types: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("server refused the connection: %s", c.reason())
	}
	types := make([]byte, n)
	if _, err := io.ReadFull(c.r, types); err != nil {
		return err
	}
	if !bytes.Contains(types, []byte{2}) {
		return fmt.Errorf("server offers security types %v, not VNC Authentication (2)", types)
	}
	if _, err := c.conn.Write([]byte{2}); err != nil {
		return err
	}
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(c.r, challenge); err != nil {
		return fmt.Errorf("read VNC challenge: %w", err)
	}
	// The DES key is the password's first 8 bytes with each byte's bits
	// reversed (RFB VNC Authentication).
	key := make([]byte, 8)
	for i := 0; i < 8 && i < len(password); i++ {
		key[i] = bits.Reverse8(password[i])
	}
	block, err := des.NewCipher(key) //nolint:gosec // VNC Authentication
	if err != nil {
		return err
	}
	answer := make([]byte, 16)
	block.Encrypt(answer[:8], challenge[:8])
	block.Encrypt(answer[8:], challenge[8:])
	if _, err := c.conn.Write(answer); err != nil {
		return err
	}
	var result uint32
	if err := binary.Read(c.r, binary.BigEndian, &result); err != nil {
		return fmt.Errorf("read SecurityResult: %w", err)
	}
	if result != 0 {
		return fmt.Errorf("SecurityResult %d: %s", result, c.reason())
	}
	if _, err := c.conn.Write([]byte{1}); err != nil { // ClientInit, shared
		return err
	}
	var init struct {
		W, H uint16
		PF   [16]byte
		Len  uint32
	}
	if err := binary.Read(c.r, binary.BigEndian, &init); err != nil {
		return fmt.Errorf("read ServerInit: %w", err)
	}
	name := make([]byte, init.Len)
	if _, err := io.ReadFull(c.r, name); err != nil {
		return err
	}
	c.name, c.initW, c.initH = string(name), int(init.W), int(init.H)
	c.fb = rfbFrame{w: c.initW, h: c.initH, pix: make([]byte, c.initW*c.initH*4)}

	// SetPixelFormat: 32 bpp, depth 24, little endian, true colour, red
	// shift 16, green 8, blue 0.
	pf := []byte{0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}
	if _, err := c.conn.Write(pf); err != nil {
		return err
	}
	// SetEncodings: Raw (0), DesktopSize (-223).
	enc := []byte{2, 0, 0, 2}
	enc = binary.BigEndian.AppendUint32(enc, 0)
	enc = binary.BigEndian.AppendUint32(enc, uint32(0xffffff21)) // -223
	_, err = c.conn.Write(enc)
	return err
}

// reason reads a length-prefixed reason string.
func (c *rfbClient) reason() string {
	var n uint32
	if err := binary.Read(c.r, binary.BigEndian, &n); err != nil || n > 1<<16 {
		return "(no reason)"
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(c.r, b); err != nil {
		return "(no reason)"
	}
	return string(b)
}

func (c *rfbClient) write(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.conn.Write(b)
	return err
}

// request sends a FramebufferUpdateRequest for the whole screen.
func (c *rfbClient) request(incremental bool) error {
	c.mu.Lock()
	w, h := c.fb.w, c.fb.h
	c.mu.Unlock()
	inc := byte(0)
	if incremental {
		inc = 1
	}
	b := []byte{3, inc, 0, 0, 0, 0}
	b = binary.BigEndian.AppendUint16(b, uint16(w))
	b = binary.BigEndian.AppendUint16(b, uint16(h))
	return c.write(b)
}

func (c *rfbClient) readLoop() {
	err := c.readMessages()
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

func (c *rfbClient) readMessages() error {
	for {
		typ, err := c.r.ReadByte()
		if err != nil {
			return fmt.Errorf("read server message: %w", err)
		}
		switch typ {
		case 0: // FramebufferUpdate
			if err := c.readUpdate(); err != nil {
				return err
			}
			if err := c.request(true); err != nil {
				return err
			}
		case 1: // SetColourMapEntries
			var h struct {
				Pad      byte
				First, N uint16
			}
			if err := binary.Read(c.r, binary.BigEndian, &h); err != nil {
				return err
			}
			if _, err := c.r.Discard(int(h.N) * 6); err != nil {
				return err
			}
		case 2: // Bell
		case 3: // ServerCutText
			if _, err := c.r.Discard(3); err != nil {
				return err
			}
			var n uint32
			if err := binary.Read(c.r, binary.BigEndian, &n); err != nil {
				return err
			}
			if _, err := c.r.Discard(int(n)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected server message type %d", typ)
		}
	}
}

func (c *rfbClient) readUpdate() error {
	var h struct {
		Pad byte
		N   uint16
	}
	if err := binary.Read(c.r, binary.BigEndian, &h); err != nil {
		return fmt.Errorf("read FramebufferUpdate: %w", err)
	}
	for i := 0; i < int(h.N); i++ {
		var rect struct {
			X, Y, W, H uint16
			Enc        int32
		}
		if err := binary.Read(c.r, binary.BigEndian, &rect); err != nil {
			return fmt.Errorf("read rectangle: %w", err)
		}
		x, y, w, hh := int(rect.X), int(rect.Y), int(rect.W), int(rect.H)
		switch rect.Enc {
		case 0: // Raw
			row := make([]byte, w*4)
			for py := y; py < y+hh; py++ {
				if _, err := io.ReadFull(c.r, row); err != nil {
					return fmt.Errorf("read Raw rectangle: %w", err)
				}
				c.mu.Lock()
				if py < c.fb.h && x < c.fb.w {
					n := min(w, c.fb.w-x) * 4
					copy(c.fb.pix[(py*c.fb.w+x)*4:], row[:n])
				}
				c.mu.Unlock()
			}
		case -223: // DesktopSize
			c.mu.Lock()
			c.fb.w, c.fb.h, c.fb.pix = w, hh, make([]byte, w*hh*4)
			c.mu.Unlock()
		default:
			return fmt.Errorf("rectangle with encoding %d, not asked for", rect.Enc)
		}
	}
	c.mu.Lock()
	c.fb.n++
	c.mu.Unlock()
	return nil
}

// current is a copy of the frame buffer.
func (c *rfbClient) current() rfbFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.fb
	f.pix = append([]byte(nil), c.fb.pix...)
	return f
}

// waitFrame waits until a received frame meets cond.
func (c *rfbClient) waitFrame(timeout time.Duration, what string, cond func(rfbFrame) bool) error {
	err := waitFor(timeout, 100*time.Millisecond, func() (bool, error) {
		c.mu.Lock()
		rerr := c.err
		c.mu.Unlock()
		f := c.current()
		if f.n > 0 && cond(f) {
			return true, nil
		}
		if rerr != nil {
			return false, fmt.Errorf("the VNC session ended: %w", rerr)
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for %s: %w", what, err)
	}
	return nil
}

// tap sends a key press and release of keysym.
func (c *rfbClient) tap(t *testing.T, keysym uint32) {
	t.Helper()
	for _, down := range []byte{1, 0} {
		b := []byte{4, down, 0, 0}
		b = binary.BigEndian.AppendUint32(b, keysym)
		if err := c.write(b); err != nil {
			t.Fatalf("KeyEvent %#x: %v", keysym, err)
		}
	}
}

// pointer sends a PointerEvent with button mask at x, y.
func (c *rfbClient) pointer(t *testing.T, mask byte, x, y int) {
	t.Helper()
	b := []byte{5, mask}
	b = binary.BigEndian.AppendUint16(b, uint16(x))
	b = binary.BigEndian.AppendUint16(b, uint16(y))
	if err := c.write(b); err != nil {
		t.Fatalf("PointerEvent %d at %d,%d: %v", mask, x, y, err)
	}
}

// inputReaderScript runs as root on the guest: it finds the input device
// named argv[1] in /proc/bus/input/devices, writes "ready <device>" to
// argv[2] once the device is open and then one line "type code value" per
// event (EV_SYN left out); "no device" when the name is not listed.
const inputReaderScript = `import os, struct, sys
name, out = sys.argv[1], sys.argv[2]
dev = None
with open('/proc/bus/input/devices') as f:
    blocks = f.read().split('\n\n')
for b in blocks:
    if 'N: Name="' + name + '"' not in b:
        continue
    for line in b.splitlines():
        if line.startswith('H: Handlers='):
            for h in line.split('=', 1)[1].split():
                if h.startswith('event'):
                    dev = '/dev/input/' + h
    break
log = open(out, 'w', buffering=1)
if dev is None:
    log.write('no device\n')
    sys.exit(2)
fd = os.open(dev, os.O_RDONLY)
log.write('ready ' + dev + '\n')
size = struct.calcsize('llHHi')
while True:
    data = os.read(fd, size * 64)
    for i in range(0, len(data) - size + 1, size):
        _, _, typ, code, value = struct.unpack('llHHi', data[i:i + size])
        if typ != 0:
            log.write('%d %d %d\n' % (typ, code, value))
`

// inputEvent is one event of a guest input device.
type inputEvent struct{ typ, code, value int }

// guestInput is a running reader of one guest input device.
type guestInput struct {
	e   *env
	s   *server
	log string
}

// startGuestInput starts the reader of the named device on the server and
// waits until it has opened the device; it is stopped when the test ends.
func startGuestInput(t *testing.T, e *env, s *server, device, tag string) *guestInput {
	t.Helper()
	script := "/root/hlab-e2e-input.py"
	g := &guestInput{e: e, s: s, log: "/root/hlab-e2e-con01-" + tag + ".log"}
	if r, err := e.ssh(s, time.Minute, inputReaderScript, "cat > "+script); err != nil {
		t.Fatalf("copy the input reader to %s: %v\n%s", s.Name, err, r)
	}
	// Neither the pattern nor the path, split by empty quotes, matches the
	// killing shell's own command line; the reader still gets the real path.
	quoted := "/root/hlab-e2e-con01-''" + tag + ".log"
	stop := "pkill -f '[h]lab-e2e-con01-" + tag + ".log'; rm -f " + quoted
	start := stop + "; setsid -f timeout 600 python3 " + script + " '" + device + "' " + quoted + " </dev/null >/dev/null 2>&1"
	if r, err := e.ssh(s, time.Minute, "", start); err != nil {
		t.Fatalf("start the input reader on %s: %v\n%s", s.Name, err, r)
	}
	t.Cleanup(func() {
		if r, err := e.ssh(s, time.Minute, "", stop); err != nil {
			t.Errorf("stop the input reader on %s: %v\n%s", s.Name, err, r)
		}
	})
	var first string
	err := waitFor(time.Minute, 500*time.Millisecond, func() (bool, error) {
		r, err := e.ssh(s, 30*time.Second, "", "head -n 1 "+g.log+" 2>/dev/null; true")
		if err != nil {
			return false, nil
		}
		first = strings.TrimSpace(r.Stdout)
		if first == "no device" {
			names, _ := e.ssh(s, 30*time.Second, "", "grep '^N: Name=' /proc/bus/input/devices")
			return false, fmt.Errorf("no input device %q on %s; listed:\n%s", device, s.Name, names.Stdout)
		}
		return strings.HasPrefix(first, "ready "), nil
	})
	if err != nil {
		t.Fatalf("input reader of %q on %s (first line %q): %v", device, s.Name, first, err)
	}
	t.Logf("reading %q at %s", device, strings.TrimPrefix(first, "ready "))
	return g
}

// events returns the events read so far.
func (g *guestInput) events() ([]inputEvent, error) {
	r, err := g.e.ssh(g.s, 30*time.Second, "", "cat "+g.log)
	if err != nil {
		return nil, fmt.Errorf("read %s on %s: %v\n%s", g.log, g.s.Name, err, r)
	}
	var evs []inputEvent
	for _, line := range strings.Split(r.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		typ, err1 := strconv.Atoi(f[0])
		code, err2 := strconv.Atoi(f[1])
		value, err3 := strconv.Atoi(f[2])
		if err1 == nil && err2 == nil && err3 == nil {
			evs = append(evs, inputEvent{typ, code, value})
		}
	}
	return evs, nil
}

// wait waits until the events read meet cond.
func (g *guestInput) wait(t *testing.T, timeout time.Duration, what string, cond func([]inputEvent) bool) {
	t.Helper()
	var last []inputEvent
	err := waitFor(timeout, time.Second, func() (bool, error) {
		evs, err := g.events()
		if err != nil {
			return false, nil
		}
		last = evs
		return cond(evs), nil
	})
	if err != nil {
		t.Errorf("guest %s: %s: %v; events read: %v", g.log, what, err, last)
	}
}

// hasEvent reports whether evs hold an event of typ and code whose value
// meets ok.
func hasEvent(evs []inputEvent, typ, code int, ok func(int) bool) bool {
	for _, e := range evs {
		if e.typ == typ && e.code == code && ok(e.value) {
			return true
		}
	}
	return false
}
