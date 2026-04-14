# Lint/Format/Hooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add gofmt/goimports, golangci-lint, and shellcheck gated by lefthook pre-commit and pre-push hooks, with matching Make targets and a CI lint job.

**Architecture:** Three tools wired through a single `lefthook.yml` config, a `.golangci.yml` config, and new Make targets (`fmt`, `fmt-check`, `lint`, `check`, `install-tools`, `setup-hooks`). A new CI `lint` job runs the same rules on every PR. Tooling is pinned via a build-tagged `tools.go`. A baseline-cleanup commit brings existing code into compliance so the hooks go green from day one.

**Tech Stack:** Go 1.25, golangci-lint (latest), goimports, shellcheck, lefthook. GitHub Actions.

**Reference spec:** `docs/superpowers/specs/2026-04-13-lint-format-hooks-design.md`

---

## File Structure

**New files:**
- `.golangci.yml` — linter config with default preset plus `gofmt`, `goimports`, `misspell`.
- `lefthook.yml` — pre-commit (auto-format staged Go, lint staged Go diff, shellcheck staged shell) and pre-push (`make check`).
- `tools.go` — `//go:build tools` — pins linter/hook binary versions via `go.mod`.

**Modified files:**
- `Makefile` — add `fmt`, `fmt-check`, `lint`, `check`, `install-tools`, `setup-hooks` targets; update `.PHONY`.
- `.github/workflows/ci.yml` — add parallel `lint` job.
- Existing Go source files — one-time baseline cleanup so `make lint` is green.
- Install docs (e.g. `docs/src/content/docs/getting-started/installation.md` or equivalent) — add a "setting up developer tooling" one-liner.

---

## Task 1: Baseline — install tools locally and take a first look

**Files:** none (exploration only)

