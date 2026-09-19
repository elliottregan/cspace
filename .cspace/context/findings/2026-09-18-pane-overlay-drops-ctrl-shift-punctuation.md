---
title: the pane key overlay drops Ctrl+Shift+<punctuation> in legacy mode
date: 2026-09-18
kind: finding
status: resolved
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

Scope: legacy mode (`encLegacy`) — **and**, it turned out, the forced state
too (`encForced`, `pane.ExtendedKeys`). With kitty genuinely negotiated, rule
3 claims every modified printable key and emits the CSI-u form, which
carries the Shift bit explicitly, so that case is unaffected. But "a Claude
pane always has kitty on" was wrong: a Claude pane run directly negotiates
kitty, but a Claude pane sitting behind tmux — which eats the negotiation —
is `encForced`, not `encKitty` (`pane.ExtendedKeys`, `keys.go`'s `encForced`
state). Under the old, letters-only `ctrlLetterByte`, `legacyLosesModifier`'s
rule-6 branch declined every key in this finding's set there too, so a
tmux'd Claude pane got zero bytes from `Ctrl+Shift+^` exactly like a plain
shell pane did. The fix below (widening `ctrlLetterByte`) closes both at
once: legacy degrades to the control byte, forced escalates to CSI-u.

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

### 2026-09-19 — status: resolved
Widened `ctrlLetterByte` (`internal/pane/keys.go`) exactly as proposed
above: it now recognizes `@ [ \ ] ^ _` and space alongside letters, via the
same `code & 0x1f` formula. That alone fixes this finding's whole complaint
— `Ctrl+Shift+<punctuation>` in legacy mode now degrades to its control byte
through rule 6 instead of vanishing, the same way `Ctrl+Shift+C` already
did.

Landed alongside N1 from
`.superpowers/sdd/2026-09-18-control-plane-4b-panes-in-the-dashboard/review-fixwave.md`,
which is why the scope paragraph above changed: N1 caught that this finding
was also live under `encForced` (a Claude pane behind tmux), not just plain
shell panes in `encLegacy`, and that `encForced` needed more than the
widened set — a Ctrl-held printable `ctrlLetterByte` still does not
recognize at all (`Ctrl+/`, `Ctrl+-`) has no legacy byte whatsoever, which
`legacyLosesModifier` was treating as "faithful" rather than "lossy". Fixed
by teaching `legacyLosesModifier` that "sends nothing" is a stronger loss
than "drops a modifier", so `encForced` now escalates that case to CSI-u via
rule 3 instead of leaving it silent. Both halves are covered by new rows in
`TestEncodeKey`: `encForced` rows for `Ctrl+Shift+-` and `Ctrl+/` (CSI-u,
`\x1b[45;6u` / `\x1b[47;5u`) and a plain `Ctrl+_` row pinning that it stays
x/vt's own (no extra bit to degrade), plus an `encLegacy` row for `Ctrl+[`
(x/vt's own byte, `0x1b`, unclaimed by this package either way).

Status resolved: the finding's whole complaint — `Ctrl+Shift+<@ [ \ ] ^ _
space>` vanishing — is addressed in both encodings it turns out to reach.
