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
//  0. Any modifier bit this overlay cannot express is masked off before
//     any other rule runs. ultraviolet defines five bits past the four
//     below — ModHyper, ModSuper, and the ModCapsLock/ModNumLock/
//     ModScrollLock lock states — and its kitty decoder sets the lock ones
//     and Super straight from the host terminal's own report. Neither this
//     file nor x/vt has an encoding for any of them: x/vt's default branch
//     emits bytes only when Mod == 0, so an unmasked lock bit makes an
//     ordinary letter vanish, and rule 3 would otherwise claim the key for
//     a CSI-u form whose modifier parameter ignores the bit anyway. A key
//     left with no expressible modifier at all, but which the terminal
//     already resolved to text (CapsLock+a is Text "A"; a NumLock keypad
//     digit is its digit), is typed as that text rather than handed to
//     x/vt, which would re-derive it from the BASE codepoint and send a
//     lowercase "a". Everything else falls to rule 1.
//  1. Nothing modified goes through here. x/vt encodes unmodified keys
//     correctly AND mode-sensitively — DECCKM decides between ESC O A and
//     ESC [ A for Up, the application keypad decides the keypad forms — and
//     none of that state is reachable from outside the package.
//  2. Shift+Tab with kitty off is x/vt's, which emits ESC [ Z.
//  3. With kitty on, a modified key takes the CSI-u form: the four legacy
//     keys by their fixed codepoints, printable keys by their rune — but
//     only when something besides Shift is held. The kitty spec only
//     escape-codes Esc, alt+key, ctrl+key, ctrl+alt+key and shift+alt+key;
//     a bare Shift+<printable> is still plain text (rule 8 below) because
//     the terminal already applied the shift to it. Code is documented as
//     the BASE, unshifted codepoint (bubbletea's and ultraviolet's Key.Code:
//     shift+a leaves Code == 'a' and reports the shift in Mod), so the
//     CSI-u codepoint for e.g. Ctrl+Shift+A is 'a' (97), not 'A' — no
//     case-folding needed here.
//  4. A modified arrow / Home / End / F1-F4 takes the xterm CSI 1 ; mod
//     <final> form, and the tilde family CSI n ; mod ~ — regardless of
//     kitty: the protocol keeps the cursor/function-key letter and tilde
//     forms even under the disambiguate flag, it just adds the modifier
//     parameter the same way xterm does.
//  5. Without kitty, a modified legacy key degrades to its plain byte (with
//     an ESC prefix when Alt is held), which is what a terminal that cannot
//     express the modifier does.
//  6. Without kitty, a modified Ctrl+<letter> that x/vt does not already
//     encode degrades to its control byte the same way. x/vt's own SendKey
//     matches an exact struct literal per letter (Code plus Mod == ModCtrl,
//     with Alt already stripped and re-added as an ESC prefix), so it owns
//     plain Ctrl+<letter> and Ctrl+Alt+<letter> — but the extra Shift bit on
//     Ctrl+Shift+<letter> matches none of its cases and the key vanishes
//     entirely. This is exactly the gap rule 6 exists to close; it declines
//     whenever x/vt's own combination (Ctrl, optionally with Alt) already
//     covers the key.
//  7. Without kitty, Shift+Alt+<printable> sends ESC followed by the
//     produced text. x/vt strips the Alt bit and prepends ESC itself, but
//     what is left — Mod == ModShift — matches none of its case literals, so
//     its default branch (which only appends a rune when Mod == 0) emits
//     nothing after the ESC and the letter is lost. The terminal already
//     applied the shift to Text, so only the ESC needs adding back.
//  8. A printable key held with Shift alone (no Alt) is its own text: the
//     terminal already applied the shift, and x/vt would drop it for
//     Mod != 0. Rule 0's masked-to-nothing text keys take this same path,
//     early.
//
// ok == false means "x/vt has this one"; the adapter falls through to
// SendKey.
func encodeKey(k KeyEvent, kitty bool) (string, bool) {
	mod := k.Mod & encodableMods // rule 0
	masked := mod != k.Mod
	k.Mod = mod

	if k.Mod == 0 {
		if masked && k.Text != "" {
			// Rule 0 into rule 8: the key carried only modifiers nothing
			// downstream can express, but it did produce text. x/vt would
			// ignore that text and send the base codepoint.
			return k.Text, true
		}
		return "", false // rule 1
	}
	if !kitty && k.Mod == ModShift && k.Code == KeyTab {
		return "", false // rule 2
	}

	if kitty { // rule 3
		if cp, ok := kittyCodepoint(k.Code); ok {
			return csiU(cp, k.Mod), true
		}
		if isPrintable(k.Code) && k.Mod&^ModShift != 0 {
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

	if !kitty && k.Mod&ModCtrl != 0 && k.Mod&^(ModCtrl|ModAlt) != 0 { // rule 6
		if b, ok := ctrlLetterByte(k.Code); ok {
			if k.Mod&ModAlt != 0 {
				return "\x1b" + string(b), true
			}
			return string(b), true
		}
	}

	if !kitty && k.Mod == ModShift|ModAlt && isPrintable(k.Code) && k.Text != "" { // rule 7
		return "\x1b" + k.Text, true
	}

	if k.Mod == ModShift && k.Text != "" { // rule 8
		return k.Text, true
	}
	return "", false
}

// encodableMods is the modifier set this overlay and x/vt between them can
// actually encode. Rule 0 masks everything else off; see encodeKey.
const encodableMods = ModShift | ModAlt | ModCtrl | ModMeta

// modParam is the xterm/kitty modifier parameter: 1 plus a bitmask of
// shift 1, alt 2, ctrl 4, meta 8.
//
// The xterm forms (rule 4) agree with this exactly. The kitty protocol does
// NOT: in it bit 8 is super and meta is 32, and ultraviolet deliberately
// swaps the two so its own ModMeta matches XTerm's ("Meta and Super are
// swapped in the Kitty protocol, this is to preserve compatibility with
// XTerm modifiers", ultraviolet/key.go:20-26). So rule 3's CSI-u form
// reports uv's ModMeta in bit 8, which a kitty-speaking child reads as
// Super. The encoding is left as is: the alternative is a kitty-specific
// variant mapping ModMeta to 32, and the bit that would make the swap
// visible the other way — uv's ModSuper — is masked off by rule 0 before
// this is ever called, so nothing here can emit a wrong Super.
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

// ctrlLetterByte is the classic Ctrl+<letter> control byte (the letter with
// bits 5 and 6 cleared), the same formula a real terminal uses for
// Ctrl+Shift+C — the case that matters here, since x/vt owns plain
// Ctrl+<letter> itself (see rule 6) and this is only reached for the
// combination it does not.
func ctrlLetterByte(code rune) (byte, bool) {
	if code >= 'a' && code <= 'z' || code >= 'A' && code <= 'Z' {
		return byte(code) & 0x1f, true
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