- [ ] **Step 1: Install the tools locally**

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/evilmartians/lefthook@latest
# shellcheck via system package manager
brew install shellcheck   # macOS
# OR: sudo apt-get install -y shellcheck   # Debian/Ubuntu
```

Verify each: `golangci-lint --version`, `goimports -h`, `lefthook version`, `shellcheck --version`. All four must work before continuing.

- [ ] **Step 2: Dry-run golangci-lint with default preset + the three additions**

Run from repo root:

```bash
golangci-lint run --enable=gofmt,goimports,misspell --timeout=3m ./... 2>&1 | tee /tmp/baseline-go.log
```

Capture the output. Do NOT fix anything yet — we're sizing the baseline.

- [ ] **Step 3: Dry-run shellcheck**

```bash
shellcheck lib/scripts/*.sh lib/hooks/*.sh 2>&1 | tee /tmp/baseline-shell.log
```

- [ ] **Step 4: Report**

Report the counts from each log (e.g. "golangci-lint: 12 findings across 4 files; shellcheck: 3 findings in 2 files"). Do NOT commit anything. This task is pure reconnaissance and informs Task 7's scope.

---

## Task 2: `.golangci.yml`

**Files:**
- Create: `.golangci.yml`

- [ ] **Step 1: Write the config**

Create `.golangci.yml`:

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

- [ ] **Step 2: Verify the config parses**

Run: `golangci-lint config verify`
Expected: no error. If your golangci-lint version doesn't support `config verify`, run `golangci-lint run --help` to confirm it accepts the config without a parse error, then run `golangci-lint linters` to see the active linters — `gofmt`, `goimports`, `misspell` must appear alongside defaults.

- [ ] **Step 3: Run against the repo to confirm no crash**

Run: `golangci-lint run ./...`
Expected: either clean or produces findings — but does not error out with a config problem. Findings are fine; Task 7 fixes them.

- [ ] **Step 4: Commit**

```bash
git add .golangci.yml
git commit -m "ci: add golangci-lint config with default preset + gofmt/goimports/misspell"
```

---

## Task 3: `tools.go` — pin tool dependencies

**Files:**
- Create: `tools.go` (at repo root)
- Modify: `go.mod`, `go.sum` (via `go get`)

- [ ] **Step 1: Add the build-tagged tools file**

Create `tools.go` at the repo root (same directory as `go.mod`):

```go
//go:build tools

package tools

import (
	_ "github.com/evilmartians/lefthook"
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "golang.org/x/tools/cmd/goimports"
)
```

- [ ] **Step 2: Pull the modules**

Run:

```bash
go get github.com/evilmartians/lefthook
go get github.com/golangci/golangci-lint/cmd/golangci-lint
go get golang.org/x/tools/cmd/goimports
```

`go mod tidy` will try to drop them because no non-tools file imports them; the `//go:build tools` tag keeps them listed when running `go mod tidy` with `-compat=1.17` or newer. Run `go mod tidy` and verify the three packages remain in `go.mod` (they should stay because the tagged file still references them under `go list -tags=tools`). If `go mod tidy` removes them, re-run the `go get` commands.

- [ ] **Step 3: Verify the build still works**

Run: `make build && make test && make vet`
Expected: all clean. The `tools` tag is off by default so the file doesn't compile during normal builds.

- [ ] **Step 4: Commit**

```bash
git add tools.go go.mod go.sum
git commit -m "tools: pin golangci-lint, goimports, and lefthook via tools.go"
```

---

## Task 4: Makefile — `fmt`, `fmt-check`, `lint`, `check`, `install-tools`, `setup-hooks`

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add the new targets**

Open `Makefile`. Change the `.PHONY` line (currently line 6) from:

```makefile
.PHONY: build build-linux clean test vet sync-embedded
```

to:

```makefile
.PHONY: build build-linux clean test vet sync-embedded fmt fmt-check lint check install-tools setup-hooks
```

Then append the following to the end of the file:

```makefile

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
	@echo "Install shellcheck via your package manager (brew install shellcheck / apt install shellcheck)."

setup-hooks:
	lefthook install
```

- [ ] **Step 2: Verify each target**

Run each in turn:

```bash
make fmt          # may modify files; then run `git diff` to see if anything changed
make fmt-check    # will fail if make fmt changed anything that isn't staged
make lint         # may fail with baseline findings (that's expected — Task 7 fixes them)
```

Do NOT commit the side effects of `make fmt` in this task — those changes belong in Task 7 as the dedicated baseline-cleanup commit.

If `make fmt` mutated files, stash or reset them: `git checkout -- .` (after confirming `.golangci.yml` and `Makefile` are staged, not blown away).

- [ ] **Step 3: Verify `make install-tools` and `make setup-hooks` exist but don't run them yet**

```bash
make -n install-tools    # dry-run, confirms target exists
make -n setup-hooks      # dry-run, confirms target exists
```

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "make: add fmt, fmt-check, lint, check, install-tools, setup-hooks targets"
```

---

## Task 5: `lefthook.yml` — pre-commit and pre-push hooks

**Files:**
- Create: `lefthook.yml`

- [ ] **Step 1: Write the hook config**

Create `lefthook.yml`:

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

- [ ] **Step 2: Install the hooks locally**

Run: `lefthook install`
Expected: creates `.git/hooks/pre-commit` and `.git/hooks/pre-push` as lefthook dispatchers. Confirm with `ls -la .git/hooks/pre-commit .git/hooks/pre-push` — both should be present.

- [ ] **Step 3: Smoke-test pre-commit (will still fail on baseline findings)**

Make a trivial whitespace-only change to a Go file (add and remove a trailing space), stage it, try to commit with an obviously throwaway message. Expect the fmt-go hook to auto-fix and stage the result, the lint-go hook to either pass (the change is pure whitespace) or flag existing issues.

If the baseline findings from Task 1 block this smoke test, that's expected — Task 7 cleans them up. For now just verify the hook fires, then reset: `git reset HEAD~1 && git restore --staged . && git restore .`

- [ ] **Step 4: Commit the hook config**

```bash
git add lefthook.yml
git commit -m "hooks: add lefthook pre-commit (fmt + staged lint) and pre-push (make check)"
```

---

## Task 6: CI — parallel `lint` job

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Replace the workflow**

Replace the contents of `.github/workflows/ci.yml` with:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Vet
        run: make vet

      - name: Test
        run: make test

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
          sudo apt-get update
          sudo apt-get install -y shellcheck
          shellcheck lib/scripts/*.sh lib/hooks/*.sh
```

- [ ] **Step 2: Validate the YAML**

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))"`
Expected: no output (parses cleanly). If you don't have Python, use `yq`: `yq '.' .github/workflows/ci.yml > /dev/null`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add parallel lint job (golangci-lint + shellcheck)"
```

Note: this commit will cause CI to fail on the `lint` job until Task 7 lands. Task 7 is next, so keep moving.

---

## Task 7: Baseline cleanup — make `make lint` green

**Files:**
- Modify: any Go files flagged by `make lint`, plus `lib/scripts/*.sh` / `lib/hooks/*.sh` if shellcheck flagged them.

- [ ] **Step 1: Run the formatter**

```bash
make fmt
git diff --stat
```

Expected: some files may be reformatted. Inspect the diff — `gofmt -s` simplifications and `goimports` reorderings only. No semantic changes.

- [ ] **Step 2: Run the linter and capture findings**

```bash
make lint 2>&1 | tee /tmp/lint-findings.log
```

Address findings **conservatively**:

- `errcheck`: either handle the error, assign to `_`, or wrap in a minimal helper. Do not silence a real error check just to appease the linter.
- `ineffassign`, `staticcheck`, `unused`: remove dead code or fix the real bug. If a finding looks like a false positive, use an inline `//nolint:<linter>` with a comment explaining why (not blanket disabling).
- `gofmt`, `goimports`: already fixed by step 1.
- `misspell`: fix typos.
- `shellcheck`: fix quoting, use `${var}`, add `shellcheck disable=SCXXXX` with a reason only when the warning is genuinely wrong.

Never ignore an `errcheck` finding in `lib/scripts/*.sh`-adjacent security-relevant code (firewall, init-claude-plugins) without a typed-out justification.

- [ ] **Step 3: Re-run and confirm clean**

```bash
make fmt-check
make lint
```

Both must succeed with exit 0. If `make fmt` keeps re-formatting files, that's a Task 4 bug — go fix the Makefile.

- [ ] **Step 4: Run the full check target**

```bash
make check
```

Expected: `fmt-check` + `vet` + `lint` + `test` all pass.

- [ ] **Step 5: Commit**

Split the commit if the diff is large (formatting in one, lint fixes in another). Minimum:

```bash
git add -A
git commit -m "Baseline cleanup: gofmt/goimports + golangci-lint + shellcheck"
```

If you split:

```bash
# commit 1: pure formatting
git add <formatted files>
git commit -m "Format: apply gofmt -s and goimports across the repo"

# commit 2: lint fixes
git add -A
git commit -m "Lint: address golangci-lint and shellcheck findings"
```

---

## Task 8: Developer onboarding docs

**Files:**
- Modify: one install doc under `docs/src/content/docs/` (verify the exact path exists before editing)

- [ ] **Step 1: Locate the install doc**

Run: `ls docs/src/content/docs/ && find docs/src/content/docs -maxdepth 3 -iname '*install*'`
Identify the primary install doc (likely something like `getting-started/installation.md` or `install.md`). If none exists, target the doc where "make build" is first mentioned.

- [ ] **Step 2: Add a "Developer tooling" section**

Append the following to the end of that file (adapt the markdown style to match the file — MDX vs plain markdown):

```markdown
## Developer tooling

After cloning, set up lint/format/test hooks:

```bash
make install-tools   # installs golangci-lint, goimports, lefthook (requires Go)
make setup-hooks     # wires pre-commit and pre-push hooks via lefthook
```

Install `shellcheck` separately via your system package manager:

- macOS: `brew install shellcheck`
- Debian/Ubuntu: `sudo apt-get install shellcheck`
- Alpine: `sudo apk add shellcheck`

Run the same checks CI runs:

```bash
make check   # fmt-check + vet + lint + test
```
```

- [ ] **Step 3: Verify the doc still renders (if the site has a build step)**

If the docs site has a build command (check `docs/package.json`), run it. Otherwise visual-inspect the diff.

- [ ] **Step 4: Commit**

```bash
git add docs/src/content/docs/<path>
git commit -m "docs: add developer tooling setup instructions"
```

---

## Task 9: Final end-to-end verification

**Files:** none modified

- [ ] **Step 1: Fresh checkout simulation**

From a clean working tree:

```bash
git status                # must be clean
make install-tools        # re-run, idempotent
make setup-hooks          # re-run, idempotent
make check                # must pass end-to-end
```

- [ ] **Step 2: Hook smoke test**

Introduce a deliberate formatting error in a Go file:

```bash
echo "var _ =1+2" >> cmd/cspace/main.go   # bad spacing around operators? gofmt will fix
git add cmd/cspace/main.go
git commit -m "smoke: deliberate violation"
```

Expected: pre-commit fires, formatting is auto-applied, the commit lands with the formatted version. If golangci-lint flags the addition (e.g., unused variable), the commit is blocked — revert and try with a harmless change.

Reset: `git reset --hard HEAD~1` (if the commit landed) or `git restore cmd/cspace/main.go` (if it didn't).

- [ ] **Step 3: Pre-push smoke test**

On a disposable branch:

```bash
git checkout -b smoke-pre-push
echo "// deliberate test" >> cmd/cspace/main.go   # harmless
git add -A
git commit -m "smoke: harmless change" --no-verify   # skip pre-commit to isolate pre-push
git push -u origin smoke-pre-push
```

Expected: pre-push runs `make check`; if clean, push proceeds. If CI would fail, pre-push catches it first. If the push succeeds, delete the branch remotely and locally: `git push origin --delete smoke-pre-push && git checkout main && git branch -D smoke-pre-push`.

- [ ] **Step 4: Confirm CI on the feature branch PR (if one exists)**

Push the branch that contains Tasks 2–8; confirm both `check` and `lint` jobs run and are green.

- [ ] **Step 5: No commit needed**

This task is verification only.

---

## Self-Review Checklist

**Spec coverage:**

- ✅ `.golangci.yml` with default preset + gofmt/goimports/misspell — Task 2
- ✅ `lefthook.yml` with pre-commit (auto-format staged, staged-diff lint, staged shell lint) and pre-push (`make check`) — Task 5
- ✅ Makefile: `fmt`, `fmt-check`, `lint`, `check`, `install-tools`, `setup-hooks` — Task 4
- ✅ `tools.go` under `//go:build tools` — Task 3
- ✅ CI lint job — Task 6
- ✅ Baseline cleanup commit — Task 7
- ✅ Developer onboarding docs — Task 8
- ✅ Scope: Go code + shell scripts in `lib/scripts` / `lib/hooks`; excludes `internal/assets/embedded`
- ✅ Smoke tests at the end — Task 9

**Ordering:** Tools first (Task 2 config, Task 3 pinning, Task 4 Make). Then hooks (Task 5). Then CI (Task 6). Then baseline fix (Task 7) so the lint rules go green before contributors feel them. Then docs (Task 8). Final e2e check (Task 9).

**Placeholders:** None. Each step has executable commands or complete code.

**Type/name consistency:** All target names (`fmt`, `fmt-check`, `lint`, `check`, `install-tools`, `setup-hooks`) match across Makefile, lefthook.yml, CI, and docs. Linter list (`gofmt`, `goimports`, `misspell`) is identical in `.golangci.yml` and the spec.
