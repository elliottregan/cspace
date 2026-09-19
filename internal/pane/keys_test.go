package pane

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// The five modifier bits ultraviolet defines past the four this package
// encodes. They live in the test rather than in keys.go on purpose: rule 0
// masks by the positive set, so nothing in the package needs to name them,
// and exporting them would invite a caller to think they mean something
// here. TestInexpressibleModBitsMatchUltraviolet keeps the numbering honest.
const (
	modHyper KeyMod = 1 << (4 + iota)
	modSuper
	modCapsLock
	modNumLock
	modScrollLock
)

// TestInexpressibleModBitsMatchUltraviolet pins the constants above to
// ultraviolet's own, the same way KeyMod's four exported bits are pinned by
// being ultraviolet's values bit for bit. A drift here would make every
// rule-0 case below test a bit the host terminal never sets.
func TestInexpressibleModBitsMatchUltraviolet(t *testing.T) {
	cases := []struct {
		name string
		ours KeyMod
		uv   uv.KeyMod
	}{
		{"hyper", modHyper, uv.ModHyper},
		{"super", modSuper, uv.ModSuper},
		{"capslock", modCapsLock, uv.ModCapsLock},
		{"numlock", modNumLock, uv.ModNumLock},
		{"scrolllock", modScrollLock, uv.ModScrollLock},
	}
	for _, tc := range cases {
		if KeyMod(tc.uv) != tc.ours {
			t.Errorf("%s: test constant %d, ultraviolet %d", tc.name, tc.ours, tc.uv)
		}
		if tc.ours&encodableMods != 0 {
			t.Errorf("%s (%d) overlaps encodableMods, which rule 0 would then keep", tc.name, tc.ours)
		}
	}
}

