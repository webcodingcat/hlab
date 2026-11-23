package console

import (
	"bufio"
	"bytes"
	"context"
	"crypto/des"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// stepTimeout is the deadline of every single waiting step.
const stepTimeout = 3 * time.Second

// testPassword is a password of the form Issue hands out.
const testPassword = "Ab3dE6gH9jK2mN5pQ8sT1vW4yZ7bC0eF"

// RFB encodings the tests use.
const (
	encRaw         int32 = 0
	encDesktopSize int32 = -223
	encQEMUKey     int32 = -258
)

// RGB565 colours.
const (
	black565 uint16 = 0x0000
	red565   uint16 = 0xF800
	green565 uint16 = 0x07E0
	blue565  uint16 = 0x001F
	white565 uint16 = 0xFFFF
	gray565  uint16 = 0x8410
)

// eventually polls cond until it holds or the step deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(stepTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// --- frames ---

func solidFrame(w, h int, c uint16) platform.Frame {
	f := platform.Frame{Width: w, Height: h, Pix: make([]byte, w*h*2)}
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(f.Pix[2*i:], c)
	}
	return f
}

// patternFrame has a different colour on neighbouring pixels so that a
// wrong pixel format or offset shows.
func patternFrame(w, h int) platform.Frame {
	palette := []uint16{red565, green565, blue565, white565, gray565, black565}
	f := platform.Frame{Width: w, Height: h, Pix: make([]byte, w*h*2)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			binary.LittleEndian.PutUint16(f.Pix[2*(y*w+x):], palette[(x+2*y)%len(palette)])
		}
	}
	return f
}

// withPixel returns a copy of f with pixel x, y set to c.
func withPixel(f platform.Frame, x, y int, c uint16) platform.Frame {
	g := platform.Frame{Width: f.Width, Height: f.Height, Pix: append([]byte(nil), f.Pix...)}
	binary.LittleEndian.PutUint16(g.Pix[2*(y*f.Width+x):], c)
	return g
}

// rgb888 converts an RGB565 colour to 0xRRGGBB.
func rgb888(c uint16) uint32 {
	r := uint32(c>>11) * 255 / 31
	g := uint32(c>>5&0x3F) * 255 / 63
	b := uint32(c&0x1F) * 255 / 31
	return r<<16 | g<<8 | b
}

// near accepts the rounding differences of RGB565 to 8-bit conversions.
func near(a, b uint32) bool {
	for shift := 0; shift <= 16; shift += 8 {
		x, y := int(a>>shift&0xFF), int(b>>shift&0xFF)
		if x-y > 8 || y-x > 8 {
			return false
		}
	}
	return true
}

// --- fake console source ---

// fakeConn is a platform.ConsoleConn with a scripted screen and a log of
// the input it receives.
type fakeConn struct {
	mu     sync.Mutex
	frame  platform.Frame
	ok     bool
	grabs  int
	codes  []byte
	mouse  []string
	closed bool
}

func newFakeConn(f platform.Frame) *fakeConn { return &fakeConn{frame: f, ok: true} }

// setScreen sets what the next grab returns; ok false is "no picture".
func (c *fakeConn) setScreen(f platform.Frame, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frame, c.ok = f, ok
}

func (c *fakeConn) grabCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.grabs
}

func (c *fakeConn) scancodes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.codes...)
}

func (c *fakeConn) mouseLog() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.mouse...)
}

func (c *fakeConn) Screen(context.Context) (platform.Frame, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.grabs++
	if !c.ok {
		return platform.Frame{}, false, nil
	}
	return platform.Frame{Width: c.frame.Width, Height: c.frame.Height, Pix: append([]byte(nil), c.frame.Pix...)}, true, nil
}

func (c *fakeConn) Scancodes(_ context.Context, codes []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes = append(c.codes, codes...)
	return nil
}

func (c *fakeConn) MouseMove(_ context.Context, x, y int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mouse = append(c.mouse, fmt.Sprintf("move %d %d", x, y))
	return nil
}

func (c *fakeConn) MouseButton(_ context.Context, index int, down bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mouse = append(c.mouse, fmt.Sprintf("button %d %v", index, down))
	return nil
}

