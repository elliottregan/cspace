VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS         := -ldflags "-X github.com/elliottregan/cspace/internal/cli.Version=$(VERSION)"
LDFLAGS_RELEASE := -ldflags "-s -w -X github.com/elliottregan/cspace/internal/cli.Version=$(VERSION)"
GOBIN           := ./bin/cspace-go

.PHONY: build build-linux clean test test-scripts vet sync-embedded fmt fmt-check lint check install-tools setup-hooks check-hooks cspace-linux cspace-image release
# (registry-daemon target removed; daemon is embedded as `cspace daemon serve`.)

# Sync lib/ contents into internal/assets/embedded/ for go:embed.
# Explicit allowlist of files actually consumed at runtime — by the Go
# binary (defaults.json, planets.json) or by `cspace image build`'s
# `container build` context (Dockerfile + runtime scripts/features +
# agent-supervisor source). Prevents stray local build artifacts (e.g.
# lib/agent-supervisor-bun/dist/cspace-supervisor at ~100MB) from
# inflating the embedded payload silently.
sync-embedded:
	@rm -rf internal/assets/embedded
	@mkdir -p internal/assets/embedded/templates
	@mkdir -p internal/assets/embedded/runtime/scripts
	@mkdir -p internal/assets/embedded/runtime/features
	@mkdir -p internal/assets/embedded/agent-supervisor-bun/src
	@touch internal/assets/embedded/.gitkeep
	@cp lib/defaults.json lib/planets.json internal/assets/embedded/
	@cp lib/templates/Dockerfile internal/assets/embedded/templates/
	@cp lib/runtime/scripts/*.sh internal/assets/embedded/runtime/scripts/
	@rm -f internal/assets/embedded/runtime/scripts/*.test.sh
	@cp lib/runtime/features/*.sh internal/assets/embedded/runtime/features/
	@cp lib/agent-supervisor-bun/package.json \
	    lib/agent-supervisor-bun/bun.lock \
	    lib/agent-supervisor-bun/build.ts \
	    internal/assets/embedded/agent-supervisor-bun/
	@cp lib/agent-supervisor-bun/src/*.ts internal/assets/embedded/agent-supervisor-bun/src/
	@mkdir -p internal/assets/embedded/plugins
	@cp -R lib/plugins/. internal/assets/embedded/plugins/

build: check-hooks sync-embedded bin/cspace-go

bin/cspace-go: $(shell find cmd/cspace internal -name '*.go') sync-embedded
	go build $(LDFLAGS) -o $@ ./cmd/cspace

build-linux: sync-embedded
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS_RELEASE) -o dist/cspace-linux-amd64 ./cmd/cspace
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS_RELEASE) -o dist/cspace-linux-arm64 ./cmd/cspace

test: sync-embedded
	go test ./...

# Bash-level tests (release guards, in-sandbox scripts). Not covered by
# `go test`; run alongside it so `make check` gates the release script too.
test-scripts:
	@for t in scripts/*.test.sh lib/runtime/scripts/*.test.sh; do \
		[ -f "$$t" ] || continue; \
		echo "bash $$t"; \
		bash "$$t" || exit 1; \
	done

vet: sync-embedded
	go vet ./...

clean:
	rm -f $(GOBIN) $(GOBIN)-linux-amd64 $(GOBIN)-linux-arm64
	rm -rf dist
	rm -rf internal/assets/embedded

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
	shellcheck lib/runtime/scripts/*.sh scripts/*.sh

check: fmt-check vet lint test test-scripts

install-tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/evilmartians/lefthook@latest
	@echo "Install shellcheck via your package manager (brew install shellcheck / apt install shellcheck)."

setup-hooks:
	@command -v lefthook >/dev/null 2>&1 || { echo "ERROR: lefthook not found in PATH. Run 'make install-tools' first." >&2; exit 1; }
	lefthook install

# Emit a warning if git hooks are not installed. Called by build so
# contributors notice early without blocking the build itself.
check-hooks:
	@if [ -d .git ] && [ ! -f .git/hooks/pre-commit ]; then \
		echo "" >&2; \
		echo "WARNING: Git hooks are not installed. Run 'make setup-hooks' to enable pre-commit and pre-push checks." >&2; \
		echo "" >&2; \
	fi

# P0: cross-compile cspace for the Linux microVM.
.PHONY: cspace-linux
cspace-linux:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
		-o bin/cspace-linux-arm64 \
		./cmd/cspace

# Build the cspace sandbox image. CSPACE_VERSION bakes into the
# cspace.version label that `cspace up` compares against the running CLI —
# without it every locally-built image reports "dev" and reads as stale.
.PHONY: cspace-image
cspace-image: cspace-linux
	container build \
		--platform linux/arm64 \
		--tag cspace:latest \
		--file lib/templates/Dockerfile \
		--build-arg CSPACE_VERSION=$(VERSION) \
		.

# Cut a release from this Mac: tag, publish binaries + casks via goreleaser,
# and publish the sandbox image to ghcr. Local rather than CI because Apple
# Container needs Virtualization.framework, which GitHub's hosted macOS
# runners do not expose.
#   make release TAG=v1.0.0-rc.47
#   make release TAG=v1.0.0-rc.47 ARGS=--dry-run
release:
	@[ -n "$(TAG)" ] || { echo "usage: make release TAG=vX.Y.Z[-rc.N] [ARGS=\"--dry-run --skip-image\"]" >&2; exit 2; }
	./scripts/release.sh $(TAG) $(ARGS)
