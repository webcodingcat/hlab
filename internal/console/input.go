// Package console serves the graphical console of a server over RFB
// (VNC) and translates its input to platform.ConsoleConn calls.
package console

// Scan code set 1 bytes.
const (
	prefixExtended = 0xE0
	breakBit       = 0x80
	codeShiftL     = 0x2A
	codeShiftR     = 0x36
)

// Keysyms of the Shift keys.
const (
	keysymShiftL = 0xFFE1
	keysymShiftR = 0xFFE2
)

// usKey is a key on a US keyboard: its set 1 make code, whether it needs
// the 0xE0 prefix and, for a character, whether the character needs Shift.
type usKey struct {
	code     byte
	extended bool
	char     bool // a character: Shift is corrected to shift
	shift    bool
}

// keysyms maps an X11 keysym to its key on a US keyboard.
var keysyms = buildKeysyms()

func buildKeysyms() map[uint32]usKey {
	m := map[uint32]usKey{}
	chars := func(codes map[byte][2]rune) {
		for code, c := range codes {
			m[uint32(c[0])] = usKey{code: code, char: true}
			m[uint32(c[1])] = usKey{code: code, char: true, shift: true}
		}
	}
	// Letters: lower case without Shift, upper case with it.
	rows := []struct {
		first   byte
		letters string
	}{{0x10, "qwertyuiop"}, {0x1E, "asdfghjkl"}, {0x2C, "zxcvbnm"}}
	letters := map[byte][2]rune{}
	for _, r := range rows {
		for i, l := range r.letters {
			letters[r.first+byte(i)] = [2]rune{l, l - 'a' + 'A'}
		}
	}
	chars(letters)
	chars(map[byte][2]rune{
		0x02: {'1', '!'}, 0x03: {'2', '@'}, 0x04: {'3', '#'}, 0x05: {'4', '$'},
		0x06: {'5', '%'}, 0x07: {'6', '^'}, 0x08: {'7', '&'}, 0x09: {'8', '*'},
		0x0A: {'9', '('}, 0x0B: {'0', ')'}, 0x0C: {'-', '_'}, 0x0D: {'=', '+'},
		0x1A: {'[', '{'}, 0x1B: {']', '}'}, 0x27: {';', ':'}, 0x28: {'\'', '"'},
		0x29: {'`', '~'}, 0x2B: {'\\', '|'}, 0x33: {',', '<'}, 0x34: {'.', '>'},
		0x35: {'/', '?'},
	})
	m[' '] = usKey{code: 0x39, char: true}

	plain := map[uint32]byte{
		0xFF08: 0x0E, // BackSpace
		0xFF09: 0x0F, // Tab
		0xFF0D: 0x1C, // Return
		0xFF1B: 0x01, // Escape
		0xFFE1: codeShiftL,
		0xFFE2: codeShiftR,
		0xFFE3: 0x1D, // Control_L
		0xFFE9: 0x38, // Alt_L
		0xFF7F: 0x45, // Num_Lock
		0xFFAA: 0x37, // KP_Multiply
		0xFFAB: 0x4E, // KP_Add
		0xFFAD: 0x4A, // KP_Subtract
		0xFFAE: 0x53, // KP_Decimal
		0xFF95: 0x47, // KP_Home
		0xFF96: 0x4B, // KP_Left
		0xFF97: 0x48, // KP_Up
		0xFF98: 0x4D, // KP_Right
		0xFF99: 0x50, // KP_Down
		0xFF9A: 0x49, // KP_Prior
		0xFF9B: 0x51, // KP_Next
		0xFF9C: 0x4F, // KP_End
		0xFF9D: 0x4C, // KP_Begin
		0xFF9E: 0x52, // KP_Insert
		0xFF9F: 0x53, // KP_Delete
	}
	// F1-F10 are consecutive, F11 and F12 are not.
	for i := uint32(0); i < 10; i++ {
		plain[0xFFBE+i] = 0x3B + byte(i)
	}
	plain[0xFFC8] = 0x57
	plain[0xFFC9] = 0x58
	// KP_0 to KP_9.
	for i, code := range []byte{0x52, 0x4F, 0x50, 0x51, 0x4B, 0x4C, 0x4D, 0x47, 0x48, 0x49} {
		plain[0xFFB0+uint32(i)] = code
	}
	for sym, code := range plain {
		m[sym] = usKey{code: code}
	}

	extended := map[uint32]byte{
		0xFFFF: 0x53, // Delete
		0xFF50: 0x47, // Home
		0xFF51: 0x4B, // Left
		0xFF52: 0x48, // Up
		0xFF53: 0x4D, // Right
		0xFF54: 0x50, // Down
		0xFF55: 0x49, // Prior (Page Up)
		0xFF56: 0x51, // Next (Page Down)
		0xFF57: 0x4F, // End
		0xFF63: 0x52, // Insert
		0xFFE4: 0x1D, // Control_R
		0xFFEA: 0x38, // Alt_R
		0xFFEB: 0x5B, // Super_L
		0xFFEC: 0x5C, // Super_R
		0xFF8D: 0x1C, // KP_Enter
		0xFFAF: 0x35, // KP_Divide
	}
	for sym, code := range extended {
		m[sym] = usKey{code: code, extended: true}
	}
	return m
}