func (c *fakeConn) MouseScroll(_ context.Context, delta int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mouse = append(c.mouse, fmt.Sprintf("scroll %d", delta))
	return nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// --- test clock ---

// fakeClock is a scheduler.Clock whose time the test sets and whose After
// channels fire only on tick.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, ch)
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fire fires all pending After channels and reports whether there was one.
func (c *fakeClock) fire() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.waiters {
		ch <- c.now
	}
	n := len(c.waiters)
	c.waiters = nil
	return n > 0
}

// tick waits until someone waits on the clock and fires it.
func (c *fakeClock) tick(t *testing.T) {
	t.Helper()
	eventually(t, "a waiter on the test clock", c.fire)
}

// --- RFB test client ---

type pixelFormat struct {
	BPP, Depth             uint8
	BigEndian, TrueColour  bool
	RMax, GMax, BMax       uint16
	RShift, GShift, BShift uint8
}

// noVNCFormat is the format noVNC 1.7.0 sets (E24).
var noVNCFormat = pixelFormat{BPP: 32, Depth: 24, TrueColour: true,
	RMax: 255, GMax: 255, BMax: 255, RShift: 0, GShift: 8, BShift: 16}

func (p pixelFormat) bytes() []byte {
	b := make([]byte, 16)
	b[0], b[1] = p.BPP, p.Depth
	if p.BigEndian {
		b[2] = 1
	}
	if p.TrueColour {
		b[3] = 1
	}
	binary.BigEndian.PutUint16(b[4:], p.RMax)
	binary.BigEndian.PutUint16(b[6:], p.GMax)
	binary.BigEndian.PutUint16(b[8:], p.BMax)
	b[10], b[11], b[12] = p.RShift, p.GShift, p.BShift
	return b
}

func parsePixelFormat(b []byte) pixelFormat {
	return pixelFormat{
		BPP: b[0], Depth: b[1], BigEndian: b[2] != 0, TrueColour: b[3] != 0,
		RMax: binary.BigEndian.Uint16(b[4:]), GMax: binary.BigEndian.Uint16(b[6:]),
		BMax:   binary.BigEndian.Uint16(b[8:]),
		RShift: b[10], GShift: b[11], BShift: b[12],
	}
}

// decode turns one pixel in format p into 0xRRGGBB.
func (p pixelFormat) decode(b []byte) uint32 {
	var v uint32
	if p.BigEndian {
		v = binary.BigEndian.Uint32(b)
	} else {
		v = binary.LittleEndian.Uint32(b)
	}
	ch := func(shift uint8, max uint16) uint32 {
		if max == 0 {
			return 0
		}
		return (v >> shift & uint32(max)) * 255 / uint32(max)
	}
	return ch(p.RShift, p.RMax)<<16 | ch(p.GShift, p.GMax)<<8 | ch(p.BShift, p.BMax)
}

type rect struct {
	X, Y, W, H int
	Enc        int32
}

// rfbClient is a minimal RFB 3.8 client that keeps the framebuffer.
type rfbClient struct {
	t      *testing.T
	conn   net.Conn
	r      *bufio.Reader
	pf     pixelFormat
	width  int
	height int
	name   string
	fb     []uint32
	set    []bool
	qemu   bool // the server answered the QEMU Extended Key Event pseudo-encoding
}

