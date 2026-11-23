package console

import (
	"bytes"
	"fmt"
	"testing"
)

// keyEvent is one RFB KeyEvent as a client sends it.
type keyEvent struct {
	keysym uint32
	down   bool
}

// usKeyboard replays scan code set 1 bytes on a simulated US keyboard and
// records the characters the keys produce, so Shift corrections can be
// checked without fixing the exact order of Shift breaks and makes.
type usKeyboard struct {
	shiftL, shiftR bool
	typed          []rune
}

// usChars maps a set 1 make code to its unshifted and shifted character.
var usChars = map[byte][2]rune{
	0x02: {'1', '!'},
	0x1E: {'a', 'A'},
	0x35: {'/', '?'},
}

func (u *usKeyboard) feed(t *testing.T, codes []byte) {
	t.Helper()
	extended := false
	for _, c := range codes {
		if c == 0xE0 {
			extended = true
			continue
		}
		isMake := c&0x80 == 0
		code := c &^ 0x80
		switch {
		case !extended && code == 0x2A:
			u.shiftL = isMake
		case !extended && code == 0x36:
			u.shiftR = isMake
		case !extended && isMake:
			chars, ok := usChars[code]
			if !ok {
				t.Fatalf("unexpected make code %#x in %x", code, codes)
			}
			if u.shiftL || u.shiftR {
				u.typed = append(u.typed, chars[1])
			} else {
				u.typed = append(u.typed, chars[0])
			}
		}
		extended = false
	}
}

