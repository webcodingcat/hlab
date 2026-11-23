package console

import (
	"bufio"
	"context"
	"crypto/des"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/scheduler"
)

// SessionOptions configure one RFB session.
type SessionOptions struct {
	Name     string          // desktop name in ServerInit, the server's name
	Interval time.Duration   // poll interval; 0 = 200 ms (5 frames/s)
	Clock    scheduler.Clock // nil = real clock
}

const (
	rfbVersion        = "RFB 003.008\n"
	secVNCAuth        = 2
	authFailureReason = "authentication failed"
	defaultInterval   = 200 * time.Millisecond
)

// RFB encodings the server understands.
const (
	encodingRaw         int32 = 0
	encodingDesktopSize int32 = -223
	encodingQEMUKey     int32 = -258
)

// Client message types.
const (
	msgSetPixelFormat     = 0
	msgSetEncodings       = 2
	msgUpdateRequest      = 3
	msgKeyEvent           = 4
	msgPointerEvent       = 5
	msgClientCutText      = 6
	msgQEMU               = 255
	qemuExtendedKeyEvent  = 0
	serverFramebufferType = 0
)

// errAuth is returned by Serve when the client's password is wrong.
var errAuth = errors.New("console: authentication failed")

// Serve speaks RFB 3.8 on conn: VNC Authentication with password (only the
// first 8 characters count, as at Hetzner), Raw encoding in the client's
// 32-bpp pixel format, DesktopSize, QEMU Extended Key Event; returns when
// the client leaves, ctx ends or src fails. Serve closes conn when it
// returns or ctx ends.
func Serve(ctx context.Context, conn net.Conn, src platform.ConsoleConn, password string, o SessionOptions) error {
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	r := bufio.NewReader(conn)
	if err := authenticate(r, conn, password); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if _, err := r.ReadByte(); err != nil { // ClientInit; shared or not, all clients share
		return clientErr(ctx, err)
	}
	s := newSession(conn, src, o)
	if err := s.grab(ctx); err != nil {
		return err
	}
	if err := s.serverInit(o.Name); err != nil {
		return clientErr(ctx, err)
	}
	return s.run(ctx, r)
}

// clientErr turns the end of the client connection into nil and a
// connection closed by ctx into ctx's error.
func clientErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// authenticate runs version and security negotiation with VNC
// Authentication and writes the SecurityResult.
func authenticate(r *bufio.Reader, w io.Writer, password string) error {
	if _, err := io.WriteString(w, rfbVersion); err != nil {
		return err
	}
	version := make([]byte, len(rfbVersion))
	if _, err := io.ReadFull(r, version); err != nil {
		return err
	}
	if string(version) != rfbVersion {
		return fmt.Errorf("console: unsupported client version %q", version)
	}
	if _, err := w.Write([]byte{1, secVNCAuth}); err != nil {
		return err
	}
	choice, err := r.ReadByte()
	if err != nil {
		return err
	}
	if choice != secVNCAuth {
		return fmt.Errorf("console: client chose security type %d", choice)
	}
	challenge := make([]byte, 16)
	if _, err := rand.Read(challenge); err != nil {
		return err
	}
	if _, err := w.Write(challenge); err != nil {
		return err
	}
	response := make([]byte, 16)
	if _, err := io.ReadFull(r, response); err != nil {
		return err
	}
	want, err := vncEncrypt(password, challenge)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(response, want) != 1 {
		msg := binary.BigEndian.AppendUint32(nil, 1)
		msg = binary.BigEndian.AppendUint32(msg, uint32(len(authFailureReason)))
		msg = append(msg, authFailureReason...)
		if _, err := w.Write(msg); err != nil {
			return err
		}
		return errAuth
	}
	_, err = w.Write([]byte{0, 0, 0, 0})
	return err
}

// vncEncrypt encrypts the challenge as VNC Authentication does. The DES key
// is the password cut to 8 bytes (zero padded), so only the first 8
// characters of a password count, as at Hetzner; VNC reverses the bits of
// every key byte, a quirk of the original implementation every client
// follows (E22).
func vncEncrypt(password string, challenge []byte) ([]byte, error) {
	key := make([]byte, 8)
	copy(key, password)
	for i := range key {
		key[i] = bits.Reverse8(key[i])
	}
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	block.Encrypt(out[:8], challenge[:8])
	block.Encrypt(out[8:], challenge[8:16])
	return out, nil
}

// clientMsg is one parsed client message, or the read error that ended
// the connection.
type clientMsg struct {
	typ         byte
	pf          rfbFormat // SetPixelFormat
	encodings   []int32   // SetEncodings
	incremental bool      // FramebufferUpdateRequest
	down        bool      // KeyEvent, QEMU key
	keysym      uint32    // KeyEvent, QEMU key
	keycode     uint32    // QEMU key
	mask        uint8     // PointerEvent
	x, y        int       // PointerEvent
	err         error
}

