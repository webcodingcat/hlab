package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/scheduler"
)

// Size of the black screen shown before the source delivers a picture.
const (
	blankWidth  = 1024
	blankHeight = 768
)

// rfbFormat is an RFB PIXEL_FORMAT; the server only serves 32-bpp true
// colour.
type rfbFormat struct {
	bpp, depth             uint8
	bigEndian, trueColour  bool
	rMax, gMax, bMax       uint16
	rShift, gShift, bShift uint8
}

// defaultFormat is the format of ServerInit: 32 bpp, depth 24, little
// endian, true colour, shifts 16/8/0.
var defaultFormat = rfbFormat{bpp: 32, depth: 24, trueColour: true,
	rMax: 255, gMax: 255, bMax: 255, rShift: 16, gShift: 8, bShift: 0}

func parseFormat(b []byte) rfbFormat {
	return rfbFormat{
		bpp: b[0], depth: b[1], bigEndian: b[2] != 0, trueColour: b[3] != 0,
		rMax: binary.BigEndian.Uint16(b[4:]), gMax: binary.BigEndian.Uint16(b[6:]),
		bMax:   binary.BigEndian.Uint16(b[8:]),
		rShift: b[10], gShift: b[11], bShift: b[12],
	}
}

func (p rfbFormat) bytes() []byte {
	b := make([]byte, 16)
	b[0], b[1] = p.bpp, p.depth
	if p.bigEndian {
		b[2] = 1
	}
	if p.trueColour {
		b[3] = 1
	}
	binary.BigEndian.PutUint16(b[4:], p.rMax)
	binary.BigEndian.PutUint16(b[6:], p.gMax)
	binary.BigEndian.PutUint16(b[8:], p.bMax)
	b[10], b[11], b[12] = p.rShift, p.gShift, p.bShift
	return b
}

// table maps every RGB565 value to its 32-bit pixel in format p.
func (p rfbFormat) table() []uint32 {
	scale := func(v, from uint32, max uint16) uint32 {
		return (v*uint32(max) + from/2) / from
	}
	t := make([]uint32, 1<<16)
	for c := range t {
		v := uint32(c)
		t[c] = scale(v>>11, 31, p.rMax)<<p.rShift |
			scale(v>>5&0x3F, 63, p.gMax)<<p.gShift |
			scale(v&0x1F, 31, p.bMax)<<p.bShift
	}
	return t
}

// area is a rectangle of the screen.
type area struct{ x, y, w, h int }

func (a area) empty() bool { return a.w <= 0 || a.h <= 0 }

// clip limits a to a w x h screen.
func (a area) clip(w, h int) area {
	a.w = min(a.x+a.w, w) - a.x
	a.h = min(a.y+a.h, h) - a.y
	return a
}

// changed returns the bounding box of the pixels that differ between two
// frames of the same size; empty when they are equal.
func changed(old, cur platform.Frame) area {
	row := cur.Width * 2
	top, bottom, left, right := -1, -1, cur.Width, -1
	for y := 0; y < cur.Height; y++ {
		a, b := old.Pix[y*row:(y+1)*row], cur.Pix[y*row:(y+1)*row]
		if bytes.Equal(a, b) {
			continue
		}
		if top < 0 {
			top = y
		}
		bottom = y
		for x := 0; x < left; x++ {
			if a[2*x] != b[2*x] || a[2*x+1] != b[2*x+1] {
				left = x
				break
			}
		}
		for x := cur.Width - 1; x > right; x-- {
			if a[2*x] != b[2*x] || a[2*x+1] != b[2*x+1] {
				right = x
				break
			}
		}
	}
	if top < 0 {
		return area{}
	}
	return area{x: left, y: top, w: right - left + 1, h: bottom - top + 1}
}

// session is one RFB connection after the handshake. All calls on src and
// all writes to conn happen on the goroutine of run.
type session struct {
	conn     net.Conn
	src      platform.ConsoleConn
	clock    scheduler.Clock
	interval time.Duration

	format rfbFormat
	pixels []uint32 // RGB565 to client pixel

	last   platform.Frame // latest picture; black before the first
	sent   platform.Frame // what the client shows; Pix nil when nothing
	cw, ch int            // the client's framebuffer size

	desktopSize, qemu, qemuPending bool
	pending, full                  bool // a held FramebufferUpdateRequest
	grabSoon                       bool // an input event asks for a grab

	keyboard   Keyboard
	mask       uint8
	havePos    bool
	posX, posY int
}

func newSession(conn net.Conn, src platform.ConsoleConn, o SessionOptions) *session {
	s := &session{conn: conn, src: src, clock: o.Clock, interval: o.Interval}
	if s.clock == nil {
		s.clock = scheduler.RealClock()
	}
	if s.interval <= 0 {
		s.interval = defaultInterval
	}
	s.last = platform.Frame{Width: blankWidth, Height: blankHeight, Pix: make([]byte, blankWidth*blankHeight*2)}
	s.setFormat(defaultFormat)
	return s
}