func TestInput(t *testing.T) {
	t.Run("CON-01/keysym", func(t *testing.T) {
		const (
			shiftL   = 0xFFE1
			controlL = 0xFFE3
			altL     = 0xFFE9
			deleteK  = 0xFFFF
		)
		cases := []struct {
			name   string
			events []keyEvent
			// want holds the exact bytes per event; nil entries mean no key.
			want [][]byte
			// typed and shiftHeld check the result on a simulated US
			// keyboard instead of exact bytes.
			typed     string
			shiftHeld bool
		}{
			{
				name:   "letter",
				events: []keyEvent{{'a', true}, {'a', false}},
				want:   [][]byte{{0x1E}, {0x9E}},
			},
			{
				name:   "capital_with_shift_held",
				events: []keyEvent{{shiftL, true}, {'A', true}, {'A', false}, {shiftL, false}},
				want:   [][]byte{{0x2A}, {0x1E}, {0x9E}, {0xAA}},
			},
			{
				// A layout where '/' needs Shift: the client holds Shift,
				// the US key for '/' needs none.
				name:      "symbol_shift_held_but_not_needed",
				events:    []keyEvent{{shiftL, true}, {'/', true}, {'/', false}},
				typed:     "/",
				shiftHeld: true,
			},
			{
				// A client that sends '!' without Shift: the US key needs it.
				name:      "symbol_shift_needed_but_not_held",
				events:    []keyEvent{{'!', true}, {'!', false}},
				typed:     "!",
				shiftHeld: false,
			},
			{
				name:   "digit",
				events: []keyEvent{{'1', true}, {'1', false}},
				want:   [][]byte{{0x02}, {0x82}},
			},
			{
				name:   "return",
				events: []keyEvent{{0xFF0D, true}, {0xFF0D, false}},
				want:   [][]byte{{0x1C}, {0x9C}},
			},
			{
				name:   "backspace",
				events: []keyEvent{{0xFF08, true}, {0xFF08, false}},
				want:   [][]byte{{0x0E}, {0x8E}},
			},
			{
				name:   "home",
				events: []keyEvent{{0xFF50, true}, {0xFF50, false}},
				want:   [][]byte{{0xE0, 0x47}, {0xE0, 0xC7}},
			},
			{
				name: "arrows",
				events: []keyEvent{
					{0xFF51, true}, {0xFF51, false},
					{0xFF52, true}, {0xFF52, false},
					{0xFF53, true}, {0xFF53, false},
					{0xFF54, true}, {0xFF54, false},
				},
				want: [][]byte{
					{0xE0, 0x4B}, {0xE0, 0xCB},
					{0xE0, 0x48}, {0xE0, 0xC8},
					{0xE0, 0x4D}, {0xE0, 0xCD},
					{0xE0, 0x50}, {0xE0, 0xD0},
				},
			},
			{
				name:   "f5",
				events: []keyEvent{{0xFFC2, true}, {0xFFC2, false}},
				want:   [][]byte{{0x3F}, {0xBF}},
			},
			{
				// The order noVNC's sendCtrlAltDel uses.
				name: "ctrl_alt_del",
				events: []keyEvent{
					{controlL, true}, {altL, true}, {deleteK, true},
					{deleteK, false}, {altL, false}, {controlL, false},
				},
				want: [][]byte{
					{0x1D}, {0x38}, {0xE0, 0x53},
					{0xE0, 0xD3}, {0xB8}, {0x9D},
				},
			},
			{
				name:   "unknown_keysym",
				events: []keyEvent{{0x0100FFFF, true}, {0x0100FFFF, false}},
				want:   [][]byte{nil, nil},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				k := &Keyboard{}
				if tc.want != nil {
					for i, ev := range tc.events {
						got := k.Keysym(ev.keysym, ev.down)
						if !bytes.Equal(got, tc.want[i]) {
							t.Errorf("Keysym(%#x, %v) = % x, want % x", ev.keysym, ev.down, got, tc.want[i])
						}
					}
					return
				}
				var u usKeyboard
				for _, ev := range tc.events {
					u.feed(t, k.Keysym(ev.keysym, ev.down))
				}
				if string(u.typed) != tc.typed {
					t.Errorf("typed %q on a US keyboard, want %q", string(u.typed), tc.typed)
				}
				if held := u.shiftL || u.shiftR; held != tc.shiftHeld {
					t.Errorf("Shift held afterwards = %v, want %v", held, tc.shiftHeld)
				}
			})
		}
	})

	t.Run("CON-01/qemu_key", func(t *testing.T) {
		cases := []struct {
			name     string
			keycode  uint32
			down, up []byte
		}{
			{name: "letter", keycode: 0x1E, down: []byte{0x1E}, up: []byte{0x9E}},
			// noVNC's getRFBkeycode: E0 47 (Home) becomes 0x47|0x80.
			{name: "extended_home", keycode: 0xC7, down: []byte{0xE0, 0x47}, up: []byte{0xE0, 0xC7}},
			// E0 1D (right Control) becomes 0x9D.
			{name: "extended_right_control", keycode: 0x9D, down: []byte{0xE0, 0x1D}, up: []byte{0xE0, 0x9D}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := QEMUKey(tc.keycode, true); !bytes.Equal(got, tc.down) {
					t.Errorf("QEMUKey(%#x, down) = % x, want % x", tc.keycode, got, tc.down)
				}
				if got := QEMUKey(tc.keycode, false); !bytes.Equal(got, tc.up) {
					t.Errorf("QEMUKey(%#x, up) = % x, want % x", tc.keycode, got, tc.up)
				}
			})
		}
	})

	t.Run("CON-01/pointer", func(t *testing.T) {
		const w, h = 1024, 768
		cases := []struct {
			name         string
			prev, mask   uint8
			x, y         int
			wantX, wantY int
			wantMove     bool        // a move must be among the operations
			want         []PointerOp // the non-move operations in order
		}{
			{name: "left_press", prev: 0, mask: 1, x: 10, y: 20, wantX: 10, wantY: 20,
				want: []PointerOp{{Kind: PointerButton, Button: 1, Down: true}}},
			{name: "left_release", prev: 1, mask: 0, x: 10, y: 20, wantX: 10, wantY: 20,
				want: []PointerOp{{Kind: PointerButton, Button: 1, Down: false}}},
			{name: "middle_press", prev: 0, mask: 2, x: 10, y: 20, wantX: 10, wantY: 20,
				want: []PointerOp{{Kind: PointerButton, Button: 3, Down: true}}},
			{name: "middle_release", prev: 2, mask: 0, x: 10, y: 20, wantX: 10, wantY: 20,
				want: []PointerOp{{Kind: PointerButton, Button: 3, Down: false}}},
			{name: "right_press", prev: 0, mask: 4, x: 10, y: 20, wantX: 10, wantY: 20,
				want: []PointerOp{{Kind: PointerButton, Button: 2, Down: true}}},
			{name: "right_release", prev: 4, mask: 0, x: 10, y: 20, wantX: 10, wantY: 20,
				want: []PointerOp{{Kind: PointerButton, Button: 2, Down: false}}},
			{name: "held_button_no_transition", prev: 1, mask: 1, x: 10, y: 20, wantX: 10, wantY: 20,
				want: nil},
			{name: "scroll_up", prev: 0, mask: 8, x: 10, y: 20, wantX: 10, wantY: 20,
				want: []PointerOp{{Kind: PointerScroll, Delta: 120}}},
			{name: "scroll_up_release", prev: 8, mask: 0, x: 10, y: 20, wantX: 10, wantY: 20,
				want: nil},
			{name: "scroll_down", prev: 0, mask: 16, x: 10, y: 20, wantX: 10, wantY: 20,
				want: []PointerOp{{Kind: PointerScroll, Delta: -120}}},
			{name: "clamp_to_screen", prev: 0, mask: 0, x: 5000, y: 900, wantX: w - 1, wantY: h - 1,
				wantMove: true},
			{name: "clamp_with_click", prev: 0, mask: 1, x: 1024, y: 768, wantX: w - 1, wantY: h - 1,
				wantMove: true, want: []PointerOp{{Kind: PointerButton, Button: 1, Down: true}}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ops := Pointer(tc.prev, tc.mask, tc.x, tc.y, w, h)
				var others []PointerOp
				moved := false
				for i, op := range ops {
					if op.Kind != PointerMove {
						others = append(others, PointerOp{Kind: op.Kind, Button: op.Button, Down: op.Down, Delta: op.Delta})
						continue
					}
					moved = true
					if len(others) > 0 {
						t.Errorf("op %d: move after a button or scroll in %+v", i, ops)
					}
					if op.X != tc.wantX || op.Y != tc.wantY {
						t.Errorf("move to %d,%d, want %d,%d", op.X, op.Y, tc.wantX, tc.wantY)
					}
				}
				if tc.wantMove && !moved {
					t.Errorf("no move in %+v", ops)
				}
				if fmt.Sprint(others) != fmt.Sprint(tc.want) {
					t.Errorf("button and scroll operations %+v, want %+v", others, tc.want)
				}
			})
		}
	})
}
