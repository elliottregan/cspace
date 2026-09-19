---
title: the pane key overlay drops Ctrl+Shift+<punctuation> in legacy mode
date: 2026-09-18
kind: finding
status: open
category: bug
tags: control-plane, pane, keys, terminal
---

## Summary
With the kitty keyboard protocol off, `Ctrl+Shift+` one of `@ [ \ ] ^ _` or
space produces **zero bytes**: the overlay's rule 6 declines it because
`ctrlLetterByte` only recognizes letters, and x/vt then declines it too
because its switch owns those six only for Ctrl and Ctrl+Alt, never with
Shift also held. The keystroke vanishes, which is the exact failure class
`internal/pane/keys.go` exists to prevent.

## Details
`encodeKey`'s rule 6 (`internal/pane/keys.go:120-127`) is the only path that
can encode a `Ctrl+Shift+<printable>` when the child has not enabled kitty.
It delegates the byte to `ctrlLetterByte` (`keys.go:203-213`), whose guard
is letters only:

```go
if code >= 'a' && code <= 'z' || code >= 'A' && code <= 'Z' {
	return byte(code) & 0x1f, true
}
```

For any other printable code the rule declines, and nothing downstream picks
the key up: rule 7 requires `Mod == ModShift|ModAlt`, rule 8 requires
`Mod == ModShift`, and both fail with Ctrl held. `encodeKey` returns
`("", false)`, so `vtEmulator.SendKey` (`internal/pane/vt.go`) falls through
to x/vt — whose `SendKey` matches whole key structs, owns these six
codepoints for `ModCtrl` and `ModCtrl|ModAlt` only (x/vt `key.go:103-113`),
and whose default branch emits bytes solely when `Mod == 0`
(x/vt `key.go:292-296`). Nothing is written to the child.

A real terminal sends the same control byte it sends without the Shift:
`Ctrl+@`/`Ctrl+Space` → `0x00`, `Ctrl+[` → `0x1b`, `Ctrl+\` → `0x1c`,
`Ctrl+]` → `0x1d`, `Ctrl+^` → `0x1e`, `Ctrl+_` → `0x1f`. The Shift is how
several of those characters are typed in the first place on a US layout
(`^` is Shift+6, `_` is Shift+-), so the combination is not exotic: an
operator reaching for `Ctrl+^` is holding Shift by necessity.

Scope: legacy mode only. With kitty on, rule 3 claims every modified
printable key and emits the CSI-u form, which carries the Shift bit
explicitly — and a Claude pane always has kitty on, so the common case is
unaffected. The exposure is a plain shell pane under a child that did not
enable the protocol.

Related but distinct: the same vanish class caused by an unmaskable
modifier bit (CapsLock, NumLock, Super) was fixed in
`control-plane-4a-pane-engine` by rule 0's mask. This one has a different
cause — `ctrlLetterByte`'s guard — and was left open deliberately.

Fix: widen `ctrlLetterByte` to the classic set. The formula `code & 0x1f`
already produces the right byte for `@ [ \ ] ^ _` (0x40-0x5f), so the change
is the guard plus a space case (`KeySpace`/`' '` → `0x00`), and a row per
key in `TestEncodeKey`.

## Updates
### 2026-09-18 — status: open
Filed from the `control-plane-4a-pane-engine` final review, which adjudicated
it as a finding rather than a fix: it is adjacent to Important 2 (the
modifier mask) but has an independent cause, and the review's own probe of
the Ctrl-only and Ctrl+Alt forms confirmed x/vt owns those six and only
those.
