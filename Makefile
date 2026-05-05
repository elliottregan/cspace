VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS         := -ldflags "-X github.com/elliottregan/cspace/internal/cli.Version=$(VERSION)"
LDFLAGS_RELEASE := -ldflags "-s -w -X github.com/elliottregan/cspace/internal/cli.Version=$(VERSION)"
GOBIN           := ./bin/cspace-go

.PHONY: build build-linux clean test vet sync-embedded fmt fmt-check lint check install-tools setup-hooks check-hooks cspace-linux cspace-image
# (registry-daemon target removed; daemon is embedded as `cspace daemon serve`.)

# Sync lib/ contents into internal/assets/embedded/ for go:embed
sync-embedded:
	@rm -rf internal/assets/embedded
	@mkdir -p internal/assets/embedded
	@touch internal/assets/embedded/.gitkeep
	@cp -r lib/templates internal/assets/embedded/
	@cp -r lib/runtime internal/assets/embedded/
	@cp -r lib/agent-supervisor-bun internal/assets/embedded/
	@rm -rf internal/assets/embedded/agent-supervisor-bun/node_modules
	@cp lib/defaults.json internal/assets/embedded/
	@cp lib/planets.json internal/assets/embedded/

build: check-hooks sync-embedded bin/cspace-go

bin/cspace-go: $(shell find cmd/cspace internal -name '*.go') sync-embedded
	go build $(LDFLAGS) -o $@ ./cmd/cspace

build-linux: sync-embedded
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS_RELEASE) -o dist/cspace-linux-amd64 ./cmd/cspace
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS_RELEASE) -o dist/cspace-linux-arm64 ./cmd/cspace

test: sync-embedded
	go test ./...

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
	shellcheck lib/runtime/scripts/*.sh

check: fmt-check vet lint test

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

# Build the cspace sandbox image.
.PHONY: cspace-image
cspace-image: cspace-linux
	container build \
		--platform linux/arm64 \
		--tag cspace:latest \
		--file lib/templates/Dockerfile \
		.