// Every row of the overlay's tables, and every rule that decides between
// them. The cases are written as the bytes a real terminal sends, so a
// mismatch reads as "this is not what Ghostty would have sent".
func TestEncodeKey(t *testing.T) {
	cases := []struct {
		name string
		key  KeyEvent
		enc  keyEncoding
		want string
		ours bool
	}{
		// x/vt already gets every unmodified key right, including the ones
		// whose encoding depends on the child's own mode (DECCKM arrows,
		// application keypad), which is precisely why the overlay must not
		// take them.
		{"plain rune", KeyEvent{Code: 'z', Text: "z"}, encLegacy, "", false},
		{"plain enter", KeyEvent{Code: KeyEnter}, encLegacy, "", false},
		{"plain up", KeyEvent{Code: KeyUp}, encLegacy, "", false},
		{"plain f1", KeyEvent{Code: KeyF1}, encLegacy, "", false},
		{"plain delete", KeyEvent{Code: KeyDelete}, encLegacy, "", false},
		{"ctrl+c", KeyEvent{Code: 'c', Mod: ModCtrl}, encLegacy, "", false},
		{"alt+x", KeyEvent{Code: 'x', Mod: ModAlt, Text: "x"}, encLegacy, "", false},
		{"shift+tab", KeyEvent{Code: KeyTab, Mod: ModShift}, encLegacy, "", false},

		// The xterm modifier forms: CSI 1 ; mod <final> for the arrows, Home,
		// End and F1-F4, CSI n ; mod ~ for the tilde family. The modifier
		// parameter is 1 + shift(1) + alt(2) + ctrl(4) + meta(8).
		{"ctrl+right", KeyEvent{Code: KeyRight, Mod: ModCtrl}, encLegacy, "\x1b[1;5C", true},
		{"shift+up", KeyEvent{Code: KeyUp, Mod: ModShift}, encLegacy, "\x1b[1;2A", true},
		{"alt+down", KeyEvent{Code: KeyDown, Mod: ModAlt}, encLegacy, "\x1b[1;3B", true},
		{"ctrl+shift+left", KeyEvent{Code: KeyLeft, Mod: ModCtrl | ModShift}, encLegacy, "\x1b[1;6D", true},
		{"ctrl+home", KeyEvent{Code: KeyHome, Mod: ModCtrl}, encLegacy, "\x1b[1;5H", true},
		{"ctrl+end", KeyEvent{Code: KeyEnd, Mod: ModCtrl}, encLegacy, "\x1b[1;5F", true},
		{"shift+f1", KeyEvent{Code: KeyF1, Mod: ModShift}, encLegacy, "\x1b[1;2P", true},
		{"shift+f4", KeyEvent{Code: KeyF4, Mod: ModShift}, encLegacy, "\x1b[1;2S", true},
		{"shift+insert", KeyEvent{Code: KeyInsert, Mod: ModShift}, encLegacy, "\x1b[2;2~", true},
		{"ctrl+delete", KeyEvent{Code: KeyDelete, Mod: ModCtrl}, encLegacy, "\x1b[3;5~", true},
		{"shift+pgup", KeyEvent{Code: KeyPgUp, Mod: ModShift}, encLegacy, "\x1b[5;2~", true},
		{"shift+pgdown", KeyEvent{Code: KeyPgDown, Mod: ModShift}, encLegacy, "\x1b[6;2~", true},
		{"ctrl+f5", KeyEvent{Code: KeyF5, Mod: ModCtrl}, encLegacy, "\x1b[15;5~", true},
		{"ctrl+f12", KeyEvent{Code: KeyF12, Mod: ModCtrl}, encLegacy, "\x1b[24;5~", true},
		{"meta+up", KeyEvent{Code: KeyUp, Mod: ModMeta}, encLegacy, "\x1b[1;9A", true},

		// Kitty on: the four legacy keys keep their codepoints (13, 9, 127,
		// 27) and take the CSI-u form, which is the only way Shift+Enter can
		// be told from Enter. This is the probe the design's evidence table
		// names.
		{"kitty shift+enter", KeyEvent{Code: KeyEnter, Mod: ModShift}, encKitty, "\x1b[13;2u", true},
		{"kitty shift+tab", KeyEvent{Code: KeyTab, Mod: ModShift}, encKitty, "\x1b[9;2u", true},
		{"kitty ctrl+backspace", KeyEvent{Code: KeyBackspace, Mod: ModCtrl}, encKitty, "\x1b[127;5u", true},
		{"kitty alt+escape", KeyEvent{Code: KeyEscape, Mod: ModAlt}, encKitty, "\x1b[27;3u", true},
		{"kitty ctrl+shift+c", KeyEvent{Code: 'c', Mod: ModCtrl | ModShift, Text: "c"}, encKitty, "\x1b[99;6u", true},
		// Ctrl+C with kitty ON is the overlay's, not x/vt's: rule 3 claims
		// every modified printable key, so the interrupt takes the CSI-u
		// form the child itself asked for. Pinned rather than incidental,
		// because a Claude pane always has kitty on and this is the key the
		// live verification presses.
		{"kitty ctrl+c", KeyEvent{Code: 'c', Mod: ModCtrl, Text: "c"}, encKitty, "\x1b[99;5u", true},
		// bubbletea's Key.Code (and ultraviolet's, which it aliases) is
		// documented as the BASE, unshifted codepoint: pressing shift+a
		// leaves Code == 'a' and reports the shift in Mod, not in Code. So
		// the CSI-u codepoint for a Ctrl+Shift+letter combo is the lowercase
		// rune, not the shifted one — no case-folding needed here.
		{"kitty ctrl+shift+a uses the base codepoint", KeyEvent{Code: 'a', Mod: ModCtrl | ModShift}, encKitty, "\x1b[97;6u", true},
		// Shift+Alt+<printable> is escape-coded under kitty like any other
		// key with something besides Shift held — rule 3 already covers it,
		// this just pins the modifier arithmetic (1 + shift(1) + alt(2) = 4).
		{"kitty shift+alt+a is CSI-u", KeyEvent{Code: 'a', Mod: ModShift | ModAlt, Text: "A"}, encKitty, "\x1b[97;4u", true},
		// Kitty still leaves the unmodified keys alone: a real terminal only
		// switches to CSI-u once there is something to disambiguate.
		{"kitty plain enter", KeyEvent{Code: KeyEnter}, encKitty, "", false},

		// Kitty off: a modified legacy key degrades to its plain byte, which
		// is what a terminal without CSI-u does. x/vt would emit nothing.
		{"shift+enter degrades", KeyEvent{Code: KeyEnter, Mod: ModShift}, encLegacy, "\r", true},
		{"shift+backspace degrades", KeyEvent{Code: KeyBackspace, Mod: ModShift}, encLegacy, "\x7f", true},
		{"alt+enter keeps its esc", KeyEvent{Code: KeyEnter, Mod: ModAlt}, encLegacy, "\x1b\r", true},
		{"ctrl+shift+enter degrades", KeyEvent{Code: KeyEnter, Mod: ModCtrl | ModShift}, encLegacy, "\r", true},

		// x/vt strips Alt, prepends ESC, and is then left matching Mod ==
		// ModShift against case literals that all require Mod == 0 or
		// ModCtrl — none match, so its default branch appends nothing and
		// the child receives a bare ESC with the letter lost. A real
		// terminal sends ESC followed by the text the shift already
		// produced, so the overlay owns exactly that.
		{"shift+alt+a keeps its letter", KeyEvent{Code: 'a', Mod: ModShift | ModAlt, Text: "A"}, encLegacy, "\x1bA", true},
		{"shift+alt+1 keeps its symbol", KeyEvent{Code: '1', Mod: ModShift | ModAlt, Text: "!"}, encLegacy, "\x1b!", true},

		// x/vt's own SendKey matches an exact struct literal per letter
		// (Code + Mod == ModCtrl, Alt already stripped) — so it owns plain
		// Ctrl+<letter> and Ctrl+Alt+<letter>, but the extra Shift bit on
		// Ctrl+Shift+<letter> matches none of its cases and the key vanishes.
		// The overlay owns exactly that gap: the same control byte
		// (letter & 0x1f) a real terminal sends for Ctrl+Shift+C.
		{"ctrl+shift+c degrades to its control byte", KeyEvent{Code: 'c', Mod: ModCtrl | ModShift}, encLegacy, "\x03", true},
		// Plain Ctrl+<letter> is x/vt's alone: the overlay must decline it
		// rather than double-encode it. TestVTEmulatorSendsAModifiedKeyThroughTheOverlay
		// proves x/vt still delivers \x01 for this one end to end.
		{"ctrl+a is x/vt's alone", KeyEvent{Code: 'a', Mod: ModCtrl}, encLegacy, "", false},

		// A printable key with only Shift held is its own text. x/vt's
		// default branch drops it because Mod != 0; the terminal that
		// produced it already applied the shift.
		{"shift+a", KeyEvent{Code: 'A', Mod: ModShift, Text: "A"}, encLegacy, "A", true},
		// ...but with no text there is nothing to send, so leave it to x/vt
		// rather than inventing bytes.
		{"shift with no text", KeyEvent{Code: 'a', Mod: ModShift}, encLegacy, "", false},

		// Shift alone never escapes a text-producing key, kitty or not: the
		// kitty spec only escape-codes Esc/alt+key/ctrl+key/ctrl+alt+key/
		// shift+alt+key, so a bare Shift+letter or Shift+digit is still its
		// own plain text — rule 3's printable branch must not claim it just
		// because Mod != 0.
		{"kitty shift+a is text, not CSI-u", KeyEvent{Code: 'a', Mod: ModShift, Text: "A"}, encKitty, "A", true},
		{"kitty shift+1 is text, not CSI-u", KeyEvent{Code: '1', Mod: ModShift, Text: "!"}, encKitty, "!", true},
		{"shift+a is text (kitty off)", KeyEvent{Code: 'a', Mod: ModShift, Text: "A"}, encLegacy, "A", true},
		{"shift+1 is text (kitty off)", KeyEvent{Code: '1', Mod: ModShift, Text: "!"}, encLegacy, "!", true},

		// Rule 0. ultraviolet's kitty decoder sets ModCapsLock, ModNumLock
		// and ModSuper straight from the host terminal's own report, and
		// neither this overlay nor x/vt can encode any of them — x/vt's
		// default branch emits bytes only when Mod == 0, so an unmasked
		// lock bit makes an ordinary letter vanish entirely, which is the
		// exact failure this overlay exists to prevent. Masked, a key that
		// the terminal already resolved to text types that text: Code is
		// the BASE codepoint, so handing it to x/vt instead would send a
		// lowercase "a" for a capital.
		{"capslock+a types its capital", KeyEvent{Code: 'a', Mod: modCapsLock, Text: "A"}, encLegacy, "A", true},
		{"kitty capslock+a types its capital", KeyEvent{Code: 'a', Mod: modCapsLock, Text: "A"}, encKitty, "A", true},
		{"numlock+keypad digit types its digit", KeyEvent{Code: uv.KeyKp5, Mod: modNumLock, Text: "5"}, encLegacy, "5", true},
		{"scrolllock+a types its text", KeyEvent{Code: 'a', Mod: modScrollLock, Text: "a"}, encLegacy, "a", true},
		// ...and a masked-away bit alongside a real one leaves the real one
		// alone: this degrades to plain Ctrl+A, which is x/vt's own case.
		// Unmasked, rule 6 would have claimed it (Mod&^(ModCtrl|ModAlt) is
		// non-zero for the super bit) and double-encoded the control byte.
		{"super+ctrl+a is masked back to plain ctrl+a", KeyEvent{Code: 'a', Mod: modSuper | ModCtrl}, encLegacy, "", false},
		{"hyper+ctrl+right is masked back to plain ctrl+right", KeyEvent{Code: KeyRight, Mod: modHyper | ModCtrl}, encLegacy, "\x1b[1;5C", true},
		// A masked-to-nothing key with no text has nothing to type, so it
		// goes to x/vt like any unmodified key — which encodes Enter
		// mode-sensitively, as rule 1 wants.
		{"capslock+enter is x/vt's", KeyEvent{Code: KeyEnter, Mod: modCapsLock}, encLegacy, "", false},

		// encForced — pane.ExtendedKeys, i.e. a Claude pane whose kitty
		// negotiation tmux ate. THIS BLOCK IS THE RULE: the CSI-u form is
		// used only where the legacy bytes would arrive with a modifier
		// missing (keys.go's legacyLosesModifier), and every other key
		// keeps exactly what encLegacy sends it. A row that moves between
		// the two halves below is a key whose meaning changed inside a
		// running Claude session, which is what the first cut of this
		// option did to Shift+Tab.
		//
		// Lossy under legacy, so forced: rule 5's four control-byte keys
		// with a modifier the byte cannot carry...
		{"forced shift+enter", KeyEvent{Code: KeyEnter, Mod: ModShift}, encForced, "\x1b[13;2u", true},
		{"forced ctrl+enter", KeyEvent{Code: KeyEnter, Mod: ModCtrl}, encForced, "\x1b[13;5u", true},
		{"forced ctrl+shift+enter", KeyEvent{Code: KeyEnter, Mod: ModCtrl | ModShift}, encForced, "\x1b[13;6u", true},
		{"forced ctrl+tab", KeyEvent{Code: KeyTab, Mod: ModCtrl}, encForced, "\x1b[9;5u", true},
		{"forced ctrl+backspace", KeyEvent{Code: KeyBackspace, Mod: ModCtrl}, encForced, "\x1b[127;5u", true},
		{"forced shift+escape", KeyEvent{Code: KeyEscape, Mod: ModShift}, encForced, "\x1b[27;2u", true},
		// ...and rule 6's Ctrl+<letter> with a Shift the control byte drops.
		{"forced ctrl+shift+c", KeyEvent{Code: 'c', Mod: ModCtrl | ModShift}, encForced, "\x1b[99;6u", true},
		{"forced meta+ctrl+a", KeyEvent{Code: 'a', Mod: ModCtrl | ModMeta}, encForced, "\x1b[97;13u", true},

		// Faithful under legacy, so left alone. Shift+Tab is the one this
		// list exists for: ESC[Z is a distinct sequence, and in a Claude
		// pane it is the permission-mode cycle.
		{"forced shift+tab stays x/vt's ESC[Z", KeyEvent{Code: KeyTab, Mod: ModShift}, encForced, "", false},
		// Alt is carried by rule 5's ESC prefix, which is what a terminal
		// has always sent and what Claude Code decodes for Option+Enter.
		{"forced alt+enter keeps its esc prefix", KeyEvent{Code: KeyEnter, Mod: ModAlt}, encForced, "\x1b\r", true},
		{"forced alt+backspace keeps its esc prefix", KeyEvent{Code: KeyBackspace, Mod: ModAlt}, encForced, "\x1b\x7f", true},
		{"forced alt+escape keeps its esc prefix", KeyEvent{Code: KeyEscape, Mod: ModAlt}, encForced, "\x1b\x1b", true},
		// x/vt's own cases: plain Ctrl+<letter>, Ctrl+Alt+<letter>, and
		// Alt+<printable> all reach the child with their modifier intact.
		{"forced ctrl+c is x/vt's control byte", KeyEvent{Code: 'c', Mod: ModCtrl}, encForced, "", false},
		{"forced ctrl+alt+a is x/vt's", KeyEvent{Code: 'a', Mod: ModCtrl | ModAlt}, encForced, "", false},
		{"forced alt+x is x/vt's", KeyEvent{Code: 'x', Mod: ModAlt, Text: "x"}, encForced, "", false},
		// Rule 4's xterm forms already carry the modifier as a parameter.
		{"forced ctrl+right keeps the xterm form", KeyEvent{Code: KeyRight, Mod: ModCtrl}, encForced, "\x1b[1;5C", true},
		{"forced shift+f5 keeps the tilde form", KeyEvent{Code: KeyF5, Mod: ModShift}, encForced, "\x1b[15;2~", true},
		{"forced ctrl+delete keeps the tilde form", KeyEvent{Code: KeyDelete, Mod: ModCtrl}, encForced, "\x1b[3;5~", true},
		// Text the terminal already shifted, and rule 7's ESC + text.
		{"forced shift+a is text", KeyEvent{Code: 'a', Mod: ModShift, Text: "A"}, encForced, "A", true},
		{"forced shift+alt+a keeps its letter", KeyEvent{Code: 'a', Mod: ModShift | ModAlt, Text: "A"}, encForced, "\x1bA", true},
		// Rule 1 and rule 0 are untouched by the option: an unmodified key
		// is still x/vt's mode-sensitive encoding, and a masked-to-nothing
		// key is still its own text.
		{"forced plain enter is x/vt's", KeyEvent{Code: KeyEnter}, encForced, "", false},
		{"forced plain up is x/vt's", KeyEvent{Code: KeyUp}, encForced, "", false},
		{"forced capslock+a types its capital", KeyEvent{Code: 'a', Mod: modCapsLock, Text: "A"}, encForced, "A", true},
		{"forced super+ctrl+a is masked back to plain ctrl+a", KeyEvent{Code: 'a', Mod: modSuper | ModCtrl}, encForced, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ours := encodeKey(tc.key, tc.enc)
			if ours != tc.ours {
				t.Fatalf("encodeKey ownership = %v, want %v (got %q)", ours, tc.ours, got)
			}
			if ours && got != tc.want {
				t.Errorf("encodeKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestModParam(t *testing.T) {
	cases := []struct {
		mod  KeyMod
		want int
	}{
		{0, 1},
		{ModShift, 2},
		{ModAlt, 3},
		{ModCtrl, 5},
		{ModCtrl | ModShift, 6},
		{ModMeta, 9},
		{ModShift | ModAlt | ModCtrl | ModMeta, 16},
	}
	for _, tc := range cases {
		if got := modParam(tc.mod); got != tc.want {
			t.Errorf("modParam(%d) = %d, want %d", tc.mod, got, tc.want)
		}
	}
}

// TestVTEmulatorSendsAModifiedKeyThroughTheOverlay is the wiring, not the
// table: the table above proves encodeKey is right, and this proves
// vtEmulator.SendKey actually asks it. Task 1 shipped SendKey as a straight
// hand-off to x/vt, whose switch drops Ctrl+Right entirely — zero bytes, not
// a degraded key — so this case fails until Step 4 replaces that body.
//
// It reuses the drain helpers from emulator_test.go: x/vt answers through an
// unbuffered pipe, so nothing a SendKey pushes is observable without a
// reader already running.
func TestVTEmulatorSendsAModifiedKeyThroughTheOverlay(t *testing.T) {
	e, r := newTestEmulator(t, func(cols, rows int) Emulator { return newVTEmulator(cols, rows, false) }, 20, 4)

	// The overlay owns this one: kitty is off, so it is the xterm modifier
	// form, and x/vt on its own would have emitted nothing.
	e.SendKey(KeyEvent{Code: KeyRight, Mod: ModCtrl})
	r.waitFor(t, "\x1b[1;5C")

	// ...and an unmodified key still falls through to x/vt, which encodes it
	// mode-sensitively. The overlay must not claim it.
	e.SendKey(KeyEvent{Code: KeyEnter})
	r.waitFor(t, "\r")

	// ...and plain Ctrl+A is x/vt's own case (Code + ModCtrl matches its
	// switch literal), so the overlay must decline it too — proving the
	// end-to-end path still delivers the byte x/vt encodes, not just that
	// encodeKey reports "not mine" in isolation.
	e.SendKey(KeyEvent{Code: 'a', Mod: ModCtrl})
	r.waitFor(t, "\x01")
}

// TestVTEmulatorMasksAnInexpressibleModifier is rule 0's wiring test, and
// the only one that can catch a mask applied in encodeKey but not in
// vtEmulator.SendKey: encodeKey works on its own copy of the event, so the
// fall-through to x/vt would still hand over the unmasked Mod. x/vt matches
// whole key structs and its default branch emits bytes only when Mod == 0,
// so an unmasked Super bit riding along with Ctrl means zero bytes reach the
// child.
func TestVTEmulatorMasksAnInexpressibleModifier(t *testing.T) {
	e, r := newTestEmulator(t, func(cols, rows int) Emulator { return newVTEmulator(cols, rows, false) }, 20, 4)

	// Ctrl+A with the Command key also down, as ultraviolet reports it from
	// a kitty-speaking host terminal. x/vt owns plain Ctrl+A, so this only
	// arrives as \x01 if SendKey masked the Super bit before falling
	// through to it.
	e.SendKey(KeyEvent{Code: 'a', Mod: modSuper | ModCtrl})
	r.waitFor(t, "\x01")

	// CapsLock+a: nothing expressible is left after the mask, but the host
	// terminal already resolved the key to "A", so the overlay types that
	// rather than letting x/vt re-derive a lowercase "a" from Code.
	e.SendKey(KeyEvent{Code: 'a', Mod: modCapsLock, Text: "A"})
	r.waitFor(t, "A")
}