func newRFBClient(t *testing.T, conn net.Conn) *rfbClient {
	return &rfbClient{t: t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *rfbClient) read(n int) []byte {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(stepTimeout)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(c.r, b); err != nil {
		c.t.Fatalf("read %d bytes: %v", n, err)
	}
	return b
}

func (c *rfbClient) u8() uint8   { c.t.Helper(); return c.read(1)[0] }
func (c *rfbClient) u16() uint16 { c.t.Helper(); return binary.BigEndian.Uint16(c.read(2)) }
func (c *rfbClient) u32() uint32 { c.t.Helper(); return binary.BigEndian.Uint32(c.read(4)) }

func (c *rfbClient) write(parts ...[]byte) {
	c.t.Helper()
	if err := c.conn.SetWriteDeadline(time.Now().Add(stepTimeout)); err != nil {
		c.t.Fatalf("set write deadline: %v", err)
	}
	if _, err := c.conn.Write(bytes.Join(parts, nil)); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// vncResponse encrypts the challenge as VNC Authentication does: DES-ECB
// with the first 8 password bytes, each key byte bit-reversed.
func vncResponse(t *testing.T, password string, challenge []byte) []byte {
	t.Helper()
	key := make([]byte, 8)
	copy(key, password)
	for i := range key {
		key[i] = bits.Reverse8(key[i])
	}
	block, err := des.NewCipher(key)
	if err != nil {
		t.Fatalf("des: %v", err)
	}
	out := make([]byte, 16)
	block.Encrypt(out[:8], challenge[:8])
	block.Encrypt(out[8:], challenge[8:16])
	return out
}

// handshake runs version and security negotiation and returns the
// SecurityResult and its reason. On success it also runs ClientInit and
// reads ServerInit.
func (c *rfbClient) handshake(password string) (uint32, string) {
	c.t.Helper()
	if v := string(c.read(12)); v != "RFB 003.008\n" {
		c.t.Fatalf("server version %q, want RFB 003.008", v)
	}
	c.write([]byte("RFB 003.008\n"))
	n := int(c.u8())
	if n == 0 {
		c.t.Fatalf("server offers no security type: %q", c.read(int(c.u32())))
	}
	types := c.read(n)
	if !bytes.Contains(types, []byte{2}) {
		c.t.Fatalf("security types %v without VNC Authentication", types)
	}
	c.write([]byte{2})
	c.write(vncResponse(c.t, password, c.read(16)))
	result := c.u32()
	if result != 0 {
		return result, string(c.read(int(c.u32())))
	}
	c.write([]byte{1}) // ClientInit, shared
	c.width, c.height = int(c.u16()), int(c.u16())
	c.pf = parsePixelFormat(c.read(16))
	c.name = string(c.read(int(c.u32())))
	c.resize(c.width, c.height)
	return 0, ""
}

// login runs the handshake and fails the test unless it succeeds.
func (c *rfbClient) login(password string) {
	c.t.Helper()
	if result, reason := c.handshake(password); result != 0 {
		c.t.Fatalf("SecurityResult %d (%q), want 0", result, reason)
	}
}

func (c *rfbClient) resize(w, h int) {
	c.width, c.height = w, h
	c.fb = make([]uint32, w*h)
	c.set = make([]bool, w*h)
}

func (c *rfbClient) setPixelFormat(p pixelFormat) {
	c.t.Helper()
	c.write([]byte{0, 0, 0, 0}, p.bytes())
	c.pf = p
}

func (c *rfbClient) setEncodings(encs ...int32) {
	c.t.Helper()
	b := []byte{2, 0, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(len(encs)))
	for _, e := range encs {
		b = binary.BigEndian.AppendUint32(b, uint32(e))
	}
	c.write(b)
}

func (c *rfbClient) request(incremental bool) {
	c.t.Helper()
	b := []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if incremental {
		b[1] = 1
	}
	binary.BigEndian.PutUint16(b[6:], uint16(c.width))
	binary.BigEndian.PutUint16(b[8:], uint16(c.height))
	c.write(b)
}

func (c *rfbClient) key(down bool, keysym uint32) {
	c.t.Helper()
	b := []byte{4, 0, 0, 0}
	if down {
		b[1] = 1
	}
	c.write(binary.BigEndian.AppendUint32(b, keysym))
}

func (c *rfbClient) qemuKey(down bool, keysym, keycode uint32) {
	c.t.Helper()
	b := []byte{255, 0, 0, 0}
	if down {
		b[3] = 1
	}
	b = binary.BigEndian.AppendUint32(b, keysym)
	c.write(binary.BigEndian.AppendUint32(b, keycode))
}

func (c *rfbClient) pointer(mask uint8, x, y int) {
	c.t.Helper()
	b := []byte{5, mask, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(x))
	binary.BigEndian.PutUint16(b[4:], uint16(y))
	c.write(b)
}

// readUpdate reads server messages up to the next FramebufferUpdate,
// applies it and returns its rectangles.
func (c *rfbClient) readUpdate() []rect {
	c.t.Helper()
	for {
		switch typ := c.u8(); typ {
		case 0:
		case 2: // Bell
			continue
		case 3: // ServerCutText
			c.read(3)
			c.read(int(c.u32()))
			continue
		default:
			c.t.Fatalf("unexpected server message type %d", typ)
		}
		c.read(1)
		n := int(c.u16())
		rects := make([]rect, 0, n)
		for i := 0; i < n; i++ {
			r := rect{X: int(c.u16()), Y: int(c.u16()), W: int(c.u16()), H: int(c.u16()), Enc: int32(c.u32())}
			rects = append(rects, r)
			switch r.Enc {
			case encRaw:
				if c.pf.BPP != 32 || !c.pf.TrueColour {
					c.t.Fatalf("pixel format %+v is not 32-bpp true colour", c.pf)
				}
				if r.X+r.W > c.width || r.Y+r.H > c.height {
					c.t.Fatalf("rectangle %+v outside the %dx%d screen", r, c.width, c.height)
				}
				data := c.read(r.W * r.H * 4)
				for y := 0; y < r.H; y++ {
					for x := 0; x < r.W; x++ {
						i := (r.Y+y)*c.width + r.X + x
						c.fb[i] = c.pf.decode(data[4*(y*r.W+x):])
						c.set[i] = true
					}
				}
			case encDesktopSize:
				c.resize(r.W, r.H)
			case encQEMUKey:
				c.qemu = true
			default:
				c.t.Fatalf("rectangle %+v in an encoding the client did not announce", r)
			}
		}
		return rects
	}
}

// diff describes how the framebuffer differs from f; empty when it shows f.
func (c *rfbClient) diff(f platform.Frame) string {
	if c.width != f.Width || c.height != f.Height {
		return fmt.Sprintf("screen %dx%d, want %dx%d", c.width, c.height, f.Width, f.Height)
	}
	for i := range c.fb {
		if !c.set[i] {
			return fmt.Sprintf("pixel %d,%d never sent", i%c.width, i/c.width)
		}
		want := rgb888(binary.LittleEndian.Uint16(f.Pix[2*i:]))
		if !near(c.fb[i], want) {
			return fmt.Sprintf("pixel %d,%d is %06x, want %06x", i%c.width, i/c.width, c.fb[i], want)
		}
	}
	return ""
}

// waitScreen requests updates until the framebuffer shows f. nudge, if
// set, runs after each update that does not show f yet.
func (c *rfbClient) waitScreen(f platform.Frame, incremental bool, nudge func()) {
	c.t.Helper()
	deadline := time.Now().Add(stepTimeout)
	for {
		d := c.diff(f)
		if d == "" {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("screen never matched: %s", d)
		}
		c.request(incremental)
		c.readUpdate()
		if nudge != nil {
			nudge()
		}
	}
}

// expectClosed drains the connection until the server closes it.
func (c *rfbClient) expectClosed() {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(stepTimeout)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	if _, err := io.Copy(io.Discard, c.r); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			c.t.Fatalf("connection still open")
		}
	}
}

