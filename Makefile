VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS         := -ldflags "-X github.com/elliottregan/cspace/internal/cli.Version=$(VERSION)"
LDFLAGS_RELEASE := -ldflags "-s -w -X github.com/elliottregan/cspace/internal/cli.Version=$(VERSION)"
GOBIN           := ./bin/cspace-go

.PHONY: build build-linux clean test test-node vet sync-embedded overlay-demo overlay-web fmt fmt-check lint check install-tools setup-hooks check-hooks cspace-linux cspace2-image
# (registry-daemon target removed; daemon is embedded as `cspace daemon serve`.)

# Sync lib/ contents into internal/assets/embedded/ for go:embed
sync-embedded:
	@rm -rf internal/assets/embedded
	@mkdir -p internal/assets/embedded
	@touch internal/assets/embedded/.gitkeep
	@cp -r lib/templates internal/assets/embedded/
	@cp -r lib/scripts internal/assets/embedded/
	@cp -r lib/hooks internal/assets/embedded/
	@cp -r lib/agents internal/assets/embedded/
	@cp -r lib/advisors internal/assets/embedded/
	@cp -r lib/agent-supervisor internal/assets/embedded/
	@rm -rf internal/assets/embedded/agent-supervisor/node_modules
	@cp -r lib/skills internal/assets/embedded/
	@cp -r lib/commands internal/assets/embedded/
	@cp lib/defaults.json internal/assets/embedded/
	@cp lib/planets.json internal/assets/embedded/

build: check-hooks sync-embedded bin/cspace-go

bin/cspace-go: $(shell find cmd/cspace internal -name '*.go') sync-embedded
	go build $(LDFLAGS) -o $@ ./cmd/cspace

# Run the provisioning overlay against synthesized phase events. Useful for
# iterating on the UI without spinning up a real container. Forwards any
# extra args through ARGS, e.g. `make overlay-demo ARGS="--planet=jupiter"`.
overlay-demo: sync-embedded
	@go run ./cmd/overlay-demo/ $(ARGS)

# Serve a browser preview of the overlay at http://localhost:3000/ with
# sliders for the main image parameters. Faster visual iteration than
# the TUI demo. Override the port with ARGS="-addr :9000".
overlay-web: sync-embedded
	@go run ./cmd/overlay-web/ $(ARGS)

build-linux: sync-embedded
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS_RELEASE) -o dist/cspace-linux-amd64 ./cmd/cspace
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS_RELEASE) -o dist/cspace-linux-arm64 ./cmd/cspace

test: sync-embedded test-node
	go test ./...

# Run Node tests in lib/agent-supervisor. node:test runner, no extra deps.
test-node:
	cd lib/agent-supervisor && node --test

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
	shellcheck lib/scripts/*.sh lib/hooks/*.sh

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
		echo "⚠  Git hooks are not installed. Run 'make setup-hooks' to enable pre-commit and pre-push checks." >&2; \
		echo "" >&2; \
	fi

# P0: cross-compile cspace for the Linux microVM.
.PHONY: cspace-linux
cspace-linux:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
		-o bin/cspace-linux-arm64 \
		./cmd/cspace

# Build the cspace2 sandbox image.
.PHONY: cspace2-image
cspace2-image: cspace-linux
	container build \
		--platform linux/arm64 \
		--tag cspace2:latest \
		--file lib/templates/Dockerfile.cspace2 \
		.

