# Linting, Formatting, and Git Hooks — Design

**Date:** 2026-04-13
**Status:** Approved for implementation planning

## Problem

The repo runs `go vet` and `go test` in CI but has no lint/format enforcement. Drift accumulates: inconsistent imports, dead code, subtle issues the default `go vet` doesn't catch. Shell scripts under `lib/scripts/` and `lib/hooks/` drive container networking and git operations and currently ship with no lint pass. Developers have no local pre-commit gate, so every push is the first time CI sees the change.

## Solution

Add Go formatting, Go linting, and shell linting with a shared task runner (Make), a local git-hook gate (lefthook), and matching CI jobs so the same rules block both local pushes and PRs.

## Scope

Go code in `cmd/` and `internal/`, plus shell scripts in `lib/scripts/` and `lib/hooks/`. Explicitly out of scope: the Node.js agent supervisor in `lib/agent-supervisor/` (separate ecosystem, single self-contained file for now) and generated content under `internal/assets/embedded/`.

## Tooling

- **`gofmt -s`** + **`goimports`** — Go formatting. `gofmt` for canonical form; `goimports` groups and trims imports.
- **`golangci-lint`** — Meta-linter. Default preset (`errcheck`, `gosimple`, `govet`, `ineffassign`, `staticcheck`, `unused`) plus `gofmt`, `goimports`, `misspell`.
- **`shellcheck`** — Default config, no overrides.
- **`lefthook`** — Git hook runner. Single `lefthook.yml`, parallel execution, per-file globs.

Rationale for Make over Just: existing repo uses Make; adding lint/fmt is a small extension. Migrating the task runner is a separate concern and out of scope.

## Configs

### `.golangci.yml`

```yaml
run:
  timeout: 3m
linters:
  enable:
    - gofmt
    - goimports
    - misspell
issues:
  exclude-dirs:
    - internal/assets/embedded
```

Default linter set stays on. The three additions are cheap, well-liked, and low-noise on existing Go code.

### `lefthook.yml`

```yaml
pre-commit:
  parallel: true
  commands:
    fmt-go:
      glob: "*.go"
      run: gofmt -w {staged_files} && goimports -w {staged_files}
      stage_fixed: true
    lint-go:
      glob: "*.go"
      run: golangci-lint run --new-from-rev=HEAD~1 ./...
    lint-shell:
      glob: "*.sh"
      run: shellcheck {staged_files}

pre-push:
  parallel: false
  commands:
    check:
      run: make check
```

`stage_fixed: true` re-stages formatted files so the commit includes the fix. `--new-from-rev=HEAD~1` on pre-commit scopes golangci-lint to changed lines; full-project lint runs in `make check` at pre-push and in CI.

Pre-push serializes so output stays readable.

## Makefile additions

```makefile
.PHONY: fmt fmt-check lint check install-tools setup-hooks

fmt:
	gofmt -s -w .
	goimports -w $$(go list -f '{{.Dir}}' ./...)

fmt-check:
	@unformatted=$$(gofmt -s -l . | grep -v '^internal/assets/embedded/' || true); \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted files:"; echo "$$unformatted"; exit 1; \
	fi

lint: sync-embedded
	golangci-lint run ./...
	shellcheck lib/scripts/*.sh lib/hooks/*.sh

check: fmt-check vet lint test

install-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/evilmartians/lefthook@latest
	@echo "Install shellcheck via your package manager (brew install shellcheck / apk add shellcheck)."

setup-hooks:
	lefthook install
```

`check` is the pre-push and CI target.

## `tools.go`

Under a `tools` build tag so the deps are trackable via `go.mod` without bloating the main build:

```go
//go:build tools

package tools

import (
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "github.com/evilmartians/lefthook"
	_ "golang.org/x/tools/cmd/goimports"
)
```

This keeps tool versions pinned via `go.mod` and surfaces them in `go mod graph`.

## CI changes

Add a parallel `lint` job to `.github/workflows/ci.yml`:

```yaml
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest
      - name: Shellcheck
        run: |
          sudo apt-get update && sudo apt-get install -y shellcheck
          shellcheck lib/scripts/*.sh lib/hooks/*.sh
```

Existing `check` job keeps running `make vet && make test` unchanged. New `lint` runs alongside it.

## Developer onboarding

- One-time: `make install-tools && make setup-hooks`.
- Add a brief section to the install docs pointing at these targets.

## Baseline cleanup

First `make lint` on the existing codebase will surface issues. Fix them in a dedicated commit before turning hooks on, so contributors don't inherit a broken baseline.

## Testing

Dev-tooling; correctness is observable in use.

- Manual smoke: introduce trailing whitespace, commit — hook auto-formats and stages. Introduce an unused import — commit fails. Remove, commit passes. Push a branch with a failing test — pre-push blocks.
- CI parity: the new `lint` job runs `make lint` end-to-end. If local `make check` is green, CI will be.

## Files

**New:**
- `.golangci.yml`
- `lefthook.yml`
- `tools.go`

**Modified:**
- `Makefile` — add `fmt`, `fmt-check`, `lint`, `check`, `install-tools`, `setup-hooks`
- `.github/workflows/ci.yml` — add `lint` job
- Existing Go source — one-time baseline cleanup commit
- Install docs — onboarding one-liner

## Out of scope

- Node.js linting/formatting for `lib/agent-supervisor/`.
- Migration from Make to Just.
- Stricter linter presets (`revive`, `gocritic`, etc.) — can enable incrementally later.
- Pre-commit framework or husky alternatives.
