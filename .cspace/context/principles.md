# Principles

- **Package for cohesion, not just for containment.** A new package is justified when (a) the code has a single clear responsibility beyond a one-liner, (b) tests want their own package boundary, or (c) multiple callers in unrelated places will depend on it.
- **Helpers with one caller belong next to that caller.** Don't speculatively create a package for code that's only used in one place.
- **Public API surface should stay minimal.** Prefer unexported helpers until a second caller materializes.
- **File layout follows actual consumers.** If the only caller is in `internal/cli/`, a helper lives in `internal/cli/`. Promote to its own package the moment a second consumer appears.
