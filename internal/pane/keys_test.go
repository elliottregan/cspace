package pane

import "testing"

// Every row of the overlay's tables, and every rule that decides between
// them. The cases are written as the bytes a real terminal sends, so a
// mismatch reads as "this is not what Ghostty would have sent".
func TestEncodeKey(t *testing.T) {
	cases := []struct {
		name  string
		key   KeyEvent
		kitty bool
		want  string
		ours  bool
	}{
		// x/vt already gets every unmodified key right, including the ones
		// whose encoding depends on the child's own mode (DECCKM arrows,
		// application keypad), which is precisely why the overlay must not
		// take them.
		{"plain rune", KeyEvent{Code: 'z', Text: "z"}, false, "", false},
		{"plain enter", KeyEvent{Code: KeyEnter}, false, "", false},
		{"plain up", KeyEvent{Code: KeyUp}, false, "", false},
		{"plain f1", KeyEvent{Code: KeyF1}, false, "", false},
		{"plain delete", KeyEvent{Code: KeyDelete}, false, "", false},
		{"ctrl+c", KeyEvent{Code: 'c', Mod: ModCtrl}, false, "", false},
		{"alt+x", KeyEvent{Code: 'x', Mod: ModAlt, Text: "x"}, false, "", false},
		{"shift+tab", KeyEvent{Code: KeyTab, Mod: ModShift}, false, "", false},

		// The xterm modifier forms: CSI 1 ; mod <final> for the arrows, Home,
		// End and F1-F4, CSI n ; mod ~ for the tilde family. The modifier
		// parameter is 1 + shift(1) + alt(2) + ctrl(4) + meta(8).
		{"ctrl+right", KeyEvent{Code: KeyRight, Mod: ModCtrl}, false, "\x1b[1;5C", true},
		{"shift+up", KeyEvent{Code: KeyUp, Mod: ModShift}, false, "\x1b[1;2A", true},
		{"alt+down", KeyEvent{Code: KeyDown, Mod: ModAlt}, false, "\x1b[1;3B", true},
		{"ctrl+shift+left", KeyEvent{Code: KeyLeft, Mod: ModCtrl | ModShift}, false, "\x1b[1;6D", true},
		{"ctrl+home", KeyEvent{Code: KeyHome, Mod: ModCtrl}, false, "\x1b[1;5H", true},
		{"ctrl+end", KeyEvent{Code: KeyEnd, Mod: ModCtrl}, false, "\x1b[1;5F", true},
		{"shift+f1", KeyEvent{Code: KeyF1, Mod: ModShift}, false, "\x1b[1;2P", true},
		{"shift+f4", KeyEvent{Code: KeyF4, Mod: ModShift}, false, "\x1b[1;2S", true},
		{"shift+insert", KeyEvent{Code: KeyInsert, Mod: ModShift}, false, "\x1b[2;2~", true},
		{"ctrl+delete", KeyEvent{Code: KeyDelete, Mod: ModCtrl}, false, "\x1b[3;5~", true},
		{"shift+pgup", KeyEvent{Code: KeyPgUp, Mod: ModShift}, false, "\x1b[5;2~", true},
		{"shift+pgdown", KeyEvent{Code: KeyPgDown, Mod: ModShift}, false, "\x1b[6;2~", true},
		{"ctrl+f5", KeyEvent{Code: KeyF5, Mod: ModCtrl}, false, "\x1b[15;5~", true},
		{"ctrl+f12", KeyEvent{Code: KeyF12, Mod: ModCtrl}, false, "\x1b[24;5~", true},
		{"meta+up", KeyEvent{Code: KeyUp, Mod: ModMeta}, false, "\x1b[1;9A", true},

		// Kitty on: the four legacy keys keep their codepoints (13, 9, 127,
		// 27) and take the CSI-u form, which is the only way Shift+Enter can
		// be told from Enter. This is the probe the design's evidence table
		// names.
		{"kitty shift+enter", KeyEvent{Code: KeyEnter, Mod: ModShift}, true, "\x1b[13;2u", true},
		{"kitty shift+tab", KeyEvent{Code: KeyTab, Mod: ModShift}, true, "\x1b[9;2u", true},
		{"kitty ctrl+backspace", KeyEvent{Code: KeyBackspace, Mod: ModCtrl}, true, "\x1b[127;5u", true},
		{"kitty alt+escape", KeyEvent{Code: KeyEscape, Mod: ModAlt}, true, "\x1b[27;3u", true},
		{"kitty ctrl+shift+c", KeyEvent{Code: 'c', Mod: ModCtrl | ModShift, Text: "c"}, true, "\x1b[99;6u", true},
		// Ctrl+C with kitty ON is the overlay's, not x/vt's: rule 3 claims
		// every modified printable key, so the interrupt takes the CSI-u
		// form the child itself asked for. Pinned rather than incidental,
		// because a Claude pane always has kitty on and this is the key the
		// live verification presses.
		{"kitty ctrl+c", KeyEvent{Code: 'c', Mod: ModCtrl, Text: "c"}, true, "\x1b[99;5u", true},
		// bubbletea's Key.Code (and ultraviolet's, which it aliases) is
		// documented as the BASE, unshifted codepoint: pressing shift+a
		// leaves Code == 'a' and reports the shift in Mod, not in Code. So
		// the CSI-u codepoint for a Ctrl+Shift+letter combo is the lowercase
		// rune, not the shifted one — no case-folding needed here.
		{"kitty ctrl+shift+a uses the base codepoint", KeyEvent{Code: 'a', Mod: ModCtrl | ModShift}, true, "\x1b[97;6u", true},
		// Shift+Alt+<printable> is escape-coded under kitty like any other
		// key with something besides Shift held — rule 3 already covers it,
		// this just pins the modifier arithmetic (1 + shift(1) + alt(2) = 4).
		{"kitty shift+alt+a is CSI-u", KeyEvent{Code: 'a', Mod: ModShift | ModAlt, Text: "A"}, true, "\x1b[97;4u", true},
		// Kitty still leaves the unmodified keys alone: a real terminal only
		// switches to CSI-u once there is something to disambiguate.
		{"kitty plain enter", KeyEvent{Code: KeyEnter}, true, "", false},

		// Kitty off: a modified legacy key degrades to its plain byte, which
		// is what a terminal without CSI-u does. x/vt would emit nothing.
		{"shift+enter degrades", KeyEvent{Code: KeyEnter, Mod: ModShift}, false, "\r", true},
		{"shift+backspace degrades", KeyEvent{Code: KeyBackspace, Mod: ModShift}, false, "\x7f", true},
		{"alt+enter keeps its esc", KeyEvent{Code: KeyEnter, Mod: ModAlt}, false, "\x1b\r", true},
		{"ctrl+shift+enter degrades", KeyEvent{Code: KeyEnter, Mod: ModCtrl | ModShift}, false, "\r", true},

		// x/vt strips Alt, prepends ESC, and is then left matching Mod ==
		// ModShift against case literals that all require Mod == 0 or
		// ModCtrl — none match, so its default branch appends nothing and
		// the child receives a bare ESC with the letter lost. A real
		// terminal sends ESC followed by the text the shift already
		// produced, so the overlay owns exactly that.
		{"shift+alt+a keeps its letter", KeyEvent{Code: 'a', Mod: ModShift | ModAlt, Text: "A"}, false, "\x1bA", true},
		{"shift+alt+1 keeps its symbol", KeyEvent{Code: '1', Mod: ModShift | ModAlt, Text: "!"}, false, "\x1b!", true},

		// x/vt's own SendKey matches an exact struct literal per letter
		// (Code + Mod == ModCtrl, Alt already stripped) — so it owns plain
		// Ctrl+<letter> and Ctrl+Alt+<letter>, but the extra Shift bit on
		// Ctrl+Shift+<letter> matches none of its cases and the key vanishes.
		// The overlay owns exactly that gap: the same control byte
		// (letter & 0x1f) a real terminal sends for Ctrl+Shift+C.
		{"ctrl+shift+c degrades to its control byte", KeyEvent{Code: 'c', Mod: ModCtrl | ModShift}, false, "\x03", true},
		// Plain Ctrl+<letter> is x/vt's alone: the overlay must decline it
		// rather than double-encode it. TestVTEmulatorSendsAModifiedKeyThroughTheOverlay
		// proves x/vt still delivers \x01 for this one end to end.
		{"ctrl+a is x/vt's alone", KeyEvent{Code: 'a', Mod: ModCtrl}, false, "", false},

		// A printable key with only Shift held is its own text. x/vt's
		// default branch drops it because Mod != 0; the terminal that
		// produced it already applied the shift.
		{"shift+a", KeyEvent{Code: 'A', Mod: ModShift, Text: "A"}, false, "A", true},
		// ...but with no text there is nothing to send, so leave it to x/vt
		// rather than inventing bytes.
		{"shift with no text", KeyEvent{Code: 'a', Mod: ModShift}, false, "", false},

		// Shift alone never escapes a text-producing key, kitty or not: the
		// kitty spec only escape-codes Esc/alt+key/ctrl+key/ctrl+alt+key/
		// shift+alt+key, so a bare Shift+letter or Shift+digit is still its
		// own plain text — rule 3's printable branch must not claim it just
		// because Mod != 0.
		{"kitty shift+a is text, not CSI-u", KeyEvent{Code: 'a', Mod: ModShift, Text: "A"}, true, "A", true},
		{"kitty shift+1 is text, not CSI-u", KeyEvent{Code: '1', Mod: ModShift, Text: "!"}, true, "!", true},
		{"shift+a is text (kitty off)", KeyEvent{Code: 'a', Mod: ModShift, Text: "A"}, false, "A", true},
		{"shift+1 is text (kitty off)", KeyEvent{Code: '1', Mod: ModShift, Text: "!"}, false, "!", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ours := encodeKey(tc.key, tc.kitty)
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
	e, r := newTestEmulator(t, func(cols, rows int) Emulator { return newVTEmulator(cols, rows) }, 20, 4)

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