func (s *session) setFormat(p rfbFormat) {
	s.format = p
	s.pixels = p.table()
}

// serverInit writes ServerInit with the size of the current picture.
func (s *session) serverInit(name string) error {
	s.cw, s.ch = s.last.Width, s.last.Height
	b := binary.BigEndian.AppendUint16(nil, uint16(s.cw))
	b = binary.BigEndian.AppendUint16(b, uint16(s.ch))
	b = append(b, s.format.bytes()...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(name)))
	b = append(b, name...)
	_, err := s.conn.Write(b)
	return err
}

// grab takes the current screen; without a picture the last one stays.
func (s *session) grab(ctx context.Context) error {
	f, ok, err := s.src.Screen(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if f.Width <= 0 || f.Height <= 0 || len(f.Pix) != f.Width*f.Height*2 {
		return fmt.Errorf("console: frame %dx%d with %d bytes", f.Width, f.Height, len(f.Pix))
	}
	s.last = f
	return nil
}

// run serves client messages, polls the screen every interval and grabs
// right after input, until the client leaves, ctx ends or src fails.
func (s *session) run(ctx context.Context, r *bufio.Reader) error {
	msgs := make(chan clientMsg, 64)
	done := make(chan struct{})
	defer close(done)
	go readMessages(r, msgs, done)

	tick := s.clock.After(s.interval)
	for {
		// Input grabs wait until the queued messages are handled, so a
		// burst of pointer events costs one grab.
		if s.grabSoon && len(msgs) == 0 {
			s.grabSoon = false
			if err := s.grab(ctx); err != nil {
				return err
			}
			if err := s.flush(); err != nil {
				return clientErr(ctx, err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m := <-msgs:
			if m.err != nil {
				return clientErr(ctx, m.err)
			}
			if err := s.handle(ctx, m); err != nil {
				return clientErr(ctx, err)
			}
		case <-tick:
			tick = s.clock.After(s.interval)
			if err := s.grab(ctx); err != nil {
				return err
			}
			if err := s.flush(); err != nil {
				return clientErr(ctx, err)
			}
		}
	}
}

// flush answers a held update request when there is something to send:
// a full request at once, an incremental one after a change.
func (s *session) flush() error {
	if !s.pending {
		return nil
	}
	var rects [][]byte
	if s.qemuPending {
		rects = append(rects, rectHeader(area{w: s.cw, h: s.ch}, encodingQEMUKey))
		s.qemuPending = false
	}
	if s.desktopSize && (s.last.Width != s.cw || s.last.Height != s.ch) {
		// After DesktopSize the client has no picture; the next request
		// gets the whole screen at the new size.
		s.cw, s.ch = s.last.Width, s.last.Height
		s.sent = platform.Frame{}
		rects = append(rects, rectHeader(area{w: s.cw, h: s.ch}, encodingDesktopSize))
	} else {
		a := area{w: s.last.Width, h: s.last.Height}
		if !s.full && s.sent.Pix != nil && s.sent.Width == s.last.Width && s.sent.Height == s.last.Height {
			a = changed(s.sent, s.last)
		}
		// A client without DesktopSize keeps its size and sees the part
		// that fits.
		if a = a.clip(s.cw, s.ch); !a.empty() {
			rects = append(rects, s.raw(a))
		}
		s.sent = s.last
	}
	if len(rects) == 0 {
		return nil
	}
	s.pending, s.full = false, false
	msg := []byte{serverFramebufferType, 0}
	msg = binary.BigEndian.AppendUint16(msg, uint16(len(rects)))
	_, err := s.conn.Write(bytes.Join(append([][]byte{msg}, rects...), nil))
	return err
}

func rectHeader(a area, encoding int32) []byte {
	b := binary.BigEndian.AppendUint16(nil, uint16(a.x))
	b = binary.BigEndian.AppendUint16(b, uint16(a.y))
	b = binary.BigEndian.AppendUint16(b, uint16(a.w))
	b = binary.BigEndian.AppendUint16(b, uint16(a.h))
	return binary.BigEndian.AppendUint32(b, uint32(encoding))
}

// raw encodes area a of the last picture as a Raw rectangle in the
// client's pixel format.
func (s *session) raw(a area) []byte {
	b := rectHeader(a, encodingRaw)
	out := make([]byte, len(b)+a.w*a.h*4)
	copy(out, b)
	p := out[len(b):]
	put := binary.LittleEndian.PutUint32
	if s.format.bigEndian {
		put = binary.BigEndian.PutUint32
	}
	for y := 0; y < a.h; y++ {
		src := s.last.Pix[2*((a.y+y)*s.last.Width+a.x):]
		for x := 0; x < a.w; x++ {
			put(p, s.pixels[binary.LittleEndian.Uint16(src[2*x:])])
			p = p[4:]
		}
	}
	return out
}