// --- session harness ---

type served struct {
	done chan struct{}
	err  error
}

// startServe runs Serve on one end of a loopback TCP connection and
// returns the client end.
func startServe(t *testing.T, src platform.ConsoleConn, password string, o SessionOptions) (net.Conn, *served) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &served{done: make(chan struct{})}
	go func() {
		defer close(s.done)
		// Closed only after Accept: on Windows closing the listener resets
		// a dialed connection that is not accepted yet.
		conn, err := ln.Accept()
		_ = ln.Close()
		if err != nil {
			s.err = err
			return
		}
		defer func() { _ = conn.Close() }()
		s.err = Serve(ctx, conn, src, password, o)
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		cancel()
		_ = ln.Close()
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = conn.Close()
		select {
		case <-s.done:
		case <-time.After(stepTimeout):
			t.Errorf("Serve did not return after the client left")
		}
	})
	return conn, s
}

// startSession serves src and logs in with testPassword.
func startSession(t *testing.T, src platform.ConsoleConn, o SessionOptions) *rfbClient {
	t.Helper()
	conn, _ := startServe(t, src, testPassword, o)
	c := newRFBClient(t, conn)
	c.login(testPassword)
	return c
}

func TestRFB(t *testing.T) {
	fast := SessionOptions{Name: "web-1", Interval: 10 * time.Millisecond}

	t.Run("CON-01/session", func(t *testing.T) {
		cases := []struct {
			name string
			pf   *pixelFormat // nil: keep the server's default
		}{
			{name: "novnc", pf: &noVNCFormat},
			{name: "server_default"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				screen := patternFrame(16, 8)
				c := startSession(t, newFakeConn(screen), fast)
				if c.width != 16 || c.height != 8 {
					t.Errorf("ServerInit size %dx%d, want 16x8", c.width, c.height)
				}
				if c.name != "web-1" {
					t.Errorf("ServerInit name %q, want web-1", c.name)
				}
				if tc.pf != nil {
					c.setPixelFormat(*tc.pf)
				} else if c.pf.BPP != 32 || !c.pf.TrueColour {
					t.Fatalf("server default pixel format %+v, want 32-bpp true colour", c.pf)
				}
				c.setEncodings(encRaw)
				c.waitScreen(screen, false, nil)
			})
		}
	})

	t.Run("CON-01/updates", func(t *testing.T) {
		clk := newFakeClock()
		screen := solidFrame(16, 16, blue565)
		src := newFakeConn(screen)
		c := startSession(t, src, SessionOptions{Name: "web-1", Interval: 200 * time.Millisecond, Clock: clk})
		c.setPixelFormat(noVNCFormat)
		c.setEncodings(encRaw, encDesktopSize)
		c.waitScreen(screen, false, func() { clk.fire() })

		// An incremental request is held while the screen stays the same.
		c.request(true)
		before := src.grabCount()
		clk.tick(t)
		eventually(t, "a poll grab", func() bool { return src.grabCount() > before })

		changed := withPixel(screen, 5, 9, red565)
		src.setScreen(changed, true)
		clk.tick(t)
		rects := c.readUpdate()
		if d := c.diff(changed); d != "" {
			t.Fatalf("first update after the change: %s (rectangles %+v)", d, rects)
		}
		area, covers := 0, false
		for _, r := range rects {
			area += r.W * r.H
			if r.X <= 5 && 5 < r.X+r.W && r.Y <= 9 && 9 < r.Y+r.H {
				covers = true
			}
		}
		if !covers || area >= 16*16 {
			t.Errorf("rectangles %+v, want only the changed area around 5,9", rects)
		}

		// A key event grabs at once; the clock does not tick.
		c.request(true)
		afterKey := withPixel(changed, 0, 0, green565)
		src.setScreen(afterKey, true)
		c.key(true, 'a')
		c.key(false, 'a')
		c.readUpdate()
		if d := c.diff(afterKey); d != "" {
			t.Fatalf("update after a key event: %s", d)
		}

		// So does a pointer event.
		c.request(true)
		afterPointer := withPixel(afterKey, 15, 15, white565)
		src.setScreen(afterPointer, true)
		c.pointer(0, 3, 3)
		c.readUpdate()
		if d := c.diff(afterPointer); d != "" {
			t.Fatalf("update after a pointer event: %s", d)
		}
	})

	t.Run("CON-01/resize", func(t *testing.T) {
		small := patternFrame(8, 6)
		src := newFakeConn(small)
		c := startSession(t, src, fast)
		c.setPixelFormat(noVNCFormat)
		c.setEncodings(encRaw, encDesktopSize)
		c.waitScreen(small, false, nil)

		big := patternFrame(20, 12)
		src.setScreen(big, true)
		c.request(true)
		rects := c.readUpdate()
		if len(rects) == 0 || rects[len(rects)-1].Enc != encDesktopSize {
			t.Fatalf("rectangles %+v, want DesktopSize as the last", rects)
		}
		if last := rects[len(rects)-1]; last.W != 20 || last.H != 12 {
			t.Errorf("DesktopSize %dx%d, want 20x12", last.W, last.H)
		}
		for _, r := range rects[:len(rects)-1] {
			if r.Enc == encDesktopSize {
				t.Errorf("rectangles %+v, more than one DesktopSize", rects)
			}
		}
		c.waitScreen(big, true, nil)
	})

	t.Run("CON-01/no_picture", func(t *testing.T) {
		src := newFakeConn(platform.Frame{})
		src.setScreen(platform.Frame{}, false)
		c := startSession(t, src, fast)
		if c.width <= 0 || c.height <= 0 {
			t.Fatalf("ServerInit size %dx%d without a picture", c.width, c.height)
		}
		c.setPixelFormat(noVNCFormat)
		c.setEncodings(encRaw, encDesktopSize)
		blank := solidFrame(c.width, c.height, black565)
		c.request(false)
		c.readUpdate()
		if d := c.diff(blank); d != "" {
			t.Fatalf("before the first picture: %s", d)
		}
		n := src.grabCount()
		eventually(t, "polls without a picture", func() bool { return src.grabCount() >= n+2 })
		c.request(false)
		c.readUpdate()
		if d := c.diff(blank); d != "" {
			t.Fatalf("still without a picture: %s", d)
		}

		pic := patternFrame(8, 6)
		src.setScreen(pic, true)
		c.waitScreen(pic, true, nil)

		src.setScreen(platform.Frame{}, false)
		n = src.grabCount()
		eventually(t, "polls without a picture", func() bool { return src.grabCount() >= n+2 })
		c.request(false)
		c.readUpdate()
		if d := c.diff(pic); d != "" {
			t.Fatalf("without a picture the last frame should stay: %s", d)
		}

		live := solidFrame(8, 6, green565)
		src.setScreen(live, true)
		c.waitScreen(live, true, nil)
	})

	t.Run("CON-01/input", func(t *testing.T) {
		src := newFakeConn(solidFrame(16, 16, blue565))
		c := startSession(t, src, fast)
		c.setPixelFormat(noVNCFormat)
		c.setEncodings(encRaw, encDesktopSize)

		c.key(true, 'a')
		c.key(false, 'a')
		eventually(t, "scan codes of a plain key event", func() bool {
			return bytes.Equal(src.scancodes(), []byte{0x1E, 0x9E})
		})

		c.setEncodings(encRaw, encDesktopSize, encQEMUKey)
		deadline := time.Now().Add(stepTimeout)
		for !c.qemu {
			if time.Now().After(deadline) {
				t.Fatalf("no QEMU Extended Key Event pseudo-rectangle")
			}
			c.request(false)
			c.readUpdate()
		}
		c.qemuKey(true, 0xFF50, 0xC7) // Home
		c.qemuKey(false, 0xFF50, 0xC7)
		eventually(t, "scan codes of an extended key event", func() bool {
			return bytes.Equal(src.scancodes(), []byte{0x1E, 0x9E, 0xE0, 0x47, 0xE0, 0xC7})
		})

		c.pointer(1, 2, 3)
		want := []string{"move 2 3", "button 1 true"}
		eventually(t, "mouse calls of a pointer event", func() bool {
			return strings.Join(src.mouseLog(), ",") == strings.Join(want, ",")
		})
	})

	t.Run("CON-02/wrong_password", func(t *testing.T) {
		src := newFakeConn(patternFrame(16, 8))
		conn, s := startServe(t, src, testPassword, fast)
		c := newRFBClient(t, conn)
		result, reason := c.handshake("WrongPassword-WrongPassword-1234")
		if result != 1 {
			t.Fatalf("SecurityResult %d, want 1", result)
		}
		if reason == "" {
			t.Errorf("SecurityResult without a reason")
		}
		if err := conn.SetReadDeadline(time.Now().Add(stepTimeout)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		rest, err := io.ReadAll(c.r)
		if len(rest) > 0 {
			t.Errorf("%d bytes after the failed SecurityResult", len(rest))
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatalf("connection still open after the failed SecurityResult")
		}
		select {
		case <-s.done:
		case <-time.After(stepTimeout):
			t.Fatalf("Serve did not return after the failed authentication")
		}
	})
}
