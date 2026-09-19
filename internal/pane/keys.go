package pane

import (
	"strconv"
	"unicode"
)

// encodeKey is the overlay: the keys this package encodes itself because
// x/vt's SendKey does not.
//
// x/vt switches on whole key structs. Every case but Ctrl+<letter> and
// Shift+Tab requires Mod == 0, and its default branch emits nothing unless
// Mod == 0 — so a modified special key produces no bytes at all, which is
// worse than a wrong encoding because the keystroke simply vanishes. Its
// source marks the gap in its own comment ("Support Kitty, CSI u, and XTerm
// modifyOtherKeys").
//
// The rules, in the order they are applied:
//
//  1. Nothing modified goes through here. x/vt encodes unmodified keys
//     correctly AND mode-sensitively — DECCKM decides between ESC O A and
//     ESC [ A for Up, the application keypad decides the keypad forms — and
//     none of that state is reachable from outside the package.
//  2. Shift+Tab with kitty off is x/vt's, which emits ESC [ Z.
//  3. With kitty on, a modified key takes the CSI-u form: the four legacy
//     keys by their fixed codepoints, printable keys by their rune.
//  4. Without kitty, a modified arrow / Home / End / F1-F4 takes the xterm
//     CSI 1 ; mod <final> form, and the tilde family CSI n ; mod ~.
//  5. Without kitty, a modified legacy key degrades to its plain byte (with
//     an ESC prefix when Alt is held), which is what a terminal that cannot
//     express the modifier does.
//  6. A printable key held with Shift alone is its own text: the terminal
//     already applied the shift, and x/vt would drop it for Mod != 0.
//
// ok == false means "x/vt has this one"; the adapter falls through to
// SendKey.
func encodeKey(k KeyEvent, kitty bool) (string, bool) {
	if k.Mod == 0 {
		return "", false // rule 1
	}
	if !kitty && k.Mod == ModShift && k.Code == KeyTab {
		return "", false // rule 2
	}

	if kitty { // rule 3
		if cp, ok := kittyCodepoint(k.Code); ok {
			return csiU(cp, k.Mod), true
		}
		if isPrintable(k.Code) {
			return csiU(int(k.Code), k.Mod), true
		}
	}

	if final, ok := xtermFinal[k.Code]; ok { // rule 4
		return "\x1b[1;" + strconv.Itoa(modParam(k.Mod)) + string(final), true
	}
	if n, ok := xtermTilde[k.Code]; ok {
		return "\x1b[" + strconv.Itoa(n) + ";" + strconv.Itoa(modParam(k.Mod)) + "~", true
	}

	if b, ok := legacyByte[k.Code]; ok { // rule 5
		if k.Mod&ModAlt != 0 {
			return "\x1b" + b, true
		}
		return b, true
	}

	if k.Mod == ModShift && k.Text != "" { // rule 6
		return k.Text, true
	}
	return "", false
}

// modParam is the xterm/kitty modifier parameter: 1 plus a bitmask of
// shift 1, alt 2, ctrl 4, meta 8. Both encodings agree on these four bits.
func modParam(mod KeyMod) int {
	n := 1
	if mod&ModShift != 0 {
		n += 1
	}
	if mod&ModAlt != 0 {
		n += 2
	}
	if mod&ModCtrl != 0 {
		n += 4
	}
	if mod&ModMeta != 0 {
		n += 8
	}
	return n
}

func csiU(code int, mod KeyMod) string {
	return "\x1b[" + strconv.Itoa(code) + ";" + strconv.Itoa(modParam(mod)) + "u"
}

// isPrintable reports whether a key code is an ordinary character rather than
// one of the special keys. ultraviolet puts every special key above
// unicode.MaxRune, so the test is exact rather than a guess; C0 and DEL are
// excluded because the legacy table owns those.
func isPrintable(code rune) bool {
	return code >= 0x20 && code != 0x7f && code <= unicode.MaxRune
}

// kittyCodepoint is the stable codepoint the kitty protocol reports for the
// four keys whose classic encoding is a bare control byte. They are the only
// keys whose modified form is otherwise unrepresentable, which is why
// Shift+Enter is the probe the design's evidence table records.
func kittyCodepoint(code rune) (int, bool) {
	switch code {
	case KeyEnter:
		return 13, true
	case KeyTab:
		return 9, true
	case KeyBackspace:
		return 127, true
	case KeyEscape:
		return 27, true
	}
	return 0, false
}

// legacyByte is what each of those four keys degrades to when the child has
// no way to express the modifier. This is what a real terminal does; x/vt
// sends nothing at all.
var legacyByte = map[rune]string{
	KeyEnter:     "\r",
	KeyTab:       "\t",
	KeyBackspace: "\x7f",
	KeyEscape:    "\x1b",
}

// xtermFinal is the CSI 1 ; mod <final> family: the arrows, Home, End, and
// F1-F4 — which migrate out of their SS3 forms (ESC O P) into the CSI form
// the moment a modifier is present, exactly as xterm does.
var xtermFinal = map[rune]byte{
	KeyUp:    'A',
	KeyDown:  'B',
	KeyRight: 'C',
	KeyLeft:  'D',
	KeyHome:  'H',
	KeyEnd:   'F',
	KeyF1:    'P',
	KeyF2:    'Q',
	KeyF3:    'R',
	KeyF4:    'S',
}

// xtermTilde is the CSI n ; mod ~ family: Insert, Delete, the page keys, and
// F5-F12.
var xtermTilde = map[rune]int{
	KeyInsert: 2,
	KeyDelete: 3,
	KeyPgUp:   5,
	KeyPgDown: 6,
	KeyF5:     15,
	KeyF6:     17,
	KeyF7:     18,
	KeyF8:     19,
	KeyF9:     20,
	KeyF10:    21,
	KeyF11:    23,
	KeyF12:    24,
}