// readMessages parses client messages into msgs until the connection
// fails; the last message carries the error. It stops when done closes.
func readMessages(r *bufio.Reader, msgs chan<- clientMsg, done <-chan struct{}) {
	for {
		m, err := readMessage(r)
		if err != nil {
			m = clientMsg{err: err}
		}
		select {
		case msgs <- m:
		case <-done:
			return
		}
		if err != nil {
			return
		}
	}
}

func readMessage(r *bufio.Reader) (clientMsg, error) {
	typ, err := r.ReadByte()
	if err != nil {
		return clientMsg{}, err
	}
	m := clientMsg{typ: typ}
	read := func(n int) ([]byte, error) {
		b := make([]byte, n)
		_, err := io.ReadFull(r, b)
		return b, err
	}
	switch typ {
	case msgSetPixelFormat:
		b, err := read(3 + 16)
		if err != nil {
			return m, err
		}
		m.pf = parseFormat(b[3:])
	case msgSetEncodings:
		b, err := read(3)
		if err != nil {
			return m, err
		}
		n := int(binary.BigEndian.Uint16(b[1:]))
		b, err = read(4 * n)
		if err != nil {
			return m, err
		}
		for i := 0; i < n; i++ {
			m.encodings = append(m.encodings, int32(binary.BigEndian.Uint32(b[4*i:])))
		}
	case msgUpdateRequest:
		b, err := read(9) // region ignored: every update covers the whole screen
		if err != nil {
			return m, err
		}
		m.incremental = b[0] != 0
	case msgKeyEvent:
		b, err := read(7)
		if err != nil {
			return m, err
		}
		m.down = b[0] != 0
		m.keysym = binary.BigEndian.Uint32(b[3:])
	case msgPointerEvent:
		b, err := read(5)
		if err != nil {
			return m, err
		}
		m.mask = b[0]
		m.x = int(binary.BigEndian.Uint16(b[1:]))
		m.y = int(binary.BigEndian.Uint16(b[3:]))
	case msgClientCutText:
		b, err := read(7)
		if err != nil {
			return m, err
		}
		if _, err := io.CopyN(io.Discard, r, int64(binary.BigEndian.Uint32(b[3:]))); err != nil {
			return m, err
		}
	case msgQEMU:
		b, err := read(11)
		if err != nil {
			return m, err
		}
		if b[0] != qemuExtendedKeyEvent {
			return m, fmt.Errorf("console: unsupported QEMU message %d", b[0])
		}
		m.down = binary.BigEndian.Uint16(b[1:]) != 0
		m.keysym = binary.BigEndian.Uint32(b[3:])
		m.keycode = binary.BigEndian.Uint32(b[7:])
	default:
		return m, fmt.Errorf("console: unsupported client message %d", typ)
	}
	return m, nil
}

// handle applies one client message to the session.
func (s *session) handle(ctx context.Context, m clientMsg) error {
	switch m.typ {
	case msgSetPixelFormat:
		if m.pf.bpp != 32 || !m.pf.trueColour || m.pf.rShift > 24 || m.pf.gShift > 24 || m.pf.bShift > 24 {
			return fmt.Errorf("console: unsupported pixel format %+v", m.pf)
		}
		s.setFormat(m.pf)
	case msgSetEncodings:
		s.desktopSize = false
		qemu := false
		for _, e := range m.encodings {
			switch e {
			case encodingDesktopSize:
				s.desktopSize = true
			case encodingQEMUKey:
				qemu = true
			}
		}
		if qemu && !s.qemu {
			s.qemuPending = true
		}
		s.qemu = qemu
		return s.flush()
	case msgUpdateRequest:
		s.pending = true
		if !m.incremental {
			s.full = true
		}
		return s.flush()
	case msgKeyEvent:
		s.grabSoon = true
		return s.scancodes(ctx, s.keyboard.Keysym(m.keysym, m.down))
	case msgQEMU:
		s.grabSoon = true
		codes := QEMUKey(m.keycode, m.down)
		if codes == nil {
			codes = s.keyboard.Keysym(m.keysym, m.down)
		}
		return s.scancodes(ctx, codes)
	case msgPointerEvent:
		s.grabSoon = true
		return s.pointer(ctx, m.mask, m.x, m.y)
	}
	return nil
}

func (s *session) scancodes(ctx context.Context, codes []byte) error {
	if len(codes) == 0 {
		return nil
	}
	return s.src.Scancodes(ctx, codes)
}

// pointer sends the mouse calls of one PointerEvent; a move only when the
// position changed.
func (s *session) pointer(ctx context.Context, mask uint8, x, y int) error {
	ops := Pointer(s.mask, mask, x, y, s.last.Width, s.last.Height)
	s.mask = mask
	for _, op := range ops {
		var err error
		switch op.Kind {
		case PointerMove:
			if s.havePos && op.X == s.posX && op.Y == s.posY {
				continue
			}
			s.havePos, s.posX, s.posY = true, op.X, op.Y
			err = s.src.MouseMove(ctx, op.X, op.Y)
		case PointerButton:
			err = s.src.MouseButton(ctx, op.Button, op.Down)
		case PointerScroll:
			err = s.src.MouseScroll(ctx, op.Delta)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