// Keyboard turns RFB KeyEvents (keysyms) into scan code set 1 bytes on a
// US layout, correcting Shift where the client's Shift state differs from
// what the character needs on a US keyboard; it tracks held modifiers.
type Keyboard struct {
	shiftL, shiftR bool
}

// Keysym returns the set 1 bytes for a key press (down) or release; nil
// when the keysym has no key on a US keyboard.
func (k *Keyboard) Keysym(keysym uint32, down bool) []byte {
	key, ok := keysyms[keysym]
	if !ok {
		return nil
	}
	switch keysym {
	case keysymShiftL:
		k.shiftL = down
	case keysymShiftR:
		k.shiftR = down
	}
	if !down {
		return scancode(key.code, key.extended, false)
	}
	held := k.shiftL || k.shiftR
	if !key.char || key.shift == held {
		return scancode(key.code, key.extended, true)
	}
	// The character needs another Shift state than the client holds: set
	// it around the make and restore the held state afterwards.
	var out []byte
	if key.shift {
		out = append(out, codeShiftL)
		out = append(out, scancode(key.code, false, true)...)
		return append(out, codeShiftL|breakBit)
	}
	if k.shiftL {
		out = append(out, codeShiftL|breakBit)
	}
	if k.shiftR {
		out = append(out, codeShiftR|breakBit)
	}
	out = append(out, scancode(key.code, false, true)...)
	if k.shiftL {
		out = append(out, codeShiftL)
	}
	if k.shiftR {
		out = append(out, codeShiftR)
	}
	return out
}

// scancode returns the make or break of one key.
func scancode(code byte, extended, down bool) []byte {
	if !down {
		code |= breakBit
	}
	if extended {
		return []byte{prefixExtended, code}
	}
	return []byte{code}
}

// QEMUKey turns a QEMU Extended Key Event keycode (XT, 0xE0 prefix as the
// high bit 0x80 of the byte, as noVNC's getRFBkeycode) into set 1 bytes;
// nil for a keycode outside one byte or 0.
func QEMUKey(keycode uint32, down bool) []byte {
	code := byte(keycode &^ breakBit)
	if keycode > 0xFF || code == 0 {
		return nil
	}
	return scancode(code, keycode&breakBit != 0, down)
}

// PointerKind is the kind of a PointerOp.
type PointerKind int

// Pointer operation kinds.
const (
	PointerMove PointerKind = iota
	PointerButton
	PointerScroll
)

// PointerOp is one ConsoleConn mouse call.
type PointerOp struct {
	Kind   PointerKind
	X, Y   int  // PointerMove, clamped to 0..w-1, 0..h-1
	Button int  // PointerButton: 1 left, 2 right, 3 middle
	Down   bool // PointerButton
	Delta  int  // PointerScroll: +120 up (bit 3), -120 down (bit 4)
}

// rfbButtons maps RFB button mask bits 0, 1 and 2 (left, middle, right) to
// Hyper-V button indexes.
var rfbButtons = [3]int{1, 3, 2}

// Pointer returns the operations for an RFB PointerEvent: first a move to
// the clamped position (the caller drops it when the position did not
// change), then the button transitions of bits 0/1/2 (to 1/3/2), then one
// scroll per press of bit 3 or 4.
func Pointer(prevMask, mask uint8, x, y, w, h int) []PointerOp {
	ops := []PointerOp{{Kind: PointerMove, X: clamp(x, w), Y: clamp(y, h)}}
	for bit, button := range rfbButtons {
		was, is := prevMask&(1<<bit) != 0, mask&(1<<bit) != 0
		if was != is {
			ops = append(ops, PointerOp{Kind: PointerButton, Button: button, Down: is})
		}
	}
	if pressed(prevMask, mask, 3) {
		ops = append(ops, PointerOp{Kind: PointerScroll, Delta: 120})
	}
	if pressed(prevMask, mask, 4) {
		ops = append(ops, PointerOp{Kind: PointerScroll, Delta: -120})
	}
	return ops
}

// pressed reports whether bit went from released to pressed.
func pressed(prevMask, mask uint8, bit int) bool {
	return prevMask&(1<<bit) == 0 && mask&(1<<bit) != 0
}

// clamp limits v to 0..size-1.
func clamp(v, size int) int {
	if v >= size {
		v = size - 1
	}
	if v < 0 {
		v = 0
	}
	return v
}
